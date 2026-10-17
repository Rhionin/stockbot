package restock

import (
	"context"
	"database/sql"
	"testing"

	"github.com/Rhionin/stockbot/internal/app"
	_ "modernc.org/sqlite"
)

func newTestTargets(t *testing.T) (*Targets, *sql.DB) {
	t.Helper()
	conn, err := sql.Open("sqlite", ":memory:")
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

func TestCopyLegacyKeepsCurrentBehavior(t *testing.T) {
	targets, db := newTestTargets(t)
	ctx := context.Background()
	insertProduct(t, db, "chips", "Tortilla chips")
	insertProduct(t, db, "beans", "Green beans")
	insertProduct(t, db, "milk", "Oat milk")
	if _, err := db.Exec(`
		INSERT INTO items (id, user_id, product_id, target_quantity, replenishment_mode) VALUES
			('item-chips', 'user-1', 'chips', 4, 'target'),
			('item-beans', 'user-1', 'beans', NULL, 'target'),
			('item-milk', 'user-1', 'milk', 9, 'replenish')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO supply_overrides (product_id, window_months) VALUES ('chips', 2)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO product_groups (id, user_id, name, name_key) VALUES ('g1', 'user-1', 'Beans', 'beans')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO product_group_members (group_id, product_id, no_restock) VALUES ('g1', 'beans', 1)`); err != nil {
		t.Fatal(err)
	}

	if err := CopyLegacy(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := CopyLegacy(ctx, db); err != nil {
		t.Fatal(err)
	}

	snap, err := targets.Snapshot(ctx)
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
	insertProduct(t, db, "bananas", "Bananas")
	insertProduct(t, db, "lemons", "Lemons")
	if _, err := db.Exec(`
		INSERT INTO items (id, user_id, product_id) VALUES
			('item-bananas', 'user-1', 'bananas'),
			('item-lemons', 'user-1', 'lemons')`); err != nil {
		t.Fatal(err)
	}
	created, err := targets.CreateCategory(ctx, "Produce")
	if err != nil {
		t.Fatal(err)
	}
	if err := targets.AddItemCategory(ctx, "item-bananas", created.ID); err != nil {
		t.Fatal(err)
	}
	if err := targets.AddItemCategory(ctx, "item-lemons", created.ID); err != nil {
		t.Fatal(err)
	}
	if err := targets.SetItem(ctx, "item-lemons", Target{Kind: KindAmount, Value: 6}); err != nil {
		t.Fatal(err)
	}

	preview, err := targets.PreviewCategory(ctx, created.ID, Target{Kind: KindMonths, Value: 0})
	if err != nil {
		t.Fatal(err)
	}
	if preview.Title != "Stop restocking Produce?" || len(preview.Stop) != 1 || preview.Stop[0].Name != "Bananas" {
		t.Fatalf("preview stop: %+v", preview)
	}
	if len(preview.Keep) != 1 || preview.Keep[0].Name != "Lemons" || preview.Keep[0].Reason != "Set on this item" {
		t.Fatalf("preview keep: %+v", preview.Keep)
	}

	if err := targets.SetCategoryTarget(ctx, created.ID, &Target{Kind: KindMonths, Value: 0}); err != nil {
		t.Fatal(err)
	}
	snap, err := targets.Snapshot(ctx)
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

	if err := targets.RemoveItemCategory(ctx, "item-bananas", created.ID); err != nil {
		t.Fatal(err)
	}
	if err := targets.SaveOffTags(ctx, "bananas", []string{"en:fresh-fruits", "en:undefined"}, nil); err != nil {
		t.Fatal(err)
	}
	cards, err := targets.Suggestions(ctx)
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
	insertProduct(t, db, "salt", "Salt")
	if _, err := db.Exec(`INSERT INTO items (id, user_id, product_id) VALUES ('item-salt', 'user-1', 'salt')`); err != nil {
		t.Fatal(err)
	}
	if err := targets.SetHousehold(ctx, Target{Kind: KindMonths, Value: 0}); err != nil {
		t.Fatal(err)
	}
	snap, err := targets.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Household.Value != 0 || snap.Items[0].Level != LevelHousehold || snap.Items[0].Target.Value != 0 {
		t.Fatalf("household 0: %+v item %+v", snap.Household, snap.Items[0].Target)
	}
	var months string
	if err := db.QueryRow(`SELECT value FROM app_settings WHERE key = 'supply_months'`).Scan(&months); err == nil && months == "0" {
		t.Fatal("a household default of 0 must not be written as supply_months")
	}
}

func TestMergeMovesMembershipsAndNewCategories(t *testing.T) {
	targets, db := newTestTargets(t)
	ctx := context.Background()
	insertProduct(t, db, "bananas", "Bananas")
	insertProduct(t, db, "tomatoes", "Crushed tomatoes")
	if _, err := db.Exec(`
		INSERT INTO items (id, user_id, product_id) VALUES
			('item-bananas', 'user-1', 'bananas'),
			('item-tomatoes', 'user-1', 'tomatoes')`); err != nil {
		t.Fatal(err)
	}
	if err := targets.SaveOffTags(ctx, "tomatoes", []string{"en:vegetables"}, nil); err != nil {
		t.Fatal(err)
	}

	from, err := targets.CreateCategory(ctx, "Odds")
	if err != nil {
		t.Fatal(err)
	}
	into, err := targets.CreateCategory(ctx, "Extras")
	if err != nil {
		t.Fatal(err)
	}
	if err := targets.AddItemCategory(ctx, "item-bananas", from.ID); err != nil {
		t.Fatal(err)
	}
	if err := targets.MergeCategory(ctx, from.ID, into.ID); err != nil {
		t.Fatal(err)
	}
	snap, err := targets.Snapshot(ctx)
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

	if err := targets.ApplySuggestions(ctx, "Trail snacks", []string{"item-bananas"}); err != nil {
		t.Fatal(err)
	}
	cards, err := targets.Suggestions(ctx)
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
	insertProduct(t, db, "salt", "Salt")
	if _, err := db.Exec(`INSERT INTO items (id, user_id, product_id) VALUES ('item-salt', 'user-1', 'salt')`); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		run  func() error
	}{
		{"blank name", func() error { _, err := targets.CreateCategory(ctx, "  "); return err }},
		{"duplicate name", func() error {
			if _, err := targets.CreateCategory(ctx, "Extras"); err != nil {
				return err
			}
			_, err := targets.CreateCategory(ctx, "Extras")
			return err
		}},
		{"months too high", func() error {
			return targets.SetHousehold(ctx, Target{Kind: KindMonths, Value: 13})
		}},
		{"fraction", func() error {
			return targets.SetItem(ctx, "item-salt", Target{Kind: KindAmount, Value: 1.5})
		}},
		{"missing item", func() error {
			return targets.SetItem(ctx, "missing", Target{Kind: KindMonths, Value: 1})
		}},
		{"missing category", func() error {
			return targets.SetCategoryTarget(ctx, "missing", &Target{Kind: KindMonths, Value: 1})
		}},
		{"merge into itself", func() error {
			created, err := targets.CreateCategory(ctx, "Pantry")
			if err != nil {
				return err
			}
			return targets.MergeCategory(ctx, created.ID, created.ID)
		}},
		{"unknown group", func() error {
			_, err := targets.PreviewGroup(ctx, "missing", Target{Kind: KindMonths, Value: 0})
			return err
		}},
		{"no suggestion picked", func() error {
			return targets.ApplySuggestions(ctx, "Produce", nil)
		}},
	}
	for _, tc := range cases {
		if err := tc.run(); err == nil {
			t.Errorf("%s: expected a rejection", tc.name)
		}
	}
	if err := targets.SetItem(ctx, "item-salt", Target{Kind: KindAmount, Value: 1000}); err != nil {
		t.Fatal(err)
	}
	clamped, err := targets.ItemTarget(ctx, "item-salt")
	if err != nil || clamped == nil || clamped.Value != 999 {
		t.Fatalf("amounts over 999 are clamped, got %+v %v", clamped, err)
	}
}
