package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/cart"
	"github.com/Rhionin/stockbot/internal/cart/carttest"
	"github.com/Rhionin/stockbot/internal/product"
)

func TestStagedCart(t *testing.T) {
	now := time.Now().UTC()
	stageBeans := func(env testEnv) {
		createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-stage", "Beans", "item-stage")
		beginUsing(env.T, env.DB, now)
		setSupplyQuantity(env.T, env.DB, "prod-stage", 4)
	}

	tests := []handlerTestCase{
		{
			name:  "the list stays empty until the cart is filled",
			setup: stageBeans,
			httpExchange: httpExchange{
				method:         "GET",
				path:           "/api/shopping-list",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$", value: []interface{}{}},
				},
			},
			afterRequest: exchanges(httpExchange{
				method:         "POST",
				path:           "/api/shopping-list/fill",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$[0].itemId", value: "item-stage"},
					{path: "$[0].quantity", value: float64(3)},
					{path: "$[0].source", value: "auto"},
					{path: "$[1]", absent: true},
				},
			}),
		},
		{
			name: "a quantity edit survives a refill",
			setup: func(env testEnv) {
				stageBeans(env)
				registerProductionShapeKroger(env)
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/shopping-list/fill",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$[0].quantity", value: float64(3)},
				},
			},
			afterRequest: func(env testEnv) {
				handler := stagedCartHandler(env)
				entryID := stagedEntryID(env.T, handler)
				exchangeJSON(env.T, handler, http.MethodPut, "/api/shopping-list/items/"+entryID+"/adjustment?provider=kroger", `{"adjustment":9}`, http.StatusOK)
				exchangeJSON(env.T, handler, http.MethodPut, "/api/products/prod-stage/supply-override", `{"quantity":20}`, http.StatusOK)
				exchangeJSON(env.T, handler, http.MethodPost, "/api/shopping-list/fill", ``, http.StatusOK)
				body := exchangeJSON(env.T, handler, http.MethodGet, "/api/shopping-list?provider=kroger", ``, http.StatusOK)
				var rows []map[string]any
				if err := json.Unmarshal(body, &rows); err != nil {
					env.T.Fatalf("decode list: %v", err)
				}
				if len(rows) != 1 {
					env.T.Fatalf("rows: %#v", rows)
				}
				if rows[0]["id"] != entryID {
					env.T.Fatalf("refill replaced the edited line: %#v", rows[0])
				}
				if rows[0]["quantity"] != float64(9) {
					env.T.Fatalf("edited quantity: %#v", rows[0])
				}
			},
		},
		{
			name: "send uses the staged quantity",
			setup: func(env testEnv) {
				stageBeans(env)
				if err := env.ProductStore.UpsertBarcodeMapping(context.Background(), "000444444448", "prod-stage", "global", ""); err != nil {
					env.T.Fatalf("barcode: %v", err)
				}
				script := carttest.NewScript().
					WithDispositions(cart.DispositionAccepted).
					WithIdentityLookup("000444444448", cart.ProductIdentity("beans"))
				provider := carttest.NewFake(cart.Capabilities{
					Auth:         cart.AuthNone,
					Delivery:     cart.DeliveryServerPush,
					Confirmation: cart.ConfirmPerRequest,
					Mutation:     cart.MutateAddOnly,
					Identity:     cart.IdentityDerived,
				}, script)
				if err := env.Registry.Register(provider, cart.WithCredentialsConfigured(true)); err != nil {
					env.T.Fatalf("register: %v", err)
				}
				seedDefaultExportApp(env.T, env.DB, string(provider.ID()))
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/shopping-list/fill",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$[0].quantity", value: float64(3)},
				},
			},
			afterRequest: exchanges(
				httpExchange{
					method:         "PUT",
					path:           "/api/products/prod-stage/supply-override",
					body:           `{"quantity":20}`,
					expectedStatus: http.StatusOK,
				},
				httpExchange{
					method:         "POST",
					path:           "/api/shopping-list/export",
					body:           `{"provider":"test-none-server_push"}`,
					expectedStatus: http.StatusOK,
					assertions: []assertion{
						{path: "$.exported", value: float64(1)},
						{path: "$.entries[0].quantity", value: float64(3)},
						{path: "$.items[0].quantity", value: float64(3)},
					},
				},
			),
		},
		{
			name:  "a removed line stays off the cart",
			setup: stageBeans,
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
					env.T.Fatalf("decode list: %v", err)
				}
				if len(rows) != 0 {
					env.T.Fatalf("removed line returned: %#v", rows)
				}
			},
		},
	}

	runHandlerTests(t, tests)
}

func stagedCartHandler(env testEnv) http.Handler {
	var now func() time.Time
	if env.Clock != nil {
		now = env.Clock.Now
	}
	registry := env.Registry
	if registry == nil {
		registry = cart.NewRegistry()
	}
	handler, _ := NewHandler(env.ProductStore, &product.LookupService{
		Catalog:   env.ProductStore,
		Upstream:  env.Upstream,
		Refresher: env.Refresher,
		Now:       now,
		MissTTL:   env.MissTTL,
	}, env.Refresher, env.DB, WithCartRegistry(registry, cart.NewLedger(env.DB)))
	return handler
}

func stagedEntryID(t *testing.T, handler http.Handler) string {
	t.Helper()
	body := exchangeJSON(t, handler, http.MethodGet, "/api/shopping-list", ``, http.StatusOK)
	var rows []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &rows); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(rows) != 1 || rows[0].ID == "" {
		t.Fatalf("staged rows: %#v", rows)
	}
	return rows[0].ID
}

func exchangeJSON(t *testing.T, handler http.Handler, method, path, payload string, want int) []byte {
	t.Helper()
	var reader io.Reader
	if payload != "" {
		reader = strings.NewReader(payload)
	}
	req := httptest.NewRequest(method, path, reader)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	res := rec.Result()
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read %s %s: %v", method, path, err)
	}
	if res.StatusCode != want {
		t.Fatalf("%s %s status %d: %s", method, path, res.StatusCode, body)
	}
	return body
}
