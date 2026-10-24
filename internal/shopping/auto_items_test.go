package shopping_test

import (
	"context"
	"testing"

	"github.com/Rhionin/stockbot/internal/shopping"
)

func TestSavePlannedLinesKeepsTouchedLinesAndSkipsRemoved(t *testing.T) {
	deps := newTestStore(t)
	ctx := context.Background()
	scope := scopeOf(t, deps.db)
	itemID := createTestItem(t, deps, ctx, "kept-product", "Kept Product")
	lines := []shopping.PlannedLine{{ItemID: itemID, Quantity: 2, Note: "replacing 2 you used"}}
	if err := deps.shopping.SavePlannedLines(ctx, scope, lines, nil); err != nil {
		t.Fatalf("SavePlannedLines: %v", err)
	}
	active, err := deps.shopping.ListUnpurchased(ctx, scope)
	if err != nil {
		t.Fatalf("ListUnpurchased: %v", err)
	}
	if len(active) != 1 {
		t.Fatalf("rows: %#v", active)
	}
	if err := deps.shopping.SetAdjustment(ctx, scope, active[0].ID, "kroger", 9); err != nil {
		t.Fatalf("SetAdjustment: %v", err)
	}
	changed := []shopping.PlannedLine{{ItemID: itemID, Quantity: 4, Note: "replacing 4 you used"}}
	if err := deps.shopping.SavePlannedLines(ctx, scope, changed, nil); err != nil {
		t.Fatalf("SavePlannedLines refill: %v", err)
	}
	active, err = deps.shopping.ListUnpurchased(ctx, scope)
	if err != nil {
		t.Fatalf("ListUnpurchased after refill: %v", err)
	}
	if len(active) != 1 || active[0].Quantity != 2 || active[0].Note != "replacing 2 you used" {
		t.Fatalf("touched line changed: %#v", active)
	}
	if err := deps.shopping.RemoveItem(ctx, scope, active[0].ID); err != nil {
		t.Fatalf("RemoveItem: %v", err)
	}
	if err := deps.shopping.SavePlannedLines(ctx, scope, changed, nil); err != nil {
		t.Fatalf("SavePlannedLines after remove: %v", err)
	}
	active, err = deps.shopping.ListUnpurchased(ctx, scope)
	if err != nil {
		t.Fatalf("ListUnpurchased after remove: %v", err)
	}
	if len(active) != 0 {
		t.Fatalf("removed line returned: %#v", active)
	}
}

func TestSavePlannedLinesSnapshotsAutoRowsAndLeavesManuals(t *testing.T) {
	deps := newTestStore(t)
	ctx := context.Background()
	scope := scopeOf(t, deps.db)
	autoID := createTestItem(t, deps, ctx, "auto-product", "Auto Product")
	manualID := createTestItem(t, deps, ctx, "manual-product", "Manual Product")
	if _, err := deps.shopping.AddManualItem(ctx, scope, manualID, 5); err != nil {
		t.Fatalf("AddManualItem: %v", err)
	}

	lines := []shopping.PlannedLine{
		{ItemID: autoID, Quantity: 2, Note: "replacing 2 you used"},
		{ItemID: manualID, Quantity: 9, Note: "ignored", Manual: true},
	}
	if err := deps.shopping.SavePlannedLines(ctx, scope, lines, nil); err != nil {
		t.Fatalf("SavePlannedLines: %v", err)
	}
	active, err := deps.shopping.ListUnpurchased(ctx, scope)
	if err != nil {
		t.Fatalf("ListUnpurchased: %v", err)
	}
	if len(active) != 2 {
		t.Fatalf("rows: got %#v", active)
	}
	var autoRow, manualRow shopping.ShoppingListItem
	for _, row := range active {
		switch row.ItemID {
		case autoID:
			autoRow = row
		case manualID:
			manualRow = row
		}
	}
	if autoRow.Quantity != 2 || autoRow.Note != "replacing 2 you used" || autoRow.ID == "" {
		t.Fatalf("auto row: %#v", autoRow)
	}
	if manualRow.Quantity != 5 || manualRow.Source != "manual" {
		t.Fatalf("manual row was resized: %#v", manualRow)
	}

	if err := deps.shopping.MarkPurchased(ctx, scope, autoRow.ID); err != nil {
		t.Fatalf("MarkPurchased: %v", err)
	}
	if err := deps.shopping.SavePlannedLines(ctx, scope, lines, nil); err != nil {
		t.Fatalf("SavePlannedLines refresh: %v", err)
	}
	active, err = deps.shopping.ListUnpurchased(ctx, scope)
	if err != nil {
		t.Fatalf("ListUnpurchased after purchase: %v", err)
	}
	if len(active) != 1 || active[0].ItemID != manualID {
		t.Fatalf("unchanged plan should keep the purchase hidden, got %#v", active)
	}

	changed := []shopping.PlannedLine{{ItemID: autoID, Quantity: 3, Note: "replacing 3 you used"}}
	if err := deps.shopping.SavePlannedLines(ctx, scope, changed, nil); err != nil {
		t.Fatalf("SavePlannedLines changed: %v", err)
	}
	active, err = deps.shopping.ListUnpurchased(ctx, scope)
	if err != nil {
		t.Fatalf("ListUnpurchased after change: %v", err)
	}
	found := false
	for _, row := range active {
		if row.ItemID != autoID {
			continue
		}
		found = true
		if row.ID == autoRow.ID || row.Quantity != 3 || row.PurchasedAt != nil {
			t.Fatalf("changed plan should add a new unpurchased line, got %#v", row)
		}
	}
	if !found {
		t.Fatalf("auto row missing after change: %#v", active)
	}
	purchased, err := deps.shopping.GetItemByID(ctx, scope, autoRow.ID)
	if err != nil {
		t.Fatal(err)
	}
	if purchased == nil || purchased.PurchasedAt == nil || purchased.Quantity != 2 || purchased.Note != "replacing 2 you used" {
		t.Fatalf("changed plan rewrote the purchased row: %#v", purchased)
	}

	if err := deps.shopping.SavePlannedLines(ctx, scope, nil, nil); err != nil {
		t.Fatalf("SavePlannedLines empty: %v", err)
	}
	active, err = deps.shopping.ListUnpurchased(ctx, scope)
	if err != nil {
		t.Fatalf("ListUnpurchased after clear: %v", err)
	}
	if len(active) != 1 || active[0].ItemID != manualID || active[0].Quantity != 5 {
		t.Fatalf("clearing auto rows should leave the manual, got %#v", active)
	}
}

