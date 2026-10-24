// Package export builds a household's Export format v1 zip.
// The file holds that household's rows and the shared products those rows
// reference. It does not hold another household's overrides, and it does not
// hold secrets or membership.
package export

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
)

// ErrNotFound is a household id that is not in the database.
var ErrNotFound = fmt.Errorf("household not found")

// Bundle is the zip and the three files inside it.
type Bundle struct {
	JSON        []byte
	ItemsCSV    []byte
	ShoppingCSV []byte
	Zip         []byte
	Document    Document
}

// Document is Export format v1. Item, instance, scan, shopping-list, move,
// group, and category ids are the household's own ids, unchanged. Product ids
// stay the catalog ids the household already stored. Import remaps products.
type Document struct {
	SchemaVersion     int                `json:"schemaVersion"`
	ImportID          string             `json:"importId"`
	ExportedAt        string             `json:"exportedAt"`
	Source            string             `json:"source"`
	HouseholdName     string             `json:"householdName"`
	LegacyUserID      string             `json:"legacyUserId,omitempty"`
	Products          []Product          `json:"products"`
	BarcodeOverrides  []BarcodeOverride  `json:"barcodeOverrides"`
	Items             []Item             `json:"items"`
	ItemInstances     []ItemInstance     `json:"itemInstances"`
	ScanEntries       []ScanEntry        `json:"scanEntries"`
	ShoppingListItems []ShoppingListItem `json:"shoppingListItems"`
	StockMoves        []StockMove        `json:"stockMoves"`
	Groups            []Group            `json:"groups"`
	Categories        []Category         `json:"categories"`
	Settings          Settings           `json:"settings"`
}

// Product is one shared catalog row this household actually references.
type Product struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Category      string   `json:"category"`
	UnitOfMeasure string   `json:"unitOfMeasure"`
	Barcodes      []string `json:"barcodes"`
}

// BarcodeOverride is a barcode this household typed. Global barcodes stay on the product.
type BarcodeOverride struct {
	Barcode   string `json:"barcode"`
	ProductID string `json:"productId"`
}

// Item is one inventory row. Category, group, and stock target feed items.csv.
type Item struct {
	ID          string   `json:"id"`
	ProductID   string   `json:"productId"`
	Category    *string  `json:"category"`
	Group       *string  `json:"group"`
	StockTarget *float64 `json:"stockTarget"`
	CreatedAt   string   `json:"createdAt"`
}

// ItemInstance is one unit on the shelf or one unit already taken off.
type ItemInstance struct {
	ID            string  `json:"id"`
	ItemID        string  `json:"itemId"`
	StockInAt     string  `json:"stockInAt"`
	ExpiresAt     *string `json:"expiresAt"`
	RemovedAt     *string `json:"removedAt"`
	RemovalReason *string `json:"removalReason"`
	CreatedAt     string  `json:"createdAt"`
}

// ScanEntry is one scan. productId is the file's product id.
type ScanEntry struct {
	ID          string  `json:"id"`
	Barcode     string  `json:"barcode"`
	ScannedAt   string  `json:"scannedAt"`
	Direction   *string `json:"direction"`
	UnitCount   int     `json:"unitCount"`
	ExpiresAt   *string `json:"expiresAt"`
	Status      string  `json:"status"`
	ProductID   *string `json:"productId"`
	CommittedAt *string `json:"committedAt"`
	CreatedAt   string  `json:"createdAt"`
}

// ShoppingListItem is one line on the list.
type ShoppingListItem struct {
	ID          string  `json:"id"`
	ItemID      string  `json:"itemId"`
	Quantity    int     `json:"quantity"`
	Source      string  `json:"source"`
	PurchasedAt *string `json:"purchasedAt"`
	CreatedAt   string  `json:"createdAt"`
}

// StockMove is one stock in or stock out.
type StockMove struct {
	ID          string  `json:"id"`
	ItemID      string  `json:"itemId"`
	ProductID   string  `json:"productId"`
	Direction   string  `json:"direction"`
	Quantity    int     `json:"quantity"`
	At          string  `json:"at"`
	Source      string  `json:"source"`
	ScanEntryID *string `json:"scanEntryId"`
	CreatedAt   string  `json:"createdAt"`
}

// Group is a product group and the products in it.
type Group struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	ProductIDs []string `json:"productIds"`
}

// Category is a category and the items in it.
type Category struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	ItemIDs []string `json:"itemIds"`
}

