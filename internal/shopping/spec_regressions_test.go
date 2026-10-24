package shopping_test

import (
	"context"
	"testing"

	"github.com/Rhionin/stockbot/internal/shopping"
)

// A target change is a new plan passed to SavePlannedLines. It may add or
// remove unpurchased planned lines. Purchased (checked-off) rows and manual
// rows stay. Putting the previous plan back is the don't-restock undo: it
// restores the unpurchased line that was removed and does not re-add a line
// that was already bought.
func TestPlanChangesOnlyTouchUnpurchasedPlannedLines(t *testing.T) {
	deps := newTestStore(t)
	ctx := context.Background()
	scope := scopeOf(t, deps.db)
	boughtID := createTestItem(t, deps, ctx, "bought", "Bought Beans")
	openID := createTestItem(t, deps, ctx, "open", "Open Oats")
	manualID := createTestItem(t, deps, ctx, "manual", "Manual Milk")
	if _, err := deps.shopping.AddManualItem(ctx, scope, manualID, 5); err != nil {
		t.Fatal(err)
	}

	planned := []shopping.PlannedLine{
		{ItemID: boughtID, Quantity: 2, Note: "keep 2"},
		{ItemID: openID, Quantity: 3, Note: "keep 3"},
	}
	if err := deps.shopping.SavePlannedLines(ctx, scope, planned, nil); err != nil {
		t.Fatal(err)
	}
	active, err := deps.shopping.ListUnpurchased(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	var boughtRow string
	for _, row := range active {
		if row.ItemID == boughtID && row.Source == "auto" {
			boughtRow = row.ID
		}
	}
	if boughtRow == "" {
		t.Fatalf("setup: planned lines = %+v", active)
	}
	if err := deps.shopping.MarkPurchased(ctx, scope, boughtRow); err != nil {
		t.Fatal(err)
	}

	// The target went to 0, so the plan no longer has those lines.
	if err := deps.shopping.SavePlannedLines(ctx, scope, nil, nil); err != nil {
		t.Fatal(err)
	}
	active, err = deps.shopping.ListUnpurchased(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range active {
		switch row.ItemID {
		case openID:
			t.Errorf("dropping the plan left the unpurchased planned line %+v", row)
		case boughtID:
			t.Errorf("dropping the plan brought back a purchased line as unpurchased %+v", row)
		case manualID:
			if row.Quantity != 5 || row.Source != "manual" || row.ID == "" {
				t.Errorf("manual row changed when the plan was dropped: %+v", row)
			}
		}
	}
	if !hasManual(active, manualID, 5) {
		t.Errorf("manual row did not survive dropping the plan: %+v", active)
	}
	purchased, err := deps.shopping.GetItemByID(ctx, scope, boughtRow)
	if err != nil {
		t.Fatal(err)
	}
	if purchased == nil || purchased.PurchasedAt == nil || purchased.Quantity != 2 || purchased.Source != "auto" {
		t.Errorf("setting the target to 0 removed the purchased (checked-off) row %s: %+v", boughtRow, purchased)
	}

	// Undo of don't-restock puts the previous plan back.
	if err := deps.shopping.SavePlannedLines(ctx, scope, planned, nil); err != nil {
		t.Fatal(err)
	}
	active, err = deps.shopping.ListUnpurchased(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	var openBack bool
	for _, row := range active {
		switch row.ItemID {
		case openID:
			openBack = row.Quantity == 3 && row.Note == "keep 3" && row.Source == "auto"
		case boughtID:
			t.Errorf("undo of don't-restock re-added an already-bought line: %+v", row)
		case manualID:
			if row.Quantity != 5 || row.Source != "manual" {
				t.Errorf("manual row changed when the plan came back: %+v", row)
			}
		}
	}
	if !openBack {
		t.Errorf("undo of don't-restock did not restore the unpurchased planned line: %+v", active)
	}
	if !hasManual(active, manualID, 5) {
		t.Errorf("manual row did not survive switching the target back: %+v", active)
	}
	purchased, err = deps.shopping.GetItemByID(ctx, scope, boughtRow)
	if err != nil {
		t.Fatal(err)
	}
	if purchased == nil || purchased.PurchasedAt == nil || purchased.Quantity != 2 {
		t.Errorf("switching the target back did not keep the purchased row %s: %+v", boughtRow, purchased)
	}
}

// A different target still plans the item, at a new quantity. The purchased
// row stays as it was. The new need is an unpurchased planned line.
func TestTargetChangeLeavesThePurchasedRowAndPlansANewLine(t *testing.T) {
	deps := newTestStore(t)
	ctx := context.Background()
	scope := scopeOf(t, deps.db)
	itemID := createTestItem(t, deps, ctx, "bought", "Bought Beans")
	original := shopping.PlannedLine{ItemID: itemID, Quantity: 2, Note: "keep 2"}
	if err := deps.shopping.SavePlannedLines(ctx, scope, []shopping.PlannedLine{original}, nil); err != nil {
		t.Fatal(err)
	}
	active, err := deps.shopping.ListUnpurchased(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 1 {
		t.Fatalf("setup: %+v", active)
	}
	boughtRow := active[0].ID
	if err := deps.shopping.MarkPurchased(ctx, scope, boughtRow); err != nil {
		t.Fatal(err)
	}

	changed := shopping.PlannedLine{ItemID: itemID, Quantity: 4, Note: "keep 4"}
	if err := deps.shopping.SavePlannedLines(ctx, scope, []shopping.PlannedLine{changed}, nil); err != nil {
		t.Fatal(err)
	}
	purchased, err := deps.shopping.GetItemByID(ctx, scope, boughtRow)
	if err != nil {
		t.Fatal(err)
	}
	if purchased == nil || purchased.PurchasedAt == nil || purchased.Quantity != 2 || purchased.Note != "keep 2" {
		t.Errorf("the target change rewrote the purchased row instead of leaving it: %+v", purchased)
	}
	active, err = deps.shopping.ListUnpurchased(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	var planned *shopping.ShoppingListItem
	for i := range active {
		if active[i].ItemID == itemID {
			row := active[i]
			planned = &row
		}
	}
	if planned == nil || planned.ID == boughtRow || planned.Quantity != 4 || planned.Source != "auto" {
		t.Errorf("the target change should add a separate unpurchased planned line of 4, got %+v (purchased id %s)", active, boughtRow)
	}
}

func hasManual(rows []shopping.ShoppingListItem, itemID string, quantity int) bool {
	for _, row := range rows {
		if row.ItemID == itemID && row.Source == "manual" && row.Quantity == quantity {
			return true
		}
	}
	return false
}
