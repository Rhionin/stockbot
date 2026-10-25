package export

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/app"
)

const (
	northID = "0190aaaa-bbbb-7000-8000-0000000000a1"
	southID = "0190aaaa-bbbb-7000-8000-0000000000b2"
)

func TestBuildExportV1IsIsolatedAndRemapped(t *testing.T) {
	db := openExportDB(t)
	seedTwoHouseholds(t, db)
	when := time.Date(2026, 4, 2, 15, 4, 5, 0, time.UTC)
	bundle, err := Build(context.Background(), db, northID, when)
	if err != nil {
		t.Fatal(err)
	}
	again, err := Build(context.Background(), db, northID, when)
	if err != nil {
		t.Fatal(err)
	}

	dec := json.NewDecoder(bytes.NewReader(bundle.JSON))
	dec.DisallowUnknownFields()
	var doc Document
	if err := dec.Decode(&doc); err != nil {
		t.Fatalf("document: %v\n%s", err, bundle.JSON)
	}
	if doc.SchemaVersion != 1 || doc.Source != "pi" || doc.ExportedAt != "2026-04-02T15:04:05Z" {
		t.Fatalf("header %+v", doc)
	}
	if doc.HouseholdName != "north-pantry-z9" || doc.LegacyUserID != "" {
		t.Fatalf("household %+v", doc)
	}
	if doc.ImportID == "" || doc.ImportID == again.Document.ImportID {
		t.Fatal("import id was missing or reused")
	}
	if doc.Settings.RestockMode != "ask_hold" || doc.Settings.SupplyDefault != "months:2" {
		t.Fatalf("settings %+v", doc.Settings)
	}
	if doc.Settings.ProductBrands == nil || doc.Settings.ProductBrands["p-north"] != "north-brand-q7" {
		t.Fatalf("product brands %+v", doc.Settings.ProductBrands)
	}

	if len(doc.Products) != 1 || doc.Products[0].ID != "p-north" || doc.Products[0].Name != "north-typed-q7" {
		t.Fatalf("products %+v", doc.Products)
	}
	if doc.Products[0].Category != "dry" || doc.Products[0].UnitOfMeasure != "each" {
		t.Fatalf("product fields %+v", doc.Products[0])
	}
	if strings.Join(doc.Products[0].Barcodes, ",") != "global-bc-q7" {
		t.Fatalf("barcodes %+v", doc.Products[0].Barcodes)
	}
	if len(doc.BarcodeOverrides) != 1 || doc.BarcodeOverrides[0].Barcode != "north-override-bc-q7" || doc.BarcodeOverrides[0].ProductID != "p-north" {
		t.Fatalf("overrides %+v", doc.BarcodeOverrides)
	}

	if len(doc.Items) != 2 || doc.Items[0].ID != "i-1" || doc.Items[1].ID != "i-2" {
		t.Fatalf("items %+v", doc.Items)
	}
	if doc.Items[0].ProductID != "p-north" || doc.Items[0].Category == nil || *doc.Items[0].Category != "north-cat-q7" {
		t.Fatalf("item category %+v", doc.Items[0])
	}
	if doc.Items[0].Group == nil || *doc.Items[0].Group != "north-group-q7" || doc.Items[0].StockTarget == nil || *doc.Items[0].StockTarget != 4 {
		t.Fatalf("item group/target %+v", doc.Items[0])
	}
	if doc.Items[1].StockTarget == nil || *doc.Items[1].StockTarget != 1 {
		t.Fatalf("second target %+v", doc.Items[1])
	}
	if len(doc.ItemInstances) != 2 || doc.ItemInstances[0].ID != "n-1" || doc.ItemInstances[0].ItemID != "i-1" {
		t.Fatalf("instances %+v", doc.ItemInstances)
	}
	if doc.ItemInstances[0].ExpiresAt == nil || *doc.ItemInstances[0].ExpiresAt != "2026-06-01T00:00:00Z" {
		t.Fatalf("expires %+v", doc.ItemInstances[0])
	}
	if len(doc.ScanEntries) != 1 || doc.ScanEntries[0].ID != "s-north" || doc.ScanEntries[0].Barcode != "scan-north-q7" {
		t.Fatalf("scans %+v", doc.ScanEntries)
	}
	if doc.ScanEntries[0].ProductID == nil || *doc.ScanEntries[0].ProductID != "p-north" {
		t.Fatalf("scan product %+v", doc.ScanEntries[0])
	}
	if len(doc.ShoppingListItems) != 1 || doc.ShoppingListItems[0].ID != "shop-north" || doc.ShoppingListItems[0].Quantity != 3 {
		t.Fatalf("shopping %+v", doc.ShoppingListItems)
	}
	if len(doc.StockMoves) != 1 || doc.StockMoves[0].ID != "m-north" || doc.StockMoves[0].ScanEntryID == nil || *doc.StockMoves[0].ScanEntryID != "s-north" {
		t.Fatalf("moves %+v", doc.StockMoves)
	}
	if len(doc.Groups) != 1 || doc.Groups[0].ID != "g-north" || doc.Groups[0].Name != "north-group-q7" || strings.Join(doc.Groups[0].ProductIDs, ",") != "p-north" {
		t.Fatalf("groups %+v", doc.Groups)
	}
	if len(doc.Categories) != 1 || doc.Categories[0].ID != "c-north" || strings.Join(doc.Categories[0].ItemIDs, ",") != "i-1" {
		t.Fatalf("categories %+v", doc.Categories)
	}
	if again.Document.Items[0].ID != "i-1" || again.Document.Products[0].ID != "p-north" {
		t.Fatal("ids changed between builds")
	}
	if !strings.Contains(string(bundle.JSON), `"supplyDefault":"months:2"`) {
		t.Fatal("supplyDefault was omitted")
	}

	items := readCSV(t, bundle.ItemsCSV)
	if strings.Join(items[0], ",") != "name,barcode,on-hand count,expires,category,group,stock target" {
		t.Fatalf("items header %q", items[0])
	}
	if len(items) != 3 {
		t.Fatalf("items rows %d", len(items))
	}
	if items[1][0] != "north-typed-q7" || items[1][1] != "north-override-bc-q7" || items[1][2] != "1" || items[1][3] != "2026-06-01" {
		t.Fatalf("items row %+v", items[1])
	}
	if items[1][4] != "north-cat-q7" || items[1][5] != "north-group-q7" || items[1][6] != "4" {
		t.Fatalf("items tail %+v", items[1])
	}
	if items[2][2] != "0" || items[2][6] != "1" {
		t.Fatalf("removed instance still counted %+v", items[2])
	}
	shopping := readCSV(t, bundle.ShoppingCSV)
	if strings.Join(shopping[0], ",") != "name,quantity" || len(shopping) != 2 || shopping[1][0] != "north-typed-q7" || shopping[1][1] != "3" {
		t.Fatalf("shopping csv %+v", shopping)
	}

	names := zipNames(t, bundle.Zip)
	if strings.Join(names, ",") != "stockbot-export.json,items.csv,shopping-list.csv" {
		t.Fatalf("zip names %v", names)
	}
	raw := string(bundle.Zip) + string(bundle.JSON) + string(bundle.ItemsCSV) + string(bundle.ShoppingCSV)
	for _, secret := range []string{
		"south-pantry-z9", "south-typed-q7", "south-catalog-q7", "unused-catalog-q7",
		"north-catalog-q7", "foreign-typed-q7", "south-override-bc-q7", "scan-south-q7",
		"south-group-q7", "south-cat-q7", "south-shop-q7", "foreign-global-q7",
		"channel-sentinel-q7", "sbd_", "sbs_", "pw-hash-sentinel-q7", "session-key-sentinel-q7",
		"secret-setting-q7", "nia@pantry.test", "invite-hash-sentinel-q7", "device-label-q7",
		"memberships", "channel_key", "device_tokens", "household_password", "password_resets",
	} {
		if strings.Contains(raw, secret) {
			t.Fatalf("export contains %q", secret)
		}
	}
	for _, id := range []string{"p-north", "i-1", "i-2", "n-1", "s-north", "shop-north", "m-north", "g-north", "c-north"} {
		if !strings.Contains(string(bundle.JSON), `"`+id+`"`) {
			t.Fatalf("preserved id %s missing from json", id)
		}
	}
	for _, id := range []string{"p-south", "p-unused", "g-south", "item_1", "prod_1"} {
		if strings.Contains(string(bundle.JSON), `"`+id+`"`) {
			t.Fatalf("foreign or remapped id %s leaked into json", id)
		}
	}

	south, err := Build(context.Background(), db, southID, when)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(south.JSON), "south-typed-q7") || !strings.Contains(string(south.JSON), "foreign-typed-q7") || strings.Contains(string(south.JSON), "north-typed-q7") || strings.Contains(string(south.JSON), "north-cat-q7") || strings.Contains(string(south.JSON), "global-bc-q7") || !strings.Contains(string(south.JSON), "foreign-global-q7") {
		t.Fatalf("south export mixed names %s", south.JSON)
	}
	if _, err := Build(context.Background(), db, "0190aaaa-bbbb-7000-8000-000000000099", when); err != ErrNotFound {
		t.Fatalf("missing household: %v", err)
	}
	if _, err := Build(context.Background(), nil, northID, when); err != ErrNotFound {
		t.Fatal("nil db was not not-found")
	}
}

func openExportDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)
	if err := app.RunMigrations(db); err != nil {
		t.Fatal(err)
	}
	return db
}

func seedTwoHouseholds(t *testing.T, db *sql.DB) {
	t.Helper()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.Exec(query, args...); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}
	exec(`INSERT INTO households (id, name, restock_mode, created_at, channel_key, is_primary, legacy_user_id)
		VALUES (?, 'north-pantry-z9', 'ask_hold', '2026-01-01T00:00:00Z', 'channel-sentinel-q7', 0, '')`, northID)
	exec(`INSERT INTO households (id, name, restock_mode, created_at, channel_key, is_primary, legacy_user_id)
		VALUES (?, 'south-pantry-z9', 'auto', '2026-01-01T00:00:00Z', 'channel-south-q7', 0, '')`, southID)
	exec(`INSERT INTO products (id, name, category, unit_of_measure) VALUES
		('p-north', 'north-catalog-q7', 'dry', 'each'),
		('p-south', 'south-catalog-q7', 'dry', 'each'),
		('p-unused', 'unused-catalog-q7', 'dry', 'each')`)
	exec(`INSERT INTO household_product_overrides (household_id, product_id, display_name, updated_at) VALUES
		(?, 'p-north', 'north-typed-q7', '2026-01-01T00:00:00Z'),
		(?, 'p-north', 'foreign-typed-q7', '2026-01-01T00:00:00Z'),
		(?, 'p-south', 'south-typed-q7', '2026-01-01T00:00:00Z')`, northID, southID, southID)
	exec(`INSERT INTO barcodes (barcode, product_id, source, user_id, household_id) VALUES
		('global-bc-q7', 'p-north', 'global', '', ?),
		('foreign-global-q7', 'p-north', 'global', '', ?),
		('north-override-bc-q7', 'p-north', 'user_override', 'acct-north', ?),
		('south-override-bc-q7', 'p-south', 'user_override', 'acct-south', ?)`, northID, southID, northID, southID)
	exec(`INSERT INTO items (id, user_id, product_id, household_id, target_quantity, created_at) VALUES
		('i-1', 'acct-north', 'p-north', ?, 9, '2026-01-02T00:00:00Z'),
		('i-2', 'acct-north-b', 'p-north', ?, 1, '2026-01-03T00:00:00Z'),
		('i-south', 'acct-south', 'p-south', ?, 2, '2026-01-02T00:00:00Z')`, northID, northID, southID)
	exec(`INSERT INTO item_instances (id, item_id, stock_in_at, expires_at, household_id, created_at) VALUES
		('n-1', 'i-1', '2026-01-02T00:00:00Z', '2026-06-01T00:00:00Z', ?, '2026-01-02T00:00:00Z')`, northID)
	exec(`INSERT INTO item_instances (id, item_id, stock_in_at, expires_at, removed_at, removal_reason, household_id, created_at) VALUES
		('n-2', 'i-2', '2026-01-03T00:00:00Z', '2026-01-04T00:00:00Z', '2026-01-05T00:00:00Z', 'consumed', ?, '2026-01-03T00:00:00Z')`, northID)
	exec(`INSERT INTO item_targets (household_id, item_id, kind, value) VALUES (?, 'i-1', 'amount', 4)`, northID)
	exec(`INSERT INTO categories (id, household_id, name, name_key) VALUES
		('c-north', ?, 'north-cat-q7', 'north-cat-q7'),
		('c-south', ?, 'south-cat-q7', 'south-cat-q7')`, northID, southID)
	exec(`INSERT INTO item_categories (household_id, item_id, category_id, source) VALUES (?, 'i-1', 'c-north', 'user')`, northID)
	exec(`INSERT INTO product_groups (id, user_id, name, name_key, household_id) VALUES
		('g-north', 'acct-north', 'north-group-q7', 'north-group-q7', ?),
		('g-south', 'acct-south', 'south-group-q7', 'south-group-q7', ?)`, northID, southID)
	exec(`INSERT INTO product_group_members (household_id, product_id, group_id) VALUES
		(?, 'p-north', 'g-north'),
		(?, 'p-south', 'g-south')`, northID, southID)
	exec(`INSERT INTO scan_entries (id, user_id, barcode, scanned_at, direction, unit_count, status, product_id, household_id, created_at) VALUES
		('s-north', 'acct-north', 'scan-north-q7', '2026-02-01T00:00:00Z', 'stock_in', 1, 'committed', 'p-north', ?, '2026-02-01T00:00:00Z'),
		('s-south', 'acct-south', 'scan-south-q7', '2026-02-01T00:00:00Z', 'stock_in', 1, 'committed', 'p-south', ?, '2026-02-01T00:00:00Z')`, northID, southID)
	exec(`INSERT INTO stock_moves (id, item_id, product_id, direction, quantity, at, source, scan_entry_id, household_id, created_at) VALUES
		('m-north', 'i-1', 'p-north', 'in', 1, '2026-02-01T00:00:00Z', 'scan', 's-north', ?, '2026-02-01T00:00:00Z')`, northID)
	exec(`INSERT INTO shopping_list_items (id, user_id, item_id, quantity, source, household_id, created_at) VALUES
		('shop-north', 'acct-north', 'i-1', 3, 'manual', ?, '2026-02-02T00:00:00Z'),
		('shop-south', 'acct-south', 'i-south', 8, 'manual', ?, '2026-02-02T00:00:00Z')`, northID, southID)
	exec(`INSERT INTO household_settings (household_id, key, value) VALUES
		(?, 'supply_target_kind', 'months'),
		(?, 'supply_target_value', '2'),
		(?, 'product_brand:p-north', 'north-brand-q7'),
		(?, 'secret_note', 'secret-setting-q7')`, northID, northID, northID, northID)
	exec(`INSERT INTO household_password (household_id, login_name, pw_hash, state, session_key) VALUES
		(?, 'keeper', 'pw-hash-sentinel-q7', 'on', 'session-key-sentinel-q7')`, northID)
	exec(`INSERT INTO devices (id, household_id, kind, label, created_at) VALUES
		('dev-north', ?, 'direct', 'device-label-q7', '2026-01-01T00:00:00Z')`, northID)
	exec(`INSERT INTO device_tokens (id, device_id, household_id, token_hash, prefix, created_at) VALUES
		('tok-north', 'dev-north', ?, 'hash-sbd-sentinel-q7', 'sbd_sent7', '2026-01-01T00:00:00Z')`, northID)
	exec(`INSERT INTO sessions (id_hash, user_id, kind, origin, active_household_id, created_at, last_seen_at, expires_at) VALUES
		('sbs-hash-q7', 'acct-north', 'cookie', 'personal', ?, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', '2026-02-01T00:00:00Z')`, northID)
	exec(`INSERT INTO users (id, name, email_norm, pw_hash, created_at) VALUES
		('acct-north', 'Nia North', 'nia@pantry.test', 'account-hash-q7', '2026-01-01T00:00:00Z')`)
	exec(`INSERT INTO memberships (household_id, user_id, role, created_at) VALUES (?, 'acct-north', 'owner', '2026-01-01T00:00:00Z')`, northID)
	exec(`INSERT INTO invites (id, kind, household_id, code_hash, expires_at) VALUES
		('inv-north', 'join_household', ?, 'invite-hash-sentinel-q7', '2026-04-01T00:00:00Z')`, northID)
}

