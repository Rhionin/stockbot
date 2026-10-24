package server

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/group"
	"github.com/Rhionin/stockbot/internal/product"
)

func TestShoppingListUsesGroups(t *testing.T) {
	const oz = 28.349523125
	beans := func(env testEnv) {
		createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-gv", "Great Value Cut Green Beans", "item-gv")
		createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-kr", "Kroger Cut Green Beans", "item-kr")
		createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-dm", "Del Monte Cut Green Beans", "item-dm")
		mustExec(env, `UPDATE products SET unit_of_measure = 'can', net_base_value = ?, net_dimension = 'mass', net_size_origin = 'manual' WHERE id IN ('prod-gv', 'prod-kr', 'prod-dm')`, 14.5*oz)
		insertConsumptionEvent(env.T, env.DB, "ate-kr", "item-kr", time.Now().UTC())
		ounces := 48.0
		if _, err := group.NewGroups(env.DB).Create(env.T.Context(), primaryScope(env.T, env.DB), "Cut green beans", []string{"prod-gv", "prod-kr", "prod-dm"}, &group.TargetInput{
			Quantity:  &ounces,
			Dimension: product.DimensionMass,
		}); err != nil {
			env.T.Fatal(err)
		}
		beginUsing(env.T, env.DB, time.Now())
	}

	tests := []handlerTestCase{
		{
			name:  "a group buys the product that last ran out",
			setup: beans,
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/shopping-list/fill",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$[0].itemId", value: "item-kr"},
					{path: "$[0].quantity", value: float64(1)},
					{path: "$[0].source", value: "auto"},
					{path: "$[0].group.name", value: "Cut green beans"},
					{path: "$[0].group.rule", value: "same_as_ran_out"},
					{path: "$[1]", absent: true},
				},
				bodyContains: []string{"The last one used up was Kroger Cut Green Beans."},
			},
		},
		{
			name: "a favorite pin overrides what last ran out",
			setup: func(env testEnv) {
				beans(env)
				var id string
				if err := env.DB.QueryRow(`SELECT id FROM product_groups`).Scan(&id); err != nil {
					env.T.Fatal(err)
				}
				pin := "prod-gv"
				if _, err := group.NewGroups(env.DB).SetRule(env.T.Context(), primaryScope(env.T, env.DB), id, "favorite", &pin, true); err != nil {
					env.T.Fatal(err)
				}
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/shopping-list/fill",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$[0].itemId", value: "item-gv"},
					{path: "$[0].group.rule", value: "favorite"},
					{path: "$[0].group.ruleConfirmed", value: true},
					{path: "$[1]", absent: true},
				},
				bodyContains: []string{"This is the one with the star."},
			},
		},
		{
			name: "best deal buys the cheaper ounce",
			setup: func(env testEnv) {
				createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-small", "Small Can", "item-small")
				createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-big", "Big Can", "item-big")
				mustExec(env, `UPDATE products SET net_base_value = ?, net_dimension = 'mass', net_size_origin = 'manual', unit_of_measure = 'can' WHERE id = 'prod-small'`, 14.5*oz)
				mustExec(env, `UPDATE products SET net_base_value = ?, net_dimension = 'mass', net_size_origin = 'manual', unit_of_measure = 'can' WHERE id = 'prod-big'`, 29*oz)
				mustExec(env, `INSERT INTO item_deals (user_id, item_id, price_cents, regular_price_cents, label) VALUES ('user-1', 'item-small', 200, 250, 'Sale')`)
				mustExec(env, `INSERT INTO item_deals (user_id, item_id, price_cents, regular_price_cents, label) VALUES ('user-1', 'item-big', 150, 300, 'Sale')`)
				ounces := 50.0
				view, err := group.NewGroups(env.DB).Create(env.T.Context(), primaryScope(env.T, env.DB), "Cut green beans", []string{"prod-small", "prod-big"}, &group.TargetInput{
					Quantity: &ounces, Dimension: product.DimensionMass,
				})
				if err != nil {
					env.T.Fatal(err)
				}
				if _, err := group.NewGroups(env.DB).SetRule(env.T.Context(), primaryScope(env.T, env.DB), view.ID, "best_deal", nil, true); err != nil {
					env.T.Fatal(err)
				}
				beginUsing(env.T, env.DB, time.Now())
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/shopping-list/fill",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$[0].itemId", value: "item-big"},
					{path: "$[1]", absent: true},
				},
				bodyContains: []string{"On sale for $1.50"},
			},
		},
		{
			name: "favor variety buys the product stocked least recently and skips don't restock",
			setup: func(env testEnv) {
				createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-early", "Bran muffin", "item-early")
				createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-late", "Blueberry muffin", "item-late")
				createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-fancy", "Lehi roller", "item-fancy")
				mustExec(env, `UPDATE products SET unit_of_measure = 'box', net_base_value = ?, net_dimension = 'mass', net_size_origin = 'manual' WHERE id IN ('prod-early', 'prod-late', 'prod-fancy')`, 14.5*oz)
				early := time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC)
				late := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
				mustExec(env, `DELETE FROM stock_in_events WHERE product_id IN ('prod-early', 'prod-late', 'prod-fancy')`)
				insertStockIn(env.T, env.DB, "prod-early", early)
				insertStockIn(env.T, env.DB, "prod-late", late)
				ounces := 48.0
				view, err := group.NewGroups(env.DB).Create(env.T.Context(), primaryScope(env.T, env.DB), "Muffin mix", []string{"prod-early", "prod-late", "prod-fancy"}, &group.TargetInput{
					Quantity: &ounces, Dimension: product.DimensionMass,
				})
				if err != nil {
					env.T.Fatal(err)
				}
				if _, err := group.NewGroups(env.DB).SetRule(env.T.Context(), primaryScope(env.T, env.DB), view.ID, "favor_variety", nil, true); err != nil {
					env.T.Fatal(err)
				}
				if _, err := group.NewGroups(env.DB).SetMemberRestock(env.T.Context(), primaryScope(env.T, env.DB), view.ID, "prod-fancy", true); err != nil {
					env.T.Fatal(err)
				}
				beginUsing(env.T, env.DB, time.Now())
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/shopping-list/fill",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$[0].itemId", value: "item-early"},
					{path: "$[0].group.rule", value: "favor_variety"},
					{path: "$[1]", absent: true},
				},
				bodyContains: []string{"Next up: Bran muffin · rotates through 2"},
			},
		},
		{
			name: "a group of only don't-restock products adds nothing",
			setup: func(env testEnv) {
				createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-only", "Only fancy", "item-only")
				mustExec(env, `UPDATE products SET unit_of_measure = 'box', net_base_value = ?, net_dimension = 'mass', net_size_origin = 'manual' WHERE id = 'prod-only'`, 14.5*oz)
				ounces := 48.0
				view, err := group.NewGroups(env.DB).Create(env.T.Context(), primaryScope(env.T, env.DB), "Fancy only", []string{"prod-only"}, &group.TargetInput{
					Quantity: &ounces, Dimension: product.DimensionMass,
				})
				if err != nil {
					env.T.Fatal(err)
				}
				if _, err := group.NewGroups(env.DB).SetRule(env.T.Context(), primaryScope(env.T, env.DB), view.ID, "favor_variety", nil, true); err != nil {
					env.T.Fatal(err)
				}
				if _, err := group.NewGroups(env.DB).SetMemberRestock(env.T.Context(), primaryScope(env.T, env.DB), view.ID, "prod-only", true); err != nil {
					env.T.Fatal(err)
				}
				beginUsing(env.T, env.DB, time.Now())
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/shopping-list/fill",
				expectedStatus: http.StatusOK,
				assertions:     []assertion{{path: "$[0]", absent: true}},
			},
		},
		{
			name: "a missing size counts items",
			setup: func(env testEnv) {
				createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-a", "Can A", "item-a")
				createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-b", "Can B", "item-b")
				if _, err := group.NewGroups(env.DB).Create(env.T.Context(), primaryScope(env.T, env.DB), "Cut green beans", []string{"prod-a", "prod-b"}, nil); err != nil {
					env.T.Fatal(err)
				}
				started := time.Now().UTC().Add(-60 * 24 * time.Hour)
				beginUsing(env.T, env.DB, started)
				insertStockIn(env.T, env.DB, "prod-a", started.Add(24*time.Hour))
				insertConsumptionEvent(env.T, env.DB, "out-1", "item-a", started.Add(20*24*time.Hour))
				insertConsumptionEvent(env.T, env.DB, "out-2", "item-a", started.Add(50*24*time.Hour))
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/shopping-list/fill",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$[0].group.name", value: "Cut green beans"},
					{path: "$[1]", absent: true},
				},
				bodyContains: []string{"Some sizes aren't known, so this counts items."},
			},
		},
		{
			name: "a group with no target pools at the household window",
			setup: func(env testEnv) {
				createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-a", "Can A", "item-a")
				createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-b", "Can B", "item-b")
				if _, err := group.NewGroups(env.DB).Create(env.T.Context(), primaryScope(env.T, env.DB), "Cut green beans", []string{"prod-a", "prod-b"}, nil); err != nil {
					env.T.Fatal(err)
				}
				started := time.Now().UTC().Add(-60 * 24 * time.Hour)
				beginUsing(env.T, env.DB, started)
				insertStockIn(env.T, env.DB, "prod-a", started.Add(24*time.Hour))
				insertConsumptionEvent(env.T, env.DB, "out-1", "item-a", started.Add(20*24*time.Hour))
				insertConsumptionEvent(env.T, env.DB, "out-2", "item-a", started.Add(50*24*time.Hour))
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/shopping-list/fill",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$[0].group.name", value: "Cut green beans"},
					{path: "$[1]", absent: true},
				},
			},
		},
		{
			name:  "a this-trip swap survives a refill",
			setup: beans,
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/shopping-list/fill",
				expectedStatus: http.StatusOK,
			},
			afterRequest: func(env testEnv) {
				handler := stagedCartHandler(env)
				body := exchangeJSON(env.T, handler, http.MethodGet, "/api/shopping-list", ``, http.StatusOK)
				var rows []struct {
					ID       string  `json:"id"`
					ItemID   string  `json:"itemId"`
					Quantity float64 `json:"quantity"`
					Group    struct {
						ID string `json:"id"`
					} `json:"group"`
				}
				if err := json.Unmarshal(body, &rows); err != nil {
					env.T.Fatal(err)
				}
				if len(rows) != 1 || rows[0].ItemID != "item-kr" || rows[0].Group.ID == "" {
					env.T.Fatalf("rows: %#v", rows)
				}
				exchangeJSON(env.T, handler, http.MethodPost, "/api/shopping-list/items/"+rows[0].ID+"/swap", `{"itemId":"item-dm"}`, http.StatusOK)
				exchangeJSON(env.T, handler, http.MethodPut, "/api/groups/"+rows[0].Group.ID+"/target", `{"quantity":80,"dimension":"mass"}`, http.StatusOK)
				exchangeJSON(env.T, handler, http.MethodPost, "/api/shopping-list/fill", ``, http.StatusOK)
				again := exchangeJSON(env.T, handler, http.MethodGet, "/api/shopping-list", ``, http.StatusOK)
				var next []struct {
					ItemID   string  `json:"itemId"`
					Quantity float64 `json:"quantity"`
				}
				if err := json.Unmarshal(again, &next); err != nil {
					env.T.Fatal(err)
				}
				if len(next) != 1 || next[0].ItemID != "item-dm" || next[0].Quantity != rows[0].Quantity {
					env.T.Fatalf("swap did not stick: %#v", next)
				}
			},
		},
		{
			name:  "a removed group line stays gone",
			setup: beans,
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/shopping-list/fill",
				expectedStatus: http.StatusOK,
			},
			afterRequest: func(env testEnv) {
				handler := stagedCartHandler(env)
				entryID := stagedEntryID(env.T, handler)
				exchangeJSON(env.T, handler, http.MethodDelete, "/api/shopping-list/items/"+entryID, ``, http.StatusOK)
				exchangeJSON(env.T, handler, http.MethodPost, "/api/shopping-list/fill", ``, http.StatusOK)
				body := exchangeJSON(env.T, handler, http.MethodGet, "/api/shopping-list", ``, http.StatusOK)
				var rows []map[string]any
				if err := json.Unmarshal(body, &rows); err != nil {
					env.T.Fatal(err)
				}
				if len(rows) != 0 {
					env.T.Fatalf("removed group line returned: %#v", rows)
				}
			},
		},
	}
	runHandlerTests(t, tests)
}
