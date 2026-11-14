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