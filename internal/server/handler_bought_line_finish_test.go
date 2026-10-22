package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"
)

// A bought line is finished once this product has a stock-in at or after the
// moment the line was added, whether that stock-in happened before or after
// the tick. A finished line is cleaned up and the next fill plans normally.
// Until that stock-in, the bought line still blocks an identical line.
//
// A stock-in at the exact added-at instant counts too, and it is not covered
// by a test. The line's added-at is time.Now().UTC() inside SavePlannedLines
// and GET /api/shopping-list does not return it. POST /api/scans stamps
// scannedAt with time.Now(), and neither create nor update accepts a
// timestamp. The test clock is only wired into product lookup and refresh, so
// nothing the API can do places a stock-in on that same instant.

func TestStockInThenTickFinishesTheBoughtLine(t *testing.T) {
	handler, env, itemID, bought := openAmountShortfall(t, "prod-then-tick", "Then Tick", "item-then-tick", "000111222001", 2)

	stockInByScan(t, env, handler, "000111222001")
	still := shopLineByItem(shoppingLines(t, handler), itemID)
	if still == nil || still.ID != bought.ID {
		t.Fatalf("stock-in hid the line before it was ticked: %+v", shoppingLines(t, handler))
	}
	tickBought(t, handler, bought.ID)
	useOneByScan(t, env, handler, "000111222001")

	lines := fillShopping(t, handler)
	planned := shopLineByItem(lines, itemID)
	if planned == nil || planned.ID == bought.ID || planned.Quantity != 1 || planned.Note != "" || planned.Source != "auto" {
		t.Fatalf("stocked in before the tick, but the bought line still held back the next fill: %+v", lines)
	}
	assertBoughtLineCleared(t, handler, bought.ID)
}

func TestTickThenStockInFinishesTheBoughtLine(t *testing.T) {
	handler, env, itemID, bought := openAmountShortfall(t, "prod-tick-then", "Tick Then", "item-tick-then", "000111222002", 2)

	tickBought(t, handler, bought.ID)
	stockInByScan(t, env, handler, "000111222002")
	useOneByScan(t, env, handler, "000111222002")

	lines := fillShopping(t, handler)
	planned := shopLineByItem(lines, itemID)
	if planned == nil || planned.ID == bought.ID || planned.Quantity != 1 || planned.Note != "" || planned.Source != "auto" {
		t.Fatalf("ticked then stocked in, but the next fill did not plan the shortfall: %+v", lines)
	}
	assertBoughtLineCleared(t, handler, bought.ID)
}

func TestBoughtLineWithoutAStockInIsNotDuplicated(t *testing.T) {
	handler, _, itemID, bought := openAmountShortfall(t, "prod-held", "Held", "item-held", "000111222003", 2)

	tickBought(t, handler, bought.ID)
	lines := fillShopping(t, handler)
	if shopLineByItem(lines, itemID) != nil || len(lines) != 0 {
		t.Fatalf("refill with no stock-in duplicated the bought line: %+v", lines)
	}
	if !boughtLineRemains(t, handler, bought.ID) {
		t.Fatal("refill with no stock-in dropped the bought line")
	}
}

func TestOneFillCleansUpEveryFinishedBoughtLine(t *testing.T) {
	handler, env, itemID, first := openAmountShortfall(t, "prod-stale", "Stale", "item-stale", "000111222004", 4)

	stockInByScan(t, env, handler, "000111222004")
	if still := shopLineByItem(shoppingLines(t, handler), itemID); still == nil || still.ID != first.ID {
		t.Fatalf("first stock-in hid the line before it was ticked: %+v", shoppingLines(t, handler))
	}
	tickBought(t, handler, first.ID)

	secondLines := fillShopping(t, handler)
	second := shopLineByItem(secondLines, itemID)
	if second == nil || second.ID == first.ID || second.Quantity != 2 || second.Note != "" {
		t.Fatalf("setup: the new shortfall did not add a second line: %+v", secondLines)
	}

	stockInByScan(t, env, handler, "000111222004")
	if still := shopLineByItem(shoppingLines(t, handler), itemID); still == nil || still.ID != second.ID {
		t.Fatalf("second stock-in hid the line before it was ticked: %+v", shoppingLines(t, handler))
	}
	tickBought(t, handler, second.ID)
	useOneByScan(t, env, handler, "000111222004")

	lines := fillShopping(t, handler)
	firstGone := shoppingLineStatus(t, handler, first.ID) == http.StatusNotFound
	secondGone := shoppingLineStatus(t, handler, second.ID) == http.StatusNotFound
	planned := shopLineByItem(lines, itemID)
	if !firstGone || !secondGone || planned == nil || planned.ID == first.ID || planned.ID == second.ID || planned.Quantity != 2 || planned.Note != "" {
		t.Fatalf("one fill did not clean up both finished lines (first gone %v, second gone %v) or plan the 2 still needed: %+v", firstGone, secondGone, lines)
	}
}

// openAmountShortfall puts one unit on the shelf and an item amount target
// above that, then fills. The open line is the shortfall, with an empty note.
func openAmountShortfall(t *testing.T, productID, name, itemID, barcode string, target int) (http.Handler, testEnv, string, shopLine) {
	t.Helper()
	handler, env := setupTestWithDB(t)
	beginUsing(t, env.DB, time.Now().Add(-30*24*time.Hour))
	createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", productID, name, itemID)
	if err := env.ProductStore.UpsertBarcodeMapping(context.Background(), barcode, productID, "global", ""); err != nil {
		t.Fatal(err)
	}
	exchangeJSON(t, handler, http.MethodPut, "/api/restock/items/"+itemID, `{"kind":"amount","value":`+strconv.Itoa(target)+`}`, http.StatusOK)

	lines := fillShopping(t, handler)
	line := shopLineByItem(lines, itemID)
	if line == nil || line.Quantity != target-1 || line.Note != "" || line.Source != "auto" || line.ID == "" {
		t.Fatalf("setup fill: %+v", lines)
	}
	return handler, env, itemID, *line
}

func stockInByScan(t *testing.T, env testEnv, handler http.Handler, barcode string) {
	t.Helper()
	commitDirectedScan(t, env, handler, barcode, "stock_in")
}

func commitDirectedScan(t *testing.T, env testEnv, handler http.Handler, barcode, direction string) {
	t.Helper()
	body := exchangeJSON(t, handler, http.MethodPost, "/api/scans",
		`{"barcode":"`+barcode+`","direction":"`+direction+`","userId":"user-1"}`, http.StatusCreated)
	var created struct {
		ID        string  `json:"id"`
		Status    string  `json:"status"`
		ProductID *string `json:"productId"`
	}
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("scan: %v body %s", err, body)
	}
	if created.ID == "" || created.Status != "pending" || created.ProductID == nil {
		t.Fatalf("scan was not pending: %s", body)
	}
	env.Refresher.Wait()
	exchangeJSON(t, handler, http.MethodPost, "/api/scans/"+created.ID+"/commit", ``, http.StatusOK)
}

func tickBought(t *testing.T, handler http.Handler, id string) {
	t.Helper()
	exchangeJSON(t, handler, http.MethodPatch, "/api/shopping-list/items/"+id, `{"purchased":true}`, http.StatusOK)
}

func useOneByScan(t *testing.T, env testEnv, handler http.Handler, barcode string) {
	t.Helper()
	commitDirectedScan(t, env, handler, barcode, "stock_out")
}
