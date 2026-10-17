// Package syncsnap is the read-only picture grocy-sync reconciles from.
// It is one household (user-1). Prices are not included.
package syncsnap

import (
	"context"
	"database/sql"
	"math"
	"sort"
	"time"

	"github.com/Rhionin/stockbot/internal/group"
	"github.com/Rhionin/stockbot/internal/restock"
)

const householdUser = "user-1"

// Snapshot is the pantry state a one-way sync needs.
// Slices are empty rather than null so a client can range without a nil check.
type Snapshot struct {
	GeneratedAt  time.Time      `json:"generatedAt"`
	Products     []Product      `json:"products"`
	Groups       []Group        `json:"groups"`
	Instances    []Instance     `json:"instances"`
	Removals     []Removal      `json:"removals"`
	Moves        []Move         `json:"moves"`
	ShoppingList []ShoppingLine `json:"shoppingList"`
}

// Product is one catalog row plus the household's fixed count target, if any.
type Product struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Category       string   `json:"category,omitempty"`
	UnitOfMeasure  string   `json:"unitOfMeasure,omitempty"`
	Brand          string   `json:"brand,omitempty"`
	NetBaseValue   *float64 `json:"netBaseValue,omitempty"`
	NetDimension   string   `json:"netDimension,omitempty"`
	PackCount      *int     `json:"packCount,omitempty"`
	Barcodes       []string `json:"barcodes"`
	TargetQuantity *int     `json:"targetQuantity,omitempty"`
	GroupID        string   `json:"groupId,omitempty"`
	NoRestock      bool     `json:"noRestock,omitempty"`
}

// Group is a named set of products. QuantityBase is grams or milliliters.
// A window and a quantity are not a fixed item count.
type Group struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Rule            string   `json:"rule"`
	PinnedProductID string   `json:"pinnedProductId,omitempty"`
	WindowMonths    *int     `json:"windowMonths,omitempty"`
	QuantityBase    *float64 `json:"quantityBase,omitempty"`
	Dimension       string   `json:"dimension,omitempty"`
	MemberIDs       []string `json:"memberIds"`
}

// Instance is one unit still on the shelf.
type Instance struct {
	ID          string     `json:"id"`
	ProductID   string     `json:"productId"`
	StockInAt   time.Time  `json:"stockInAt"`
	ExpiresAt   *time.Time `json:"expiresAt,omitempty"`
	StockMoveID string     `json:"stockMoveId,omitempty"`
}

// Removal is one unit that left the shelf.
// StockInAt and ExpiresAt are the dates that unit was put on the shelf.
type Removal struct {
	InstanceID      string     `json:"instanceId"`
	ProductID       string     `json:"productId"`
	Reason          string     `json:"reason,omitempty"`
	StockMoveID     string     `json:"stockMoveId,omitempty"`
	RemovedByMoveID string     `json:"removedByMoveId,omitempty"`
	StockInAt       time.Time  `json:"stockInAt"`
	ExpiresAt       *time.Time `json:"expiresAt,omitempty"`
}

// Move is one stock-in or stock-out the household can still correct.
type Move struct {
	ID        string    `json:"id"`
	ItemID    string    `json:"itemId"`
	ProductID string    `json:"productId"`
	Direction string    `json:"direction"`
	Quantity  int       `json:"quantity"`
	At        time.Time `json:"at"`
	Source    string    `json:"source"`
}

// ShoppingLine is an open line. Purchased lines are omitted.
// GroupID set means the line was planned for that group.
type ShoppingLine struct {
	ID        string `json:"id"`
	ProductID string `json:"productId"`
	GroupID   string `json:"groupId,omitempty"`
	Quantity  int    `json:"quantity"`
	Source    string `json:"source"`
}

// Build reads the household's current pantry. now is the snapshot's generatedAt.
func Build(ctx context.Context, db *sql.DB, now time.Time) (Snapshot, error) {
	snap := emptySnapshot(now)
	products, err := loadProducts(ctx, db)
	if err != nil {
		return Snapshot{}, err
	}
	byID := make(map[string]*Product, len(products))
	for i := range products {
		byID[products[i].ID] = &products[i]
	}
	if err := attachBarcodes(ctx, db, byID); err != nil {
		return Snapshot{}, err
	}
	if err := attachItems(ctx, db, byID); err != nil {
		return Snapshot{}, err
	}
	groups, err := loadGroups(ctx, db)
	if err != nil {
		return Snapshot{}, err
	}
	groupByID := make(map[string]*Group, len(groups))
	for i := range groups {
		groupByID[groups[i].ID] = &groups[i]
	}
	if err := attachMembers(ctx, db, byID, groupByID); err != nil {
		return Snapshot{}, err
	}
	if err := attachItemTargets(ctx, db, byID); err != nil {
		return Snapshot{}, err
	}
	if err := applyResolvedRestock(ctx, db, byID); err != nil {
		return Snapshot{}, err
	}
	for i := range products {
		title := ""
		if g := groupByID[products[i].GroupID]; g != nil {
			title = g.Name
		}
		products[i].Brand = group.ProductBrand(products[i].Name, title)
	}
	for i := range products {
		sort.Strings(products[i].Barcodes)
	}
	snap.Products = products
	snap.Groups = groups
	snap.Instances, err = loadInstances(ctx, db)
	if err != nil {
		return Snapshot{}, err
	}
	snap.Removals, err = loadRemovals(ctx, db)
	if err != nil {
		return Snapshot{}, err
	}
	snap.Moves, err = loadMoves(ctx, db)
	if err != nil {
		return Snapshot{}, err
	}
	snap.ShoppingList, err = loadShopping(ctx, db)
	if err != nil {
		return Snapshot{}, err
	}
	return snap, nil
}

