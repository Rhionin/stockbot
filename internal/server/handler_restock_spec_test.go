package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Rhionin/stockbot/internal/restock"
	"github.com/Rhionin/stockbot/internal/scan"
	"github.com/Rhionin/stockbot/internal/shopping"
)

// Spec regressions for the restock-target clarifications on issues #231 and #240.
// A missing target field must be rejected. Wiping inventory keeps targets.
// Shopping reconciliation may add or remove only unpurchased planned lines.

func TestWipeKeepsRestockTargets(t *testing.T) {
	handler, env := setupTestWithDB(t)
	setupProduct("prod-salt", "Salt", "Pantry")(env)
	if _, err := env.DB.Exec(`INSERT INTO items (id, user_id, product_id) VALUES ('item-salt', 'user-1', 'prod-salt')`); err != nil {
		t.Fatal(err)
	}
	if _, err := env.DB.Exec(`INSERT INTO item_instances (id, item_id, stock_in_at) VALUES ('inst-salt', 'item-salt', CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	targets := restock.NewTargets(env.DB)
	if err := targets.SetHousehold(context.Background(), primaryScope(t, env.DB), restock.Target{Kind: restock.KindMonths, Value: 6}); err != nil {
		t.Fatal(err)
	}
	if err := targets.SetItem(context.Background(), primaryScope(t, env.DB), "item-salt", restock.Target{Kind: restock.KindAmount, Value: 4}); err != nil {
		t.Fatal(err)
	}
	produce := produceID(t, handler)
	exchangeJSON(t, handler, http.MethodPut, "/api/restock/categories/"+produce+"/target", `{"kind":"amount","value":2}`, http.StatusOK)
	snacks := decodeID(t, exchangeJSON(t, handler, http.MethodPost, "/api/restock/categories", `{"name":"Snacks"}`, http.StatusOK))
	exchangeJSON(t, handler, http.MethodPut, "/api/restock/categories/"+snacks+"/target", `{"kind":"months","value":1}`, http.StatusOK)
	insertGroup(env, "group-salt", "Seasonings")
	insertMember(env, "group-salt", "prod-salt")
	exchangeJSON(t, handler, http.MethodPut, "/api/groups/group-salt/target", `{"windowMonths":6}`, http.StatusOK)

	before := mustRestock(t, handler)
	salt := restockItem(before, "item-salt")
	if salt == nil || salt.Target.Kind != restock.KindAmount || salt.Target.Value != 4 || salt.Level != restock.LevelItem {
		t.Fatalf("setup: item target = %+v", salt)
	}
	if before.Household.Kind != restock.KindMonths || before.Household.Value != 6 {
		t.Fatalf("setup: household = %+v", before.Household)
	}

	status, body := callJSON(handler, http.MethodPost, "/api/inventory/wipe", `{"confirmation":"WIPE INVENTORY"}`)
	if status != http.StatusOK {
		t.Fatalf("wipe: status %d body %s", status, body)
	}
	after := mustRestock(t, handler)

	t.Run("household default stays", func(t *testing.T) {
		if after.Household.Kind != restock.KindMonths || after.Household.Value != 6 {
			t.Fatalf("household target after wipe = %+v, want 6 months", after.Household)
		}
	})
	t.Run("category targets stay", func(t *testing.T) {
		if got := categoryTarget(after, "Produce"); got == nil || got.Kind != restock.KindAmount || got.Value != 2 {
			t.Fatalf("Produce target after wipe = %+v", got)
		}
		if got := categoryTarget(after, "Snacks"); got == nil || got.Kind != restock.KindMonths || got.Value != 1 {
			t.Fatalf("Snacks target after wipe = %+v", got)
		}
	})
	t.Run("group target stays", func(t *testing.T) {
		status, body := callJSON(handler, http.MethodGet, "/api/groups/group-salt", "")
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
	t.Run("stock is cleared", func(t *testing.T) {
		status, body := callJSON(handler, http.MethodGet, "/api/inventory/item-salt/instances", "")
		if status != http.StatusOK {
			t.Fatalf("instances: status %d body %s", status, body)
		}
		var instances []any
		if err := json.Unmarshal(body, &instances); err != nil {
			t.Fatal(err)
		}
		if len(instances) != 0 {
			t.Fatalf("stock after wipe = %s", body)
		}
	})
	t.Run("item target stays", func(t *testing.T) {
		if restockItem(after, "item-salt") != nil || restockByProduct(after, "prod-salt") != nil {
			t.Fatal("wipe left Salt on restock; a wiped product stays hidden until it is stocked in again")
		}
		if lines := fillShopping(t, handler); len(lines) != 0 {
			t.Fatalf("wipe suggested a buy: %+v", lines)
		}

		insertPendingScan(env, "scan-salt-again", "prod-salt", scan.StockIn)
		exchangeJSON(t, handler, http.MethodPost, "/api/scans/scan-salt-again/commit", ``, http.StatusOK)

		back := mustRestock(t, handler)
		item := restockByProduct(back, "prod-salt")
		if item == nil || item.Target.Kind != restock.KindAmount || item.Target.Value != 4 || item.Level != restock.LevelItem || item.SourceLabel != "Set on this item" {
			t.Fatalf("stocking Salt in again did not bring back its item target of 4 set on this item: %+v", item)
		}
	})
}

func TestExplicitZeroIsDontRestock(t *testing.T) {
	handler, env := setupTestWithDB(t)
	setupProduct("prod-salt", "Salt", "Pantry")(env)
	if _, err := env.DB.Exec(`INSERT INTO items (id, user_id, product_id) VALUES ('item-salt', 'user-1', 'prod-salt')`); err != nil {
		t.Fatal(err)
	}
	insertGroup(env, "group-salt", "Seasonings")
	insertMember(env, "group-salt", "prod-salt")

	t.Run("item value 0", func(t *testing.T) {
		exchangeJSON(t, handler, http.MethodPut, "/api/restock/items/item-salt", `{"kind":"months","value":0}`, http.StatusOK)
		snap := mustRestock(t, handler)
		item := restockItem(snap, "item-salt")
		if item == nil || item.Target.Value != 0 || item.Level != restock.LevelItem {
			t.Fatalf("explicit 0 = %+v", item)
		}
	})
	t.Run("group window 0", func(t *testing.T) {
		body := exchangeJSON(t, handler, http.MethodPut, "/api/groups/group-salt/target", `{"windowMonths":0}`, http.StatusOK)
		var group struct {
			WindowMonths *float64 `json:"windowMonths"`
		}
		if err := json.Unmarshal(body, &group); err != nil {
			t.Fatal(err)
		}
		if group.WindowMonths == nil || *group.WindowMonths != 0 {
			t.Fatalf("explicit group 0 = %+v", group.WindowMonths)
		}
	})
}

func TestMissingOrMalformedTargetChangesNothing(t *testing.T) {
	cases := []struct {
		name string
		path string
		body string
	}{
		{"item missing value", "/api/restock/items/item-salt", `{"kind":"months"}`},
		{"item null value", "/api/restock/items/item-salt", `{"kind":"months","value":null}`},
		{"item missing kind", "/api/restock/items/item-salt", `{"value":4}`},
		{"item null kind", "/api/restock/items/item-salt", `{"kind":null,"value":4}`},
		{"item malformed kind", "/api/restock/items/item-salt", `{"kind":"weeks","value":4}`},
		{"item malformed value", "/api/restock/items/item-salt", `{"kind":"months","value":"nope"}`},
		{"item empty object", "/api/restock/items/item-salt", `{}`},

		{"household missing value", "/api/restock/household", `{"kind":"months"}`},
		{"household null value", "/api/restock/household", `{"kind":"amount","value":null}`},
		{"household missing kind", "/api/restock/household", `{"value":6}`},
		{"household null kind", "/api/restock/household", `{"kind":null,"value":6}`},
		{"household malformed kind", "/api/restock/household", `{"kind":"weeks","value":6}`},
		{"household malformed value", "/api/restock/household", `{"kind":"months","value":"nope"}`},
		{"household empty object", "/api/restock/household", `{}`},

		{"category missing value", "/api/restock/categories/CATEGORY/target", `{"kind":"amount"}`},
		{"category null value", "/api/restock/categories/CATEGORY/target", `{"kind":"amount","value":null}`},
		{"category missing kind", "/api/restock/categories/CATEGORY/target", `{"value":5}`},
		{"category null kind", "/api/restock/categories/CATEGORY/target", `{"kind":null,"value":5}`},
		{"category malformed kind", "/api/restock/categories/CATEGORY/target", `{"kind":"weeks","value":5}`},
		{"category malformed value", "/api/restock/categories/CATEGORY/target", `{"kind":"amount","value":"nope"}`},
		{"category empty object", "/api/restock/categories/CATEGORY/target", `{}`},

		{"group missing kind and value", "/api/groups/group-salt/target", `{"kind":"months"}`},
		{"group value without a window", "/api/groups/group-salt/target", `{"kind":"months","value":0}`},
		{"group null window", "/api/groups/group-salt/target", `{"windowMonths":null}`},
		{"group malformed window", "/api/groups/group-salt/target", `{"windowMonths":"nope"}`},
		{"group malformed quantity", "/api/groups/group-salt/target", `{"quantity":"lots","dimension":"mass"}`},
		{"group empty object", "/api/groups/group-salt/target", `{}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			handler, env := setupTestWithDB(t)
			categoryID := seedKnownTargets(t, handler, env)
			path := strings.ReplaceAll(tc.path, "CATEGORY", categoryID)
			status, body := callJSON(handler, http.MethodPut, path, tc.body)
			if status != http.StatusBadRequest {
				t.Fatalf("%s returned %d (%s); a missing or malformed target must be 400 and must not change the stored target", tc.name, status, clip(body))
			}
			assertKnownTargets(t, handler, categoryID)
		})
	}
}

