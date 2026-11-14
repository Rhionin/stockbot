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

func TestBuildOffListPropagatesNameLookupErrors(t *testing.T) {
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
		skipped := map[string]struct{}{key: {}}
		_, _, err := buildOffListAndGroups(context.Background(), env.DB, scope, planViews, nil, shoppingProvision{}, nil, nil, skipped, false, nil, nil, "")
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
		skipped := map[string]struct{}{key: {}}
		_, _, err := buildOffListAndGroups(context.Background(), env.DB, scope, planViews, nil, shoppingProvision{}, nil, nil, skipped, false, nil, nil, "")
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
	BatchOffListProductNamesHook = func(context.Context, *sql.DB, tenant.Scope, map[string]supply.LinePlanView) (map[string]string, error) {
		return map[string]string{"prod-hook": "Hooked"}, nil
	}
	t.Cleanup(func() { BatchOffListProductNamesHook = nil })
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
	BatchOffListGroupNamesHook = func(context.Context, *sql.DB, tenant.Scope, map[string]supply.LinePlanView) (map[string]string, error) {
		return nil, fmt.Errorf("injected group name fault")
	}
	t.Cleanup(func() { BatchOffListGroupNamesHook = nil })
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
	BatchOffListProductNamesHook = func(context.Context, *sql.DB, tenant.Scope, map[string]supply.LinePlanView) (map[string]string, error) {
		return nil, fmt.Errorf("injected product name fault")
	}
	t.Cleanup(func() { BatchOffListProductNamesHook = nil })
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