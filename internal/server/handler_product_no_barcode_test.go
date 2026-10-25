package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Rhionin/stockbot/internal/changes"
	"github.com/Rhionin/stockbot/internal/product"
	"github.com/Rhionin/stockbot/internal/tenant"
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
			name: "query too short one letter",
			httpExchange: httpExchange{
				method:         "GET",
				path:           "/api/products/search?q=a",
				expectedStatus: http.StatusBadRequest,
				assertions: []assertion{
					{path: "$.code", value: codeQueryTooShort},
					{path: "$.error", value: "Type at least 2 letters."},
				},
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
				ProductID string  `json:"productId"`
				ItemID    *string `json:"itemId"`
			}
			if err := json.Unmarshal(searchBody, &hits); err != nil || len(hits) == 0 || hits[0].ItemID != nil {
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
			backfillBody := exchangeJSON(t, handler, http.MethodPost, "/api/inventory/"+itemID+"/stock-out", `{"backfill":true}`, http.StatusOK)
			var backfill struct {
				Backfill struct {
					StockInMoveID  string `json:"stockInMoveId"`
					StockOutMoveID string `json:"stockOutMoveId"`
				} `json:"backfill"`
			}
			if err := json.Unmarshal(backfillBody, &backfill); err != nil {
				t.Fatalf("backfill json: %v", err)
			}
			inID := backfill.Backfill.StockInMoveID
			outID := backfill.Backfill.StockOutMoveID
			if inID == "" || outID == "" || inID == outID {
				t.Fatalf("backfill ids = %+v", backfill.Backfill)
			}
			exchangeJSON(t, handler, http.MethodDelete, "/api/moves/"+inID, "", http.StatusConflict)
			exchangeJSON(t, handler, http.MethodDelete, "/api/moves/"+outID, "", http.StatusOK)
			exchangeJSON(t, handler, http.MethodDelete, "/api/moves/"+inID, "", http.StatusOK)
		},
	}})
}

func TestStockCursorMovesOnBarcodelessMutations(t *testing.T) {
	runHandlerTests(t, []handlerTestCase{{
		name: "create, ensure item, and backfill bump inventory since",
		setup: func(env testEnv) {
			finishOpening(env)
		},
		httpExchange: httpExchange{
			method:         "GET",
			path:           "/api/settings/supply",
			expectedStatus: http.StatusOK,
		},
		afterRequest: func(env testEnv) {
			handler := handlerFromEnv(env)
			scope := primaryScope(env.T, env.DB)
			ctx := context.Background()
			cursor, err := changes.Current(ctx, env.DB, scope.HouseholdID(), changes.ScopeStock)
			if err != nil {
				env.T.Fatal(err)
			}
			createBody := exchangeJSON(env.T, handler, http.MethodPost, "/api/products",
				`{"name":"Cursor jam"}`, http.StatusCreated)
			var created struct {
				ItemID string `json:"itemId"`
			}
			if err := json.Unmarshal(createBody, &created); err != nil || created.ItemID == "" {
				env.T.Fatalf("create %s (%v)", createBody, err)
			}
			assertInventoryChangedSince(env.T, handler, cursor)
			cursor, err = changes.Current(ctx, env.DB, scope.HouseholdID(), changes.ScopeStock)
			if err != nil {
				env.T.Fatal(err)
			}
			barcodeBody := exchangeJSON(env.T, handler, http.MethodPost, "/api/products",
				`{"name":"Cursor oats","barcode":"2005000000055"}`, http.StatusCreated)
			var barcodeProduct struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(barcodeBody, &barcodeProduct); err != nil || barcodeProduct.ID == "" {
				env.T.Fatalf("barcode product %s (%v)", barcodeBody, err)
			}
			exchangeJSON(env.T, handler, http.MethodPost, "/api/products/"+barcodeProduct.ID+"/item", "", http.StatusOK)
			assertInventoryChangedSince(env.T, handler, cursor)
			cursor, err = changes.Current(ctx, env.DB, scope.HouseholdID(), changes.ScopeStock)
			if err != nil {
				env.T.Fatal(err)
			}
			searchBody := exchangeJSON(env.T, handler, http.MethodGet, "/api/products/search?q=Cursor", "", http.StatusOK)
			var hits []struct {
				ItemID string `json:"itemId"`
			}
			if err := json.Unmarshal(searchBody, &hits); err != nil || len(hits) == 0 || hits[0].ItemID == "" {
				env.T.Fatalf("search %s (%v)", searchBody, err)
			}
			exchangeJSON(env.T, handler, http.MethodPost, "/api/inventory/"+hits[0].ItemID+"/stock-out", `{"backfill":true}`, http.StatusOK)
			assertInventoryChangedSince(env.T, handler, cursor)
		},
	}})
}

