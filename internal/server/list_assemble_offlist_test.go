package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/shopping"
	"github.com/Rhionin/stockbot/internal/supply"
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
			key: {GroupID: groupID, ProductID: "prod-g", ItemID: "item-g"},
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
	const itemID = "item-offlist-500"
	createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", "prod-offlist-500", "Lookup Err", itemID)
	beginUsing(t, env.DB, time.Now())
	exchangeJSON(t, handler, http.MethodPut, "/api/restock/items/"+itemID, `{"kind":"amount","value":2}`, http.StatusOK)
	exchangeJSON(t, handler, http.MethodPost, "/api/shopping-list/fill", ``, http.StatusOK)
	mustExecTest(t, env.DB, `DELETE FROM shopping_list_items WHERE household_id = ? AND item_id = ?`, hid, itemID)
	mustExecTest(t, env.DB, `INSERT INTO staged_cart_skips (household_id, user_id, item_id, expires_trip) VALUES (?, ?, ?, 2)`, hid, hid, itemID)
	if err := env.DB.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/shopping-list?status=1", nil)
	req.Header.Set("X-Household-Id", hid)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status %d want 500 body %s", rr.Code, rr.Body.String())
	}
}