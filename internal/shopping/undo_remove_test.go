package shopping_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/Rhionin/stockbot/internal/shopping"
	"github.com/Rhionin/stockbot/internal/tenant"
)

func TestUndoRemoveRestoresAutoLine(t *testing.T) {
	deps := newTestStore(t)
	ctx := context.Background()
	scope := scopeOf(t, deps.db)
	itemID := createTestItem(t, deps, ctx, "undo-one", "Undo One")
	line := seedAutoLine(t, deps, itemID)
	if err := deps.shopping.RemoveItem(ctx, scope, line.ID); err != nil {
		t.Fatal(err)
	}
	planned := shopping.PlannedLine{ItemID: itemID, Quantity: line.Quantity, Note: line.Note}
	restored, err := deps.shopping.UndoRemove(ctx, scope, itemID, planned)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Source != "auto" || restored.ItemID != itemID || restored.Quantity != line.Quantity {
		t.Fatalf("restored: %#v", restored)
	}
	if skipActive(t, deps.db, scope.HouseholdID(), itemID) {
		t.Fatal("skip should be cleared")
	}
}

func TestUndoRemoveGroupWhenPlannerPicksDifferentMember(t *testing.T) {
	deps := newTestStore(t)
	ctx := context.Background()
	scope := scopeOf(t, deps.db)
	household := scope.HouseholdID()
	const groupID = "grp-undo-switch"
	if _, err := deps.db.Exec(`INSERT INTO product_groups (id, household_id, user_id, name, name_key, rule, rule_confirmed) VALUES (?, ?, ?, 'Dairy', 'dairy', 'favorite', 1)`,
		groupID, household, household); err != nil {
		t.Fatal(err)
	}
	itemA := createTestItem(t, deps, ctx, "undo-sw-a", "Milk A")
	itemB := createTestItem(t, deps, ctx, "undo-sw-b", "Milk B")
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
	if !skipActive(t, deps.db, household, itemA) {
		t.Fatal("skip on removed member")
	}
	planned := shopping.PlannedLine{ItemID: itemB, GroupKey: groupID, Quantity: 2, Note: "group"}
	restored, err := deps.shopping.UndoRemove(ctx, scope, itemA, planned)
	if err != nil {
		t.Fatal(err)
	}
	if restored.GroupID != groupID || restored.ItemID != itemB || restored.Source != "auto" {
		t.Fatalf("restored: %#v", restored)
	}
	if skipActive(t, deps.db, household, itemA) {
		t.Fatal("skip should be cleared")
	}
}

func TestUndoRemoveGroupedRestoresLine(t *testing.T) {
	deps := newTestStore(t)
	ctx := context.Background()
	scope := scopeOf(t, deps.db)
	household := scope.HouseholdID()
	const groupID = "grp-undo"
	if _, err := deps.db.Exec(`INSERT INTO product_groups (id, household_id, user_id, name, name_key, rule, rule_confirmed) VALUES (?, ?, ?, 'Dairy', 'dairy', 'favorite', 1)`,
		groupID, household, household); err != nil {
		t.Fatal(err)
	}
	itemA := createTestItem(t, deps, ctx, "undo-grp-a", "Milk A")
	itemB := createTestItem(t, deps, ctx, "undo-grp-b", "Milk B")
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
	planned := shopping.PlannedLine{ItemID: itemA, GroupKey: groupID, Quantity: 2, Note: "group"}
	if _, err := deps.shopping.UndoRemove(ctx, scope, itemA, planned); err != nil {
		t.Fatal(err)
	}
	active, _ = deps.shopping.ListUnpurchased(ctx, scope)
	if len(active) != 1 || active[0].GroupID != groupID || active[0].Source != "auto" {
		t.Fatalf("group restored: %#v", active)
	}
}

