package shopping_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/Rhionin/stockbot/internal/shopping"
	"github.com/Rhionin/stockbot/internal/trip"
)

func seedAutoLine(t *testing.T, deps testDeps, itemID string) *shopping.ShoppingListItem {
	t.Helper()
	ctx := context.Background()
	scope := scopeOf(t, deps.db)
	lines := []shopping.PlannedLine{{ItemID: itemID, Quantity: 2, Note: "short 2"}}
	if err := deps.shopping.SavePlannedLines(ctx, scope, lines, nil); err != nil {
		t.Fatalf("SavePlannedLines: %v", err)
	}
	active, err := deps.shopping.ListUnpurchased(ctx, scope)
	if err != nil {
		t.Fatalf("ListUnpurchased: %v", err)
	}
	for _, row := range active {
		if row.ItemID == itemID {
			return &row
		}
	}
	t.Fatalf("no line for %s", itemID)
	return nil
}

func skipActive(t *testing.T, db *sql.DB, household, itemID string) bool {
	t.Helper()
	var n int
	err := db.QueryRow(`
		SELECT COUNT(*) FROM staged_cart_skips s
		JOIN households h ON h.id = s.household_id
		WHERE s.household_id = ? AND s.item_id = ? AND h.trip_seq < s.expires_trip`,
		household, itemID).Scan(&n)
	if err != nil {
		t.Fatal(err)
	}
	return n == 1
}

func TestEndTripSetsNewCartCloseMetadata(t *testing.T) {
	deps := newTestStore(t)
	ctx := context.Background()
	scope := scopeOf(t, deps.db)
	before := time.Now().UTC()
	if err := deps.shopping.EndTrip(ctx, scope); err != nil {
		t.Fatal(err)
	}
	var cause sql.NullString
	var closed sql.NullString
	if err := deps.db.QueryRow(`SELECT trip_closed_cause, trip_closed_at FROM households WHERE id = ?`, scope.HouseholdID()).
		Scan(&cause, &closed); err != nil {
		t.Fatal(err)
	}
	if !cause.Valid || cause.String != "new_cart" {
		t.Fatalf("cause: %v", cause)
	}
	if !closed.Valid {
		t.Fatal("missing trip_closed_at")
	}
	got, err := time.Parse(time.RFC3339Nano, closed.String)
	if err != nil {
		t.Fatal(err)
	}
	if got.Before(before.Add(-time.Second)) || got.After(time.Now().UTC().Add(time.Second)) {
		t.Fatalf("closed at out of range: %v", got)
	}
}