func emptySnapshot(now time.Time) Snapshot {
	return Snapshot{
		GeneratedAt:  now.UTC(),
		Products:     []Product{},
		Groups:       []Group{},
		Instances:    []Instance{},
		Removals:     []Removal{},
		Moves:        []Move{},
		ShoppingList: []ShoppingLine{},
	}
}

func loadProducts(ctx context.Context, db *sql.DB) ([]Product, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, name, COALESCE(category, ''), COALESCE(unit_of_measure, ''),
		       net_base_value, COALESCE(net_dimension, ''), pack_count
		FROM products
		ORDER BY name, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Product{}
	for rows.Next() {
		var p Product
		var base sql.NullFloat64
		var pack sql.NullInt64
		if err := rows.Scan(&p.ID, &p.Name, &p.Category, &p.UnitOfMeasure, &base, &p.NetDimension, &pack); err != nil {
			return nil, err
		}
		if base.Valid {
			v := base.Float64
			p.NetBaseValue = &v
		}
		if pack.Valid {
			n := int(pack.Int64)
			p.PackCount = &n
		}
		p.Barcodes = []string{}
		out = append(out, p)
	}
	return out, rows.Err()
}

// attachBarcodes keeps one row per barcode. A household override wins over the
// global row so the sync follows the name the household actually uses.
func attachBarcodes(ctx context.Context, db *sql.DB, byID map[string]*Product) error {
	rows, err := db.QueryContext(ctx, `
		SELECT barcode, product_id, source FROM barcodes
		ORDER BY barcode, source`)
	if err != nil {
		return err
	}
	defer rows.Close()
	chosen := map[string]string{}
	for rows.Next() {
		var barcode, productID, source string
		if err := rows.Scan(&barcode, &productID, &source); err != nil {
			return err
		}
		if _, ok := byID[productID]; !ok {
			continue
		}
		// source sorts global before user_override, so the override replaces.
		if prev, ok := chosen[barcode]; ok && prev != productID {
			removeBarcode(byID[prev], barcode)
		}
		chosen[barcode] = productID
		if !hasBarcode(byID[productID], barcode) {
			byID[productID].Barcodes = append(byID[productID].Barcodes, barcode)
		}
	}
	return rows.Err()
}

func hasBarcode(p *Product, barcode string) bool {
	for _, b := range p.Barcodes {
		if b == barcode {
			return true
		}
	}
	return false
}

func removeBarcode(p *Product, barcode string) {
	kept := p.Barcodes[:0]
	for _, b := range p.Barcodes {
		if b != barcode {
			kept = append(kept, b)
		}
	}
	p.Barcodes = kept
}

func attachItems(ctx context.Context, db *sql.DB, byID map[string]*Product) error {
	rows, err := db.QueryContext(ctx, `
		SELECT product_id, target_quantity FROM items WHERE user_id = ?`, householdUser)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var productID string
		var target sql.NullInt64
		if err := rows.Scan(&productID, &target); err != nil {
			return err
		}
		p := byID[productID]
		if p == nil || !target.Valid {
			continue
		}
		n := int(target.Int64)
		p.TargetQuantity = &n
	}
	return rows.Err()
}

func loadGroups(ctx context.Context, db *sql.DB) ([]Group, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, name, rule, COALESCE(pinned_product_id, ''),
		       window_months, quantity_base_value, COALESCE(quantity_dimension, '')
		FROM product_groups
		WHERE user_id = ?
		ORDER BY name, id`, householdUser)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Group{}
	for rows.Next() {
		var g Group
		var window sql.NullInt64
		var qty sql.NullFloat64
		if err := rows.Scan(&g.ID, &g.Name, &g.Rule, &g.PinnedProductID, &window, &qty, &g.Dimension); err != nil {
			return nil, err
		}
		if window.Valid {
			n := int(window.Int64)
			g.WindowMonths = &n
		}
		if qty.Valid {
			v := qty.Float64
			g.QuantityBase = &v
		}
		g.MemberIDs = []string{}
		out = append(out, g)
	}
	return out, rows.Err()
}

func attachMembers(ctx context.Context, db *sql.DB, products map[string]*Product, groups map[string]*Group) error {
	rows, err := db.QueryContext(ctx, `
		SELECT group_id, product_id
		FROM product_group_members
		ORDER BY group_id, product_id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var groupID, productID string
		if err := rows.Scan(&groupID, &productID); err != nil {
			return err
		}
		if g := groups[groupID]; g != nil {
			g.MemberIDs = append(g.MemberIDs, productID)
		}
		if p := products[productID]; p != nil {
			p.GroupID = groupID
		}
	}
	return rows.Err()
}

