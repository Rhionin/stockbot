package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/shopping"
	"github.com/Rhionin/stockbot/internal/supply"
	"github.com/Rhionin/stockbot/internal/tenant"
)

func TestBatchOffListPropagatesNameLookupErrors(t *testing.T) {
	t.Run("group_names", func(t *testing.T) {
		handler, env := setupTestWithDB(t)
		_ = handler
		scope := primaryScope(t, env.DB)
		if err := env.DB.Close(); err != nil {
			t.Fatal(err)
		}
		groupID := "grp-offlist-err"
		key := shopping.GroupLineKey(groupID)
		planViews := map[string]supply.LinePlanView{
			key: {GroupID: groupID, ItemID: "item-g"},
		}
		_, err := batchOffListGroupNames(context.Background(), env.DB, scope, planViews)
		if err == nil {
			t.Fatal("expected group name lookup error")
		}
	})
	t.Run("product_names", func(t *testing.T) {
		handler, env := setupTestWithDB(t)
		_ = handler
		scope := primaryScope(t, env.DB)
		if err := env.DB.Close(); err != nil {
			t.Fatal(err)
		}
		const itemID = "item-offlist-err"
		key := shopping.ItemLineKey(itemID)
		planViews := map[string]supply.LinePlanView{
			key: {ProductID: "prod-offlist-err", ItemID: itemID},
		}
		_, err := batchOffListProductNames(context.Background(), env.DB, scope, planViews)
		if err == nil {
			t.Fatal("expected product name lookup error")
		}
	})
}

func TestBatchOffListProductNamesHookFault(t *testing.T) {
	handler, env := setupTestWithDB(t)
	_ = handler
	scope := primaryScope(t, env.DB)
	key := shopping.ItemLineKey("item-hook")
	planViews := map[string]supply.LinePlanView{key: {ProductID: "prod-hook", ItemID: "item-hook"}}
	batchOffListProductNamesHook = func(context.Context, *sql.DB, tenant.Scope, map[string]supply.LinePlanView) (map[string]string, error) {
		return map[string]string{"prod-hook": "Hooked"}, nil
	}
	t.Cleanup(func() { batchOffListProductNamesHook = nil })
	names, err := batchOffListProductNames(context.Background(), env.DB, scope, planViews)
	if err != nil || names["prod-hook"] != "Hooked" {
		t.Fatalf("hook names %#v err %v", names, err)
	}
}

func TestBatchOffListProductNamesUsesHouseholdOverride(t *testing.T) {
	handler, env := setupTestWithDB(t)
	_ = handler
	scope := primaryScope(t, env.DB)
	const prodID = "prod-batch-offlist"
	const rawName = "Raw Batch Name"
	const overrideName = "Override Batch Name"
	createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", prodID, rawName, "item-batch-offlist")
	if err := env.ProductStore.SaveDisplayName(context.Background(), scope, prodID, overrideName); err != nil {
		t.Fatal(err)
	}
	beginUsing(t, env.DB, time.Now())
	key := shopping.ItemLineKey("item-batch-offlist")
	planViews := map[string]supply.LinePlanView{
		key: {ProductID: prodID, ItemID: "item-batch-offlist"},
	}
	names, err := batchOffListProductNames(context.Background(), env.DB, scope, planViews)
	if err != nil {
		t.Fatal(err)
	}
	if names[prodID] != overrideName {
		t.Fatalf("batch name got %q want %q", names[prodID], overrideName)
	}
}