func TestTripSkipClearsOnNewCart(t *testing.T) {
	deps := newTestStore(t)
	ctx := context.Background()
	scope := scopeOf(t, deps.db)
	itemID := createTestItem(t, deps, ctx, "trip-clear-cart", "Trip Cart")
	line := seedAutoLine(t, deps, itemID)
	if err := deps.shopping.RemoveItem(ctx, scope, line.ID); err != nil {
		t.Fatalf("RemoveItem: %v", err)
	}
	if !skipActive(t, deps.db, scope.HouseholdID(), itemID) {
		t.Fatal("skip should be active before boundary")
	}
	if err := deps.shopping.EndTrip(ctx, scope); err != nil {
		t.Fatalf("EndTrip: %v", err)
	}
	lines := []shopping.PlannedLine{{ItemID: itemID, Quantity: 2, Note: "short 2"}}
	if err := deps.shopping.SavePlannedLines(ctx, scope, lines, nil); err != nil {
		t.Fatalf("SavePlannedLines: %v", err)
	}
	active, err := deps.shopping.ListUnpurchased(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, row := range active {
		if row.ItemID == itemID {
			found = true
		}
	}
	if !found {
		t.Fatal("item should return after skip expires at new cart")
	}
}

func TestTripSkipClearsOnMassStockInClose(t *testing.T) {
	deps := newTestStore(t)
	ctx := context.Background()
	scope := scopeOf(t, deps.db)
	itemID := createTestItem(t, deps, ctx, "trip-mass-in", "Mass In")
	line := seedAutoLine(t, deps, itemID)
	if err := deps.shopping.RemoveItem(ctx, scope, line.ID); err != nil {
		t.Fatalf("RemoveItem: %v", err)
	}
	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		at := base.Add(time.Duration(i) * time.Minute)
		prod := createTestItem(t, deps, ctx, fmt.Sprintf("mass-prod-%d", i), "Mass product")
		if _, err := deps.db.Exec(`
			INSERT INTO stock_moves (id, household_id, item_id, product_id, direction, quantity, at, source, tracked, created_at)
			VALUES (?, ?, ?, (SELECT product_id FROM items WHERE id = ?), 'in', 1, ?, 'scan', 1, ?)`,
			fmt.Sprintf("mass-move-%d", i), scope.HouseholdID(), prod, prod, at.Format(time.RFC3339Nano), at.Format(time.RFC3339Nano)); err != nil {
			t.Fatal(err)
		}
	}
	tx, err := deps.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	now := base.Add(10 * time.Minute)
	if err := trip.MaybeCloseMassStockIn(ctx, scope, tx, now); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := deps.shopping.SavePlannedLines(ctx, scope, []shopping.PlannedLine{{ItemID: itemID, Quantity: 2, Note: "short 2"}}, nil); err != nil {
		t.Fatal(err)
	}
	active, _ := deps.shopping.ListUnpurchased(ctx, scope)
	found := false
	for _, row := range active {
		if row.ItemID == itemID {
			found = true
		}
	}
	if !found {
		t.Fatal("item should return after mass stock-in trip boundary")
	}
}

