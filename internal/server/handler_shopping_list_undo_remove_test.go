package server

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/group"
	"github.com/Rhionin/stockbot/internal/product"
)

func TestShoppingListUndoRemove(t *testing.T) {
	now := time.Now().UTC()
	runHandlerTests(t, []handlerTestCase{
		{
			name: "undo remove restores auto line",
			setup: func(env testEnv) {
				registerOpenAndKroger(env)
				createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-undo", "Undo Line", "item-undo")
				beginUsing(env.T, env.DB, now)
				setSupplyQuantity(env.T, env.DB, "prod-undo", 4)
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/shopping-list/fill",
				expectedStatus: http.StatusOK,
			},
			afterRequest: func(env testEnv) {
				handler := stagedCartHandler(env)
				lineID := stagedEntryID(env.T, handler)
				exchangeJSON(env.T, handler, http.MethodDelete, "/api/shopping-list/items/"+lineID, ``, http.StatusOK)
				body := exchangeJSON(env.T, handler, http.MethodPost, "/api/shopping-list/items/item-undo/undo-remove", ``, http.StatusOK)
				var row ShoppingListEntryResponse
				if err := json.Unmarshal(body, &row); err != nil {
					env.T.Fatal(err)
				}
				if row.ItemID != "item-undo" || row.Source != "auto" || row.Quantity < 1 {
					env.T.Fatalf("undo response: %#v", row)
				}
			},
		},
		{
			name: "double undo is not found",
			setup: func(env testEnv) {
				registerOpenAndKroger(env)
				createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-double", "Double", "item-double")
				beginUsing(env.T, env.DB, now)
				setSupplyQuantity(env.T, env.DB, "prod-double", 4)
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/shopping-list/fill",
				expectedStatus: http.StatusOK,
			},
			afterRequest: func(env testEnv) {
				handler := stagedCartHandler(env)
				lineID := stagedEntryID(env.T, handler)
				exchangeJSON(env.T, handler, http.MethodDelete, "/api/shopping-list/items/"+lineID, ``, http.StatusOK)
				exchangeJSON(env.T, handler, http.MethodPost, "/api/shopping-list/items/item-double/undo-remove", ``, http.StatusOK)
				exchangeJSON(env.T, handler, http.MethodPost, "/api/shopping-list/items/item-double/undo-remove", ``, http.StatusNotFound)
			},
		},
		{
			name: "undo group line after planner picks a different member",
			setup: func(env testEnv) {
				createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-gv", "Great Value Cut Green Beans", "item-gv")
				createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-kr", "Kroger Cut Green Beans", "item-kr")
				insertConsumptionEvent(env.T, env.DB, "ate-kr", "item-kr", time.Now().UTC())
				const oz = 28.349523125
				mustExec(env, `UPDATE products SET unit_of_measure = 'can', net_base_value = ?, net_dimension = 'mass', net_size_origin = 'manual' WHERE id IN ('prod-gv', 'prod-kr')`, 14.5*oz)
				ounces := 48.0
				view, err := group.NewGroups(env.DB).Create(env.T.Context(), primaryScope(env.T, env.DB), "Beans", []string{"prod-gv", "prod-kr"}, &group.TargetInput{
					Quantity:  &ounces,
					Dimension: product.DimensionMass,
				})
				if err != nil {
					env.T.Fatal(err)
				}
				env.T.Cleanup(func() {
					_ = view
				})
				beginUsing(env.T, env.DB, now)
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/shopping-list/fill",
				expectedStatus: http.StatusOK,
			},
			afterRequest: func(env testEnv) {
				handler := stagedCartHandler(env)
				lineID := stagedEntryID(env.T, handler)
				exchangeJSON(env.T, handler, http.MethodDelete, "/api/shopping-list/items/"+lineID, ``, http.StatusOK)
				var id string
				if err := env.DB.QueryRow(`SELECT id FROM product_groups`).Scan(&id); err != nil {
					env.T.Fatal(err)
				}
				pin := "prod-gv"
				if _, err := group.NewGroups(env.DB).SetRule(env.T.Context(), primaryScope(env.T, env.DB), id, "favorite", &pin, true); err != nil {
					env.T.Fatal(err)
				}
				body := exchangeJSON(env.T, handler, http.MethodPost, "/api/shopping-list/items/item-kr/undo-remove", ``, http.StatusOK)
				var row ShoppingListEntryResponse
				if err := json.Unmarshal(body, &row); err != nil {
					env.T.Fatal(err)
				}
				if row.ItemID != "item-gv" || row.Group == nil || row.Group.Rule != "favorite" {
					env.T.Fatalf("undo group response: %#v", row)
				}
				list := exchangeJSON(env.T, handler, http.MethodGet, "/api/shopping-list", ``, http.StatusOK)
				var rows []ShoppingListEntryResponse
				if err := json.Unmarshal(list, &rows); err != nil {
					env.T.Fatal(err)
				}
				if len(rows) != 1 || rows[0].ItemID != "item-gv" {
					env.T.Fatalf("list after undo: %#v", rows)
				}
			},
		},
		{
			name: "undo unknown pantry item is not found",
			setup: func(env testEnv) {
				registerOpenAndKroger(env)
				beginUsing(env.T, env.DB, now)
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/shopping-list/items/item-missing/undo-remove",
				expectedStatus: http.StatusNotFound,
			},
		},
		{
			name: "undo when plan no longer buys keeps skip",
			setup: func(env testEnv) {
				registerOpenAndKroger(env)
				createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-nobuy", "No Buy", "item-nobuy")
				beginUsing(env.T, env.DB, now)
				setSupplyQuantity(env.T, env.DB, "prod-nobuy", 4)
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/shopping-list/fill",
				expectedStatus: http.StatusOK,
			},
			afterRequest: func(env testEnv) {
				handler := stagedCartHandler(env)
				lineID := stagedEntryID(env.T, handler)
				exchangeJSON(env.T, handler, http.MethodDelete, "/api/shopping-list/items/"+lineID, ``, http.StatusOK)
				mustExec(env, `UPDATE items SET shelved = 1 WHERE id = 'item-nobuy'`)
				exchangeJSON(env.T, handler, http.MethodPost, "/api/shopping-list/items/item-nobuy/undo-remove", ``, http.StatusNotFound)
				var n int
				if err := env.DB.QueryRow(`SELECT COUNT(*) FROM staged_cart_skips WHERE item_id = 'item-nobuy'`).Scan(&n); err != nil {
					env.T.Fatal(err)
				}
				if n != 1 {
					env.T.Fatalf("skip should remain: %d", n)
				}
			},
		},
		{
			name: "undo missing skip is not found",
			setup: func(env testEnv) {
				registerOpenAndKroger(env)
				createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-noskip", "No Skip", "item-noskip")
				beginUsing(env.T, env.DB, now)
				setSupplyQuantity(env.T, env.DB, "prod-noskip", 4)
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/shopping-list/items/item-noskip/undo-remove",
				expectedStatus: http.StatusNotFound,
			},
		},
	})
}
