package inventory

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/Rhionin/stockbot/internal/product"
	"github.com/Rhionin/stockbot/internal/tenant"
)

// InventoryItem represents an aggregated inventory item with instance counts
// and attention flags derived from expiry status.
type InventoryItem struct {
	Item            Item          `json:"item"`
	InstanceCount   int           `json:"instanceCount"`
	NearExpiryCount int           `json:"nearExpiryCount"`
	ExpiredCount    int           `json:"expiredCount"`
	NeedsAttention  bool          `json:"needsAttention"`
	Group           *GroupSummary `json:"group,omitempty"`
}

// GroupMemberView is one product inside the group summary on an inventory row.
type GroupMemberView struct {
	ProductID string   `json:"productId"`
	Name      string   `json:"name"`
	OnHand    int      `json:"onHand"`
	ItemID    string   `json:"itemId,omitempty"`
	Barcodes  []string `json:"barcodes,omitempty"`
}

// GroupSummary is the shared group attached to every member row.
// OnHand is the sum of those members, counted in SQL with the list.
type GroupSummary struct {
	ID              string            `json:"id"`
	Name            string            `json:"name"`
	Rule            string            `json:"rule"`
	RuleConfirmed   bool              `json:"ruleConfirmed"`
	PinnedProductID string            `json:"pinnedProductId,omitempty"`
	OnHand          int               `json:"onHand"`
	WindowMonths    *int              `json:"windowMonths,omitempty"`
	Quantity        *float64          `json:"quantity,omitempty"`
	Dimension       string            `json:"dimension,omitempty"`
	MemberCount     int               `json:"memberCount"`
	Members         []GroupMemberView `json:"members"`
}

// DefaultWarningDays is the near-expiry window GetInventoryList and
// GetInventoryItem use when no caller-specific value is required.
const DefaultWarningDays = 7

// GetInventoryList returns this household's items with aggregated instance
// information including counts and expiry-based attention flags.
// Items are ordered by product name. If query is non-empty, only items
// whose name or category contains the query string (case-insensitive) are returned.
//
// Validates Requirements 2.2, 2.3, 2.4, 2.10, 2.11
func (r *Pantry) GetInventoryList(ctx context.Context, scope tenant.Scope, now time.Time, warningDays int, query string) ([]InventoryItem, error) {
	if err := scope.Require(); err != nil {
		return nil, err
	}
	items, err := r.ListItems(ctx, scope)
	if err != nil {
		return nil, err
	}

	result := make([]InventoryItem, 0, len(items))

	for _, item := range items {
		if query != "" && !matchesQuery(item, query) {
			continue
		}

		invItem, err := r.aggregateItem(ctx, scope, item, now, warningDays)
		if err != nil {
			return nil, err
		}

		result = append(result, invItem)
	}

	if err := r.attachGroups(ctx, scope, result); err != nil {
		return nil, err
	}
	return result, nil
}

// GetInventoryItem returns the aggregated InventoryItem for a single item
// in this household, or nil if no such item exists.
func (r *Pantry) GetInventoryItem(ctx context.Context, scope tenant.Scope, itemID string, now time.Time, warningDays int) (*InventoryItem, error) {
	if err := scope.Require(); err != nil {
		return nil, err
	}
	item, err := r.getItemByID(ctx, scope, itemID)
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, nil
	}

	invItem, err := r.aggregateItem(ctx, scope, *item, now, warningDays)
	if err != nil {
		return nil, err
	}
	rows := []InventoryItem{invItem}
	if err := r.attachGroups(ctx, scope, rows); err != nil {
		return nil, err
	}
	return &rows[0], nil
}

// aggregateItem computes the InstanceCount, NearExpiryCount, ExpiredCount,
// and NeedsAttention fields for a single item, shared by GetInventoryList
// and GetInventoryItem so their behavior can never drift apart.
func (r *Pantry) aggregateItem(ctx context.Context, scope tenant.Scope, item Item, now time.Time, warningDays int) (InventoryItem, error) {
	instances, err := r.ListItemInstances(ctx, scope, item.ID)
	if err != nil {
		return InventoryItem{}, err
	}

	invItem := InventoryItem{
		Item:          item,
		InstanceCount: len(instances),
	}

	// Compute expiry status for each instance
	for _, instance := range instances {
		status := ComputeExpiryStatus(instance.ExpiresAt, now, warningDays)
		switch status {
		case ExpiryStatusNearExpiry:
			invItem.NearExpiryCount++
		case ExpiryStatusExpired:
			invItem.ExpiredCount++
		}
	}

	// Set needs attention flag if any instance is near expiry or expired
	invItem.NeedsAttention = invItem.NearExpiryCount > 0 || invItem.ExpiredCount > 0

	return invItem, nil
}

// matchesQuery returns true if the item's product name or category contains
// the query string (case-insensitive).
func matchesQuery(item Item, query string) bool {
	lowerQuery := strings.ToLower(query)
	lowerName := strings.ToLower(item.Product.Name)
	lowerCategory := strings.ToLower(item.Product.Category)

	return strings.Contains(lowerName, lowerQuery) || strings.Contains(lowerCategory, lowerQuery)
}

func (r *Pantry) attachGroups(ctx context.Context, scope tenant.Scope, items []InventoryItem) error {
	if err := scope.Require(); err != nil {
		return err
	}
	if len(items) == 0 {
		return nil
	}
	byProduct, err := r.groupSummaries(ctx, scope)
	if err != nil {
		return err
	}
	for i := range items {
		if summary, ok := byProduct[items[i].Item.ProductID]; ok {
			items[i].Group = summary
		}
	}
	return nil
}