func TestPurchasedCheckedOffAndManualRowsSurviveTargetChanges(t *testing.T) {
	handler, env := setupTestWithDB(t)
	createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", "prod-bought", "Bought Beans", "item-bought")
	createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", "prod-manual", "Manual Milk", "item-manual")
	beginUsing(t, env.DB, env.Clock.Now())
	setSupplyQuantity(t, env.DB, "prod-bought", 4)

	created := exchangeJSON(t, handler, http.MethodPost, "/api/shopping-list/items", `{"itemId":"item-manual","quantity":5}`, http.StatusCreated)
	var manual struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(created, &manual); err != nil || manual.ID == "" {
		t.Fatalf("setup: manual row %s: %v", created, err)
	}

	lines := fillShopping(t, handler)
	bought := shopLineByItem(lines, "item-bought")
	if bought == nil || bought.Source != "auto" || bought.Quantity != 3 || bought.ID == "" {
		t.Fatalf("setup: planned lines = %+v", lines)
	}
	exchangeJSON(t, handler, http.MethodPatch, "/api/shopping-list/items/"+bought.ID, `{"purchased":true}`, http.StatusOK)

	// Don't restock, then stage the plan. The checked-off row must still be there.
	exchangeJSON(t, handler, http.MethodPut, "/api/restock/items/item-bought", `{"kind":"months","value":0}`, http.StatusOK)
	fillShopping(t, handler)
	assertPurchasedRow(t, env, bought.ID, 3)
	assertManualRow(t, handler, manual.ID, 5)

	// Switching the target back must not revive the bought line or drop the manual one.
	exchangeJSON(t, handler, http.MethodPut, "/api/restock/items/item-bought", `{"kind":"amount","value":4}`, http.StatusOK)
	lines = fillShopping(t, handler)
	if again := shopLineByItem(lines, "item-bought"); again != nil {
		t.Errorf("switching the target back re-added an already-bought line: %+v", *again)
	}
	assertPurchasedRow(t, env, bought.ID, 3)
	assertManualRow(t, handler, manual.ID, 5)
}

