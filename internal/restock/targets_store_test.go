package restock

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/Rhionin/stockbot/internal/tenant"
	_ "modernc.org/sqlite"
)

func newTestTargets(t *testing.T) (*Targets, *sql.DB) {
	t.Helper()
	conn, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := app.RunMigrations(conn); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return NewTargets(conn), conn
}

func insertProduct(t *testing.T, db *sql.DB, id, name string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO products (id, name, unit_of_measure) VALUES (?, ?, 'bags')`, id, name); err != nil {
		t.Fatal(err)
	}
}

func primaryScope(t *testing.T, db *sql.DB) tenant.Scope {
	t.Helper()
	scope, err := auth.PrimaryScope(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	return scope
}

const cedarHouseholdID = "0190bbbb-cccc-7000-8000-0000000000e5"

func cedarScope(t *testing.T, db *sql.DB) tenant.Scope {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO households (id, name, created_at, channel_key, is_primary)
		VALUES (?, 'Household Cedar', '2026-02-02T00:00:00Z', 'abcdef0123456789abcdef0123456789', 0)`,
		cedarHouseholdID); err != nil {
		t.Fatal(err)
	}
	scope, err := tenant.New(cedarHouseholdID)
	if err != nil {
		t.Fatal(err)
	}
	return scope
}

