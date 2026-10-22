package server

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/restock"
	"github.com/Rhionin/stockbot/internal/scan"
)

// Purchased-row lifecycle and wipe visibility from the restock rulings on
// #231 and #240. A checked-off row blocks an identical line only until that
// product is stocked in at or after the line was added, a new cart is started,
// or the line is cleared. A wipe empties the inventory list and does not shop
// for what it removed; the saved item target is in effect again once that
// product is stocked in.

func TestRefillBeforeStockInDoesNotDuplicateTheBoughtLine(t *testing.T) {
	handler, env := setupTestWithDB(t)
	now := time.Now().UTC()
	boughtID := buyReplacement(t, handler, env, now)

	again := fillShopping(t, handler)
	if len(again) != 0 {
		t.Fatalf("refill before stock-in duplicated the bought line: %+v", again)
	}
	if !boughtLineRemains(t, handler, boughtID) {
		t.Fatal("refill before stock-in dropped the bought line")
	}
}

func TestUseBuyStockInUseAgainComesBack(t *testing.T) {
	handler, env := setupTestWithDB(t)
	now := time.Now().UTC()
	boughtID := buyReplacement(t, handler, env, now)

	// The purchase is waiting to be stocked in. A stock-in at or after the
	// line was added ends the protection; the next use has to come back.
	// now is earlier than that added time, because fill creates the line.
	stockedAt := time.Now().UTC().Add(time.Second)
	insertStockIn(t, env.DB, "prod-cyc", stockedAt)
	if lines := fillShopping(t, handler); len(lines) != 0 {
		t.Fatalf("stock-in of the purchase put Cycle back on the list before it was used again: %+v", lines)
	}
	insertConsumptionEvent(t, env.DB, "ce-cyc-2", "item-cyc", stockedAt.Add(time.Second))
	fillShopping(t, handler)

	lines := shoppingLines(t, handler)
	line := shopLineByItem(lines, "item-cyc")
	if line == nil || line.ID == boughtID || line.Note != "replacing 1 you used" || line.Quantity != 1 {
		t.Fatalf("used again after restock, but the bought line was not cleaned up into a new list line: %+v", lines)
	}
	assertBoughtLineCleared(t, handler, boughtID)
}

func TestStartingANewCartEndsTheBoughtLine(t *testing.T) {
	handler, env := setupTestWithDB(t)
	now := time.Now().UTC()
	boughtID := buyReplacement(t, handler, env, now)
	registerOpenAndKroger(env)

	exchangeJSON(t, handler, http.MethodPost, "/api/providers/"+openProvider+"/ledger/reset", ``, http.StatusCreated)
	fillShopping(t, handler)

	lines := shoppingLines(t, handler)
	line := shopLineByItem(lines, "item-cyc")
	if line == nil || line.ID == boughtID || line.Note != "replacing 1 you used" || line.Quantity != 1 {
		t.Fatalf("starting a new cart left the bought line in place; a new trip ends that protection: %+v", lines)
	}
	assertBoughtLineCleared(t, handler, boughtID)
}

func TestClearingTheBoughtLineLetsTheNextListPlanIt(t *testing.T) {
	handler, env := setupTestWithDB(t)
	now := time.Now().UTC()
	boughtID := buyReplacement(t, handler, env, now)

	exchangeJSON(t, handler, http.MethodDelete, "/api/shopping-list/items/"+boughtID, ``, http.StatusOK)
	fillShopping(t, handler)

	lines := shoppingLines(t, handler)
	line := shopLineByItem(lines, "item-cyc")
	if line == nil || line.ID == boughtID || line.Note != "replacing 1 you used" || line.Quantity != 1 {
		t.Fatalf("clearing the bought line left Cycle off the next list: %+v", lines)
	}
	assertBoughtLineCleared(t, handler, boughtID)
}