func assertInventoryChangedSince(t *testing.T, handler http.Handler, cursor string) {
	t.Helper()
	body := exchangeJSON(t, handler, http.MethodGet, "/api/inventory?since="+cursor, "", http.StatusOK)
	if !strings.Contains(string(body), `"changed":true`) {
		t.Fatalf("inventory since %s: %s", cursor, body)
	}
}

func TestProductSearchCatalogOnlyItemIdNull(t *testing.T) {
	runHandlerTests(t, []handlerTestCase{{
		name: "catalog-only hit serializes null itemId",
		setup: func(env testEnv) {
			finishOpening(env)
		},
		httpExchange: httpExchange{
			method:         "POST",
			path:           "/api/products",
			body:           `{"name":"Catalog only rice","barcode":"2006000000063"}`,
			expectedStatus: http.StatusCreated,
		},
		afterRequest: func(env testEnv) {
			handler := handlerFromEnv(env)
			raw := string(exchangeJSON(env.T, handler, http.MethodGet, "/api/products/search?q=rice", "", http.StatusOK))
			if !strings.Contains(raw, `"itemId":null`) {
				env.T.Fatalf("search body %s", raw)
			}
		},
	}})
}

func TestProductSearchHidesOtherHouseholdCatalogProduct(t *testing.T) {
	runHandlerTests(t, []handlerTestCase{{
		name: "typed product in another household does not appear in catalog union",
		setup: func(env testEnv) {
			finishOpening(env)
			cedar := cedarHousehold(t, env.DB)
			cedarScope, err := tenant.New(cedar)
			if err != nil {
				env.T.Fatal(err)
			}
			typed, err := env.ProductStore.CreateTypedProduct(context.Background(), cedarScope, product.Product{
				Name: "Cedar-only typed jam",
			})
			if err != nil {
				env.T.Fatal(err)
			}
			if typed.ID == "" {
				env.T.Fatal("missing product id")
			}
		},
		httpExchange: httpExchange{
			method:         "GET",
			path:           "/api/settings/supply",
			expectedStatus: http.StatusOK,
		},
		afterRequest: func(env testEnv) {
			body := exchangeJSON(env.T, handlerFromEnv(env), http.MethodGet, "/api/products/search?q=Cedar", "", http.StatusOK)
			if body == nil || strings.TrimSpace(string(body)) != "[]" {
				env.T.Fatalf("search leaked foreign catalog product: %s", body)
			}
		},
	}})
}

func TestProductSearchLikeWildcardsAreLiteral(t *testing.T) {
	runHandlerTests(t, []handlerTestCase{{
		name: "percent and underscore wildcards do not match every product",
		setup: func(env testEnv) {
			finishOpening(env)
			handler := handlerFromEnv(env)
			for _, body := range []string{
				`{"name":"Plain oats"}`,
				`{"name":"Deal %% sale"}`,
				`{"name":"A__B jam"}`,
			} {
				exchangeJSON(env.T, handler, http.MethodPost, "/api/products", body, http.StatusCreated)
			}
		},
		httpExchange: httpExchange{
			method:         "GET",
			path:           "/api/settings/supply",
			expectedStatus: http.StatusOK,
		},
		afterRequest: func(env testEnv) {
			body := exchangeJSON(env.T, handlerFromEnv(env), http.MethodGet, "/api/products/search?q=%25%25", "", http.StatusOK)
			var hits []struct {
				Name string `json:"name"`
			}
			if err := json.Unmarshal(body, &hits); err != nil {
				env.T.Fatal(err)
			}
			if len(hits) != 1 || hits[0].Name != "Deal %% sale" {
				env.T.Fatalf("%% search = %+v", hits)
			}
			underBody := exchangeJSON(env.T, handlerFromEnv(env), http.MethodGet, "/api/products/search?q=__", "", http.StatusOK)
			var underHits []struct {
				Name string `json:"name"`
			}
			if err := json.Unmarshal(underBody, &underHits); err != nil {
				env.T.Fatal(err)
			}
			if len(underHits) != 1 || underHits[0].Name != "A__B jam" {
				env.T.Fatalf("__ search = %+v", underHits)
			}
		},
	}})
}
