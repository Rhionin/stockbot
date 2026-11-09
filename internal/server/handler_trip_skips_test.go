package server

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

func TestTripScopedSkipClearsAfterLedgerReset(t *testing.T) {
	now := time.Now().UTC()
	setup := func(env testEnv) {
		registerOpenAndKroger(env)
		createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-trip-skip", "Trip Skip", "item-trip-skip")
		beginUsing(env.T, env.DB, now)
		setSupplyQuantity(env.T, env.DB, "prod-trip-skip", 4)
	}

	runHandlerTests(t, []handlerTestCase{
		{
			name:  "remove skip clears after start new cart",
			setup: setup,
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/shopping-list/fill",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$[0].itemId", value: "item-trip-skip"},
				},
			},
			afterRequest: func(env testEnv) {
				handler := stagedCartHandler(env)
				lineID := stagedEntryID(env.T, handler)
				exchangeJSON(env.T, handler, http.MethodDelete, "/api/shopping-list/items/"+lineID, ``, http.StatusOK)
				body := exchangeJSON(env.T, handler, http.MethodPost, "/api/shopping-list/fill", ``, http.StatusOK)
				var rows []any
				if err := json.Unmarshal(body, &rows); err != nil {
					env.T.Fatal(err)
				}
				if len(rows) != 0 {
					env.T.Fatalf("fill after remove: %#v", rows)
				}
				exchangeJSON(env.T, handler, http.MethodPost, "/api/providers/"+openProvider+"/ledger/reset", ``, http.StatusCreated)
				body = exchangeJSON(env.T, handler, http.MethodPost, "/api/shopping-list/fill", ``, http.StatusOK)
				var back []map[string]any
				if err := json.Unmarshal(body, &back); err != nil {
					env.T.Fatal(err)
				}
				if len(back) != 1 || back[0]["itemId"] != "item-trip-skip" {
					env.T.Fatalf("fill after reset: %#v", back)
				}
			},
		},
	})
}