func TestWipeEmptiesInventoryAndSuggestsNone(t *testing.T) {
	handler, env := setupTestWithDB(t)
	seedWipedMilk(t, handler, env)

	before := fillShopping(t, handler)
	if shopLineByItem(before, "item-milk") == nil {
		t.Fatalf("setup: Milk was not suggested before the wipe: %+v", before)
	}

	exchangeJSON(t, handler, http.MethodPost, "/api/inventory/wipe", `{"confirmation":"WIPE INVENTORY"}`, http.StatusOK)

	t.Run("inventory shows no items", func(t *testing.T) {
		rows := inventoryRows(t, handler)
		if len(rows) != 0 {
			t.Fatalf("inventory after a wipe lists %d items (%+v); a wipe empties the inventory list", len(rows), rows)
		}
	})
	t.Run("fill adds none of the wiped products", func(t *testing.T) {
		lines := fillShopping(t, handler)
		if len(lines) != 0 {
			t.Fatalf("a fill right after a wipe suggested %+v; wiped products stay off the list until they are stocked in again", lines)
		}
	})
	t.Run("group category and household targets stay", func(t *testing.T) {
		snap := mustRestock(t, handler)
		if snap.Household.Kind != restock.KindMonths || snap.Household.Value != 6 {
			t.Fatalf("household target after wipe = %+v, want 6 months", snap.Household)
		}
		if got := categoryTarget(snap, "Snacks"); got == nil || got.Kind != restock.KindAmount || got.Value != 5 {
			t.Fatalf("Snacks target after wipe = %+v", got)
		}
		status, body := callJSON(handler, http.MethodGet, "/api/groups/group-milk", "")
		if status != http.StatusOK {
			t.Fatalf("group after wipe: status %d body %s", status, body)
		}
		var group struct {
			WindowMonths *float64 `json:"windowMonths"`
		}
		if err := json.Unmarshal(body, &group); err != nil {
			t.Fatal(err)
		}
		if group.WindowMonths == nil || *group.WindowMonths != 6 {
			t.Fatalf("group target after wipe = %+v, want 6 months", group.WindowMonths)
		}
	})
}

func TestStockInAfterWipeUsesTheSavedItemTarget(t *testing.T) {
	handler, env := setupTestWithDB(t)
	seedWipedMilk(t, handler, env)
	exchangeJSON(t, handler, http.MethodPost, "/api/inventory/wipe", `{"confirmation":"WIPE INVENTORY"}`, http.StatusOK)

	// Stock the same product in again. Do not set a target a second time.
	insertPendingScan(env, "scan-milk-again", "prod-milk", scan.StockIn)
	exchangeJSON(t, handler, http.MethodPost, "/api/scans/scan-milk-again/commit", ``, http.StatusOK)

	t.Run("restock view", func(t *testing.T) {
		view := restockByProduct(mustRestock(t, handler), "prod-milk")
		if view == nil || view.Target.Kind != restock.KindAmount || view.Target.Value != 4 || view.Level != restock.LevelItem {
			t.Fatalf("stocking Milk in again did not bring back its item target of 4: %+v", view)
		}
	})
	t.Run("planning", func(t *testing.T) {
		view := restockByProduct(mustRestock(t, handler), "prod-milk")
		lines := fillShopping(t, handler)
		var planned *shopLine
		if view != nil {
			planned = shopLineByItem(lines, view.ItemID)
		}
		if planned == nil || planned.Quantity != 3 {
			t.Fatalf("stocked Milk in again with 1 on hand against its saved target of 4, but the list did not plan the 3 still needed: %+v", lines)
		}
	})
}

