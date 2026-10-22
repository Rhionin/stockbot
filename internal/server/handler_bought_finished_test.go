package server

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/scan"
)

func TestStockInThenTickThenUseReplans(t *testing.T) {
	handler, env := setupTestWithDB(t)
	now := time.Now().UTC()
	beginUsing(t, env.DB, now.Add(-30*24*time.Hour))
	createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", "prod-tick", "Tick", "item-tick")
	insertConsumptionEvent(t, env.DB, "ce-tick-1", "item-tick", now.Add(-2*24*time.Hour))

	lines := fillShopping(t, handler)
	bought := shopLineByItem(lines, "item-tick")
	if bought == nil || bought.Note != "replacing 1 you used" || bought.Quantity != 1 {
		t.Fatalf("setup fill: %+v", lines)
	}
	stockedAt := time.Now().UTC()
	commitScanStockIn(t, handler, env, "scan-tick-in", "prod-tick", stockedAt)
	exchangeJSON(t, handler, http.MethodPatch, "/api/shopping-list/items/"+bought.ID, `{"purchased":true}`, http.StatusOK)
	insertConsumptionEvent(t, env.DB, "ce-tick-2", "item-tick", stockedAt.Add(time.Second))

	again := shopLineByItem(fillShopping(t, handler), "item-tick")
	if again == nil || again.ID == bought.ID || again.Note != "replacing 1 you used" || again.Quantity != 1 {
		t.Fatalf("stocked in, then ticked, then used, but the next fill did not re-plan Tick: %+v", fillShopping(t, handler))
	}
	if listed := shopLineByItem(shoppingLines(t, handler), "item-tick"); listed == nil || listed.ID != again.ID || listed.Note != again.Note {
		t.Fatalf("shopping list after the re-plan: %+v", shoppingLines(t, handler))
	}
	exchangeJSON(t, handler, http.MethodDelete, "/api/shopping-list/items/"+bought.ID, ``, http.StatusNotFound)
}

func TestTickThenStockInThenUseReplans(t *testing.T) {
	handler, env := setupTestWithDB(t)
	now := time.Now().UTC()
	beginUsing(t, env.DB, now.Add(-30*24*time.Hour))
	createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", "prod-after", "After", "item-after")
	insertConsumptionEvent(t, env.DB, "ce-after-1", "item-after", now.Add(-2*24*time.Hour))

	lines := fillShopping(t, handler)
	bought := shopLineByItem(lines, "item-after")
	if bought == nil || bought.Note != "replacing 1 you used" || bought.Quantity != 1 {
		t.Fatalf("setup fill: %+v", lines)
	}
	exchangeJSON(t, handler, http.MethodPatch, "/api/shopping-list/items/"+bought.ID, `{"purchased":true}`, http.StatusOK)
	stockedAt := time.Now().UTC()
	commitScanStockIn(t, handler, env, "scan-after-in", "prod-after", stockedAt)
	insertConsumptionEvent(t, env.DB, "ce-after-2", "item-after", stockedAt.Add(time.Second))

	again := shopLineByItem(fillShopping(t, handler), "item-after")
	if again == nil || again.ID == bought.ID || again.Note != "replacing 1 you used" || again.Quantity != 1 {
		t.Fatalf("ticked, then stocked in, then used, but the next fill did not re-plan After: %+v", fillShopping(t, handler))
	}
	exchangeJSON(t, handler, http.MethodDelete, "/api/shopping-list/items/"+bought.ID, ``, http.StatusNotFound)
}

func TestTickWithNoStockInSinceTheLineWasAddedHoldsTheDuplicate(t *testing.T) {
	handler, env := setupTestWithDB(t)
	now := time.Now().UTC()
	beginUsing(t, env.DB, now.Add(-30*24*time.Hour))
	createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", "prod-hold", "Hold", "item-hold")
	insertConsumptionEvent(t, env.DB, "ce-hold-1", "item-hold", now.Add(-2*24*time.Hour))

	lines := fillShopping(t, handler)
	bought := shopLineByItem(lines, "item-hold")
	if bought == nil || bought.ID == "" || bought.Note != "replacing 1 you used" {
		t.Fatalf("setup fill: %+v", lines)
	}
	// A stock-in from before the line was added does not finish it.
	insertStockIn(t, env.DB, "prod-hold", now.Add(-72*time.Hour))
	exchangeJSON(t, handler, http.MethodPatch, "/api/shopping-list/items/"+bought.ID, `{"purchased":true}`, http.StatusOK)

	if again := fillShopping(t, handler); shopLineByItem(again, "item-hold") != nil || len(again) != 0 {
		t.Fatalf("ticked with no stock-in since the line was added, but a duplicate was planned: %+v", again)
	}
	if listed := shoppingLines(t, handler); len(listed) != 0 {
		t.Fatalf("shopping list showed the held line: %+v", listed)
	}
}

