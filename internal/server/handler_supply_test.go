package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSupplyPolicy(t *testing.T) {
	now := time.Now().UTC()

	tests := []handlerTestCase{
		{
			name: "opening emits manuals only",
			setup: func(env testEnv) {
				createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-open-manual", "Oats", "item-open-manual")
				createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-open-used", "Rice", "item-open-used")
				insertManualShoppingItem(env.T, env.DB, "sli-open", "user-1", "item-open-manual", 3)
				insertConsumptionEvent(env.T, env.DB, "ce-open", "item-open-used", now.Add(-2*24*time.Hour))
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/shopping-list/fill",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$[0].itemId", value: "item-open-manual"},
					{path: "$[0].quantity", value: float64(3)},
					{path: "$[0].source", value: "manual"},
					{path: "$[0].note", value: ""},
					{path: "$[1]", absent: true},
				},
			},
		},
		{
			name: "day 29 replaces used units and keeps a quantity override",
			setup: func(env testEnv) {
				beginUsing(env.T, env.DB, now.Add(-29*24*time.Hour))
				createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-day29", "Beans", "item-day29")
				insertConsumptionEvent(env.T, env.DB, "ce-day29-a", "item-day29", now.Add(-10*24*time.Hour))
				insertConsumptionEvent(env.T, env.DB, "ce-day29-b", "item-day29", now.Add(-2*24*time.Hour))
				createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-day29-qty", "Flour", "item-day29-qty")
				setSupplyQuantity(env.T, env.DB, "prod-day29-qty", 12)
				createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-day29-window", "Sugar", "item-day29-window")
				if _, err := env.DB.Exec(`INSERT INTO item_targets (household_id, item_id, kind, value) VALUES ('user-1', 'item-day29-window', 'months', 6)`); err != nil {
					env.T.Fatalf("window target: %v", err)
				}
				insertConsumptionEvent(env.T, env.DB, "ce-day29-window", "item-day29-window", now.Add(-1*24*time.Hour))
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/shopping-list/fill",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$[0].itemId", value: "item-day29"},
					{path: "$[0].quantity", value: float64(2)},
					{path: "$[0].source", value: "auto"},
					{path: "$[0].note", value: "replacing 2 you used"},
					{path: "$[1].itemId", value: "item-day29-qty"},
					{path: "$[1].quantity", value: float64(11)},
					{path: "$[1].note", value: ""},
					{path: "$[2].itemId", value: "item-day29-window"},
					{path: "$[2].quantity", value: float64(1)},
					{path: "$[2].note", value: "replacing 1 you used"},
					{path: "$[3]", absent: true},
				},
			},
		},
		{
			name: "a manual row is not resized by replacement",
			setup: func(env testEnv) {
				beginUsing(env.T, env.DB, now.Add(-29*24*time.Hour))
				createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-manual-keep", "Tea", "item-manual-keep")
				insertManualShoppingItem(env.T, env.DB, "sli-manual-keep", "user-1", "item-manual-keep", 5)
				insertConsumptionEvent(env.T, env.DB, "ce-manual-keep-a", "item-manual-keep", now.Add(-8*24*time.Hour))
				insertConsumptionEvent(env.T, env.DB, "ce-manual-keep-b", "item-manual-keep", now.Add(-7*24*time.Hour))
				insertConsumptionEvent(env.T, env.DB, "ce-manual-keep-c", "item-manual-keep", now.Add(-6*24*time.Hour))
				createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-manual-other", "Honey", "item-manual-other")
				insertConsumptionEvent(env.T, env.DB, "ce-manual-other", "item-manual-other", now.Add(-1*24*time.Hour))
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/shopping-list/fill",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$[0].itemId", value: "item-manual-keep"},
					{path: "$[0].quantity", value: float64(5)},
					{path: "$[0].source", value: "manual"},
					{path: "$[1].itemId", value: "item-manual-other"},
					{path: "$[1].quantity", value: float64(1)},
					{path: "$[1].note", value: "replacing 1 you used"},
					{path: "$[2]", absent: true},
				},
			},
		},
		{
			name: "one gap does not become a rate",
			setup: func(env testEnv) {
				started := now.Add(-40 * 24 * time.Hour)
				beginUsing(env.T, env.DB, started)
				createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-one-gap", "Pasta", "item-one-gap")
				insertStockIn(env.T, env.DB, "prod-one-gap", started.Add(24*time.Hour))
				insertConsumptionEvent(env.T, env.DB, "ce-one-gap", "item-one-gap", started.Add(20*24*time.Hour))
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/shopping-list/fill",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$[0].itemId", value: "item-one-gap"},
					{path: "$[0].quantity", value: float64(1)},
					{path: "$[0].note", value: "replacing 1 you used"},
				},
			},
		},
		{
			name: "a holiday-sized outlier is refused",
			setup: func(env testEnv) {
				started := now.Add(-100 * 24 * time.Hour)
				beginUsing(env.T, env.DB, started)
				createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-holiday", "Cookies", "item-holiday")
				firstIn := started.Add(24 * time.Hour)
				insertStockIn(env.T, env.DB, "prod-holiday", firstIn)
				insertConsumptionEvent(env.T, env.DB, "ce-holiday-1", "item-holiday", firstIn.Add(30*24*time.Hour))
				insertConsumptionEvent(env.T, env.DB, "ce-holiday-2", "item-holiday", firstIn.Add(60*24*time.Hour))
				burst := firstIn.Add(61 * 24 * time.Hour)
				for _, id := range []string{"ce-holiday-3", "ce-holiday-4", "ce-holiday-5", "ce-holiday-6"} {
					insertConsumptionEvent(env.T, env.DB, id, "item-holiday", burst)
				}
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/shopping-list/fill",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$[0].quantity", value: float64(6)},
					{path: "$[0].note", value: "replacing 6 you used"},
				},
			},
		},
		{
			name: "a steady rate becomes a par",
			setup: func(env testEnv) {
				started := now.Add(-200 * 24 * time.Hour)
				beginUsing(env.T, env.DB, started)
				createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-steady", "Milk", "item-steady")
				firstIn := started.Add(24 * time.Hour)
				insertStockIn(env.T, env.DB, "prod-steady", firstIn)
				for i, id := range []string{"ce-steady-1", "ce-steady-2", "ce-steady-3", "ce-steady-4"} {
					insertConsumptionEvent(env.T, env.DB, id, "item-steady", firstIn.Add(time.Duration(i+1)*30*24*time.Hour))
				}
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/shopping-list/fill",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$[0].itemId", value: "item-steady"},
					{path: "$[0].quantity", value: float64(2)},
					{path: "$[0].note", value: "about 1 a month, so 3 for 3 months."},
				},
			},
		},
		{
			name: "supply override rejects both arms",
			setup: func(env testEnv) {
				createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-xor", "Soap", "item-xor")
			},
			httpExchange: httpExchange{
				method:         "PUT",
				path:           "/api/products/prod-xor/supply-override",
				body:           `{"windowMonths":6,"quantity":12}`,
				expectedStatus: http.StatusBadRequest,
				assertions: []assertion{
					{path: "$.error", value: "Send a supply window, a quantity, or clear."},
				},
			},
		},
		{
			name: "supply override rejects an empty body",
			setup: func(env testEnv) {
				createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-xor-empty", "Soap", "item-xor-empty")
			},
			httpExchange: httpExchange{
				method:         "PUT",
				path:           "/api/products/prod-xor-empty/supply-override",
				body:           `{}`,
				expectedStatus: http.StatusBadRequest,
				assertions: []assertion{
					{path: "$.error", value: "Send a supply window, a quantity, or clear."},
				},
			},
		},
		{
			name: "supply override rejects clear together with a quantity",
			setup: func(env testEnv) {
				createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-xor-clear", "Soap", "item-xor-clear")
			},
			httpExchange: httpExchange{
				method:         "PUT",
				path:           "/api/products/prod-xor-clear/supply-override",
				body:           `{"quantity":12,"clear":true}`,
				expectedStatus: http.StatusBadRequest,
			},
		},
		{
			name: "supply override rejects clear false",
			setup: func(env testEnv) {
				createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-xor-false", "Soap", "item-xor-false")
			},
			httpExchange: httpExchange{
				method:         "PUT",
				path:           "/api/products/prod-xor-false/supply-override",
				body:           `{"clear":false}`,
				expectedStatus: http.StatusBadRequest,
			},
		},
		{
			name: "quantity override is the par during the first month",
			setup: func(env testEnv) {
				beginUsing(env.T, env.DB, now)
				createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-fixed", "Soap", "item-fixed")
			},
			httpExchange: httpExchange{
				method:         "PUT",
				path:           "/api/products/prod-fixed/supply-override",
				body:           `{"quantity":12}`,
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.quantity", value: float64(12)},
					{path: "$.windowMonths", absent: true},
				},
			},
			afterRequest: exchanges(httpExchange{
				method:         "POST",
				path:           "/api/shopping-list/fill",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$[0].itemId", value: "item-fixed"},
					{path: "$[0].quantity", value: float64(11)},
					{path: "$[0].note", value: ""},
				},
			}),
		},
		{
			name: "a window override waits until a rate qualifies",
			setup: func(env testEnv) {
				beginUsing(env.T, env.DB, now.Add(-10*24*time.Hour))
				createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-window", "Soap", "item-window")
				insertConsumptionEvent(env.T, env.DB, "ce-window", "item-window", now.Add(-24*time.Hour))
			},
			httpExchange: httpExchange{
				method:         "PUT",
				path:           "/api/products/prod-window/supply-override",
				body:           `{"windowMonths":6}`,
				expectedStatus: http.StatusOK,
			},
			afterRequest: exchanges(httpExchange{
				method:         "POST",
				path:           "/api/shopping-list/fill",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$[0].note", value: "replacing 1 you used"},
					{path: "$[0].quantity", value: float64(1)},
				},
			}),
		},
		{
			name: "clearing an override falls back to replacement",
			setup: func(env testEnv) {
				beginUsing(env.T, env.DB, now.Add(-5*24*time.Hour))
				createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-clear", "Soap", "item-clear")
				setSupplyQuantity(env.T, env.DB, "prod-clear", 12)
				insertConsumptionEvent(env.T, env.DB, "ce-clear", "item-clear", now.Add(-24*time.Hour))
			},
			httpExchange: httpExchange{
				method:         "PUT",
				path:           "/api/products/prod-clear/supply-override",
				body:           `{"clear":true}`,
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.quantity", absent: true},
					{path: "$.windowMonths", absent: true},
				},
			},
			afterRequest: exchanges(httpExchange{
				method:         "POST",
				path:           "/api/shopping-list/fill",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$[0].note", value: "replacing 1 you used"},
					{path: "$[0].quantity", value: float64(1)},
				},
			}),
		},
		{
			name: "wipe clears the start date and leaves the supply length",
			setup: func(env testEnv) {
				beginUsing(env.T, env.DB, now.Add(-24*time.Hour))
				if _, err := env.DB.Exec(`INSERT INTO app_settings (key, value) VALUES ('supply_months', '4')`); err != nil {
					env.T.Fatalf("months: %v", err)
				}
				createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-wipe-supply", "Milk", "item-wipe-supply")
				setSupplyQuantity(env.T, env.DB, "prod-wipe-supply", 8)
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/inventory/wipe",
				body:           `{"confirmation":"WIPE INVENTORY"}`,
				expectedStatus: http.StatusOK,
			},
			afterRequest: exchanges(
				httpExchange{
					method:         "GET",
					path:           "/api/settings/supply",
					expectedStatus: http.StatusOK,
					assertions: []assertion{
						{path: "$.opening", value: true},
						{path: "$.months", value: float64(4)},
					},
				},
				httpExchange{
					method:         "GET",
					path:           "/api/products/prod-wipe-supply/supply-override",
					expectedStatus: http.StatusOK,
					assertions: []assertion{
						{path: "$.quantity", absent: true},
						{path: "$.windowMonths", absent: true},
					},
				},
				httpExchange{
					method:         "GET",
					path:           "/api/products/prod-wipe-supply",
					expectedStatus: http.StatusOK,
					assertions: []assertion{
						{path: "$.name", value: "Milk"},
					},
				},
			),
		},
		{
			name: "a wrong wipe phrase does not clear the start",
			setup: func(env testEnv) {
				beginUsing(env.T, env.DB, now.Add(-24*time.Hour))
				createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-wipe-keep", "Milk", "item-wipe-keep")
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/inventory/wipe",
				body:           `{"confirmation":"wipe inventory"}`,
				expectedStatus: http.StatusBadRequest,
			},
			afterRequest: exchanges(httpExchange{
				method:         "GET",
				path:           "/api/settings/supply",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.opening", value: false},
				},
			}),
		},
		{
			name: "absent supply length means 3 months",
			httpExchange: httpExchange{
				method:         "GET",
				path:           "/api/settings/supply",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.months", value: float64(3)},
					{path: "$.opening", value: true},
					{path: "$.wipePhrase", value: "WIPE INVENTORY"},
				},
			},
		},
		{
			name: "supply length rejects a window outside 1 to 12",
			httpExchange: httpExchange{
				method:         "PUT",
				path:           "/api/settings/supply",
				body:           `{"months":0}`,
				expectedStatus: http.StatusBadRequest,
				assertions: []assertion{
					{path: "$.error", value: "supply length must be from 1 to 12 months"},
				},
			},
		},
		{
			name: "the account supply length sets the par",
			setup: func(env testEnv) {
				started := now.Add(-200 * 24 * time.Hour)
				beginUsing(env.T, env.DB, started)
				createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-months", "Milk", "item-months")
				firstIn := started.Add(24 * time.Hour)
				insertStockIn(env.T, env.DB, "prod-months", firstIn)
				for i, id := range []string{"ce-months-1", "ce-months-2", "ce-months-3", "ce-months-4"} {
					insertConsumptionEvent(env.T, env.DB, id, "item-months", firstIn.Add(time.Duration(i+1)*30*24*time.Hour))
				}
			},
			httpExchange: httpExchange{
				method:         "PUT",
				path:           "/api/settings/supply",
				body:           `{"months":6}`,
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.months", value: float64(6)},
					{path: "$.opening", value: false},
				},
			},
			afterRequest: exchanges(httpExchange{
				method:         "POST",
				path:           "/api/shopping-list/fill",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$[0].itemId", value: "item-months"},
					{path: "$[0].quantity", value: float64(5)},
					{path: "$[0].note", value: "about 1 a month, so 6 for 6 months."},
				},
			}),
		},
	}

	runHandlerTests(t, tests)
}