func TestW3OffListNameLookupErrorHTTP(t *testing.T) {
	handler, env := setupTestWithDB(t)
	scope := primaryScope(t, env.DB)
	hid := scope.HouseholdID()
	const groupID = "grp-offlist-http-500"
	const itemID = "item-offlist-grp"
	createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", "prod-grp-500", "Lookup Err", itemID)
	beginUsing(t, env.DB, time.Now())
	mustExecTest(t, env.DB, `INSERT INTO product_groups (id, household_id, user_id, name, name_key, rule, rule_confirmed) VALUES (?, ?, ?, 'G500', 'g500', 'same_as_ran_out', 1)`,
		groupID, hid, hid)
	mustExecTest(t, env.DB, `INSERT INTO shopping_list_items (id, household_id, user_id, item_id, quantity, source, group_id, created_at)
		VALUES ('line-grp-500', ?, ?, ?, 1, 'auto', ?, datetime('now'))`, hid, hid, itemID, groupID)
	mustExecTest(t, env.DB, `INSERT INTO staged_cart_skips (household_id, user_id, item_id, group_id, expires_trip) VALUES (?, ?, ?, ?, 2)`,
		hid, hid, itemID, groupID)
	batchOffListGroupNamesHook = func(context.Context, *sql.DB, tenant.Scope, map[string]supply.LinePlanView) (map[string]string, error) {
		return nil, fmt.Errorf("injected group name fault")
	}
	t.Cleanup(func() { batchOffListGroupNamesHook = nil })
	req := httptest.NewRequest(http.MethodGet, "/api/shopping-list?status=1", nil)
	req.Header.Set("X-Household-Id", hid)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status %d want 500 body %s", rr.Code, rr.Body.String())
	}
}

func TestW3OffListProductNameLookupErrorHTTP(t *testing.T) {
	handler, env := setupTestWithDB(t)
	scope := primaryScope(t, env.DB)
	hid := scope.HouseholdID()
	const itemID = "item-offlist-prod-500"
	createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", "prod-offlist-500", "Lookup Err Prod", itemID)
	beginUsing(t, env.DB, time.Now())
	mustExecTest(t, env.DB, `INSERT INTO staged_cart_skips (household_id, user_id, item_id, expires_trip) VALUES (?, ?, ?, 2)`,
		hid, hid, itemID)
	batchOffListProductNamesHook = func(context.Context, *sql.DB, tenant.Scope, map[string]supply.LinePlanView) (map[string]string, error) {
		return nil, fmt.Errorf("injected product name fault")
	}
	t.Cleanup(func() { batchOffListProductNamesHook = nil })
	req := httptest.NewRequest(http.MethodGet, "/api/shopping-list?status=1", nil)
	req.Header.Set("X-Household-Id", hid)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status %d want 500 body %s", rr.Code, rr.Body.String())
	}
	var doc struct {
		OffList []map[string]any `json:"offList"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.OffList) != 0 {
		t.Fatalf("partial offList on lookup error: %#v", doc.OffList)
	}
}

// TestW3OffListProductNameLookupFailsFirstCallOnlyHTTP — N3e: a second lookup after buildOffList would mask first-call faults.
func TestW3OffListProductNameLookupFailsFirstCallOnlyHTTP(t *testing.T) {
	handler, env := setupTestWithDB(t)
	registerProductionShapeKroger(env)
	scope := primaryScope(t, env.DB)
	hid := scope.HouseholdID()
	const itemID = "item-n3e-prod"
	const lineID = "line-n3e-prod"
	lineKey := shopping.ItemLineKey(itemID)
	createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", "prod-n3e", "N3e Product", itemID)
	beginUsing(t, env.DB, time.Now())
	exchangeJSON(t, handler, http.MethodPut, "/api/restock/items/"+itemID, `{"kind":"amount","value":2}`, http.StatusOK)
	exchangeJSON(t, handler, http.MethodPost, "/api/shopping-list/fill", ``, http.StatusOK)
	mustExecTest(t, env.DB, `UPDATE shopping_list_items SET id = ? WHERE household_id = ? AND item_id = ?`, lineID, hid, itemID)
	exchangeJSON(t, handler, http.MethodPut, "/api/shopping-list/items/"+lineID+"/adjustment?provider=kroger", `{"adjustment":0}`, http.StatusOK)
	var lookupCalls int
	batchOffListProductNamesHook = func(context.Context, *sql.DB, tenant.Scope, map[string]supply.LinePlanView) (map[string]string, error) {
		lookupCalls++
		if lookupCalls == 1 {
			return nil, fmt.Errorf("injected first-call-only product fault (N3e)")
		}
		t.Fatalf("unexpected second batchOffListProductNames call (N3e mutant)")
		return nil, nil
	}
	t.Cleanup(func() { batchOffListProductNamesHook = nil })
	req := httptest.NewRequest(http.MethodGet, "/api/shopping-list?status=1&provider=kroger", nil)
	req.Header.Set("X-Household-Id", hid)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status %d want 500; duplicate lookup mutant could return 200 body %s", rr.Code, rr.Body.String())
	}
	if lookupCalls != 1 {
		t.Fatalf("lookup calls %d want 1", lookupCalls)
	}
	var doc struct {
		OffList []map[string]any `json:"offList"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.OffList) != 0 {
		t.Fatalf("partial offList %#v for key %s", doc.OffList, lineKey)
	}
}

