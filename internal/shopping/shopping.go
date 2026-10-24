// Package shopping provides shopping list management functionality.
package shopping

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/Rhionin/stockbot/internal/product"
	"github.com/Rhionin/stockbot/internal/tenant"
)

// ErrItemNotFound is returned when a shopping list item does not exist.
var ErrItemNotFound = errors.New("shopping list item not found")

// GroupChoice is one member a shopper can buy for a group line this trip.
type GroupChoice struct {
	ItemID    string
	ProductID string
	Name      string
}

// ShoppingListItem represents a single entry on a user's shopping list.
type ShoppingListItem struct {
	ID            string
	UserID        string
	ItemID        string
	Quantity      int
	Source        string // "manual" or "auto"
	Note          string
	PurchasedAt   *time.Time
	CreatedAt     time.Time
	GroupID       string
	GroupName     string
	GroupRule     string
	RuleConfirmed bool
	GroupMembers  []GroupChoice
}

const shoppingListSelect = `
	SELECT s.id, s.user_id, s.item_id, s.quantity, s.source, s.purchased_at, s.created_at, s.note,
		COALESCE(s.group_id, ''), COALESCE(g.name, ''), COALESCE(g.rule, ''), COALESCE(g.rule_confirmed, 0)
	FROM shopping_list_items s
	LEFT JOIN product_groups g ON g.id = s.group_id AND g.household_id = s.household_id
	WHERE s.household_id = ?`

// Store provides data access for shopping list items.
type Store struct {
	db *sql.DB
}

// NewStore creates a new Store with the given database connection.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// AddManualItem inserts a new manual shopping list item for the household and item.
func (s *Store) AddManualItem(ctx context.Context, scope tenant.Scope, itemID string, quantity int) (*ShoppingListItem, error) {
	if err := scope.Require(); err != nil {
		return nil, err
	}
	household := scope.HouseholdID()
	id := uuid.NewString()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to add shopping list item: %w", err)
	}
	defer tx.Rollback()
	// A hand-added line is a correction. It may name an item the owner had removed.
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM staged_cart_skips WHERE household_id = ? AND item_id = ?`,
		household, itemID,
	); err != nil {
		return nil, fmt.Errorf("failed to add shopping list item: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO shopping_list_items (id, household_id, user_id, item_id, quantity, source)
		 VALUES (?, ?, ?, ?, ?, 'manual')`,
		id, household, household, itemID, quantity,
	); err != nil {
		return nil, fmt.Errorf("failed to add shopping list item: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to add shopping list item: %w", err)
	}
	return s.getByID(ctx, scope, id)
}

// RemoveItem deletes the shopping list item with the given ID.
// Returns ErrItemNotFound if no such item exists in this household.
func (s *Store) RemoveItem(ctx context.Context, scope tenant.Scope, id string) error {
	if err := scope.Require(); err != nil {
		return err
	}
	item, err := s.getByID(ctx, scope, id)
	if err != nil {
		return fmt.Errorf("failed to remove shopping list item: %w", err)
	}
	if item == nil {
		return ErrItemNotFound
	}
	household := scope.HouseholdID()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to remove shopping list item: %w", err)
	}
	defer tx.Rollback()
	// Removing an open auto line keeps it off this trip. Clearing a bought
	// line ends that protection, so the next list may plan the item again.
	if item.Source == "auto" && item.PurchasedAt == nil {
		var groupID any
		if item.GroupID != "" {
			groupID = item.GroupID
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO staged_cart_skips (household_id, user_id, item_id, group_id) VALUES (?, ?, ?, ?)
			 ON CONFLICT(user_id, item_id) DO UPDATE SET group_id = COALESCE(excluded.group_id, staged_cart_skips.group_id)`,
			household, household, item.ItemID, groupID,
		); err != nil {
			return fmt.Errorf("failed to remove shopping list item: %w", err)
		}
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM shopping_list_items WHERE id = ? AND household_id = ?`, id, household)
	if err != nil {
		return fmt.Errorf("failed to remove shopping list item: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to check rows affected: %w", err)
	}
	if n == 0 {
		return ErrItemNotFound
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to remove shopping list item: %w", err)
	}
	return nil
}