// Settings is the allowlist. channel_key is never a field.
type Settings struct {
	RestockMode   string `json:"restockMode"`
	SupplyDefault string `json:"supplyDefault"`
}

type rawItem struct {
	id, productID, createdAt string
	target                   sql.NullFloat64
}

type rawInstance struct {
	id, itemID, stockIn, created string
	expires, removed, reason     sql.NullString
}

// Build reads one household and returns its zip. now is the export clock.
func Build(ctx context.Context, db *sql.DB, householdID string, now time.Time) (Bundle, error) {
	if db == nil || householdID == "" {
		return Bundle{}, ErrNotFound
	}
	var name, mode, legacy string
	err := db.QueryRowContext(ctx, `
		SELECT name, restock_mode, legacy_user_id FROM households WHERE id = ?`, householdID).Scan(&name, &mode, &legacy)
	if err == sql.ErrNoRows {
		return Bundle{}, ErrNotFound
	}
	if err != nil {
		return Bundle{}, err
	}
	if mode == "" {
		mode = "auto"
	}

	products, err := loadProducts(ctx, db, householdID)
	if err != nil {
		return Bundle{}, err
	}
	overrides, err := loadOverrides(ctx, db, householdID)
	if err != nil {
		return Bundle{}, err
	}
	items, err := loadItems(ctx, db, householdID)
	if err != nil {
		return Bundle{}, err
	}
	instances, err := loadInstances(ctx, db, householdID)
	if err != nil {
		return Bundle{}, err
	}
	scans, err := loadScans(ctx, db, householdID)
	if err != nil {
		return Bundle{}, err
	}
	shopping, err := loadShopping(ctx, db, householdID)
	if err != nil {
		return Bundle{}, err
	}
	moves, err := loadMoves(ctx, db, householdID)
	if err != nil {
		return Bundle{}, err
	}
	groups, members, err := loadGroups(ctx, db, householdID)
	if err != nil {
		return Bundle{}, err
	}
	categories, links, err := loadCategories(ctx, db, householdID)
	if err != nil {
		return Bundle{}, err
	}
	itemCategory, err := loadItemCategory(ctx, db, householdID)
	if err != nil {
		return Bundle{}, err
	}
	itemGroup, err := loadItemGroup(ctx, db, householdID)
	if err != nil {
		return Bundle{}, err
	}
	itemTarget, err := loadItemTarget(ctx, db, householdID)
	if err != nil {
		return Bundle{}, err
	}
	supply, err := loadSupplyDefault(ctx, db, householdID)
	if err != nil {
		return Bundle{}, err
	}
	globals, err := loadGlobalBarcodes(ctx, db, householdID, productIDs(products))
	if err != nil {
		return Bundle{}, err
	}

	doc := Document{
		SchemaVersion:     1,
		ImportID:          uuid.NewString(),
		ExportedAt:        now.UTC().Format(time.RFC3339),
		Source:            "pi",
		HouseholdName:     name,
		LegacyUserID:      legacy,
		Products:          make([]Product, 0, len(products)),
		BarcodeOverrides:  make([]BarcodeOverride, 0, len(overrides)),
		Items:             make([]Item, 0, len(items)),
		ItemInstances:     make([]ItemInstance, 0, len(instances)),
		ScanEntries:       make([]ScanEntry, 0, len(scans)),
		ShoppingListItems: make([]ShoppingListItem, 0, len(shopping)),
		StockMoves:        make([]StockMove, 0, len(moves)),
		Groups:            make([]Group, 0, len(groups)),
		Categories:        make([]Category, 0, len(categories)),
		Settings:          Settings{RestockMode: mode, SupplyDefault: supply},
	}
	for _, id := range sortedKeys(products) {
		row := products[id]
		barcodes := append([]string(nil), globals[id]...)
		doc.Products = append(doc.Products, Product{
			ID: id, Name: row.name, Category: row.category,
			UnitOfMeasure: row.unit, Barcodes: barcodes,
		})
	}
	for _, row := range overrides {
		doc.BarcodeOverrides = append(doc.BarcodeOverrides, BarcodeOverride{
			Barcode: row.barcode, ProductID: row.productID,
		})
	}
	for _, row := range items {
		target := row.target
		if v, ok := itemTarget[row.id]; ok {
			target = sql.NullFloat64{Float64: v, Valid: true}
		}
		doc.Items = append(doc.Items, Item{
			ID: row.id, ProductID: row.productID,
			Category: opt(itemCategory[row.id]), Group: opt(itemGroup[row.productID]),
			StockTarget: floatPtr(target), CreatedAt: rfc3339(row.createdAt),
		})
	}
	for _, row := range instances {
		doc.ItemInstances = append(doc.ItemInstances, ItemInstance{
			ID: row.id, ItemID: row.itemID,
			StockInAt: rfc3339(row.stockIn), ExpiresAt: optTime(row.expires),
			RemovedAt: optTime(row.removed), RemovalReason: optNull(row.reason),
			CreatedAt: rfc3339(row.created),
		})
	}
	for _, row := range scans {
		entry := ScanEntry{
			ID: row.id, Barcode: row.barcode, ScannedAt: rfc3339(row.scannedAt),
			Direction: optNull(row.direction), UnitCount: row.unitCount, ExpiresAt: optTime(row.expires),
			Status: row.status, CommittedAt: optTime(row.committed), CreatedAt: rfc3339(row.createdAt),
		}
		if row.productID.Valid && row.productID.String != "" {
			entry.ProductID = opt(row.productID.String)
		}
		doc.ScanEntries = append(doc.ScanEntries, entry)
	}
	for _, row := range shopping {
		doc.ShoppingListItems = append(doc.ShoppingListItems, ShoppingListItem{
			ID: row.id, ItemID: row.itemID,
			Quantity: row.quantity, Source: row.source, PurchasedAt: optTime(row.purchased),
			CreatedAt: rfc3339(row.createdAt),
		})
	}
	for _, row := range moves {
		move := StockMove{
			ID: row.id, ItemID: row.itemID,
			ProductID: row.productID, Direction: row.direction,
			Quantity: row.quantity, At: rfc3339(row.at), Source: row.source,
			CreatedAt: rfc3339(row.createdAt),
		}
		if row.scanID.Valid && row.scanID.String != "" {
			move.ScanEntryID = opt(row.scanID.String)
		}
		doc.StockMoves = append(doc.StockMoves, move)
	}
	for _, id := range sortedKeys(groups) {
		productIDs := append([]string(nil), members[id]...)
		if productIDs == nil {
			productIDs = []string{}
		}
		doc.Groups = append(doc.Groups, Group{ID: id, Name: groups[id], ProductIDs: productIDs})
	}
	for _, id := range sortedKeys(categories) {
		itemIDs := append([]string(nil), links[id]...)
		if itemIDs == nil {
			itemIDs = []string{}
		}
		doc.Categories = append(doc.Categories, Category{ID: id, Name: categories[id], ItemIDs: itemIDs})
	}
	for i := range doc.Products {
		if doc.Products[i].Barcodes == nil {
			doc.Products[i].Barcodes = []string{}
		}
	}

	raw, err := json.Marshal(doc)
	if err != nil {
		return Bundle{}, err
	}
	itemsCSV, err := itemsCSV(doc, instances, items, overrides, globals)
	if err != nil {
		return Bundle{}, err
	}
	shoppingCSV, err := shoppingCSV(doc)
	if err != nil {
		return Bundle{}, err
	}
	zipped, err := zipFiles(raw, itemsCSV, shoppingCSV)
	if err != nil {
		return Bundle{}, err
	}
	return Bundle{JSON: raw, ItemsCSV: itemsCSV, ShoppingCSV: shoppingCSV, Zip: zipped, Document: doc}, nil
}