func TestUndoRemoveMissingSkipIsNotFound(t *testing.T) {
	deps := newTestStore(t)
	ctx := context.Background()
	scope := scopeOf(t, deps.db)
	itemID := createTestItem(t, deps, ctx, "undo-miss", "Miss")
	planned := shopping.PlannedLine{ItemID: itemID, Quantity: 1, Note: "n"}
	_, err := deps.shopping.UndoRemove(ctx, scope, itemID, planned)
	if !errors.Is(err, shopping.ErrTripSkipNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestUndoRemoveDoubleIsIdempotent(t *testing.T) {
	deps := newTestStore(t)
	ctx := context.Background()
	scope := scopeOf(t, deps.db)
	itemID := createTestItem(t, deps, ctx, "undo-twice", "Twice")
	line := seedAutoLine(t, deps, itemID)
	if err := deps.shopping.RemoveItem(ctx, scope, line.ID); err != nil {
		t.Fatal(err)
	}
	planned := shopping.PlannedLine{ItemID: itemID, Quantity: line.Quantity, Note: line.Note}
	if _, err := deps.shopping.UndoRemove(ctx, scope, itemID, planned); err != nil {
		t.Fatal(err)
	}
	_, err := deps.shopping.UndoRemove(ctx, scope, itemID, planned)
	if !errors.Is(err, shopping.ErrTripSkipNotFound) {
		t.Fatalf("second undo: %v", err)
	}
}

func TestUndoRemoveGroupOtherHouseholdUntouched(t *testing.T) {
	deps := newTestStore(t)
	ctx := context.Background()
	primary := scopeOf(t, deps.db)
	household := primary.HouseholdID()
	const groupID = "grp-xhh"
	if _, err := deps.db.Exec(`INSERT INTO product_groups (id, household_id, user_id, name, name_key, rule, rule_confirmed) VALUES (?, ?, ?, 'G', 'g', 'favorite', 1)`,
		groupID, household, household); err != nil {
		t.Fatal(err)
	}
	itemA := createTestItem(t, deps, ctx, "xh-a", "A")
	itemB := createTestItem(t, deps, ctx, "xh-b", "B")
	for _, itemID := range []string{itemA, itemB} {
		if _, err := deps.db.Exec(`INSERT INTO product_group_members (household_id, group_id, product_id) SELECT household_id, ?, product_id FROM items WHERE id = ?`,
			groupID, itemID); err != nil {
			t.Fatal(err)
		}
	}
	lines := []shopping.PlannedLine{{ItemID: itemA, GroupKey: groupID, Quantity: 1, Note: "g"}}
	if err := deps.shopping.SavePlannedLines(ctx, primary, lines, []shopping.ShelfMember{{ItemID: itemA, GroupKey: groupID}, {ItemID: itemB, GroupKey: groupID}}); err != nil {
		t.Fatal(err)
	}
	active, _ := deps.shopping.ListUnpurchased(ctx, primary)
	var lineID string
	for _, row := range active {
		lineID = row.ID
	}
	if err := deps.shopping.RemoveItem(ctx, primary, lineID); err != nil {
		t.Fatal(err)
	}
	insertCedarHousehold(t, deps)
	other, err := tenant.New(cedarHouseholdID)
	if err != nil {
		t.Fatal(err)
	}
	planned := shopping.PlannedLine{ItemID: itemA, GroupKey: groupID, Quantity: 1, Note: "g"}
	_, err = deps.shopping.UndoRemove(ctx, other, itemA, planned)
	if !errors.Is(err, shopping.ErrTripSkipNotFound) {
		t.Fatalf("other household undo: %v", err)
	}
	if !skipActive(t, deps.db, household, itemA) {
		t.Fatal("primary group skip should remain")
	}
}

func TestUndoRemoveOtherHouseholdSkipUntouched(t *testing.T) {
	deps := newTestStore(t)
	ctx := context.Background()
	primary := scopeOf(t, deps.db)
	itemID := createTestItem(t, deps, ctx, "undo-xhh", "Cross HH")
	line := seedAutoLine(t, deps, itemID)
	if err := deps.shopping.RemoveItem(ctx, primary, line.ID); err != nil {
		t.Fatal(err)
	}
	insertCedarHousehold(t, deps)
	other, err := tenant.New(cedarHouseholdID)
	if err != nil {
		t.Fatal(err)
	}
	planned := shopping.PlannedLine{ItemID: itemID, Quantity: line.Quantity, Note: line.Note}
	_, err = deps.shopping.UndoRemove(ctx, other, itemID, planned)
	if !errors.Is(err, shopping.ErrTripSkipNotFound) {
		t.Fatalf("other household undo: %v", err)
	}
	if !skipActive(t, deps.db, primary.HouseholdID(), itemID) {
		t.Fatal("primary skip should remain")
	}
	active, _ := deps.shopping.ListUnpurchased(ctx, primary)
	if len(active) != 0 {
		t.Fatalf("primary list should stay empty: %#v", active)
	}
}

func TestUndoRemoveRejectsMismatchedPlannedItem(t *testing.T) {
	deps := newTestStore(t)
	ctx := context.Background()
	scope := scopeOf(t, deps.db)
	itemID := createTestItem(t, deps, ctx, "undo-mismatch", "Mismatch")
	line := seedAutoLine(t, deps, itemID)
	if err := deps.shopping.RemoveItem(ctx, scope, line.ID); err != nil {
		t.Fatal(err)
	}
	planned := shopping.PlannedLine{ItemID: "other-item", Quantity: 1, Note: "n"}
	_, err := deps.shopping.UndoRemove(ctx, scope, itemID, planned)
	if err == nil {
		t.Fatal("expected mismatch error")
	}
	if !skipActive(t, deps.db, scope.HouseholdID(), itemID) {
		t.Fatal("skip should remain when undo fails")
	}
}

func TestUndoRemoveWithPrincipalDoesNotRequireActor(t *testing.T) {
	deps := newTestStore(t)
	ctx := auth.WithPrincipal(context.Background(), auth.Principal{Kind: "account", UserID: "user-undo"})
	scope := scopeOf(t, deps.db)
	if _, err := deps.db.Exec(`INSERT INTO users (id, name, email_norm, pw_hash, created_at) VALUES ('user-undo', 'U', 'u@example.com', 'x', '2020-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	itemID := createTestItem(t, deps, ctx, "undo-actor", "Actor")
	line := seedAutoLine(t, deps, itemID)
	if err := deps.shopping.RemoveItem(ctx, scope, line.ID); err != nil {
		t.Fatal(err)
	}
	planned := shopping.PlannedLine{ItemID: itemID, Quantity: line.Quantity, Note: line.Note}
	if _, err := deps.shopping.UndoRemove(ctx, scope, itemID, planned); err != nil {
		t.Fatal(err)
	}
}
