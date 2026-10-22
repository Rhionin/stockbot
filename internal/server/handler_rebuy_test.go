package server

import (
	"net/http"
	"testing"
	"time"
)

func TestRebuyAfterStockInAndUseAgain(t *testing.T) {
	now := time.Now().UTC()
	fill := httpExchange{method: "POST", path: "/api/shopping-list/fill", expectedStatus: http.StatusOK}
	runHandlerTests(t, []handlerTestCase{{
		name: "bought, restocked, used again comes back",
		setup: func(env testEnv) {
			beginUsing(env.T, env.DB, now.Add(-29*24*time.Hour))
			createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-cyc", "Cycle", "item-cyc")
			insertConsumptionEvent(env.T, env.DB, "ce-cyc-1", "item-cyc", now.Add(-2*24*time.Hour))
		},
		httpExchange: httpExchange{method: "POST", path: "/api/shopping-list/fill", expectedStatus: http.StatusOK,
			assertions: []assertion{{path: "$[0].itemId", value: "item-cyc"}, {path: "$[0].note", value: "replacing 1 you used"}}},
		afterRequest: func(env testEnv) {
			handler := handlerFromEnv(env)
			before := shopLineByItem(shoppingLines(env.T, handler), "item-cyc")
			if before == nil || before.ID == "" {
				env.T.Fatal("setup: Cycle was not on the list")
			}
			boughtID := before.ID
			if _, err := env.DB.Exec(`UPDATE shopping_list_items SET purchased_at = ? WHERE id = ?`, now.Add(-90*time.Minute), boughtID); err != nil {
				env.T.Fatal(err)
			}
			// now is earlier than the line's added time. The stock-in has to
			// land on or after that added time to finish the purchase.
			stockedAt := time.Now().UTC().Add(time.Second)
			insertStockIn(env.T, env.DB, "prod-cyc", stockedAt)
			exchanges(fill)(env)
			exchanges(httpExchange{method: "GET", path: "/api/shopping-list", expectedStatus: http.StatusOK,
				assertions: []assertion{{path: "$[0]", absent: true}}})(env)
			insertConsumptionEvent(env.T, env.DB, "ce-cyc-2", "item-cyc", stockedAt.Add(time.Second))
			exchanges(fill)(env)
			lines := shoppingLines(env.T, handler)
			line := shopLineByItem(lines, "item-cyc")
			if line == nil || line.ID == boughtID || line.Note != "replacing 1 you used" || line.Quantity != 1 {
				env.T.Fatalf("used again after restock, but no open shopping line: %+v", lines)
			}
			assertBoughtLineCleared(env.T, handler, boughtID)
		},
	}})
}
