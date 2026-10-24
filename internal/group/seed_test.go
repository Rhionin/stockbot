package group

import (
	"context"
	"testing"

	"github.com/Rhionin/stockbot/internal/product"
)

func TestSeed_OldPlanPinsFavoriteAndIncludesDelMonte(t *testing.T) {
	groups, catalog, conn := newTestGroups(t)
	ctx := context.Background()
	scope := scopeOf(t, conn)
	for _, row := range []struct{ id, name string }{
		{"gv", "Great Value Cut Green Beans"},
		{"kr", "Kroger Cut Green Beans"},
		{"dm", "Del Monte Cut Green Beans"},
	} {
		if err := catalog.CreateProduct(ctx, product.Product{ID: row.id, Name: row.name, UnitOfMeasure: "can"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := conn.Exec(`INSERT INTO items (id, household_id, user_id, product_id) VALUES ('item-gv', ?, ?, 'gv')`, scope.HouseholdID(), scope.HouseholdID()); err != nil {
		t.Fatal(err)
	}
	key, ok := needKey("Great Value Cut Green Beans", "can")
	if !ok {
		t.Fatal("need key")
	}
	if _, err := conn.Exec(`INSERT INTO brand_preferences (household_id, user_id, need_key, item_id, ignore_price) VALUES (?, ?, ?, 'item-gv', 1)`, scope.HouseholdID(), scope.HouseholdID(), key); err != nil {
		t.Fatal(err)
	}

	if err := groups.Seed(ctx, scope); err != nil {
		t.Fatal(err)
	}
	if err := groups.Seed(ctx, scope); err != nil {
		t.Fatal(err)
	}

	var n int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM group_suggestions`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("suggestions = %d, want 1", n)
	}
	var kind, rule, pin, title string
	if err := conn.QueryRow(`SELECT kind, proposed_rule, pinned_product_id, title FROM group_suggestions`).Scan(&kind, &rule, &pin, &title); err != nil {
		t.Fatal(err)
	}
	if kind != "from_old_plan" || rule != "favorite" || pin != "gv" {
		t.Fatalf("card = %s %s pin %s title %s", kind, rule, pin, title)
	}
	members := map[string]struct{}{}
	rows, err := conn.Query(`SELECT product_id FROM group_suggestion_members`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		members[id] = struct{}{}
	}
	for _, id := range []string{"gv", "kr", "dm"} {
		if _, ok := members[id]; !ok {
			t.Fatalf("missing %s in %+v", id, members)
		}
	}
}

func TestSeed_SkipsGoneAndSinglePreferences(t *testing.T) {
	groups, catalog, conn := newTestGroups(t)
	ctx := context.Background()
	scope := scopeOf(t, conn)
	if err := catalog.CreateProduct(ctx, product.Product{ID: "milk", Name: "Whole Milk", UnitOfMeasure: "carton"}); err != nil {
		t.Fatal(err)
	}
	key, ok := needKey("Whole Milk", "carton")
	if !ok {
		t.Fatal("need key")
	}
	if _, err := conn.Exec(`INSERT INTO brand_preferences (household_id, user_id, need_key, item_id, ignore_price) VALUES (?, ?, ?, 'missing-item', 1)`, scope.HouseholdID(), scope.HouseholdID(), key); err != nil {
		t.Fatal(err)
	}
	if err := groups.Seed(ctx, scope); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM group_suggestions`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("suggestions = %d, want 0", n)
	}
}
