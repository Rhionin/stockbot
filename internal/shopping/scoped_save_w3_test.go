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