func (r *Pantry) groupSummaries(ctx context.Context, scope tenant.Scope) (map[string]*GroupSummary, error) {
	if err := scope.Require(); err != nil {
		return nil, err
	}
	householdID := scope.HouseholdID()
	rows, err := r.db.QueryContext(ctx, `
		SELECT g.id, g.name, g.rule, g.rule_confirmed, g.pinned_product_id,
		       g.window_months, g.quantity_base_value, g.quantity_dimension,
		       p.id, `+product.DisplayNameSQL("p")+`,
		       COALESCE((SELECT i.id FROM items i WHERE i.product_id = p.id AND i.household_id = ? LIMIT 1), ''),
		       COALESCE((SELECT COUNT(*) FROM item_instances ii
		                  JOIN items i ON i.id = ii.item_id AND i.household_id = ii.household_id
		                  WHERE i.product_id = p.id AND i.household_id = ? AND ii.household_id = ? AND ii.removed_at IS NULL), 0)
		FROM product_group_members m
		JOIN product_groups g ON g.id = m.group_id AND g.household_id = m.household_id
		JOIN products p ON p.id = m.product_id
		WHERE g.household_id = ? AND m.household_id = ?
		ORDER BY g.name, `+product.DisplayNameSQL("p")+`, p.id`,
		householdID, householdID, householdID, householdID, householdID, householdID, householdID)
	if err != nil {
		return nil, fmt.Errorf("could not load groups for inventory: %w", err)
	}
	defer rows.Close()

	byID := map[string]*GroupSummary{}
	var order []string
	for rows.Next() {
		var (
			id, name, rule, productID, productName, itemID string
			confirmed, onHand                              int
			pin, dim                                       sql.NullString
			window                                         sql.NullInt64
			qty                                            sql.NullFloat64
		)
		if err := rows.Scan(&id, &name, &rule, &confirmed, &pin, &window, &qty, &dim, &productID, &productName, &itemID, &onHand); err != nil {
			return nil, fmt.Errorf("could not load groups for inventory: %w", err)
		}
		summary := byID[id]
		if summary == nil {
			summary = &GroupSummary{
				ID:            id,
				Name:          name,
				Rule:          rule,
				RuleConfirmed: confirmed != 0,
				Members:       []GroupMemberView{},
			}
			if pin.Valid {
				summary.PinnedProductID = pin.String
			}
			if window.Valid {
				n := int(window.Int64)
				summary.WindowMonths = &n
			}
			if qty.Valid && dim.Valid {
				if amount, _, ok := product.DisplayNetSize(qty.Float64, dim.String); ok {
					summary.Quantity = &amount
					summary.Dimension = dim.String
				}
			}
			byID[id] = summary
			order = append(order, id)
		}
		member := GroupMemberView{ProductID: productID, Name: productName, OnHand: onHand, Barcodes: []string{}}
		if itemID != "" {
			member.ItemID = itemID
		}
		summary.Members = append(summary.Members, member)
		summary.OnHand += onHand
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("could not load groups for inventory: %w", err)
	}
	for _, id := range order {
		summary := byID[id]
		summary.MemberCount = len(summary.Members)
	}
	if err := r.attachBarcodes(ctx, scope, byID); err != nil {
		return nil, err
	}

	byProduct := map[string]*GroupSummary{}
	for _, summary := range byID {
		for i := range summary.Members {
			byProduct[summary.Members[i].ProductID] = summary
		}
	}
	return byProduct, nil
}

func (r *Pantry) attachBarcodes(ctx context.Context, scope tenant.Scope, groups map[string]*GroupSummary) error {
	if err := scope.Require(); err != nil {
		return err
	}
	if len(groups) == 0 {
		return nil
	}
	householdID := scope.HouseholdID()
	rows, err := r.db.QueryContext(ctx, `
		SELECT b.product_id, b.barcode, b.source
		FROM barcodes b
		JOIN product_group_members m ON m.product_id = b.product_id AND m.household_id = ?
		JOIN product_groups g ON g.id = m.group_id AND g.household_id = ?
		WHERE (b.household_id = ? OR (b.source = 'global' AND b.user_id = ''))
		ORDER BY CASE b.source WHEN 'user_override' THEN 0 ELSE 1 END, b.barcode`,
		householdID, householdID, householdID)
	if err != nil {
		return fmt.Errorf("could not load group barcodes: %w", err)
	}
	defer rows.Close()
	codes := map[string][]string{}
	seen := map[string]map[string]struct{}{}
	claimed := map[string]struct{}{}
	for rows.Next() {
		var productID, barcode, source string
		if err := rows.Scan(&productID, &barcode, &source); err != nil {
			return fmt.Errorf("could not load group barcodes: %w", err)
		}
		if source == "user_override" {
			claimed[barcode] = struct{}{}
		} else if _, ok := claimed[barcode]; ok {
			continue
		}
		if seen[productID] == nil {
			seen[productID] = map[string]struct{}{}
		}
		if _, ok := seen[productID][barcode]; ok {
			continue
		}
		seen[productID][barcode] = struct{}{}
		codes[productID] = append(codes[productID], barcode)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("could not load group barcodes: %w", err)
	}
	for _, summary := range groups {
		for i := range summary.Members {
			if list := codes[summary.Members[i].ProductID]; len(list) > 0 {
				summary.Members[i].Barcodes = list
			}
		}
	}
	return nil
}
