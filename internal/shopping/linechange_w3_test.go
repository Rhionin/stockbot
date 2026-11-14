package shopping_test

import (
	"context"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/shopping"
)

// TestW3T25LineChangeValidateRejectsUnknownEnums — T25.
func TestW3T25LineChangeValidateRejectsUnknownEnums(t *testing.T) {
	ch := shopping.LineChange{
		LineKey: shopping.ItemLineKey("item-571-a"),
		Effect:  shopping.Effect("not_an_effect"),
		Cause:   shopping.CauseTakenOut,
		At:      time.Now().UTC(),
	}
	if err := ch.Validate(); err == nil {
		t.Fatal("expected validation error for effect")
	}
	ch.Effect = shopping.EffectAdded
	ch.Cause = shopping.Cause("bogus")
	if err := ch.Validate(); err == nil {
		t.Fatal("expected validation error for cause")
	}
}

// TestW3T33IdempotentLineChangeAt — T33: identical scoped writes keep at.
func TestW3T33IdempotentLineChangeAt(t *testing.T) {
	deps := newTestStore(t)
	scope := scopeOf(t, deps.db)
	ctx := context.Background()
	itemID := createTestItem(t, deps, ctx, "prod-571-t33", "T33")
	key := shopping.ItemLineKey(itemID)
	t0 := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	params := shopping.ScopedSaveParams{
		Lines:        []shopping.PlannedLine{{ItemID: itemID, Quantity: 2}},
		AffectedKeys: map[string]struct{}{key: {}},
		Cause:        shopping.CauseTakenOut,
		Changes: []shopping.ScopedChangeInput{{
			Key: key, Effect: shopping.EffectAdded, Cause: shopping.CauseTakenOut,
			FromQty: intPtr(0), ToQty: intPtr(2),
		}},
		Now: t0,
	}
	if err := deps.shopping.ScopedSavePlannedLines(ctx, scope, params); err != nil {
		t.Fatal(err)
	}
	var at1 string
	if err := deps.db.QueryRow(`SELECT at FROM list_line_changes WHERE household_id = ? AND line_key = ?`,
		scope.HouseholdID(), key).Scan(&at1); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := deps.db.QueryRow(`SELECT COUNT(*) FROM list_line_changes WHERE household_id = ?`, scope.HouseholdID()).Scan(&count); err != nil {
		t.Fatal(err)
	}
	params.Now = time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	if err := deps.shopping.ScopedSavePlannedLines(ctx, scope, params); err != nil {
		t.Fatal(err)
	}
	var at2 string
	if err := deps.db.QueryRow(`SELECT at FROM list_line_changes WHERE household_id = ? AND line_key = ?`,
		scope.HouseholdID(), key).Scan(&at2); err != nil {
		t.Fatal(err)
	}
	var count2 int
	if err := deps.db.QueryRow(`SELECT COUNT(*) FROM list_line_changes WHERE household_id = ?`, scope.HouseholdID()).Scan(&count2); err != nil {
		t.Fatal(err)
	}
	if count2 != 1 || count != 1 {
		t.Fatalf("count = %d / %d, want 1", count, count2)
	}
	if at1 != at2 {
		t.Fatalf("at changed %q -> %q on identical effect/qty", at1, at2)
	}
	params.Changes[0].ToQty = intPtr(3)
	params.Lines[0].Quantity = 3
	params.Now = time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	if err := deps.shopping.ScopedSavePlannedLines(ctx, scope, params); err != nil {
		t.Fatal(err)
	}
	var at3 string
	if err := deps.db.QueryRow(`SELECT at FROM list_line_changes WHERE household_id = ? AND line_key = ?`,
		scope.HouseholdID(), key).Scan(&at3); err != nil {
		t.Fatal(err)
	}
	if at3 == at2 {
		t.Fatalf("at should advance when qty changes, still %q", at3)
	}
}

func intPtr(n int) *int { return &n }