func TestCopyLegacyKeepsCurrentBehavior(t *testing.T) {
	targets, db := newTestTargets(t)
	ctx := context.Background()
	scope := primaryScope(t, db)
	insertProduct(t, db, "chips", "Tortilla chips")
	insertProduct(t, db, "beans", "Green beans")
	insertProduct(t, db, "milk", "Oat milk")
	hid := scope.HouseholdID()
	if _, err := db.Exec(`
		INSERT INTO items (id, user_id, household_id, product_id, target_quantity, replenishment_mode) VALUES
			('item-chips', ?, ?, 'chips', 4, 'target'),
			('item-beans', ?, ?, 'beans', NULL, 'target'),
			('item-milk', ?, ?, 'milk', 9, 'replenish')`,
		hid, hid, hid, hid, hid, hid); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO supply_overrides (product_id, household_id, window_months) VALUES ('chips', ?, 2)`, hid); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO product_groups (id, user_id, household_id, name, name_key) VALUES ('g1', ?, ?, 'Beans', 'beans')`, hid, hid); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO product_group_members (group_id, product_id, household_id, no_restock) VALUES ('g1', 'beans', ?, 1)`, hid); err != nil {
		t.Fatal(err)
	}

	if err := CopyLegacy(ctx, scope, db); err != nil {
		t.Fatal(err)
	}
	if err := CopyLegacy(ctx, scope, db); err != nil {
		t.Fatal(err)
	}

	snap, err := targets.Snapshot(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]ItemView{}
	for _, item := range snap.Items {
		byName[item.Name] = item
	}
	chips := byName["Tortilla chips"]
	if chips.Level != LevelItem || chips.Target.Kind != KindMonths || chips.Target.Value != 2 {
		t.Fatalf("override wins over stored quantity: %+v", chips.Target)
	}
	beans := byName["Green beans"]
	if beans.Level != LevelItem || beans.Target.Value != 0 {
		t.Fatalf("don't restock member: %+v level %s", beans.Target, beans.Level)
	}
	milk := byName["Oat milk"]
	if milk.Level != LevelHousehold || milk.Target.Value != 3 {
		t.Fatalf("replenish mode is not an item target: %+v level %s", milk.Target, milk.Level)
	}
	if len(snap.Categories) != len(StarterCategories) {
		t.Fatalf("starters: %d", len(snap.Categories))
	}
}

func TestDontRestockAndCategoryPreview(t *testing.T) {
	targets, db := newTestTargets(t)
	ctx := context.Background()
	scope := primaryScope(t, db)
	insertProduct(t, db, "bananas", "Bananas")
	insertProduct(t, db, "lemons", "Lemons")
	hid := scope.HouseholdID()
	if _, err := db.Exec(`
		INSERT INTO items (id, user_id, household_id, product_id) VALUES
			('item-bananas', ?, ?, 'bananas'),
			('item-lemons', ?, ?, 'lemons')`, hid, hid, hid, hid); err != nil {
		t.Fatal(err)
	}
	created, err := targets.CreateCategory(ctx, scope, "Produce")
	if err != nil {
		t.Fatal(err)
	}
	if err := targets.AddItemCategory(ctx, scope, "item-bananas", created.ID); err != nil {
		t.Fatal(err)
	}
	if err := targets.AddItemCategory(ctx, scope, "item-lemons", created.ID); err != nil {
		t.Fatal(err)
	}
	if err := targets.SetItem(ctx, scope, "item-lemons", Target{Kind: KindAmount, Value: 6}); err != nil {
		t.Fatal(err)
	}

	preview, err := targets.PreviewCategory(ctx, scope, created.ID, Target{Kind: KindMonths, Value: 0})
	if err != nil {
		t.Fatal(err)
	}
	if preview.Title != "Stop restocking Produce?" || len(preview.Stop) != 1 || preview.Stop[0].Name != "Bananas" {
		t.Fatalf("preview stop: %+v", preview)
	}
	if len(preview.Keep) != 1 || preview.Keep[0].Name != "Lemons" || preview.Keep[0].Reason != "Set on this item" {
		t.Fatalf("preview keep: %+v", preview.Keep)
	}

	if err := targets.SetCategoryTarget(ctx, scope, created.ID, &Target{Kind: KindMonths, Value: 0}); err != nil {
		t.Fatal(err)
	}
	snap, err := targets.Snapshot(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range snap.Items {
		if item.Name == "Bananas" && (item.Target.Value != 0 || item.Level != LevelCategory || item.QuietLine != "Not restocked · Produce") {
			t.Fatalf("bananas: %+v %s", item.Target, item.QuietLine)
		}
		if item.Name == "Lemons" && item.Level != LevelItem {
			t.Fatalf("lemons stayed on the item: %s", item.Level)
		}
	}

	if err := targets.RemoveItemCategory(ctx, scope, "item-bananas", created.ID); err != nil {
		t.Fatal(err)
	}
	if err := targets.SaveOffTags(ctx, "bananas", []string{"en:fresh-fruits", "en:undefined"}, nil); err != nil {
		t.Fatal(err)
	}
	cards, err := targets.Suggestions(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	for _, card := range cards {
		if card.Category != "Produce" {
			continue
		}
		for _, item := range card.Items {
			if item.ItemID == "item-bananas" {
				t.Fatal("a removed membership must stay removed")
			}
		}
	}
}

func TestHouseholdDefaultMayBeZero(t *testing.T) {
	targets, db := newTestTargets(t)
	ctx := context.Background()
	scope := primaryScope(t, db)
	insertProduct(t, db, "salt", "Salt")
	hid := scope.HouseholdID()
	if _, err := db.Exec(`INSERT INTO items (id, user_id, household_id, product_id) VALUES ('item-salt', ?, ?, 'salt')`, hid, hid); err != nil {
		t.Fatal(err)
	}
	if err := targets.SetHousehold(ctx, scope, Target{Kind: KindMonths, Value: 0}); err != nil {
		t.Fatal(err)
	}
	snap, err := targets.Snapshot(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Household.Value != 0 || snap.Items[0].Level != LevelHousehold || snap.Items[0].Target.Value != 0 {
		t.Fatalf("household 0: %+v item %+v", snap.Household, snap.Items[0].Target)
	}
	var months string
	err = db.QueryRow(`SELECT value FROM app_settings WHERE key = 'supply_months'`).Scan(&months)
	if err != sql.ErrNoRows {
		t.Fatalf("supply_months must not be written, got %q (%v)", months, err)
	}
}

func TestMergeMovesMembershipsAndNewCategories(t *testing.T) {
	targets, db := newTestTargets(t)
	ctx := context.Background()
	scope := primaryScope(t, db)
	insertProduct(t, db, "bananas", "Bananas")
	insertProduct(t, db, "tomatoes", "Crushed tomatoes")
	hid := scope.HouseholdID()
	if _, err := db.Exec(`
		INSERT INTO items (id, user_id, household_id, product_id) VALUES
			('item-bananas', ?, ?, 'bananas'),
			('item-tomatoes', ?, ?, 'tomatoes')`, hid, hid, hid, hid); err != nil {
		t.Fatal(err)
	}
	if err := targets.SaveOffTags(ctx, "tomatoes", []string{"en:vegetables"}, nil); err != nil {
		t.Fatal(err)
	}

	from, err := targets.CreateCategory(ctx, scope, "Odds")
	if err != nil {
		t.Fatal(err)
	}
	into, err := targets.CreateCategory(ctx, scope, "Extras")
	if err != nil {
		t.Fatal(err)
	}
	if err := targets.AddItemCategory(ctx, scope, "item-bananas", from.ID); err != nil {
		t.Fatal(err)
	}
	if err := targets.MergeCategory(ctx, scope, from.ID, into.ID); err != nil {
		t.Fatal(err)
	}
	snap, err := targets.Snapshot(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	var bananas ItemView
	for _, item := range snap.Items {
		if item.Name == "Bananas" {
			bananas = item
		}
	}
	if len(bananas.Categories) != 1 || bananas.Categories[0].Name != "Extras" {
		t.Fatalf("merged membership: %+v", bananas.Categories)
	}

	if err := targets.ApplySuggestions(ctx, scope, "Trail snacks", []string{"item-bananas"}); err != nil {
		t.Fatal(err)
	}
	cards, err := targets.Suggestions(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	var noted bool
	for _, card := range cards {
		if card.Category != "Produce" {
			continue
		}
		for _, name := range card.NotSuggested {
			if name == "Crushed tomatoes" {
				noted = true
			}
		}
	}
	if !noted {
		t.Fatalf("blocked produce: %+v", cards)
	}
}

func TestRejectedTargets(t *testing.T) {
	targets, db := newTestTargets(t)
	ctx := context.Background()
	scope := primaryScope(t, db)
	insertProduct(t, db, "salt", "Salt")
	hid := scope.HouseholdID()
	if _, err := db.Exec(`INSERT INTO items (id, user_id, household_id, product_id) VALUES ('item-salt', ?, ?, 'salt')`, hid, hid); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		run  func() error
	}{
		{"blank name", func() error { _, err := targets.CreateCategory(ctx, scope, "  "); return err }},
		{"duplicate name", func() error {
			if _, err := targets.CreateCategory(ctx, scope, "Extras"); err != nil {
				return err
			}
			_, err := targets.CreateCategory(ctx, scope, "Extras")
			return err
		}},
		{"months too high", func() error {
			return targets.SetHousehold(ctx, scope, Target{Kind: KindMonths, Value: 13})
		}},
		{"fraction", func() error {
			return targets.SetItem(ctx, scope, "item-salt", Target{Kind: KindAmount, Value: 1.5})
		}},
		{"missing item", func() error {
			return targets.SetItem(ctx, scope, "missing", Target{Kind: KindMonths, Value: 1})
		}},
		{"missing category", func() error {
			return targets.SetCategoryTarget(ctx, scope, "missing", &Target{Kind: KindMonths, Value: 1})
		}},
		{"merge into itself", func() error {
			created, err := targets.CreateCategory(ctx, scope, "Pantry")
			if err != nil {
				return err
			}
			return targets.MergeCategory(ctx, scope, created.ID, created.ID)
		}},
		{"unknown group", func() error {
			_, err := targets.PreviewGroup(ctx, scope, "missing", Target{Kind: KindMonths, Value: 0})
			return err
		}},
		{"no suggestion picked", func() error {
			return targets.ApplySuggestions(ctx, scope, "Produce", nil)
		}},
		{"missing household", func() error {
			_, err := targets.CreateCategory(ctx, tenant.Scope{}, "Pantry staples")
			return err
		}},
	}
	for _, tc := range cases {
		if err := tc.run(); err == nil {
			t.Errorf("%s: expected a rejection", tc.name)
		}
	}
	if err := targets.SetItem(ctx, scope, "item-salt", Target{Kind: KindAmount, Value: 1000}); err != nil {
		t.Fatal(err)
	}
	clamped, err := targets.ItemTarget(ctx, scope, "item-salt")
	if err != nil || clamped == nil || clamped.Value != 999 {
		t.Fatalf("amounts over 999 are clamped, got %+v %v", clamped, err)
	}
}

func TestHouseholdCategoriesStayApart(t *testing.T) {
	targets, db := newTestTargets(t)
	ctx := context.Background()
	home := primaryScope(t, db)
	cedar := cedarScope(t, db)

	if _, err := targets.CreateCategory(ctx, tenant.Scope{}, "Shared spices"); !errors.Is(err, tenant.ErrMissingHousehold) {
		t.Fatalf("missing household: %v", err)
	}

	created, err := targets.CreateCategory(ctx, cedar, "Cedar spices")
	if err != nil {
		t.Fatal(err)
	}
	homeSnap, err := targets.Snapshot(ctx, home)
	if err != nil {
		t.Fatal(err)
	}
	for _, category := range homeSnap.Categories {
		if category.ID == created.ID || category.Name == "Cedar spices" {
			t.Fatalf("primary snapshot included cedar category %+v", category)
		}
	}
	if err := targets.RenameCategory(ctx, home, created.ID, "Taken spices"); err == nil {
		t.Fatal("primary household renamed cedar's category")
	}
	if err := targets.DeleteCategory(ctx, home, created.ID); err == nil {
		t.Fatal("primary household deleted cedar's category")
	}
	cedarSnap, err := targets.Snapshot(ctx, cedar)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, category := range cedarSnap.Categories {
		if category.ID == created.ID && category.Name == "Cedar spices" {
			found = true
		}
	}
	if !found {
		t.Fatalf("cedar category missing: %+v", cedarSnap.Categories)
	}
}
