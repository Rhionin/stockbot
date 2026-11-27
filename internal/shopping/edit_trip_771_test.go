package shopping_test

import (
	"context"
	"testing"

	"github.com/Rhionin/stockbot/internal/shopping"
)

type swapFixture struct {
	deps         testDeps
	ctx          context.Context
	itemA, itemB string
	members      []shopping.ShelfMember
}

func newSwapFixture(t *testing.T) swapFixture {
	t.Helper()
	deps := newTestStore(t)
	ctx := context.Background()
	scope := scopeOf(t, deps.db)
	f := swapFixture{deps: deps, ctx: ctx}
	f.itemA = createTestItem(t, deps, ctx, "swap-a", "Swap A")
	f.itemB = createTestItem(t, deps, ctx, "swap-b", "Swap B")
	hid := scope.HouseholdID()
	for _, stmt := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO product_groups (id, household_id, user_id, name, name_key) VALUES ('g-swap', ?, ?, 'Swap', 'swap')`, []any{hid, hid}},
		{`INSERT INTO product_group_members (household_id, product_id, group_id) VALUES (?, 'swap-a', 'g-swap'), (?, 'swap-b', 'g-swap')`, []any{hid, hid}},
	} {
		if _, err := deps.db.Exec(stmt.query, stmt.args...); err != nil {
			t.Fatal(err)
		}
	}
	f.members = []shopping.ShelfMember{{ItemID: f.itemA, GroupKey: "g-swap"}, {ItemID: f.itemB, GroupKey: "g-swap"}}
	return f
}

func (f swapFixture) fill(t *testing.T, qty int) {
	t.Helper()
	var lines []shopping.PlannedLine
	if qty > 0 {
		lines = []shopping.PlannedLine{{ItemID: f.itemA, GroupKey: "g-swap", Quantity: qty, Note: "plan"}}
	}
	if err := f.deps.shopping.SavePlannedLines(f.ctx, scopeOf(t, f.deps.db), lines, f.members); err != nil {
		t.Fatal(err)
	}
}

func (f swapFixture) rows(t *testing.T) []shopping.ShoppingListItem {
	t.Helper()
	rows, err := f.deps.shopping.ListUnpurchased(f.ctx, scopeOf(t, f.deps.db))
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func (f swapFixture) swapToB(t *testing.T) {
	t.Helper()
	rows := f.rows(t)
	if len(rows) != 1 {
		t.Fatalf("rows before swap: %#v", rows)
	}
	if err := f.deps.shopping.SwapAutoLine(f.ctx, scopeOf(t, f.deps.db), rows[0].ID, f.itemB); err != nil {
		t.Fatal(err)
	}
}

func TestSwappedLineKeepsProductAndRefreshesQuantityThisTrip(t *testing.T) {
	f := newSwapFixture(t)
	f.fill(t, 2)
	f.swapToB(t)
	var touched, expires int
	if err := f.deps.db.QueryRow(`SELECT touched, touched_expires_trip FROM shopping_list_items`).Scan(&touched, &expires); err != nil {
		t.Fatal(err)
	}
	if touched != 1 || expires != 1 {
		t.Fatalf("swap: touched=%d expires=%d, want 1 and trip 0 + 1", touched, expires)
	}
	f.fill(t, 3)
	rows := f.rows(t)
	if len(rows) != 1 || rows[0].ItemID != f.itemB || rows[0].Quantity != 3 {
		t.Fatalf("still needed: want the swapped product with the new quantity, got %#v", rows)
	}
}

func TestSwappedLineIsRemovedOnceStockCoversIt(t *testing.T) {
	f := newSwapFixture(t)
	f.fill(t, 2)
	f.swapToB(t)
	f.fill(t, 0)
	if rows := f.rows(t); len(rows) != 0 {
		t.Fatalf("an edited line must go once the plan no longer needs it: %#v", rows)
	}
}

func TestSwappedLineGoesBackToThePlannerNextTrip(t *testing.T) {
	f := newSwapFixture(t)
	f.fill(t, 2)
	f.swapToB(t)
	if _, err := f.deps.db.Exec(`UPDATE households SET trip_seq = trip_seq + 1`); err != nil {
		t.Fatal(err)
	}
	f.fill(t, 2)
	rows := f.rows(t)
	if len(rows) != 1 || rows[0].ItemID != f.itemA {
		t.Fatalf("next trip the planner picks again: %#v", rows)
	}
	var touched int
	if err := f.deps.db.QueryRow(`SELECT touched FROM shopping_list_items`).Scan(&touched); err != nil {
		t.Fatal(err)
	}
	if touched != 0 {
		t.Fatalf("an expired edit is cleared: touched=%d", touched)
	}
}

func TestPerStoreQuantityIsNotAnEdit(t *testing.T) {
	f := newSwapFixture(t)
	f.fill(t, 2)
	rows := f.rows(t)
	scope := scopeOf(t, f.deps.db)
	if err := f.deps.shopping.SetAdjustment(f.ctx, scope, rows[0].ID, "kroger", 5); err != nil {
		t.Fatal(err)
	}
	if err := f.deps.shopping.RemoveAdjustment(f.ctx, scope, rows[0].ID, "kroger"); err != nil {
		t.Fatal(err)
	}
	if err := f.deps.shopping.SetAdjustment(f.ctx, scope, rows[0].ID, "kroger", 0); err != nil {
		t.Fatal(err)
	}
	var touched int
	if err := f.deps.db.QueryRow(`SELECT touched FROM shopping_list_items`).Scan(&touched); err != nil {
		t.Fatal(err)
	}
	if touched != 0 {
		t.Fatalf("a per-store quantity marked the line edited")
	}
}