// MarkPurchased sets the purchased_at timestamp on the item to the current time.
// Returns ErrItemNotFound if no such item exists in this household.
// Does not modify any item_instances rows.
func (s *Store) MarkPurchased(ctx context.Context, scope tenant.Scope, id string) error {
	if err := scope.Require(); err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE shopping_list_items SET purchased_at = ? WHERE id = ? AND household_id = ?`,
		time.Now().UTC(), id, scope.HouseholdID(),
	)
	if err != nil {
		return fmt.Errorf("failed to mark shopping list item as purchased: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to check rows affected: %w", err)
	}
	if n == 0 {
		return ErrItemNotFound
	}
	return nil
}

// ListManualItems returns all unpurchased manual shopping list items for the household,
// ordered by created_at ascending.
func (s *Store) ListManualItems(ctx context.Context, scope tenant.Scope) ([]ShoppingListItem, error) {
	if err := scope.Require(); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx,
		shoppingListSelect+`
		 AND s.source = 'manual' AND s.purchased_at IS NULL
		 ORDER BY s.created_at ASC`,
		scope.HouseholdID(),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list shopping list items: %w", err)
	}
	defer rows.Close()

	var items []ShoppingListItem
	for rows.Next() {
		item, err := scanShoppingListItem(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to read shopping list item: %w", err)
		}
		items = append(items, *item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate shopping list items: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("failed to list shopping list items: %w", err)
	}
	return s.fillGroupMembers(ctx, scope, items)
}

// ListUnpurchased returns every unpurchased shopping list row for the household,
// manual and derived. Callers that need one row per pantry item prefer a
// manual row when both exist.
func (s *Store) ListUnpurchased(ctx context.Context, scope tenant.Scope) ([]ShoppingListItem, error) {
	if err := scope.Require(); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx,
		shoppingListSelect+`
		 AND s.purchased_at IS NULL
		 ORDER BY s.created_at ASC`,
		scope.HouseholdID(),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list shopping list items: %w", err)
	}
	defer rows.Close()

	var items []ShoppingListItem
	for rows.Next() {
		item, err := scanShoppingListItem(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to read shopping list item: %w", err)
		}
		items = append(items, *item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate shopping list items: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("failed to list shopping list items: %w", err)
	}
	return s.fillGroupMembers(ctx, scope, items)
}

// GetItemByID retrieves a single shopping list item by its ID.
// Returns nil if no such item exists in this household.
func (s *Store) GetItemByID(ctx context.Context, scope tenant.Scope, id string) (*ShoppingListItem, error) {
	return s.getByID(ctx, scope, id)
}

// getByID retrieves a single shopping list item by its ID.
func (s *Store) getByID(ctx context.Context, scope tenant.Scope, id string) (*ShoppingListItem, error) {
	if err := scope.Require(); err != nil {
		return nil, err
	}
	row := s.db.QueryRowContext(ctx, shoppingListSelect+` AND s.id = ?`, scope.HouseholdID(), id)
	item, err := scanShoppingListItem(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get shopping list item: %w", err)
	}
	filled, err := s.fillGroupMembers(ctx, scope, []ShoppingListItem{*item})
	if err != nil {
		return nil, err
	}
	return &filled[0], nil
}

// scanner is a common interface for *sql.Row and *sql.Rows.
type scanner interface {
	Scan(dest ...interface{}) error
}

// scanShoppingListItem scans a row into a ShoppingListItem.
func scanShoppingListItem(row scanner) (*ShoppingListItem, error) {
	var item ShoppingListItem
	var purchasedAt sql.NullTime

	var confirmed int
	err := row.Scan(
		&item.ID,
		&item.UserID,
		&item.ItemID,
		&item.Quantity,
		&item.Source,
		&purchasedAt,
		&item.CreatedAt,
		&item.Note,
		&item.GroupID,
		&item.GroupName,
		&item.GroupRule,
		&confirmed,
	)
	if err != nil {
		return nil, err
	}

	if purchasedAt.Valid {
		item.PurchasedAt = &purchasedAt.Time
	}
	item.RuleConfirmed = confirmed != 0
	return &item, nil
}

// Adjustment holds a shopping list entry adjustment for a provider.
type Adjustment struct {
	EntryID    string
	ProviderID string
	Quantity   int // 0-999
	CreatedAt  time.Time
}

// SetAdjustment sets the adjustment quantity for one entry and provider.
// Validates quantity is in range 0-999.
func (s *Store) SetAdjustment(ctx context.Context, scope tenant.Scope, entryID, providerID string, quantity int) error {
	if err := scope.Require(); err != nil {
		return err
	}
	if quantity < 0 || quantity > 999 {
		return fmt.Errorf("adjustment quantity must be 0-999, got %d", quantity)
	}
	item, err := s.getByID(ctx, scope, entryID)
	if err != nil {
		return err
	}
	if item == nil {
		return ErrItemNotFound
	}
	household := scope.HouseholdID()
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO shopping_list_entry_adjustments (household_id, entry_id, provider_id, quantity)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(entry_id, provider_id) DO UPDATE SET quantity = ?
		 WHERE shopping_list_entry_adjustments.household_id = excluded.household_id`,
		household, entryID, providerID, quantity, quantity)
	if err != nil {
		return err
	}
	// The quantity the owner typed is a decision. The next fill leaves this line.
	_, err = s.db.ExecContext(ctx, `UPDATE shopping_list_items SET touched = 1 WHERE id = ? AND household_id = ?`, entryID, household)
	return err
}

