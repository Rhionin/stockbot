package server

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestBarcodelessProductCreateAndSearch(t *testing.T) {
	runHandlerTests(t, []handlerTestCase{
		{
			name: "createItem with barcode still ensures item row",
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/products",
				body:           `{"name":"Canned beans","barcode":"2004000000040","createItem":true}`,
				expectedStatus: http.StatusCreated,
				bodyContains:   []string{`"itemId"`},
			},
		},
		{
			name: "create without barcode returns itemId at zero on hand",
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/products",
				body:           `{"name":"Homemade jam","brand":"Pantry","category":"Preserves"}`,
				expectedStatus: http.StatusCreated,
				assertions: []assertion{
					{path: "$.name", value: "Homemade jam"},
				},
				bodyContains: []string{`"itemId"`},
			},
			afterRequest: func(env testEnv) {
				body := exchangeJSON(env.T, handlerFromEnv(env), http.MethodGet, "/api/products/search?q=Homemade", "", http.StatusOK)
				var hits []struct {
					Name   string `json:"name"`
					OnHand int    `json:"onHand"`
				}
				if err := json.Unmarshal(body, &hits); err != nil || len(hits) != 1 || hits[0].Name != "Homemade jam" || hits[0].OnHand != 0 {
					env.T.Fatalf("search %s err %v", body, err)
				}
			},
		},
		{
			name: "invalid size text is rejected",
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/products",
				body:           `{"name":"Odd size","size":"can"}`,
				expectedStatus: http.StatusBadRequest,
				assertions:     []assertion{{path: "$.code", value: "validation"}},
			},
		},
		{
			name: "create with brand and size is searchable",
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/products",
				body:           `{"name":"Berry spread","brand":"Farm stand","size":"16 oz","category":"Preserves"}`,
				expectedStatus: http.StatusCreated,
			},
			afterRequest: func(env testEnv) {
				body := exchangeJSON(env.T, handlerFromEnv(env), http.MethodGet, "/api/products/search?q=berry", "", http.StatusOK)
				var hits []struct {
					Name  string `json:"name"`
					Brand string `json:"brand"`
					Size  string `json:"size"`
				}
				if err := json.Unmarshal(body, &hits); err != nil || len(hits) != 1 {
					env.T.Fatalf("search %s (%v)", body, err)
				}
				if hits[0].Brand != "Farm stand" || hits[0].Size == "" {
					env.T.Fatalf("hits = %+v", hits)
				}
			},
		},
		{
			name: "query too short",
			httpExchange: httpExchange{
				method:         "GET",
				path:           "/api/products/search?q=j",
				expectedStatus: http.StatusBadRequest,
				assertions: []assertion{
					{path: "$.code", value: codeQueryTooShort},
					{path: "$.error", value: "Type at least 2 letters."},
				},
			},
		},
	})
}

func TestProductSearchLimitParam(t *testing.T) {
	runHandlerTests(t, []handlerTestCase{{
		name: "limit query caps results",
		setup: func(env testEnv) {
			finishOpening(env)
			handler := handlerFromEnv(env)
			for _, name := range []string{"Jam jar one", "Jam jar two", "Jam jar three"} {
				exchangeJSON(env.T, handler, http.MethodPost, "/api/products",
					`{"name":"`+name+`"}`, http.StatusCreated)
			}
		},
		httpExchange: httpExchange{
			method:         "GET",
			path:           "/api/settings/supply",
			expectedStatus: http.StatusOK,
		},
		afterRequest: func(env testEnv) {
			handler := handlerFromEnv(env)
			body := exchangeJSON(env.T, handler, http.MethodGet, "/api/products/search?q=jam&limit=1", "", http.StatusOK)
			var hits []struct{}
			if err := json.Unmarshal(body, &hits); err != nil || len(hits) != 1 {
				env.T.Fatalf("body %s err %v", body, err)
			}
		},
	}})
}

func TestEnsureProductItemNotFound(t *testing.T) {
	runHandlerTests(t, []handlerTestCase{{
		name: "unknown product id",
		httpExchange: httpExchange{
			method:         "POST",
			path:           "/api/products/0190ffff-ffff-7000-8000-000000000099/item",
			expectedStatus: http.StatusNotFound,
			assertions:     []assertion{{path: "$.code", value: "not_found"}},
		},
	}})
}

func TestEnsureProductItemForCatalogProduct(t *testing.T) {
	runHandlerTests(t, []handlerTestCase{{
		name: "ensure item for catalog product without shelf row",
		setup: func(env testEnv) {
			finishOpening(env)
		},
		httpExchange: httpExchange{
			method:         "POST",
			path:           "/api/products",
			body:           `{"name":"Shelf oats","barcode":"2003000000031"}`,
			expectedStatus: http.StatusCreated,
		},
		afterRequest: func(env testEnv) {
			handler := handlerFromEnv(env)
			searchBody := exchangeJSON(t, handler, http.MethodGet, "/api/products/search?q=oats", "", http.StatusOK)
			var hits []struct {
				ProductID string `json:"productId"`
				ItemID    string `json:"itemId"`
			}
			if err := json.Unmarshal(searchBody, &hits); err != nil || len(hits) == 0 || hits[0].ItemID != "" {
				env.T.Fatalf("search before ensure: %s (%v)", searchBody, err)
			}
			ensureBody := exchangeJSON(t, handler, http.MethodPost, "/api/products/"+hits[0].ProductID+"/item", "", http.StatusOK)
			var ensured struct {
				ItemID string `json:"itemId"`
			}
			if err := json.Unmarshal(ensureBody, &ensured); err != nil || ensured.ItemID == "" {
				env.T.Fatalf("ensure: %s (%v)", ensureBody, err)
			}
		},
	}})
}

func TestFindByNameStockInOutAndBackfill(t *testing.T) {
	runHandlerTests(t, []handlerTestCase{{
		name: "stock in, stock out, and backfill at zero",
		setup: func(env testEnv) {
			finishOpening(env)
		},
		httpExchange: httpExchange{
			method:         "POST",
			path:           "/api/products",
			body:           `{"name":"Quick oats","category":"Breakfast"}`,
			expectedStatus: http.StatusCreated,
		},
		afterRequest: func(env testEnv) {
			handler := handlerFromEnv(env)
			searchBody := exchangeJSON(t, handler, http.MethodGet, "/api/products/search?q=oats", "", http.StatusOK)
			var hits []struct {
				ItemID string `json:"itemId"`
			}
			if err := json.Unmarshal(searchBody, &hits); err != nil || len(hits) == 0 || hits[0].ItemID == "" {
				env.T.Fatalf("search: %s (%v)", searchBody, err)
			}
			itemID := hits[0].ItemID
			exchanges(
				httpExchange{
					method:         "POST",
					path:           "/api/inventory/" + itemID + "/instances",
					body:           `{}`,
					expectedStatus: http.StatusCreated,
				},
				httpExchange{
					method:         "POST",
					path:           "/api/inventory/" + itemID + "/stock-out",
					body:           `{}`,
					expectedStatus: http.StatusOK,
				},
				httpExchange{
					method:         "POST",
					path:           "/api/inventory/" + itemID + "/stock-out",
					body:           `{"backfill":true}`,
					expectedStatus: http.StatusOK,
				},
			)(env)
		},
	}})
}