// TestW3OffListGroupNameLookupFailsFirstCallOnlyHTTP — N3c: a second lookup after buildOffList would mask first-call faults.
func TestW3OffListGroupNameLookupFailsFirstCallOnlyHTTP(t *testing.T) {
	handler, env := setupTestWithDB(t)
	registerProductionShapeKroger(env)
	scope := primaryScope(t, env.DB)
	hid := scope.HouseholdID()
	const groupID = "grp-n3c"
	const itemID = "item-n3c"
	const lineID = "line-n3c"
	lineKey := shopping.GroupLineKey(groupID)
	const prodB = "prod-n3c-b"
	createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", "prod-n3c", "N3c A", itemID)
	createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", prodB, "N3c B", "item-n3c-b")
	mustExecTest(t, env.DB, `INSERT INTO product_groups (id, household_id, user_id, name, name_key, rule, rule_confirmed) VALUES (?, ?, ?, 'N3c G', 'n3cg', 'favorite', 1)`,
		groupID, hid, hid)
	mustExecTest(t, env.DB, `INSERT INTO product_group_members (household_id, group_id, product_id) VALUES (?, ?, ?), (?, ?, ?)`,
		hid, groupID, "prod-n3c", hid, groupID, prodB)
	beginUsing(t, env.DB, time.Now())
	putGroupItemsGoal(t, handler, groupID, 3)
	exchangeJSON(t, handler, http.MethodPost, "/api/shopping-list/fill", ``, http.StatusOK)
	mustExecTest(t, env.DB, `UPDATE shopping_list_items SET id = ?, group_id = ? WHERE household_id = ? AND item_id = ?`,
		lineID, groupID, hid, itemID)
	exchangeJSON(t, handler, http.MethodPut, "/api/shopping-list/items/"+lineID+"/adjustment?provider=kroger", `{"adjustment":0}`, http.StatusOK)
	var lookupCalls int
	batchOffListGroupNamesHook = func(context.Context, *sql.DB, tenant.Scope, map[string]supply.LinePlanView) (map[string]string, error) {
		lookupCalls++
		if lookupCalls == 1 {
			return nil, fmt.Errorf("injected first-call-only group fault (N3c)")
		}
		t.Fatalf("unexpected second batchOffListGroupNames call (N3c mutant)")
		return nil, nil
	}
	t.Cleanup(func() { batchOffListGroupNamesHook = nil })
	req := httptest.NewRequest(http.MethodGet, "/api/shopping-list?status=1&provider=kroger", nil)
	req.Header.Set("X-Household-Id", hid)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status %d want 500; duplicate lookup mutant could return 200 body %s", rr.Code, rr.Body.String())
	}
	if lookupCalls != 1 {
		t.Fatalf("lookup calls %d want 1", lookupCalls)
	}
	var doc struct {
		OffList []map[string]any `json:"offList"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.OffList) != 0 {
		t.Fatalf("partial offList %#v for key %s", doc.OffList, lineKey)
	}
}