func TestTargetChangeLeavesThePurchasedRow(t *testing.T) {
	handler, env := setupTestWithDB(t)
	createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", "prod-bought", "Bought Beans", "item-bought")
	beginUsing(t, env.DB, env.Clock.Now())
	setSupplyQuantity(t, env.DB, "prod-bought", 4)

	lines := fillShopping(t, handler)
	bought := shopLineByItem(lines, "item-bought")
	if bought == nil || bought.Quantity != 3 {
		t.Fatalf("setup: planned lines = %+v", lines)
	}
	exchangeJSON(t, handler, http.MethodPatch, "/api/shopping-list/items/"+bought.ID, `{"purchased":true}`, http.StatusOK)

	exchangeJSON(t, handler, http.MethodPut, "/api/restock/items/item-bought", `{"kind":"amount","value":8}`, http.StatusOK)
	lines = fillShopping(t, handler)
	assertPurchasedRow(t, env, bought.ID, 3)
	planned := shopLineByItem(lines, "item-bought")
	if planned == nil || planned.ID == bought.ID || planned.Source != "auto" || planned.Quantity != 7 {
		t.Errorf("changing the target should add a new unpurchased planned line of 7 and leave the purchased row, got %+v", lines)
	}
}

func TestUndoDontRestockRestoresTheUnpurchasedPlannedLine(t *testing.T) {
	handler, env := setupTestWithDB(t)
	createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", "prod-oats", "Open Oats", "item-oats")
	beginUsing(t, env.DB, env.Clock.Now())
	setSupplyQuantity(t, env.DB, "prod-oats", 4)

	lines := fillShopping(t, handler)
	planned := shopLineByItem(lines, "item-oats")
	if planned == nil || planned.Quantity != 3 || planned.Source != "auto" {
		t.Fatalf("setup: planned lines = %+v", lines)
	}

	dont := exchangeJSON(t, handler, http.MethodPost, "/api/restock/items/item-oats/dont-restock", ``, http.StatusOK)
	var response struct {
		Previous *restock.Target `json:"previous"`
	}
	if err := json.Unmarshal(dont, &response); err != nil || response.Previous == nil {
		t.Fatalf("setup: don't-restock response %s: %v", dont, err)
	}
	lines = fillShopping(t, handler)
	if shopLineByItem(lines, "item-oats") != nil {
		t.Fatalf("don't-restock left the unpurchased planned line: %+v", lines)
	}

	undo, err := json.Marshal(map[string]any{
		"kind":  response.Previous.Kind,
		"value": response.Previous.Value,
	})
	if err != nil {
		t.Fatal(err)
	}
	exchangeJSON(t, handler, http.MethodPut, "/api/restock/items/item-oats", string(undo), http.StatusOK)
	lines = fillShopping(t, handler)
	restored := shopLineByItem(lines, "item-oats")
	if restored == nil || restored.Source != "auto" || restored.Quantity != 3 {
		t.Fatalf("undo of don't-restock did not restore the unpurchased planned line: %+v", lines)
	}
}

