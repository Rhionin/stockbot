package app_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/Rhionin/stockbot/internal/group"
	"github.com/Rhionin/stockbot/internal/history"
	"github.com/Rhionin/stockbot/internal/inventory"
	"github.com/Rhionin/stockbot/internal/product"
	"github.com/Rhionin/stockbot/internal/restock"
	"github.com/Rhionin/stockbot/internal/scan"
	"github.com/Rhionin/stockbot/internal/shopping"
	"github.com/Rhionin/stockbot/internal/supply"
	"github.com/Rhionin/stockbot/internal/syncsnap"
	"github.com/Rhionin/stockbot/internal/tenant"
)

const (
	cedarID       = "0190bbbb-cccc-7000-8000-0000000000e5"
	publicID      = "0190cccc-dddd-7000-8000-0000000000f6"
	typedID       = "0190dddd-eeee-7000-8000-0000000000a7"
	legacyID      = "0190eeee-ffff-7000-8000-0000000000b8"
	publicName    = "Public oats fixture"
	renamedName   = "alder-sentinel-k7m2q oats"
	typedName     = "cedar-sentinel-q9p4w cereal"
	legacyName    = "legacy-sentinel-m3n8r beans"
	globalBarcode = "0001112223334"
)

func TestHouseholdDisplayNamesRoundTrip(t *testing.T) {
	db := openPrivacyDB(t)
	ctx := context.Background()
	primary := privacyPrimary(t, db)
	cedar := privacyCedar(t, db)
	catalog := product.NewCatalog(db)
	pantry := inventory.NewPantry(db)
	queue := scan.NewQueue(db)
	lists := shopping.NewStore(db)
	groups := group.NewGroups(db)
	journal := history.NewJournal(db)
	now := time.Date(2026, 6, 1, 15, 0, 0, 0, time.UTC)

	if err := catalog.CreateProduct(ctx, product.Product{
		ID: publicID, Name: publicName, Source: product.SourceExternal, UnitOfMeasure: "unit",
	}); err != nil {
		t.Fatal(err)
	}
	if err := catalog.SaveDisplayName(ctx, primary, publicID, renamedName); err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.CreateTypedProduct(ctx, cedar, product.Product{
		ID: typedID, Name: typedName, UnitOfMeasure: "unit", Category: "Pantry",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO products (id, name, source, created_at, unit_of_measure)
		VALUES (?, ?, 'user', '2026-01-01T00:00:00Z', 'unit')`, legacyID, legacyName); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO household_product_overrides (household_id, product_id, display_name, updated_at)
		SELECT h.id, p.id, p.name, p.created_at
		FROM products p
		JOIN households h ON h.is_primary = 1
		WHERE p.id = ?
		  AND NOT EXISTS (
		    SELECT 1 FROM household_product_overrides o
		    WHERE o.household_id = h.id AND o.product_id = p.id
		  )`, legacyID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE products SET name = '' WHERE id = ? AND source = 'user'`, legacyID); err != nil {
		t.Fatal(err)
	}
	if err := catalog.UpsertBarcodeMapping(ctx, globalBarcode, publicID, "global", ""); err != nil {
		t.Fatal(err)
	}

	primaryItem := mustPantryItem(t, pantry, primary, publicID)
	legacyItem := mustPantryItem(t, pantry, primary, legacyID)
	cedarItem := mustPantryItem(t, pantry, cedar, typedID)
	cedarPublic := mustPantryItem(t, pantry, cedar, publicID)
	mustInstance(t, db, primary, primaryItem.ID, now)
	mustInstance(t, db, cedar, cedarItem.ID, now)
	mustInstance(t, db, cedar, cedarPublic.ID, now)

	assertItemName(t, pantry, primary, primaryItem.ID, renamedName)
	assertItemName(t, pantry, primary, legacyItem.ID, legacyName)
	assertItemName(t, pantry, cedar, cedarItem.ID, typedName)
	assertItemName(t, pantry, cedar, cedarPublic.ID, publicName)
	assertListNames(t, pantry, primary, map[string]string{publicID: renamedName, legacyID: legacyName}, typedName)
	assertListNames(t, pantry, cedar, map[string]string{typedID: typedName, publicID: publicName}, renamedName)

	primaryGroup := mustGroup(t, groups, primary, "Primary staples", []string{publicID, legacyID})
	cedarGroup := mustGroup(t, groups, cedar, "Cedar staples", []string{typedID, publicID})
	assertMemberName(t, groups, primary, primaryGroup, publicID, renamedName)
	assertMemberName(t, groups, primary, primaryGroup, legacyID, legacyName)
	assertMemberName(t, groups, cedar, cedarGroup, typedID, typedName)
	assertMemberName(t, groups, cedar, cedarGroup, publicID, publicName)
	assertGroupSummaryName(t, pantry, primary, primaryItem.ID, now, renamedName, typedName)
	assertGroupSummaryName(t, pantry, cedar, cedarItem.ID, now, typedName, renamedName)
	assertGroupBarcode(t, pantry, cedar, cedarPublic.ID, now, globalBarcode)

	dir := scan.StockIn
	primaryScan := mustScan(t, queue, primary, "111", publicID, &dir)
	if primaryScan.Product == nil || primaryScan.Product.Name != renamedName {
		t.Fatalf("scan create name %+v", primaryScan.Product)
	}
	again, err := queue.CreateScanEntry(ctx, primary, scan.ScanEntry{
		UserID: primary.HouseholdID(), Barcode: "111", ScannedAt: now, Direction: &dir, UnitCount: 1, ProductID: &publicIDString,
	})
	if err != nil {
		t.Fatal(err)
	}
	if again.Product == nil || again.Product.Name != renamedName {
		t.Fatalf("merged scan name %+v", again.Product)
	}
	listed, err := queue.ListScanEntries(ctx, primary, "")
	if err != nil {
		t.Fatal(err)
	}
	if !scanNamed(listed, renamedName) || scanNamed(listed, typedName) {
		t.Fatalf("primary scans %+v", listed)
	}
	cedarScan := mustScan(t, queue, cedar, "222", typedID, &dir)
	if cedarScan.Product == nil || cedarScan.Product.Name != typedName {
		t.Fatalf("cedar scan %+v", cedarScan.Product)
	}
	linkID := "scan-link-cedar"
	if _, err := db.Exec(`
		INSERT INTO scan_entries (id, user_id, barcode, scanned_at, direction, unit_count, status, product_id, household_id)
		VALUES (?, ?, '999' || ?, ?, 'stock_out', 1, 'pending', ?, ?)`,
		linkID, cedar.HouseholdID(), globalBarcode, now, typedID, cedar.HouseholdID()); err != nil {
		t.Fatal(err)
	}
	suggestions, err := queue.LinkSuggestions(ctx, cedar, linkID)
	if err != nil {
		t.Fatal(err)
	}
	if !suggestionNamed(suggestions, publicName) || suggestionNamed(suggestions, renamedName) {
		t.Fatalf("link suggestions %+v", suggestions)
	}

	mustShoppingGroup(t, db, primary, primaryItem.ID, primaryGroup)
	lines, err := lists.ListUnpurchased(ctx, primary)
	if err != nil {
		t.Fatal(err)
	}
	if !choiceNamed(lines, renamedName) || !choiceNamed(lines, legacyName) || choiceNamed(lines, typedName) {
		t.Fatalf("shopping members %+v", lines)
	}

	restockSnap, err := restock.Load(ctx, primary, db)
	if err != nil {
		t.Fatal(err)
	}
	if !restockNamed(restockSnap, renamedName) || !restockNamed(restockSnap, legacyName) || restockNamed(restockSnap, typedName) {
		t.Fatalf("restock names %+v", restockSnap.Items)
	}
	cedarRestock, err := restock.Load(ctx, cedar, db)
	if err != nil {
		t.Fatal(err)
	}
	if !restockNamed(cedarRestock, typedName) || restockNamed(cedarRestock, renamedName) {
		t.Fatalf("cedar restock %+v", cedarRestock.Items)
	}

	if err := groups.NoteFromScan(ctx, cedar, typedID); err != nil {
		t.Fatal(err)
	}
	cards, err := groups.ListSuggestions(ctx, cedar)
	if err != nil {
		t.Fatal(err)
	}
	if !suggestionCardNamed(cards, typedName) {
		t.Fatalf("cedar suggestions %+v", cards)
	}
	primaryCards, err := groups.ListSuggestions(ctx, primary)
	if err != nil {
		t.Fatal(err)
	}
	if suggestionCardNamed(primaryCards, typedName) {
		t.Fatalf("primary saw cedar suggestion %+v", primaryCards)
	}

	primaryView, err := journal.Product(ctx, primary, primaryItem.ID, now)
	if err != nil || primaryView.Name != renamedName {
		t.Fatalf("journal %+v err %v", primaryView, err)
	}
	if _, err := db.Exec(`
		INSERT INTO stock_moves (id, item_id, product_id, direction, quantity, at, source, tracked, household_id)
		VALUES ('move-primary', ?, ?, 'in', 1, ?, 'manual', 0, ?)`,
		primaryItem.ID, publicID, now, primary.HouseholdID()); err != nil {
		t.Fatal(err)
	}
	ledger, err := journal.Household(ctx, primary, history.LedgerFilter{Query: "alder-sentinel"})
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Moves) == 0 {
		t.Fatal("ledger search missed the renamed product")
	}
	for _, move := range ledger.Moves {
		if move.ProductName != renamedName {
			t.Fatalf("ledger search %+v", ledger.Moves)
		}
	}
	hidden, err := journal.Household(ctx, cedar, history.LedgerFilter{Query: "alder-sentinel"})
	if err != nil {
		t.Fatal(err)
	}
	if len(hidden.Moves) != 0 {
		t.Fatalf("cedar ledger leaked %+v", hidden.Moves)
	}
	undone, err := journal.Undo(ctx, primary, "move-primary", now)
	if err != nil {
		t.Fatal(err)
	}
	if undone.Name != renamedName {
		t.Fatalf("undo view %q", undone.Name)
	}

	primarySnap, err := syncsnap.Build(ctx, primary, db, now)
	if err != nil {
		t.Fatal(err)
	}
	cedarSnap, err := syncsnap.Build(ctx, cedar, db, now)
	if err != nil {
		t.Fatal(err)
	}
	if !snapNamed(primarySnap, renamedName) || !snapNamed(primarySnap, legacyName) || snapNamed(primarySnap, typedName) {
		t.Fatalf("primary snapshot leaked or dropped a name")
	}
	if !snapNamed(cedarSnap, typedName) || snapNamed(cedarSnap, renamedName) || snapNamed(cedarSnap, legacyName) {
		t.Fatalf("cedar snapshot leaked or dropped a name")
	}
	if !snapBarcode(cedarSnap, publicID, globalBarcode) {
		t.Fatalf("cedar snapshot missed global barcode %+v", productOf(cedarSnap, publicID))
	}
}

func TestHouseholdSettingsStayApart(t *testing.T) {
	db := openPrivacyDB(t)
	ctx := context.Background()
	primary := privacyPrimary(t, db)
	cedar := privacyCedar(t, db)
	targets := restock.NewTargets(db)
	groups := group.NewGroups(db)
	svc := supply.Open(db)
	journal := history.NewJournal(db)
	started := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	later := started.Add(48 * time.Hour)

	if err := targets.SetHousehold(ctx, primary, restock.Target{Kind: restock.KindMonths, Value: 4}); err != nil {
		t.Fatal(err)
	}
	if err := targets.SetHousehold(ctx, cedar, restock.Target{Kind: restock.KindMonths, Value: 9}); err != nil {
		t.Fatal(err)
	}
	primarySnap, err := restock.Load(ctx, primary, db)
	if err != nil {
		t.Fatal(err)
	}
	if primarySnap.Household.Kind != restock.KindMonths || primarySnap.Household.Value != 4 {
		t.Fatalf("primary restock target %+v", primarySnap.Household)
	}
	cedarSnap, err := restock.Load(ctx, cedar, db)
	if err != nil {
		t.Fatal(err)
	}
	if cedarSnap.Household.Value != 9 {
		t.Fatalf("cedar restock target %+v", cedarSnap.Household)
	}

	if err := groups.SetDefaultRule(ctx, primary, string(group.KindFavorite)); err != nil {
		t.Fatal(err)
	}
	if err := groups.SetDefaultRule(ctx, cedar, string(group.KindBestDeal)); err != nil {
		t.Fatal(err)
	}
	primaryRule, err := groups.DefaultRule(ctx, primary)
	if err != nil || primaryRule != group.KindFavorite {
		t.Fatalf("primary rule %s err %v", primaryRule, err)
	}
	cedarRule, err := groups.DefaultRule(ctx, cedar)
	if err != nil || cedarRule != group.KindBestDeal {
		t.Fatalf("cedar rule %s err %v", cedarRule, err)
	}

	if _, err := svc.Complete(ctx, primary, started); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Complete(ctx, cedar, later); err != nil {
		t.Fatal(err)
	}
	primaryLedger, err := journal.Household(ctx, primary, history.LedgerFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if primaryLedger.SetupAt != started.UTC().Format(time.RFC3339) {
		t.Fatalf("primary setup %q", primaryLedger.SetupAt)
	}
	cedarLedger, err := journal.Household(ctx, cedar, history.LedgerFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if cedarLedger.SetupAt != later.UTC().Format(time.RFC3339) {
		t.Fatalf("cedar setup %q", cedarLedger.SetupAt)
	}
	if err := svc.Wipe(ctx, cedar, supply.WipePhrase); err != nil {
		t.Fatal(err)
	}
	primaryLedger, err = journal.Household(ctx, primary, history.LedgerFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if primaryLedger.SetupAt != started.UTC().Format(time.RFC3339) {
		t.Fatalf("wipe changed primary setup %q", primaryLedger.SetupAt)
	}
	cedarLedger, err = journal.Household(ctx, cedar, history.LedgerFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if cedarLedger.SetupAt != "" {
		t.Fatalf("cedar setup survived wipe %q", cedarLedger.SetupAt)
	}

	if err := groups.Seed(ctx, primary); err != nil {
		t.Fatal(err)
	}
	if err := groups.Seed(ctx, cedar); err != nil {
		t.Fatal(err)
	}
	var primarySeed, cedarSeed string
	if err := db.QueryRow(`
		SELECT value FROM household_settings WHERE household_id = ? AND key = 'group_suggestions_seeded_v1'`,
		primary.HouseholdID()).Scan(&primarySeed); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`
		SELECT value FROM household_settings WHERE household_id = ? AND key = 'group_suggestions_seeded_v1'`,
		cedar.HouseholdID()).Scan(&cedarSeed); err != nil {
		t.Fatal(err)
	}
	if primarySeed != "1" || cedarSeed != "1" {
		t.Fatalf("seed flags primary %q cedar %q", primarySeed, cedarSeed)
	}
}

func TestSeedDoesNotCopyAnotherHouseholdName(t *testing.T) {
	db := openPrivacyDB(t)
	ctx := context.Background()
	primary := privacyPrimary(t, db)
	cedar := privacyCedar(t, db)
	catalog := product.NewCatalog(db)
	groups := group.NewGroups(db)
	if _, err := catalog.CreateTypedProduct(ctx, cedar, product.Product{
		ID: typedID, Name: typedName, UnitOfMeasure: "unit",
	}); err != nil {
		t.Fatal(err)
	}
	if err := groups.Seed(ctx, primary); err != nil {
		t.Fatal(err)
	}
	var titles string
	rows, err := db.Query(`SELECT title FROM group_suggestions WHERE household_id = ?`, primary.HouseholdID())
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var title string
		if err := rows.Scan(&title); err != nil {
			t.Fatal(err)
		}
		titles += title + "\n"
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(titles, typedName) || strings.Contains(titles, "cedar-sentinel") {
		t.Fatalf("primary seed copied cedar name %q", titles)
	}
}

var publicIDString = publicID

func openPrivacyDB(t *testing.T) *sql.DB {
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

func privacyPrimary(t *testing.T, db *sql.DB) tenant.Scope {
	t.Helper()
	scope, err := auth.PrimaryScope(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	return scope
}

func privacyCedar(t *testing.T, db *sql.DB) tenant.Scope {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO households (id, name, restock_mode, created_at, channel_key, is_primary)
		VALUES (?, 'Household Cedar', '', '2026-02-02T00:00:00Z', 'abcdef0123456789abcdef0123456789', 0)`, cedarID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO history_repair (household_id, id) VALUES (?, 1)`, cedarID); err != nil {
		t.Fatal(err)
	}
	scope, err := tenant.New(cedarID)
	if err != nil {
		t.Fatal(err)
	}
	return scope
}

func mustPantryItem(t *testing.T, pantry *inventory.Pantry, scope tenant.Scope, productID string) *inventory.Item {
	t.Helper()
	item, err := pantry.GetOrCreateItem(context.Background(), scope, productID)
	if err != nil || item == nil {
		t.Fatalf("item %s: %v", productID, err)
	}
	return item
}

func mustInstance(t *testing.T, db *sql.DB, scope tenant.Scope, itemID string, at time.Time) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO item_instances (id, item_id, stock_in_at, household_id)
		VALUES (?, ?, ?, ?)`, itemID+"-inst", itemID, at, scope.HouseholdID()); err != nil {
		t.Fatal(err)
	}
}

func assertItemName(t *testing.T, pantry *inventory.Pantry, scope tenant.Scope, itemID, want string) {
	t.Helper()
	item, err := pantry.GetItem(context.Background(), scope, itemID)
	if err != nil || item == nil || item.Product == nil || item.Product.Name != want {
		t.Fatalf("item %s name %+v err %v", itemID, item, err)
	}
}

func assertListNames(t *testing.T, pantry *inventory.Pantry, scope tenant.Scope, want map[string]string, hidden string) {
	t.Helper()
	items, err := pantry.ListItems(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, item := range items {
		if item.Product == nil {
			t.Fatal("list item missing product")
		}
		got[item.ProductID] = item.Product.Name
		if strings.Contains(item.Product.Name, hidden) {
			t.Fatalf("list leaked %q in %q", hidden, item.Product.Name)
		}
	}
	for id, name := range want {
		if got[id] != name {
			t.Fatalf("product %s name %q, want %q", id, got[id], name)
		}
	}
}

func mustGroup(t *testing.T, groups *group.Groups, scope tenant.Scope, name string, productIDs []string) string {
	t.Helper()
	created, err := groups.Create(context.Background(), scope, name, productIDs, nil)
	if err != nil {
		t.Fatal(err)
	}
	return created.ID
}

func assertMemberName(t *testing.T, groups *group.Groups, scope tenant.Scope, groupID, productID, want string) {
	t.Helper()
	got, err := groups.Get(context.Background(), scope, groupID)
	if err != nil {
		t.Fatal(err)
	}
	for _, member := range got.Members {
		if member.ProductID == productID {
			if member.Name != want {
				t.Fatalf("member %s name %q", productID, member.Name)
			}
			return
		}
	}
	t.Fatalf("group %s missing %s", groupID, productID)
}

func assertGroupSummaryName(t *testing.T, pantry *inventory.Pantry, scope tenant.Scope, itemID string, now time.Time, want, hidden string) {
	t.Helper()
	row, err := pantry.GetInventoryItem(context.Background(), scope, itemID, now, 7)
	if err != nil || row == nil || row.Group == nil {
		t.Fatalf("summary %+v err %v", row, err)
	}
	found := false
	for _, member := range row.Group.Members {
		if strings.Contains(member.Name, hidden) {
			t.Fatalf("group summary leaked %q", member.Name)
		}
		if member.Name == want {
			found = true
		}
	}
	if !found {
		t.Fatalf("summary missing %q %+v", want, row.Group.Members)
	}
}

func assertGroupBarcode(t *testing.T, pantry *inventory.Pantry, scope tenant.Scope, itemID string, now time.Time, barcode string) {
	t.Helper()
	row, err := pantry.GetInventoryItem(context.Background(), scope, itemID, now, 7)
	if err != nil || row == nil || row.Group == nil {
		t.Fatalf("barcode summary %+v err %v", row, err)
	}
	for _, member := range row.Group.Members {
		for _, code := range member.Barcodes {
			if code == barcode {
				return
			}
		}
	}
	t.Fatalf("missing global barcode %+v", row.Group.Members)
}

func mustScan(t *testing.T, queue *scan.Queue, scope tenant.Scope, barcode, productID string, dir *scan.ScanDirection) *scan.ScanEntry {
	t.Helper()
	entry, err := queue.CreateScanEntry(context.Background(), scope, scan.ScanEntry{
		UserID: scope.HouseholdID(), Barcode: barcode, ScannedAt: time.Date(2026, 6, 1, 15, 0, 0, 0, time.UTC),
		Direction: dir, UnitCount: 1, ProductID: &productID,
	})
	if err != nil {
		t.Fatal(err)
	}
	return entry
}

func scanNamed(entries []scan.ScanEntry, name string) bool {
	for _, entry := range entries {
		if entry.Product != nil && entry.Product.Name == name {
			return true
		}
	}
	return false
}

func suggestionNamed(suggestions []scan.LinkSuggestion, name string) bool {
	for _, suggestion := range suggestions {
		if suggestion.Name == name {
			return true
		}
	}
	return false
}

func mustShoppingGroup(t *testing.T, db *sql.DB, scope tenant.Scope, itemID, groupID string) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO shopping_list_items (id, user_id, item_id, quantity, source, household_id, group_id)
		VALUES ('shop-primary', ?, ?, 1, 'manual', ?, ?)`,
		scope.HouseholdID(), itemID, scope.HouseholdID(), groupID); err != nil {
		t.Fatal(err)
	}
}

func choiceNamed(lines []shopping.ShoppingListItem, name string) bool {
	for _, line := range lines {
		for _, choice := range line.GroupMembers {
			if choice.Name == name {
				return true
			}
		}
	}
	return false
}

func restockNamed(snap restock.Snapshot, name string) bool {
	for _, item := range snap.Items {
		if item.Name == name {
			return true
		}
	}
	return false
}

func suggestionCardNamed(cards []group.Suggestion, name string) bool {
	for _, card := range cards {
		if strings.Contains(card.Title, name) {
			return true
		}
		for _, member := range card.Members {
			if member.Name == name || strings.Contains(member.Name, name) {
				return true
			}
		}
	}
	return false
}

func snapNamed(snap syncsnap.Snapshot, name string) bool {
	for _, item := range snap.Products {
		if item.Name == name {
			return true
		}
	}
	return false
}

func snapBarcode(snap syncsnap.Snapshot, productID, barcode string) bool {
	item := productOf(snap, productID)
	if item == nil {
		return false
	}
	for _, code := range item.Barcodes {
		if code == barcode {
			return true
		}
	}
	return false
}

func productOf(snap syncsnap.Snapshot, productID string) *syncsnap.Product {
	for i := range snap.Products {
		if snap.Products[i].ID == productID {
			return &snap.Products[i]
		}
	}
	return nil
}

func TestOverrideNoticeUsesDisplayName(t *testing.T) {
	db := openPrivacyDB(t)
	ctx := context.Background()
	primary := privacyPrimary(t, db)
	catalog := product.NewCatalog(db)
	pantry := inventory.NewPantry(db)
	groups := group.NewGroups(db)
	if err := catalog.CreateProduct(ctx, product.Product{ID: legacyID, Name: legacyName, Source: product.SourceExternal}); err != nil {
		t.Fatal(err)
	}
	if err := catalog.SaveDisplayName(ctx, primary, legacyID, renamedName); err != nil {
		t.Fatal(err)
	}
	item := mustPantryItem(t, pantry, primary, legacyID)
	if _, err := db.Exec(`
		INSERT INTO item_targets (household_id, item_id, kind, value) VALUES (?, ?, 'amount', 4)`,
		primary.HouseholdID(), item.ID); err != nil {
		t.Fatal(err)
	}
	_, err := groups.Create(ctx, primary, "Targeted staples", []string{legacyID}, nil)
	var notice *group.Error
	if !errors.As(err, &notice) {
		t.Fatalf("create err %v", err)
	}
	if len(notice.Members) != 1 || notice.Members[0].Name != renamedName {
		t.Fatalf("notice %+v", notice.Members)
	}
}
