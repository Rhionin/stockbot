package server

import (
	"net/http"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/group"
)

func TestSeededSuggestionsLeaveShoppingCombined(t *testing.T) {
	runHandlerTests(t, []handlerTestCase{
		{
			name: "a seeded suggestion does not keep the shopping plan combined",
			setup: func(env testEnv) {
				createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "gv", "Great Value Cut Green Beans", "item-gv")
				createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "kr", "Kroger Cut Green Beans", "item-kr")
				createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "dm", "Del Monte Cut Green Beans", "item-dm")
				mustExec(env, `UPDATE products SET unit_of_measure = 'can' WHERE id IN ('gv', 'kr', 'dm')`)
				mustExec(env, `INSERT INTO brand_preferences (user_id, need_key, item_id, ignore_price) VALUES ('user-1', ?, 'item-gv', 1)`, "cut green beans\x00can")
				beginUsing(env.T, env.DB, time.Now())
				setSupplyQuantity(env.T, env.DB, "gv", 4)
				setSupplyQuantity(env.T, env.DB, "kr", 4)
				if err := group.NewGroups(env.DB).Seed(env.T.Context(), primaryScope(env.T, env.DB)); err != nil {
					env.T.Fatal(err)
				}
			},
			httpExchange: httpExchange{
				method:         http.MethodGet,
				path:           "/api/group-suggestions",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$[0].kind", value: "from_old_plan"},
					{path: "$[0].proposedRule", value: "favorite"},
					{path: "$[0].pinnedProductId", value: "gv"},
					{path: "$[1]", absent: true},
				},
				bodyContains: []string{"Great Value Cut Green Beans", "Kroger Cut Green Beans", "Del Monte Cut Green Beans"},
			},
			afterRequest: exchanges(httpExchange{
				method:         http.MethodPost,
				path:           "/api/shopping-list/fill",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$[0].itemId", value: "item-gv"},
					{path: "$[1].itemId", value: "item-kr"},
					{path: "$[2]", absent: true},
				},
			}),
		},
	})
}