func TestSavePlannedLinesKeepsPurchasedRowsAcrossATargetChange(t *testing.T) {
	deps := newTestStore(t)
	ctx := context.Background()
	scope := scopeOf(t, deps.db)
	itemID := createTestItem(t, deps, ctx, "bought-product", "Bought Product")
	manualID := createTestItem(t, deps, ctx, "kept-manual", "Kept Manual")
	if _, err := deps.shopping.AddManualItem(ctx, scope, manualID, 4); err != nil {
		t.Fatalf("AddManualItem: %v", err)
	}
	line := shopping.PlannedLine{ItemID: itemID, Quantity: 2, Note: "replacing 2 you used"}
	if err := deps.shopping.SavePlannedLines(ctx, scope, []shopping.PlannedLine{line}, nil); err != nil {
		t.Fatalf("SavePlannedLines: %v", err)
	}
	active, err := deps.shopping.ListUnpurchased(ctx, scope)
	if err != nil {
		t.Fatalf("ListUnpurchased: %v", err)
	}
	var bought shopping.ShoppingListItem
	for _, row := range active {
		if row.ItemID == itemID {
			bought = row
		}
	}
	if bought.ID == "" {
		t.Fatal("missing planned row")
	}
	if err := deps.shopping.MarkPurchased(ctx, scope, bought.ID); err != nil {
		t.Fatalf("MarkPurchased: %v", err)
	}

	if err := deps.shopping.SavePlannedLines(ctx, scope, nil, nil); err != nil {
		t.Fatalf("SavePlannedLines drop: %v", err)
	}
	if err := deps.shopping.SavePlannedLines(ctx, scope, []shopping.PlannedLine{line}, nil); err != nil {
		t.Fatalf("SavePlannedLines undo: %v", err)
	}

	active, err = deps.shopping.ListUnpurchased(ctx, scope)
	if err != nil {
		t.Fatalf("ListUnpurchased after undo: %v", err)
	}
	if len(active) != 1 || active[0].ItemID != manualID || active[0].Quantity != 4 {
		t.Fatalf("list changed: %#v", active)
	}
	var purchased int
	if err := deps.db.QueryRow(`SELECT COUNT(*) FROM shopping_list_items WHERE id = ? AND household_id = ? AND purchased_at IS NOT NULL`, bought.ID, scope.HouseholdID()).Scan(&purchased); err != nil {
		t.Fatal(err)
	}
	if purchased != 1 {
		t.Fatal("purchased row was deleted or reopened")
	}
}
