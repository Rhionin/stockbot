package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/cart"
	"github.com/Rhionin/stockbot/internal/cart/carttest"
)

func TestShoppingListGETByteIdenticalAcross030(t *testing.T) {
	conn, err := app.OpenSQLite(pantryTestDSN(":memory:"))
	if err != nil {
		t.Fatal(err)
	}
	conn.SetMaxOpenConns(1)
	t.Cleanup(func() { conn.Close() })
	if err := app.RunMigrationsThrough(conn, "030_multi_store.sql"); err != nil {
		t.Fatal(err)
	}
	handler, env := setupOnDB(t, conn, nil)
	before := shoppingListBody(t, handler)
	if err := app.RunMigrations(conn); err != nil {
		t.Fatal(err)
	}
	after := shoppingListBody(t, handlerFromEnv(env))
	if before != after {
		t.Fatalf("shopping list body changed across 030:\nbefore %q\nafter %q", before, after)
	}
}

func shoppingListBody(t *testing.T, handler http.Handler) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/shopping-list", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET shopping-list status %d body %s", rec.Code, rec.Body.String())
	}
	raw, err := io.ReadAll(rec.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestCartExportWorksOnDatabaseWith030(t *testing.T) {
	runHandlerTests(t, []handlerTestCase{{
		name: "export after 030 schema",
		setup: func(env testEnv) {
			createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-b9-cart", "Synthetic B9", "item-b9-cart")
			insertManualShoppingItem(env.T, env.DB, "sli-b9-cart", "user-1", "item-b9-cart", 1)
			if err := env.ProductStore.UpsertBarcodeMapping(context.Background(), "8880001112223", "prod-b9-cart", "global", ""); err != nil {
				env.T.Fatal(err)
			}
			script := carttest.NewScript().
				WithDispositions(cart.DispositionAccepted).
				WithIdentityLookup("8880001112223", cart.ProductIdentity("SKU-B9"))
			provider := carttest.NewFake(cart.Capabilities{
				Auth:         cart.AuthNone,
				Delivery:     cart.DeliveryServerPush,
				Confirmation: cart.ConfirmPerRequest,
				Mutation:     cart.MutateAddOnly,
				Identity:     cart.IdentityDerived,
			}, script)
			if err := env.Registry.Register(provider, cart.WithCredentialsConfigured(true)); err != nil {
				env.T.Fatal(err)
			}
		},
		httpExchange: httpExchange{
			method:         http.MethodPost,
			path:           "/api/shopping-list/export",
			body:           `{"provider":"test-none-server_push"}`,
			expectedStatus: http.StatusOK,
			assertions: []assertion{
				{path: "$.sentCount", value: float64(1)},
			},
		},
	}})
}