// GetAdjustment returns the adjustment for one entry and provider, or (nil, nil) if not found.
func (s *Store) GetAdjustment(ctx context.Context, scope tenant.Scope, entryID, providerID string) (*Adjustment, error) {
	if err := scope.Require(); err != nil {
		return nil, err
	}
	var adj Adjustment
	err := s.db.QueryRowContext(ctx,
		`SELECT entry_id, provider_id, quantity, updated_at
		 FROM shopping_list_entry_adjustments
		 WHERE household_id = ? AND entry_id = ? AND provider_id = ?`,
		scope.HouseholdID(), entryID, providerID).Scan(&adj.EntryID, &adj.ProviderID, &adj.Quantity, &adj.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get adjustment: %w", err)
	}
	return &adj, nil
}

// ClearAdjustmentTx clears the adjustment for one entry and provider inside an existing transaction.
// This must be called in the same transaction as ledger reset.
func (s *Store) ClearAdjustmentTx(ctx context.Context, scope tenant.Scope, tx *sql.Tx, entryID, providerID string) error {
	if err := scope.Require(); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx,
		`DELETE FROM shopping_list_entry_adjustments WHERE household_id = ? AND entry_id = ? AND provider_id = ?`,
		scope.HouseholdID(), entryID, providerID)
	return err
}

// ErrNotGroupLine means the shopping line was not planned for a product group.
var ErrNotGroupLine = errors.New("That shopping line is not a group.")

// ErrNotGroupMember means the chosen product is not in the line's group.
var ErrNotGroupMember = errors.New("Choose a product in this group.")