func TestCompleteOnboardingTwiceKeepsTheStart(t *testing.T) {
	handler, _ := setupTestWithDB(t)

	first := postComplete(t, handler)
	second := postComplete(t, handler)
	if first.StartedAt == "" || first.StartedAt != second.StartedAt {
		t.Fatalf("start moved from %q to %q", first.StartedAt, second.StartedAt)
	}
}

type completeBody struct {
	StartedAt string `json:"startedAt"`
}

func TestSupplyOverrideRefusesGroupedProduct(t *testing.T) {
	tests := []handlerTestCase{
		{
			name: "get and put return the group id",
			setup: func(env testEnv) {
				if _, err := env.DB.Exec(`INSERT INTO products (id, name, unit_of_measure) VALUES ('prod-grouped', 'Kroger Cut Green Beans', 'can')`); err != nil {
					env.T.Fatal(err)
				}
				if _, err := env.DB.Exec(`
					INSERT INTO product_groups (id, user_id, name, name_key, rule)
					VALUES ('g-beans', 'user-1', 'Cut green beans', 'cut green beans', 'same_as_ran_out')`); err != nil {
					env.T.Fatal(err)
				}
				if _, err := env.DB.Exec(`
					INSERT INTO product_group_members (product_id, group_id) VALUES ('prod-grouped', 'g-beans')`); err != nil {
					env.T.Fatal(err)
				}
			},
			httpExchange: httpExchange{
				method:         "GET",
				path:           "/api/products/prod-grouped/supply-override",
				expectedStatus: http.StatusConflict,
				assertions: []assertion{
					{path: "$.code", value: "in_group"},
					{path: "$.members.groupId", value: "g-beans"},
				},
			},
			afterRequest: exchanges(httpExchange{
				method:         "PUT",
				path:           "/api/products/prod-grouped/supply-override",
				body:           `{"quantity":4}`,
				expectedStatus: http.StatusConflict,
				assertions: []assertion{
					{path: "$.code", value: "in_group"},
					{path: "$.members.groupId", value: "g-beans"},
				},
			}),
		},
	}
	runHandlerTests(t, tests)
}

func postComplete(t *testing.T, handler http.Handler) completeBody {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/onboarding/complete", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	res := rec.Result()
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("complete status %d: %s", res.StatusCode, body)
	}
	var got completeBody
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatalf("decode complete: %v", err)
	}
	return got
}