func TestStockInAtTheAddedTimeFinishesTheBoughtLine(t *testing.T) {
	handler, env := setupTestWithDB(t)
	now := time.Now().UTC()
	beginUsing(t, env.DB, now.Add(-30*24*time.Hour))
	createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", "prod-equal", "Equal", "item-equal")
	insertConsumptionEvent(t, env.DB, "ce-equal-1", "item-equal", now.Add(-2*24*time.Hour))

	lines := fillShopping(t, handler)
	bought := shopLineByItem(lines, "item-equal")
	if bought == nil || bought.Note != "replacing 1 you used" {
		t.Fatalf("setup fill: %+v", lines)
	}
	// The list's added time is not on the API. Pin the line and its stock-in
	// to one instant so equality is the case under test.
	added := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	if _, err := env.DB.Exec(`UPDATE shopping_list_items SET created_at = ? WHERE id = ?`, added, bought.ID); err != nil {
		t.Fatal(err)
	}
	insertStockIn(t, env.DB, "prod-equal", added)
	exchangeJSON(t, handler, http.MethodPatch, "/api/shopping-list/items/"+bought.ID, `{"purchased":true}`, http.StatusOK)
	insertConsumptionEvent(t, env.DB, "ce-equal-2", "item-equal", added.Add(time.Minute))

	again := shopLineByItem(fillShopping(t, handler), "item-equal")
	if again == nil || again.ID == bought.ID || again.Note != "replacing 1 you used" || again.Quantity != 1 {
		t.Fatalf("a stock-in at the line's added time left the bought line holding the next fill: %+v", fillShopping(t, handler))
	}
	exchangeJSON(t, handler, http.MethodDelete, "/api/shopping-list/items/"+bought.ID, ``, http.StatusNotFound)
}

func TestFillRemovesEveryFinishedBoughtLine(t *testing.T) {
	handler, env := setupTestWithDB(t)
	now := time.Now().UTC()
	beginUsing(t, env.DB, now.Add(-30*24*time.Hour))
	createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", "prod-one", "One", "item-one")
	createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", "prod-two", "Two", "item-two")
	insertConsumptionEvent(t, env.DB, "ce-one", "item-one", now.Add(-2*24*time.Hour))
	insertConsumptionEvent(t, env.DB, "ce-two", "item-two", now.Add(-2*24*time.Hour))

	lines := fillShopping(t, handler)
	one := shopLineByItem(lines, "item-one")
	two := shopLineByItem(lines, "item-two")
	if one == nil || two == nil || one.ID == "" || two.ID == "" {
		t.Fatalf("setup fill: %+v", lines)
	}
	exchangeJSON(t, handler, http.MethodPatch, "/api/shopping-list/items/"+one.ID, `{"purchased":true}`, http.StatusOK)
	exchangeJSON(t, handler, http.MethodPatch, "/api/shopping-list/items/"+two.ID, `{"purchased":true}`, http.StatusOK)
	stockedAt := time.Now().UTC().Add(time.Minute)
	insertStockIn(t, env.DB, "prod-one", stockedAt)
	insertStockIn(t, env.DB, "prod-two", stockedAt)

	if again := fillShopping(t, handler); len(again) != 0 {
		t.Fatalf("finished bought lines were planned again before another use: %+v", again)
	}
	exchangeJSON(t, handler, http.MethodDelete, "/api/shopping-list/items/"+one.ID, ``, http.StatusNotFound)
	exchangeJSON(t, handler, http.MethodDelete, "/api/shopping-list/items/"+two.ID, ``, http.StatusNotFound)
}

func TestPlannerSkipsShelvedItems(t *testing.T) {
	handler, env := setupTestWithDB(t)
	now := time.Now().UTC()
	beginUsing(t, env.DB, now.Add(-30*24*time.Hour))
	createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", "prod-hidden", "Hidden", "item-hidden")
	createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", "prod-shown", "Shown", "item-shown")
	insertConsumptionEvent(t, env.DB, "ce-hidden", "item-hidden", now.Add(-2*24*time.Hour))
	insertConsumptionEvent(t, env.DB, "ce-shown", "item-shown", now.Add(-2*24*time.Hour))
	if _, err := env.DB.Exec(`UPDATE items SET shelved = 1 WHERE id = 'item-hidden'`); err != nil {
		t.Fatal(err)
	}

	lines := fillShopping(t, handler)
	if shopLineByItem(lines, "item-hidden") != nil {
		t.Fatalf("the planner listed a shelved item: %+v", lines)
	}
	shown := shopLineByItem(lines, "item-shown")
	if shown == nil || shown.Note != "replacing 1 you used" {
		t.Fatalf("the unshelved item was not planned: %+v", lines)
	}
}