// SwapAutoLine keeps this trip's line on a different member of the same group.
// The next fill leaves the line where it is.
func (s *Store) SwapAutoLine(ctx context.Context, scope tenant.Scope, lineID, itemID string) error {
	if err := scope.Require(); err != nil {
		return err
	}
	item, err := s.getByID(ctx, scope, lineID)
	if err != nil {
		return err
	}
	if item == nil {
		return ErrItemNotFound
	}
	if item.Source != "auto" || item.GroupID == "" {
		return ErrNotGroupLine
	}
	household := scope.HouseholdID()
	var n int
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM product_group_members m
		JOIN items i ON i.product_id = m.product_id AND i.household_id = m.household_id
		WHERE m.household_id = ? AND m.group_id = ? AND i.id = ?`,
		household, item.GroupID, itemID,
	).Scan(&n); err != nil {
		return fmt.Errorf("could not change the product on this line: %w", err)
	}
	if n == 0 {
		return ErrNotGroupMember
	}
	if _, err := s.db.ExecContext(ctx,
		`UPDATE shopping_list_items SET item_id = ?, touched = 1 WHERE id = ? AND household_id = ?`,
		itemID, lineID, household,
	); err != nil {
		return fmt.Errorf("could not change the product on this line: %w", err)
	}
	return nil
}

// ItemGroups maps each pantry item to its product group, when it has one.
func (s *Store) ItemGroups(ctx context.Context, scope tenant.Scope) (map[string]string, error) {
	if err := scope.Require(); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT i.id, m.group_id
		FROM items i
		JOIN product_group_members m ON m.product_id = i.product_id AND m.household_id = i.household_id
		JOIN product_groups g ON g.id = m.group_id AND g.household_id = i.household_id
		WHERE i.household_id = ?`, scope.HouseholdID())
	if err != nil {
		return nil, fmt.Errorf("could not read product groups: %w", err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var itemID, groupID string
		if err := rows.Scan(&itemID, &groupID); err != nil {
			return nil, fmt.Errorf("could not read product groups: %w", err)
		}
		out[itemID] = groupID
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("could not read product groups: %w", err)
	}
	return out, nil
}

func (s *Store) fillGroupMembers(ctx context.Context, scope tenant.Scope, items []ShoppingListItem) ([]ShoppingListItem, error) {
	if err := scope.Require(); err != nil {
		return nil, err
	}
	needed := false
	for _, item := range items {
		if item.GroupID != "" {
			needed = true
			break
		}
	}
	if !needed {
		return items, nil
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT m.group_id, i.id, p.id, `+product.DisplayNameSQL("p")+`
		FROM product_group_members m
		JOIN product_groups g ON g.id = m.group_id AND g.household_id = m.household_id
		JOIN items i ON i.product_id = m.product_id AND i.household_id = m.household_id
		JOIN products p ON p.id = m.product_id
		WHERE m.household_id = ?
		ORDER BY `+product.DisplayNameSQL("p")+`, i.id`,
		scope.HouseholdID(), scope.HouseholdID(), scope.HouseholdID())
	if err != nil {
		return nil, fmt.Errorf("could not read product groups: %w", err)
	}
	defer rows.Close()
	choices := map[string][]GroupChoice{}
	for rows.Next() {
		var groupID string
		var choice GroupChoice
		if err := rows.Scan(&groupID, &choice.ItemID, &choice.ProductID, &choice.Name); err != nil {
			return nil, fmt.Errorf("could not read product groups: %w", err)
		}
		choices[groupID] = append(choices[groupID], choice)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("could not read product groups: %w", err)
	}
	for i := range items {
		if items[i].GroupID == "" {
			continue
		}
		items[i].GroupMembers = choices[items[i].GroupID]
		if items[i].GroupMembers == nil {
			items[i].GroupMembers = []GroupChoice{}
		}
	}
	return items, nil
}

// RemoveAdjustment removes the adjustment for one entry and provider.
func (s *Store) RemoveAdjustment(ctx context.Context, scope tenant.Scope, entryID, providerID string) error {
	if err := scope.Require(); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM shopping_list_entry_adjustments WHERE household_id = ? AND entry_id = ? AND provider_id = ?`,
		scope.HouseholdID(), entryID, providerID)
	return err
}