func TestUndoDontRestockDoesNotReaddABoughtLine(t *testing.T) {
	handler, env := setupTestWithDB(t)
	createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", "prod-bought", "Bought Beans", "item-bought")
	beginUsing(t, env.DB, env.Clock.Now())
	setSupplyQuantity(t, env.DB, "prod-bought", 4)

	lines := fillShopping(t, handler)
	bought := shopLineByItem(lines, "item-bought")
	if bought == nil || bought.Quantity != 3 {
		t.Fatalf("setup: planned lines = %+v", lines)
	}
	exchangeJSON(t, handler, http.MethodPatch, "/api/shopping-list/items/"+bought.ID, `{"purchased":true}`, http.StatusOK)

	dont := exchangeJSON(t, handler, http.MethodPost, "/api/restock/items/item-bought/dont-restock", ``, http.StatusOK)
	var response struct {
		Previous *restock.Target `json:"previous"`
	}
	if err := json.Unmarshal(dont, &response); err != nil || response.Previous == nil {
		t.Fatalf("setup: don't-restock response %s: %v", dont, err)
	}
	fillShopping(t, handler)

	undo, err := json.Marshal(map[string]any{
		"kind":  response.Previous.Kind,
		"value": response.Previous.Value,
	})
	if err != nil {
		t.Fatal(err)
	}
	exchangeJSON(t, handler, http.MethodPut, "/api/restock/items/item-bought", string(undo), http.StatusOK)
	lines = fillShopping(t, handler)
	if again := shopLineByItem(lines, "item-bought"); again != nil {
		t.Errorf("undo of don't-restock re-added an already-bought line: %+v", *again)
	}
	assertPurchasedRow(t, env, bought.ID, 3)
}

func seedKnownTargets(t *testing.T, handler http.Handler, env testEnv) string {
	t.Helper()
	setupProduct("prod-salt", "Salt", "Pantry")(env)
	if _, err := env.DB.Exec(`INSERT INTO items (id, user_id, product_id) VALUES ('item-salt', 'user-1', 'prod-salt')`); err != nil {
		t.Fatal(err)
	}
	exchangeJSON(t, handler, http.MethodPut, "/api/restock/household", `{"kind":"months","value":6}`, http.StatusOK)
	exchangeJSON(t, handler, http.MethodPut, "/api/restock/items/item-salt", `{"kind":"amount","value":4}`, http.StatusOK)
	categoryID := decodeID(t, exchangeJSON(t, handler, http.MethodPost, "/api/restock/categories", `{"name":"Snacks"}`, http.StatusOK))
	exchangeJSON(t, handler, http.MethodPut, "/api/restock/categories/"+categoryID+"/target", `{"kind":"amount","value":5}`, http.StatusOK)
	if produceID(t, handler) == "" {
		t.Fatal("setup: Produce missing")
	}
	insertGroup(env, "group-salt", "Seasonings")
	insertMember(env, "group-salt", "prod-salt")
	exchangeJSON(t, handler, http.MethodPut, "/api/groups/group-salt/target", `{"windowMonths":6}`, http.StatusOK)
	assertKnownTargets(t, handler, categoryID)
	return categoryID
}