func TestBuildExportEdgesAndCancellation(t *testing.T) {
	db := openExportDB(t)
	when := time.Date(2026, 4, 2, 15, 4, 5, 0, time.UTC)
	if _, err := Build(context.Background(), db, "", when); err != ErrNotFound {
		t.Fatal("empty id was not not-found")
	}
	if _, err := Build(context.Background(), db, "0190aaaa-bbbb-7000-8000-000000000099", when); err != ErrNotFound {
		t.Fatal("missing household was not not-found")
	}

	const (
		edgeID = "0190aaaa-bbbb-7000-8000-0000000000c3"
		valID  = "0190aaaa-bbbb-7000-8000-0000000000c4"
	)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.Exec(query, args...); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}
	exec(`INSERT INTO households (id, name, restock_mode, created_at, channel_key, is_primary)
		VALUES (?, 'edge-pantry-z9', '', '2026-01-01T00:00:00Z', 'edge-channel-q7', 0)`, edgeID)
	exec(`INSERT INTO households (id, name, restock_mode, created_at, channel_key, is_primary)
		VALUES (?, 'value-pantry-z9', 'auto', '2026-01-01T00:00:00Z', 'value-channel-q7', 0)`, valID)
	exec(`INSERT INTO products (id, name, category, unit_of_measure) VALUES ('p-edge', '=edge-catalog-q7', '', '')`)
	exec(`INSERT INTO items (id, user_id, product_id, household_id, target_quantity, created_at)
		VALUES ('i-edge', 'acct-edge', 'p-edge', ?, NULL, '')`, edgeID)
	exec(`INSERT INTO item_targets (household_id, item_id, kind, value) VALUES (?, 'i-edge', 'amount', 1.5)`, edgeID)
	exec(`INSERT INTO item_instances (id, item_id, stock_in_at, expires_at, household_id, created_at)
		VALUES ('n-edge', 'i-edge', '2026-01-02 15:04:05', 'soon', ?, '')`, edgeID)
	exec(`INSERT INTO product_groups (id, user_id, name, name_key, household_id)
		VALUES ('g-edge', 'acct-edge', 'edge-group-q7', 'edge-group-q7', ?)`, edgeID)
	exec(`INSERT INTO categories (id, household_id, name, name_key)
		VALUES ('c-edge', ?, 'edge-cat-q7', 'edge-cat-q7')`, edgeID)
	exec(`INSERT INTO household_settings (household_id, key, value) VALUES (?, 'supply_target_kind', 'months')`, edgeID)
	exec(`INSERT INTO household_settings (household_id, key, value) VALUES (?, 'supply_target_value', '4')`, valID)

	bundle, err := Build(context.Background(), db, edgeID, when)
	if err != nil {
		t.Fatal(err)
	}
	doc := bundle.Document
	if doc.Settings.RestockMode != "auto" || doc.Settings.SupplyDefault != "months" {
		t.Fatalf("settings %+v", doc.Settings)
	}
	if len(doc.Products) != 1 || len(doc.Products[0].Barcodes) != 0 {
		t.Fatalf("barcodes %+v", doc.Products)
	}
	if len(doc.Groups) != 1 || len(doc.Groups[0].ProductIDs) != 0 || doc.Groups[0].Name != "edge-group-q7" {
		t.Fatalf("groups %+v", doc.Groups)
	}
	if len(doc.Categories) != 1 || len(doc.Categories[0].ItemIDs) != 0 {
		t.Fatalf("categories %+v", doc.Categories)
	}
	if doc.Items[0].CreatedAt != "" || doc.Items[0].StockTarget == nil || *doc.Items[0].StockTarget != 1.5 {
		t.Fatalf("item %+v", doc.Items[0])
	}
	rows := readCSV(t, bundle.ItemsCSV)
	if len(rows) != 2 || rows[1][0] != "'=edge-catalog-q7" || rows[1][3] != "soon" || rows[1][6] != "1.5" {
		t.Fatalf("items csv %+v", rows)
	}
	if csvCell("-1") != "-1" || csvCell("-2+3") != "'-2+3" || csvCell("+cmd") != "'+cmd" || csvCell("@cmd") != "'@cmd" || csvCell("\tcmd") != "'\tcmd" || csvCell("\rcmd") != "'\rcmd" || csvCell("plain") != "plain" {
		t.Fatal("csvCell")
	}
	if !strings.Contains(string(bundle.JSON), `"barcodes":[]`) || !strings.Contains(string(bundle.JSON), `"productIds":[]`) {
		t.Fatalf("empty slices were omitted\n%s", bundle.JSON)
	}

	valueOnly, err := Build(context.Background(), db, valID, when)
	if err != nil {
		t.Fatal(err)
	}
	if valueOnly.Document.Settings.SupplyDefault != "4" || len(valueOnly.Document.Products) != 0 {
		t.Fatalf("value-only %+v", valueOnly.Document.Settings)
	}

	if trimFloat(2) != "2" || trimFloat(1.5) != "1.5" {
		t.Fatal("trimFloat")
	}
	if rfc3339("") != "" || rfc3339("soon") != "soon" || rfc3339("2026-01-02") != "2026-01-02T00:00:00Z" {
		t.Fatal("rfc3339")
	}
	if dateOnly("soon") != "soon" || dateOnly("2026-01-02T00:00:00Z") != "2026-01-02" {
		t.Fatal("dateOnly")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Build(ctx, db, edgeID, when); err == nil {
		t.Fatal("cancelled build succeeded")
	}
	if _, err := loadProducts(ctx, db, edgeID); err == nil {
		t.Fatal("cancelled products")
	}
	if _, err := loadOverrides(ctx, db, edgeID); err == nil {
		t.Fatal("cancelled overrides")
	}
	if _, err := loadGlobalBarcodes(ctx, db, edgeID, []string{"p-edge"}); err == nil {
		t.Fatal("cancelled barcodes")
	}
	if _, err := loadItems(ctx, db, edgeID); err == nil {
		t.Fatal("cancelled items")
	}
	if _, err := loadInstances(ctx, db, edgeID); err == nil {
		t.Fatal("cancelled instances")
	}
	if _, err := loadScans(ctx, db, edgeID); err == nil {
		t.Fatal("cancelled scans")
	}
	if _, err := loadShopping(ctx, db, edgeID); err == nil {
		t.Fatal("cancelled shopping")
	}
	if _, err := loadMoves(ctx, db, edgeID); err == nil {
		t.Fatal("cancelled moves")
	}
	if _, _, err := loadGroups(ctx, db, edgeID); err == nil {
		t.Fatal("cancelled groups")
	}
	if _, _, err := loadCategories(ctx, db, edgeID); err == nil {
		t.Fatal("cancelled categories")
	}
	if _, err := loadItemCategory(ctx, db, edgeID); err == nil {
		t.Fatal("cancelled item category")
	}
	if _, err := loadItemGroup(ctx, db, edgeID); err == nil {
		t.Fatal("cancelled item group")
	}
	if _, err := loadItemTarget(ctx, db, edgeID); err == nil {
		t.Fatal("cancelled item target")
	}
	if _, err := loadSupplyDefault(ctx, db, edgeID); err == nil {
		t.Fatal("cancelled supply")
	}
}

func readCSV(t *testing.T, raw []byte) [][]string {
	t.Helper()
	rows, err := csv.NewReader(bytes.NewReader(raw)).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func zipNames(t *testing.T, raw []byte) []string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, file := range zr.File {
		names = append(names, file.Name)
	}
	return names
}