type productRow struct {
	name, category, unit string
}

func loadProducts(ctx context.Context, db *sql.DB, householdID string) (map[string]productRow, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT p.id,
			COALESCE(NULLIF((SELECT o.display_name FROM household_product_overrides o
				WHERE o.product_id = p.id AND o.household_id = ?), ''), p.name),
			COALESCE(p.category, ''),
			COALESCE(p.unit_of_measure, '')
		FROM products p
		WHERE p.id IN (SELECT product_id FROM items WHERE household_id = ?)
		   OR p.id IN (SELECT product_id FROM scan_entries WHERE household_id = ? AND product_id IS NOT NULL AND product_id != '')
		   OR p.id IN (SELECT product_id FROM stock_moves WHERE household_id = ?)
		   OR p.id IN (SELECT m.product_id FROM product_group_members m WHERE m.household_id = ?)
		   OR p.id IN (SELECT b.product_id FROM barcodes b WHERE b.household_id = ? AND b.source = 'user_override')
		   OR p.id IN (SELECT o2.product_id FROM household_product_overrides o2 WHERE o2.household_id = ?)
		ORDER BY p.id`,
		householdID, householdID, householdID, householdID, householdID, householdID, householdID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]productRow{}
	for rows.Next() {
		var id string
		var row productRow
		if err := rows.Scan(&id, &row.name, &row.category, &row.unit); err != nil {
			return nil, err
		}
		out[id] = row
	}
	return out, rows.Err()
}

type overrideRow struct {
	barcode, productID string
}

func loadOverrides(ctx context.Context, db *sql.DB, householdID string) ([]overrideRow, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT barcode, product_id FROM barcodes
		WHERE household_id = ? AND source = 'user_override'
		ORDER BY barcode, product_id`, householdID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []overrideRow
	for rows.Next() {
		var row overrideRow
		if err := rows.Scan(&row.barcode, &row.productID); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func loadGlobalBarcodes(ctx context.Context, db *sql.DB, householdID string, productIDs []string) (map[string][]string, error) {
	out := map[string][]string{}
	for _, id := range productIDs {
		rows, err := db.QueryContext(ctx, `
			SELECT barcode FROM barcodes
			WHERE product_id = ? AND source = 'global' AND user_id = '' AND household_id = ?
			ORDER BY barcode`, id, householdID)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var barcode string
			if err := rows.Scan(&barcode); err != nil {
				rows.Close()
				return nil, err
			}
			out[id] = append(out[id], barcode)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
		if out[id] == nil {
			out[id] = []string{}
		}
	}
	return out, nil
}

func loadItems(ctx context.Context, db *sql.DB, householdID string) ([]rawItem, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, product_id, created_at, target_quantity
		FROM items WHERE household_id = ? ORDER BY id`, householdID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []rawItem
	for rows.Next() {
		var row rawItem
		if err := rows.Scan(&row.id, &row.productID, &row.createdAt, &row.target); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func loadInstances(ctx context.Context, db *sql.DB, householdID string) ([]rawInstance, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, item_id, stock_in_at, expires_at, removed_at, removal_reason, created_at
		FROM item_instances WHERE household_id = ? ORDER BY id`, householdID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []rawInstance
	for rows.Next() {
		var row rawInstance
		if err := rows.Scan(&row.id, &row.itemID, &row.stockIn, &row.expires, &row.removed, &row.reason, &row.created); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

type rawScan struct {
	id, barcode, scannedAt, status, createdAt string
	unitCount                                 int
	direction, expires, productID, committed  sql.NullString
}

func loadScans(ctx context.Context, db *sql.DB, householdID string) ([]rawScan, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, barcode, scanned_at, direction, unit_count, expires_at, status, product_id, committed_at, created_at
		FROM scan_entries WHERE household_id = ? ORDER BY id`, householdID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []rawScan
	for rows.Next() {
		var row rawScan
		if err := rows.Scan(&row.id, &row.barcode, &row.scannedAt, &row.direction, &row.unitCount, &row.expires, &row.status, &row.productID, &row.committed, &row.createdAt); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

type rawShop struct {
	id, itemID, source, createdAt string
	quantity                      int
	purchased                     sql.NullString
}

func loadShopping(ctx context.Context, db *sql.DB, householdID string) ([]rawShop, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, item_id, quantity, source, purchased_at, created_at
		FROM shopping_list_items WHERE household_id = ? ORDER BY id`, householdID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []rawShop
	for rows.Next() {
		var row rawShop
		if err := rows.Scan(&row.id, &row.itemID, &row.quantity, &row.source, &row.purchased, &row.createdAt); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

type rawMove struct {
	id, itemID, productID, direction, source, at, createdAt string
	quantity                                                int
	scanID                                                  sql.NullString
}

func loadMoves(ctx context.Context, db *sql.DB, householdID string) ([]rawMove, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, item_id, product_id, direction, quantity, at, source, scan_entry_id, created_at
		FROM stock_moves WHERE household_id = ? ORDER BY id`, householdID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []rawMove
	for rows.Next() {
		var row rawMove
		if err := rows.Scan(&row.id, &row.itemID, &row.productID, &row.direction, &row.quantity, &row.at, &row.source, &row.scanID, &row.createdAt); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func loadGroups(ctx context.Context, db *sql.DB, householdID string) (map[string]string, map[string][]string, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, name FROM product_groups WHERE household_id = ? ORDER BY id`, householdID)
	if err != nil {
		return nil, nil, err
	}
	names := map[string]string{}
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			rows.Close()
			return nil, nil, err
		}
		names[id] = name
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, nil, err
	}
	rows.Close()
	memberRows, err := db.QueryContext(ctx, `
		SELECT group_id, product_id FROM product_group_members
		WHERE household_id = ? ORDER BY group_id, product_id`, householdID)
	if err != nil {
		return nil, nil, err
	}
	defer memberRows.Close()
	members := map[string][]string{}
	for memberRows.Next() {
		var groupID, productID string
		if err := memberRows.Scan(&groupID, &productID); err != nil {
			return nil, nil, err
		}
		members[groupID] = append(members[groupID], productID)
	}
	return names, members, memberRows.Err()
}

func loadCategories(ctx context.Context, db *sql.DB, householdID string) (map[string]string, map[string][]string, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, name FROM categories WHERE household_id = ? ORDER BY id`, householdID)
	if err != nil {
		return nil, nil, err
	}
	names := map[string]string{}
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			rows.Close()
			return nil, nil, err
		}
		names[id] = name
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, nil, err
	}
	rows.Close()
	linkRows, err := db.QueryContext(ctx, `
		SELECT category_id, item_id FROM item_categories
		WHERE household_id = ? AND removed_at IS NULL
		ORDER BY category_id, item_id`, householdID)
	if err != nil {
		return nil, nil, err
	}
	defer linkRows.Close()
	links := map[string][]string{}
	for linkRows.Next() {
		var categoryID, itemID string
		if err := linkRows.Scan(&categoryID, &itemID); err != nil {
			return nil, nil, err
		}
		links[categoryID] = append(links[categoryID], itemID)
	}
	return names, links, linkRows.Err()
}

func loadItemCategory(ctx context.Context, db *sql.DB, householdID string) (map[string]string, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT ic.item_id, c.name
		FROM item_categories ic
		JOIN categories c ON c.id = ic.category_id AND c.household_id = ic.household_id
		WHERE ic.household_id = ? AND ic.removed_at IS NULL
		ORDER BY ic.item_id, c.name`, householdID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var itemID, name string
		if err := rows.Scan(&itemID, &name); err != nil {
			return nil, err
		}
		if _, ok := out[itemID]; !ok {
			out[itemID] = name
		}
	}
	return out, rows.Err()
}

func loadItemGroup(ctx context.Context, db *sql.DB, householdID string) (map[string]string, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT m.product_id, g.name
		FROM product_group_members m
		JOIN product_groups g ON g.id = m.group_id AND g.household_id = m.household_id
		WHERE m.household_id = ?
		ORDER BY m.product_id, g.name`, householdID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var productID, name string
		if err := rows.Scan(&productID, &name); err != nil {
			return nil, err
		}
		if _, ok := out[productID]; !ok {
			out[productID] = name
		}
	}
	return out, rows.Err()
}

func loadItemTarget(ctx context.Context, db *sql.DB, householdID string) (map[string]float64, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT item_id, value FROM item_targets WHERE household_id = ? ORDER BY item_id`, householdID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]float64{}
	for rows.Next() {
		var id string
		var value float64
		if err := rows.Scan(&id, &value); err != nil {
			return nil, err
		}
		out[id] = value
	}
	return out, rows.Err()
}

func loadSupplyDefault(ctx context.Context, db *sql.DB, householdID string) (string, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT key, value FROM household_settings
		WHERE household_id = ? AND key IN ('supply_target_kind', 'supply_target_value')`, householdID)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	vals := map[string]string{}
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return "", err
		}
		vals[key] = value
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	if vals["supply_target_kind"] != "" && vals["supply_target_value"] != "" {
		return vals["supply_target_kind"] + ":" + vals["supply_target_value"], nil
	}
	if vals["supply_target_value"] != "" {
		return vals["supply_target_value"], nil
	}
	return vals["supply_target_kind"], nil
}

func itemsCSV(doc Document, instances []rawInstance, items []rawItem, overrides []overrideRow, globals map[string][]string) ([]byte, error) {
	onHand := map[string]int{}
	expires := map[string]string{}
	for _, row := range instances {
		if row.removed.Valid && row.removed.String != "" {
			continue
		}
		onHand[row.itemID]++
		if row.expires.Valid && row.expires.String != "" {
			when := dateOnly(row.expires.String)
			if prev, ok := expires[row.itemID]; !ok || when < prev {
				expires[row.itemID] = when
			}
		}
	}
	overrideBarcode := map[string]string{}
	for _, row := range overrides {
		if _, ok := overrideBarcode[row.productID]; !ok {
			overrideBarcode[row.productID] = row.barcode
		}
	}
	sourceByItem := map[string]rawItem{}
	for _, row := range items {
		sourceByItem[row.id] = row
	}
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	if err := w.Write(csvRow([]string{"name", "barcode", "on-hand count", "expires", "category", "group", "stock target"})); err != nil {
		return nil, err
	}
	names := map[string]string{}
	for _, product := range doc.Products {
		names[product.ID] = product.Name
	}
	for _, item := range doc.Items {
		source := sourceByItem[item.ID]
		barcode := overrideBarcode[source.productID]
		if barcode == "" && len(globals[source.productID]) > 0 {
			barcode = globals[source.productID][0]
		}
		target := ""
		if item.StockTarget != nil {
			target = trimFloat(*item.StockTarget)
		}
		if err := w.Write(csvRow([]string{
			names[item.ProductID], barcode, fmt.Sprintf("%d", onHand[source.id]), expires[source.id],
			deref(item.Category), deref(item.Group), target,
		})); err != nil {
			return nil, err
		}
	}
	w.Flush()
	return buf.Bytes(), w.Error()
}

func shoppingCSV(doc Document) ([]byte, error) {
	sourceItem := map[string]string{}
	for _, item := range doc.Items {
		sourceItem[item.ID] = item.ProductID
	}
	names := map[string]string{}
	for _, product := range doc.Products {
		names[product.ID] = product.Name
	}
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	if err := w.Write(csvRow([]string{"name", "quantity"})); err != nil {
		return nil, err
	}
	for _, line := range doc.ShoppingListItems {
		if err := w.Write(csvRow([]string{names[sourceItem[line.ItemID]], fmt.Sprintf("%d", line.Quantity)})); err != nil {
			return nil, err
		}
	}
	w.Flush()
	return buf.Bytes(), w.Error()
}

func zipFiles(doc, items, shopping []byte) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	files := []struct {
		name string
		body []byte
	}{
		{"stockbot-export.json", doc},
		{"items.csv", items},
		{"shopping-list.csv", shopping},
	}
	for _, file := range files {
		w, err := zw.Create(file.name)
		if err != nil {
			return nil, err
		}
		if _, err := w.Write(file.body); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// csvRow neutralizes cells a spreadsheet would treat as formulas.
// A numeric literal is left alone so counts and targets stay numbers.
func csvRow(cells []string) []string {
	out := make([]string, len(cells))
	for i, cell := range cells {
		out[i] = csvCell(cell)
	}
	return out
}

func csvCell(value string) string {
	if value == "" || numericLiteral(value) {
		return value
	}
	switch value[0] {
	case '=', '+', '-', '@', '\t', '\r':
		return "'" + value
	default:
		return value
	}
}

func numericLiteral(value string) bool {
	if value == "" {
		return false
	}
	i := 0
	if value[0] == '-' {
		i++
		if i >= len(value) {
			return false
		}
	}
	dot := false
	digits := 0
	for ; i < len(value); i++ {
		switch value[i] {
		case '.':
			if dot {
				return false
			}
			dot = true
		case '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
			digits++
		default:
			return false
		}
	}
	return digits > 0
}

func sortedKeys[V any](in map[string]V) []string {
	out := make([]string, 0, len(in))
	for key := range in {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func productIDs(in map[string]productRow) []string { return sortedKeys(in) }

func opt(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func optNull(value sql.NullString) *string {
	if !value.Valid || value.String == "" {
		return nil
	}
	return &value.String
}

func optTime(value sql.NullString) *string {
	if !value.Valid || value.String == "" {
		return nil
	}
	formatted := rfc3339(value.String)
	return &formatted
}

func deref(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func floatPtr(value sql.NullFloat64) *float64 {
	if !value.Valid {
		return nil
	}
	v := value.Float64
	return &v
}

func trimFloat(value float64) string {
	if value == float64(int64(value)) {
		return fmt.Sprintf("%d", int64(value))
	}
	return fmt.Sprintf("%g", value)
}

func rfc3339(raw string) string {
	if raw == "" {
		return ""
	}
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05.999999999 -0700 MST",
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05",
		"2006-01-02",
	}
	for _, layout := range layouts {
		if parsed, err := time.Parse(layout, raw); err == nil {
			return parsed.UTC().Format(time.RFC3339)
		}
	}
	return raw
}

func dateOnly(raw string) string {
	formatted := rfc3339(raw)
	if len(formatted) >= 10 {
		return formatted[:10]
	}
	return formatted
}