func assertKnownTargets(t *testing.T, handler http.Handler, categoryID string) {
	t.Helper()
	snap := mustRestock(t, handler)
	if snap.Household.Kind != restock.KindMonths || snap.Household.Value != 6 {
		t.Fatalf("household target changed to %+v", snap.Household)
	}
	item := restockItem(snap, "item-salt")
	if item == nil || item.Target.Kind != restock.KindAmount || item.Target.Value != 4 || item.Level != restock.LevelItem {
		t.Fatalf("item target changed to %+v", item)
	}
	if got := categoryTarget(snap, "Snacks"); got == nil || got.Kind != restock.KindAmount || got.Value != 5 {
		t.Fatalf("category %s target changed to %+v", categoryID, got)
	}
	status, body := callJSON(handler, http.MethodGet, "/api/groups/group-salt", "")
	if status != http.StatusOK {
		t.Fatalf("group: status %d body %s", status, body)
	}
	var group struct {
		WindowMonths *float64 `json:"windowMonths"`
	}
	if err := json.Unmarshal(body, &group); err != nil {
		t.Fatal(err)
	}
	if group.WindowMonths == nil || *group.WindowMonths != 6 {
		t.Fatalf("group target changed to %+v", group.WindowMonths)
	}
}

func assertPurchasedRow(t *testing.T, env testEnv, id string, quantity int) {
	t.Helper()
	row, err := shopping.NewStore(env.DB).GetItemByID(context.Background(), primaryScope(t, env.DB), id)
	if err != nil {
		t.Fatal(err)
	}
	if row == nil || row.PurchasedAt == nil || row.Quantity != quantity || row.Source != "auto" {
		t.Errorf("purchased (checked-off) row %s did not survive the target change: %+v", id, row)
	}
}

func assertManualRow(t *testing.T, handler http.Handler, id string, quantity int) {
	t.Helper()
	for _, row := range shoppingLines(t, handler) {
		if row.ID == id && row.ItemID == "item-manual" && row.Source == "manual" && row.Quantity == quantity {
			return
		}
	}
	t.Errorf("manual row %s quantity %d did not survive, list = %+v", id, quantity, shoppingLines(t, handler))
}

type shopLine struct {
	ID       string `json:"id"`
	ItemID   string `json:"itemId"`
	Quantity int    `json:"quantity"`
	Source   string `json:"source"`
	Note     string `json:"note"`
}

func fillShopping(t *testing.T, handler http.Handler) []shopLine {
	t.Helper()
	body := exchangeJSON(t, handler, http.MethodPost, "/api/shopping-list/fill", ``, http.StatusOK)
	var rows []shopLine
	if err := json.Unmarshal(body, &rows); err != nil {
		t.Fatalf("fill: %v body %s", err, body)
	}
	return rows
}

func shoppingLines(t *testing.T, handler http.Handler) []shopLine {
	t.Helper()
	body := exchangeJSON(t, handler, http.MethodGet, "/api/shopping-list", ``, http.StatusOK)
	var rows []shopLine
	if err := json.Unmarshal(body, &rows); err != nil {
		t.Fatal(err)
	}
	return rows
}

func shopLineByItem(rows []shopLine, itemID string) *shopLine {
	for i := range rows {
		if rows[i].ItemID == itemID {
			return &rows[i]
		}
	}
	return nil
}

func mustRestock(t *testing.T, handler http.Handler) restock.Snapshot {
	t.Helper()
	body := exchangeJSON(t, handler, http.MethodGet, "/api/restock", ``, http.StatusOK)
	var snap restock.Snapshot
	if err := json.Unmarshal(body, &snap); err != nil {
		t.Fatalf("restock: %v body %s", err, body)
	}
	return snap
}

func restockItem(snap restock.Snapshot, itemID string) *restock.ItemView {
	for i := range snap.Items {
		if snap.Items[i].ItemID == itemID {
			return &snap.Items[i]
		}
	}
	return nil
}

func categoryTarget(snap restock.Snapshot, name string) *restock.Target {
	for _, category := range snap.Categories {
		if category.Name == name {
			return category.Target
		}
	}
	return nil
}

func callJSON(handler http.Handler, method, path, payload string) (int, []byte) {
	var reader io.Reader
	if payload != "" {
		reader = strings.NewReader(payload)
	}
	req := httptest.NewRequest(method, path, reader)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	res := rec.Result()
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	return res.StatusCode, body
}

func clip(body []byte) string {
	text := string(body)
	if len(text) > 400 {
		return text[:400] + "..."
	}
	return text
}
