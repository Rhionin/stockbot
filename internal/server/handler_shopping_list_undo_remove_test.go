package server

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"
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
