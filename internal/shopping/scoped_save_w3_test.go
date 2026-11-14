package shopping_test

import (
	"context"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/shopping"
)

// TestW3FillWritesListUpdated — T18 alternate + full fill writes list_updated.
func TestW3FillWritesListUpdated(t *testing.T) {
	deps := newTestStore(t)
	scope := scopeOf(t, deps.db)
	ctx := context.Background()
	itemID := createTestItem(t, deps, ctx, "prod-fill-w3", "Fill W3")
	line := shopping.PlannedLine{ItemID: itemID, Quantity: 1}
	if err := deps.shopping.SavePlannedLinesWithChanges(ctx, scope, []shopping.PlannedLine{line}, nil,
		shopping.CauseListUpdated, nil, 0, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	var cause string
	if err := deps.db.QueryRow(`SELECT cause FROM list_line_changes WHERE household_id = ? LIMIT 1`, scope.HouseholdID()).Scan(&cause); err != nil {
		t.Fatal(err)
	}
	if cause != string(shopping.CauseListUpdated) {
		t.Fatalf("cause %q", cause)
	}
}

func TestScopedSaveGroupLine(t *testing.T) {
	deps := newTestStore(t)
	scope := scopeOf(t, deps.db)
	ctx := context.Background()
	itemA := createTestItem(t, deps, ctx, "prod-grp-a", "GA")
	itemB := createTestItem(t, deps, ctx, "prod-grp-b", "GB")
	groupID := "grp-571-scoped"
	mustExecShopping(t, deps.db, `INSERT INTO product_groups (id, household_id, user_id, name, name_key, rule, rule_confirmed) VALUES (?, ?, ?, 'G', 'g', 'favorite', 1)`,
		groupID, scope.HouseholdID(), scope.HouseholdID())
	mustExecShopping(t, deps.db, `INSERT INTO product_group_members (household_id, group_id, product_id) VALUES (?, ?, ?), (?, ?, ?)`,
		scope.HouseholdID(), groupID, "prod-grp-a", scope.HouseholdID(), groupID, "prod-grp-b")
	key := shopping.GroupLineKey(groupID)
	params := shopping.ScopedSaveParams{
		Lines: []shopping.PlannedLine{{ItemID: itemA, GroupKey: groupID, Quantity: 3}},
		Members: []shopping.ShelfMember{{ItemID: itemA, GroupKey: groupID}, {ItemID: itemB, GroupKey: groupID}},
		AffectedKeys: map[string]struct{}{key: {}},
		Cause:        shopping.CauseGroupChanged,
		Changes: []shopping.ScopedChangeInput{{Key: key, Effect: shopping.EffectAdded, Cause: shopping.CauseGroupChanged, ToQty: intPtr(3)}},
		Now:   time.Now().UTC(),
	}
	if err := deps.shopping.ScopedSavePlannedLines(ctx, scope, params); err != nil {
		t.Fatal(err)
	}
	var qty int
	if err := deps.db.QueryRow(`SELECT quantity FROM shopping_list_items WHERE household_id = ? AND group_id = ?`, scope.HouseholdID(), groupID).Scan(&qty); err != nil || qty != 3 {
		t.Fatalf("group line qty %d err %v", qty, err)
	}
}

// TestW3PutAwayDeletesUnpurchasedAutoRow — put_away with buy 0 removes open auto rows.
func TestW3PutAwayDeletesUnpurchasedAutoRow(t *testing.T) {
	deps := newTestStore(t)
	scope := scopeOf(t, deps.db)
	ctx := context.Background()
	itemID := createTestItem(t, deps, ctx, "prod-putaway-del", "PutAwayDel")
	lineID := "line-putaway-del"
	mustExecShopping(t, deps.db, `INSERT INTO shopping_list_items (id, household_id, user_id, item_id, quantity, source, created_at)
		VALUES (?, ?, ?, ?, 2, 'auto', datetime('now'))`, lineID, scope.HouseholdID(), scope.HouseholdID(), itemID)
	key := shopping.ItemLineKey(itemID)
	params := shopping.ScopedSaveParams{
		Lines:        []shopping.PlannedLine{{ItemID: itemID, Quantity: 0}},
		AffectedKeys: map[string]struct{}{key: {}},
		Cause:        shopping.CausePutAway,
		Changes: []shopping.ScopedChangeInput{{
			Key: key, Effect: shopping.EffectTakenOff, Reason: shopping.TakenOffOnHandMeetsPar,
			Cause: shopping.CausePutAway, FromQty: intPtr(2), ToQty: intPtr(0),
		}},
		Now: time.Now().UTC(),
	}
	if err := deps.shopping.ScopedSavePlannedLines(ctx, scope, params); err != nil {
		t.Fatal(err)
	}
	var still int
	if err := deps.db.QueryRow(`SELECT COUNT(*) FROM shopping_list_items WHERE id = ?`, lineID).Scan(&still); err != nil {
		t.Fatal(err)
	}
	if still != 0 {
		t.Fatalf("auto row count %d want 0", still)
	}
}

// TestW3ScopedSaveChangeBesidePurchased — open auto beside finished purchased: added then taken_off.
func TestW3ScopedSaveChangeBesidePurchased(t *testing.T) {
	deps := newTestStore(t)
	scope := scopeOf(t, deps.db)
	ctx := context.Background()
	itemID := createTestItem(t, deps, ctx, "prod-beside-purch", "Beside Purch")
	key := shopping.ItemLineKey(itemID)
	purchasedAt := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)
	lineAdded := time.Date(2026, 3, 1, 11, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)
	mustExecShopping(t, deps.db, `INSERT INTO shopping_list_items (id, household_id, user_id, item_id, quantity, source, purchased_at, created_at)
		VALUES ('line-purch-finished', ?, ?, ?, 2, 'auto', ?, ?)`,
		scope.HouseholdID(), scope.HouseholdID(), itemID, purchasedAt, lineAdded)
	stockInAt := time.Date(2026, 3, 2, 12, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)
	mustExecShopping(t, deps.db, `INSERT INTO stock_moves (id, household_id, item_id, product_id, direction, quantity, at, source, tracked, created_at)
		SELECT ?, ?, ?, product_id, 'in', 1, ?, 'scan', 1, ? FROM items WHERE id = ?`,
		"mv-beside-purch", scope.HouseholdID(), itemID, stockInAt, stockInAt, itemID)
	now := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	if err := deps.shopping.ScopedSavePlannedLines(ctx, scope, shopping.ScopedSaveParams{
		Lines:        []shopping.PlannedLine{{ItemID: itemID, Quantity: 1}},
		AffectedKeys: map[string]struct{}{key: {}},
		Cause:        shopping.CauseListUpdated,
		Now:          now,
	}); err != nil {
		t.Fatal(err)
	}
	var effect string
	if err := deps.db.QueryRow(`SELECT effect FROM list_line_changes WHERE household_id = ? AND line_key = ?`,
		scope.HouseholdID(), key).Scan(&effect); err != nil || effect != string(shopping.EffectAdded) {
		t.Fatalf("after upsert effect %q err %v", effect, err)
	}
	if err := deps.shopping.ScopedSavePlannedLines(ctx, scope, shopping.ScopedSaveParams{
		Lines:        []shopping.PlannedLine{{ItemID: itemID, Quantity: 0}},
		AffectedKeys: map[string]struct{}{key: {}},
		Cause:        shopping.CausePutAway,
		Now:          now.Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	var reason string
	if err := deps.db.QueryRow(`SELECT effect, COALESCE(reason,'') FROM list_line_changes WHERE household_id = ? AND line_key = ?`,
		scope.HouseholdID(), key).Scan(&effect, &reason); err != nil {
		t.Fatal(err)
	}
	if effect != string(shopping.EffectTakenOff) {
		t.Fatalf("put_away effect %q", effect)
	}
	var purchCount int
	if err := deps.db.QueryRow(`SELECT COUNT(*) FROM shopping_list_items WHERE id = 'line-purch-finished'`).Scan(&purchCount); err != nil || purchCount != 1 {
		t.Fatalf("purchased row count %d", purchCount)
	}
}