func TestTripSkipSurvivesSingleStockInAndPurchased(t *testing.T) {
	deps := newTestStore(t)
	ctx := context.Background()
	scope := scopeOf(t, deps.db)
	itemID := createTestItem(t, deps, ctx, "trip-survive", "Survive")
	line := seedAutoLine(t, deps, itemID)
	if err := deps.shopping.RemoveItem(ctx, scope, line.ID); err != nil {
		t.Fatalf("RemoveItem: %v", err)
	}
	at := time.Now().UTC()
	if _, err := deps.db.Exec(`
		INSERT INTO stock_moves (id, household_id, item_id, product_id, direction, quantity, at, source, tracked, created_at)
		VALUES ('one-in', ?, ?, (SELECT product_id FROM items WHERE id = ?), 'in', 1, ?, 'scan', 1, ?)`,
		scope.HouseholdID(), itemID, itemID, at.Format(time.RFC3339Nano), at.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	tx, err := deps.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := trip.MaybeCloseMassStockIn(ctx, scope, tx, at.Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := deps.shopping.SavePlannedLines(ctx, scope, []shopping.PlannedLine{{ItemID: itemID, Quantity: 2, Note: "short 2"}}, nil); err != nil {
		t.Fatal(err)
	}
	active, _ := deps.shopping.ListUnpurchased(ctx, scope)
	if len(active) != 0 {
		t.Fatalf("skip should block replan after single stock-in: %#v", active)
	}
	if !skipActive(t, deps.db, scope.HouseholdID(), itemID) {
		t.Fatal("skip row should remain")
	}
}

func TestLegacySkipExpiresAtFirstBoundary(t *testing.T) {
	deps := newTestStore(t)
	ctx := context.Background()
	scope := scopeOf(t, deps.db)
	itemID := createTestItem(t, deps, ctx, "legacy-skip", "Legacy")
	household := scope.HouseholdID()
	if _, err := deps.db.Exec(
		`INSERT INTO staged_cart_skips (household_id, user_id, item_id) VALUES (?, ?, ?)`,
		household, household, itemID); err != nil {
		t.Fatal(err)
	}
	if err := deps.shopping.EndTrip(ctx, scope); err != nil {
		t.Fatal(err)
	}
	if err := deps.shopping.SavePlannedLines(ctx, scope, []shopping.PlannedLine{{ItemID: itemID, Quantity: 1, Note: "n"}}, nil); err != nil {
		t.Fatal(err)
	}
	active, _ := deps.shopping.ListUnpurchased(ctx, scope)
	found := false
	for _, row := range active {
		if row.ItemID == itemID {
			found = true
		}
	}
	if !found {
		t.Fatal("legacy skip should expire at first boundary and allow replan")
	}
}

func TestGroupedRemoveSkipOneTrip(t *testing.T) {
	deps := newTestStore(t)
	ctx := context.Background()
	scope := scopeOf(t, deps.db)
	household := scope.HouseholdID()
	const groupID = "grp-trip"
	if _, err := deps.db.Exec(`INSERT INTO product_groups (id, household_id, user_id, name, name_key, rule, rule_confirmed) VALUES (?, ?, ?, 'Dairy', 'dairy', 'favorite', 1)`,
		groupID, household, household); err != nil {
		t.Fatal(err)
	}
	itemA := createTestItem(t, deps, ctx, "grp-a", "Milk A")
	itemB := createTestItem(t, deps, ctx, "grp-b", "Milk B")
	for _, itemID := range []string{itemA, itemB} {
		if _, err := deps.db.Exec(`INSERT INTO product_group_members (household_id, group_id, product_id) SELECT household_id, ?, product_id FROM items WHERE id = ?`,
			groupID, itemID); err != nil {
			t.Fatal(err)
		}
	}
	lines := []shopping.PlannedLine{{ItemID: itemA, GroupKey: groupID, Quantity: 2, Note: "group"}}
	if err := deps.shopping.SavePlannedLines(ctx, scope, lines, []shopping.ShelfMember{{ItemID: itemA, GroupKey: groupID}, {ItemID: itemB, GroupKey: groupID}}); err != nil {
		t.Fatal(err)
	}
	active, _ := deps.shopping.ListUnpurchased(ctx, scope)
	var lineID string
	for _, row := range active {
		lineID = row.ID
	}
	if err := deps.shopping.RemoveItem(ctx, scope, lineID); err != nil {
		t.Fatal(err)
	}
	changed := []shopping.PlannedLine{{ItemID: itemB, GroupKey: groupID, Quantity: 2, Note: "group"}}
	if err := deps.shopping.SavePlannedLines(ctx, scope, changed, []shopping.ShelfMember{{ItemID: itemA, GroupKey: groupID}, {ItemID: itemB, GroupKey: groupID}}); err != nil {
		t.Fatal(err)
	}
	active, _ = deps.shopping.ListUnpurchased(ctx, scope)
	if len(active) != 0 {
		t.Fatalf("group skip should hold line off this trip: %#v", active)
	}
}

func TestRemoveRecordsActorAndTime(t *testing.T) {
	deps := newTestStore(t)
	ctx := auth.WithPrincipal(context.Background(), auth.Principal{Kind: "account", UserID: "user-actor-571"})
	scope := scopeOf(t, deps.db)
	if _, err := deps.db.Exec(`INSERT INTO users (id, name, email_norm, pw_hash, created_at) VALUES ('user-actor-571', 'Actor', 'actor@example.com', 'x', '2020-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	itemID := createTestItem(t, deps, ctx, "actor-skip", "Actor Skip")
	line := seedAutoLine(t, deps, itemID)
	if err := deps.shopping.RemoveItem(ctx, scope, line.ID); err != nil {
		t.Fatal(err)
	}
	var actor sql.NullString
	var removed sql.NullString
	if err := deps.db.QueryRow(`SELECT actor_user_id, removed_at FROM staged_cart_skips WHERE item_id = ?`, itemID).Scan(&actor, &removed); err != nil {
		t.Fatal(err)
	}
	if !actor.Valid || actor.String != "user-actor-571" {
		t.Fatalf("actor: %v", actor)
	}
	if !removed.Valid || removed.String == "" {
		t.Fatalf("removed_at: %v", removed)
	}
}