// attachItemTargets prefers the household item target over the older
// target_quantity column and the member flag. An amount becomes the fixed
// count. A value of 0 means don't restock.
func attachItemTargets(ctx context.Context, db *sql.DB, byProduct map[string]*Product) error {
	rows, err := db.QueryContext(ctx, `
		SELECT i.product_id, t.kind, t.value
		FROM item_targets t
		JOIN items i ON i.id = t.item_id AND i.user_id = t.household_id
		WHERE t.household_id = ?`, householdUser)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var productID, kind string
		var value float64
		if err := rows.Scan(&productID, &kind, &value); err != nil {
			return err
		}
		p := byProduct[productID]
		if p == nil {
			continue
		}
		if kind == "amount" && value > 0 {
			n := roundHalfUp(value)
			p.TargetQuantity = &n
		}
	}
	return rows.Err()
}

// applyResolvedRestock sets noRestock from the target the household actually
// uses. Zero at any level means the mobile client should not restock it.
func applyResolvedRestock(ctx context.Context, db *sql.DB, byProduct map[string]*Product) error {
	snap, err := restock.Load(ctx, db, restock.HouseholdID)
	if err != nil {
		return err
	}
	for _, item := range snap.Items {
		p := byProduct[item.ProductID]
		if p == nil {
			continue
		}
		p.NoRestock = item.Target.Value == 0
	}
	return nil
}

func roundHalfUp(x float64) int {
	if x <= 0 {
		return 0
	}
	return int(math.Floor(x + 0.5))
}

func loadInstances(ctx context.Context, db *sql.DB) ([]Instance, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT ii.id, i.product_id, ii.stock_in_at, ii.expires_at, COALESCE(ii.stock_move_id, '')
		FROM item_instances ii
		JOIN items i ON i.id = ii.item_id
		WHERE i.user_id = ? AND ii.removed_at IS NULL
		ORDER BY ii.stock_in_at, ii.id`, householdUser)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Instance{}
	for rows.Next() {
		var inst Instance
		var expires sql.NullTime
		if err := rows.Scan(&inst.ID, &inst.ProductID, &inst.StockInAt, &expires, &inst.StockMoveID); err != nil {
			return nil, err
		}
		inst.StockInAt = inst.StockInAt.UTC()
		if expires.Valid {
			t := expires.Time.UTC()
			inst.ExpiresAt = &t
		}
		out = append(out, inst)
	}
	return out, rows.Err()
}

func loadRemovals(ctx context.Context, db *sql.DB) ([]Removal, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT ii.id, i.product_id, COALESCE(ii.removal_reason, ''),
		       COALESCE(ii.stock_move_id, ''), COALESCE(ii.removed_by_move_id, ''),
		       ii.stock_in_at, ii.expires_at
		FROM item_instances ii
		JOIN items i ON i.id = ii.item_id
		WHERE i.user_id = ? AND ii.removed_at IS NOT NULL
		ORDER BY ii.id`, householdUser)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Removal{}
	for rows.Next() {
		var rm Removal
		var expires sql.NullTime
		if err := rows.Scan(&rm.InstanceID, &rm.ProductID, &rm.Reason, &rm.StockMoveID, &rm.RemovedByMoveID, &rm.StockInAt, &expires); err != nil {
			return nil, err
		}
		rm.StockInAt = rm.StockInAt.UTC()
		if expires.Valid {
			t := expires.Time.UTC()
			rm.ExpiresAt = &t
		}
		out = append(out, rm)
	}
	return out, rows.Err()
}

func loadMoves(ctx context.Context, db *sql.DB) ([]Move, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT m.id, m.item_id, m.product_id, m.direction, m.quantity, m.at, m.source
		FROM stock_moves m
		JOIN items i ON i.id = m.item_id
		WHERE i.user_id = ?
		ORDER BY m.at, m.id`, householdUser)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Move{}
	for rows.Next() {
		var m Move
		if err := rows.Scan(&m.ID, &m.ItemID, &m.ProductID, &m.Direction, &m.Quantity, &m.At, &m.Source); err != nil {
			return nil, err
		}
		m.At = m.At.UTC()
		out = append(out, m)
	}
	return out, rows.Err()
}

func loadShopping(ctx context.Context, db *sql.DB) ([]ShoppingLine, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT s.id, i.product_id, COALESCE(s.group_id, ''), s.quantity, s.source
		FROM shopping_list_items s
		JOIN items i ON i.id = s.item_id
		WHERE s.user_id = ? AND s.purchased_at IS NULL AND s.quantity > 0
		ORDER BY s.created_at, s.id`, householdUser)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ShoppingLine{}
	for rows.Next() {
		var line ShoppingLine
		if err := rows.Scan(&line.ID, &line.ProductID, &line.GroupID, &line.Quantity, &line.Source); err != nil {
			return nil, err
		}
		out = append(out, line)
	}
	return out, rows.Err()
}