func TestAmountTargetClearsTheReplaceNote(t *testing.T) {
	handler, env := setupTestWithDB(t)
	now := time.Now().UTC()
	beginUsing(t, env.DB, now.Add(-30*24*time.Hour))
	createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", "prod-amt", "Amount", "item-amt")
	insertConsumptionEvent(t, env.DB, "ce-amt", "item-amt", now.Add(-2*24*time.Hour))

	lines := fillShopping(t, handler)
	line := shopLineByItem(lines, "item-amt")
	if line == nil || line.Note != "replacing 1 you used" || line.Quantity != 1 {
		t.Fatalf("setup fill: %+v", lines)
	}
	exchangeJSON(t, handler, http.MethodPut, "/api/restock/items/item-amt", `{"kind":"amount","value":4}`, http.StatusOK)

	again := shopLineByItem(fillShopping(t, handler), "item-amt")
	if again == nil || again.Note != "" || again.Quantity != 3 {
		t.Fatalf("amount target left the replace note on the line: %+v", again)
	}
	if listed := shopLineByItem(shoppingLines(t, handler), "item-amt"); listed == nil || listed.Note != "" || listed.Quantity != 3 {
		t.Fatalf("shopping list note: %+v", shoppingLines(t, handler))
	}
}

func TestNewLineDoesNotInheritAFinishedNote(t *testing.T) {
	handler, env := setupTestWithDB(t)
	now := time.Now().UTC()
	beginUsing(t, env.DB, now.Add(-30*24*time.Hour))
	createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", "prod-inherit", "Inherit", "item-inherit")
	insertConsumptionEvent(t, env.DB, "ce-inherit", "item-inherit", now.Add(-2*24*time.Hour))

	lines := fillShopping(t, handler)
	bought := shopLineByItem(lines, "item-inherit")
	if bought == nil || bought.Note != "replacing 1 you used" {
		t.Fatalf("setup fill: %+v", lines)
	}
	exchangeJSON(t, handler, http.MethodPatch, "/api/shopping-list/items/"+bought.ID, `{"purchased":true}`, http.StatusOK)
	commitScanStockIn(t, handler, env, "scan-inherit-in", "prod-inherit", time.Now().UTC())
	exchangeJSON(t, handler, http.MethodPut, "/api/restock/items/item-inherit", `{"kind":"amount","value":4}`, http.StatusOK)

	again := shopLineByItem(fillShopping(t, handler), "item-inherit")
	if again == nil || again.ID == bought.ID || again.Note != "" || again.Quantity != 2 {
		t.Fatalf("the new line kept the finished row's note: %+v", again)
	}
}

func TestScanRestockWritesThePlannerNote(t *testing.T) {
	handler, env := setupTestWithDB(t)
	now := time.Now().UTC()
	beginUsing(t, env.DB, now.Add(-30*24*time.Hour))
	createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", "prod-note", "Noted", "item-note")
	insertConsumptionEvent(t, env.DB, "ce-note", "item-note", now.Add(-2*24*time.Hour))

	lines := fillShopping(t, handler)
	line := shopLineByItem(lines, "item-note")
	if line == nil || line.Note != "replacing 1 you used" {
		t.Fatalf("setup fill: %+v", lines)
	}
	exchangeJSON(t, handler, http.MethodPut, "/api/restock/items/item-note", `{"kind":"amount","value":4}`, http.StatusOK)
	commitScanStockIn(t, handler, env, "scan-note-in", "prod-note", time.Now().UTC())

	again := shopLineByItem(fillShopping(t, handler), "item-note")
	if again == nil || again.Note != "" || again.Quantity != 2 {
		t.Fatalf("scan restock kept the replace note on an amount line: %+v", again)
	}
	if listed := shopLineByItem(shoppingLines(t, handler), "item-note"); listed == nil || listed.Note != "" {
		t.Fatalf("shopping list note: %+v", shoppingLines(t, handler))
	}
}

func commitScanStockIn(t *testing.T, handler http.Handler, env testEnv, id, productID string, at time.Time) {
	t.Helper()
	direction := scan.StockIn
	entry := scan.ScanEntry{
		ID:        id,
		UserID:    "user-1",
		Barcode:   id,
		ScannedAt: at,
		Direction: &direction,
		UnitCount: 1,
		ProductID: &productID,
		Status:    scan.Pending,
	}
	if _, err := scan.NewQueue(env.DB).CreateScanEntry(context.Background(), entry); err != nil {
		t.Fatalf("CreateScanEntry: %v", err)
	}
	exchangeJSON(t, handler, http.MethodPost, "/api/scans/"+id+"/commit", ``, http.StatusOK)
}