// buyReplacement uses one unit, fills the replacement, and checks it off.
// The stock-in that created the unit is moved before the purchase, so the
// purchase is still waiting to be stocked in.
func buyReplacement(t *testing.T, handler http.Handler, env testEnv, now time.Time) string {
	t.Helper()
	beginUsing(t, env.DB, now.Add(-30*24*time.Hour))
	createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", "prod-cyc", "Cycle", "item-cyc")
	if _, err := env.DB.Exec(`UPDATE stock_in_events SET at = ? WHERE product_id = 'prod-cyc'`, now.Add(-48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	insertConsumptionEvent(t, env.DB, "ce-cyc-1", "item-cyc", now.Add(-24*time.Hour))

	lines := fillShopping(t, handler)
	line := shopLineByItem(lines, "item-cyc")
	if line == nil || line.ID == "" || line.Note != "replacing 1 you used" || line.Quantity != 1 {
		t.Fatalf("setup fill: %+v", lines)
	}
	exchangeJSON(t, handler, http.MethodPatch, "/api/shopping-list/items/"+line.ID, `{"purchased":true}`, http.StatusOK)
	if _, err := env.DB.Exec(`UPDATE shopping_list_items SET purchased_at = ? WHERE id = ?`, now.Add(-30*time.Minute), line.ID); err != nil {
		t.Fatal(err)
	}
	return line.ID
}

func seedWipedMilk(t *testing.T, handler http.Handler, env testEnv) {
	t.Helper()
	now := time.Now().UTC()
	beginUsing(t, env.DB, now.Add(-30*24*time.Hour))
	createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", "prod-milk", "Milk", "item-milk")
	exchangeJSON(t, handler, http.MethodPut, "/api/restock/household", `{"kind":"months","value":6}`, http.StatusOK)
	exchangeJSON(t, handler, http.MethodPut, "/api/restock/items/item-milk", `{"kind":"amount","value":4}`, http.StatusOK)
	snacks := decodeID(t, exchangeJSON(t, handler, http.MethodPost, "/api/restock/categories", `{"name":"Snacks"}`, http.StatusOK))
	exchangeJSON(t, handler, http.MethodPut, "/api/restock/categories/"+snacks+"/target", `{"kind":"amount","value":5}`, http.StatusOK)
	insertGroup(env, "group-milk", "Dairy")
	insertMember(env, "group-milk", "prod-milk")
	exchangeJSON(t, handler, http.MethodPut, "/api/groups/group-milk/target", `{"windowMonths":6}`, http.StatusOK)
}

// boughtLineRemains reports whether that shopping line is still the bought
// row. The active list hides purchased lines, so the check is the line's own
// response: it answers with this id and a purchased time.
func boughtLineRemains(t *testing.T, handler http.Handler, id string) bool {
	t.Helper()
	status, body := callJSON(handler, http.MethodPatch, "/api/shopping-list/items/"+id, `{"purchased":true}`)
	if status == http.StatusNotFound {
		return false
	}
	if status != http.StatusOK {
		t.Fatalf("bought line %s: status %d body %s", id, status, body)
	}
	var row struct {
		ID          string  `json:"id"`
		PurchasedAt *string `json:"purchasedAt"`
	}
	if err := json.Unmarshal(body, &row); err != nil {
		t.Fatalf("bought line %s: %v body %s", id, err, body)
	}
	return row.ID == id && row.PurchasedAt != nil
}

func assertBoughtLineCleared(t *testing.T, handler http.Handler, id string) {
	t.Helper()
	if shoppingLineStatus(t, handler, id) != http.StatusNotFound {
		t.Fatalf("bought line %s was not cleaned up", id)
	}
}

func shoppingLineStatus(t *testing.T, handler http.Handler, id string) int {
	t.Helper()
	status, body := callJSON(handler, http.MethodPatch, "/api/shopping-list/items/"+id, `{"purchased":true}`)
	if status != http.StatusOK && status != http.StatusNotFound {
		t.Fatalf("shopping line %s: status %d body %s", id, status, body)
	}
	return status
}

type inventoryRow struct {
	Item struct {
		ID      string `json:"id"`
		Product struct {
			Name string `json:"name"`
		} `json:"product"`
	} `json:"item"`
	InstanceCount int `json:"instanceCount"`
}

func inventoryRows(t *testing.T, handler http.Handler) []inventoryRow {
	t.Helper()
	body := exchangeJSON(t, handler, http.MethodGet, "/api/inventory", ``, http.StatusOK)
	var rows []inventoryRow
	if err := json.Unmarshal(body, &rows); err != nil {
		t.Fatalf("inventory: %v body %s", err, body)
	}
	return rows
}

func restockByProduct(snap restock.Snapshot, productID string) *restock.ItemView {
	for i := range snap.Items {
		if snap.Items[i].ProductID == productID {
			return &snap.Items[i]
		}
	}
	return nil
}
