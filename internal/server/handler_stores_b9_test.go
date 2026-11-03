package server

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/cart"
	"github.com/Rhionin/stockbot/internal/cart/carttest"
)

// TestShoppingListGETByteIdenticalAcross030 uses a zero-store household with a
// populated shopping list (manual, auto, and purchased lines) before 030.
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
	hid := primaryScope(t, conn).HouseholdID()
	seedPopulatedZeroStoreList(t, conn, hid)
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

func seedPopulatedZeroStoreList(t *testing.T, db *sql.DB, hid string) {
	t.Helper()
	// Household has no stores (stores table does not exist until 030).
	when := "2026-01-15T12:00:00Z"
	bought := "2026-01-16T12:00:00Z"
	products := []struct{ id, name, item string }{
		{"prod-b9-manual", "B9 Manual", "item-b9-manual"},
		{"prod-b9-auto", "B9 Auto", "item-b9-auto"},
		{"prod-b9-bought", "B9 Bought", "item-b9-bought"},
	}
	for _, p := range products {
		if _, err := db.Exec(`INSERT INTO products (id, name) VALUES (?, ?)`, p.id, p.name); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`
			INSERT INTO items (id, user_id, product_id, household_id) VALUES (?, 'user-1', ?, ?)`,
			p.item, p.id, hid); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`
		INSERT INTO shopping_list_items (id, household_id, item_id, user_id, quantity, source, created_at)
		VALUES ('sli-b9-manual', ?, 'item-b9-manual', 'user-1', 2, 'manual', ?)`, hid, when); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO shopping_list_items (id, household_id, item_id, user_id, quantity, source, created_at)
		VALUES ('sli-b9-auto', ?, 'item-b9-auto', 'user-1', 1, 'auto', ?)`, hid, when); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO shopping_list_items (id, household_id, item_id, user_id, quantity, source, created_at, purchased_at)
		VALUES ('sli-b9-bought', ?, 'item-b9-bought', 'user-1', 1, 'manual', ?, ?)`, hid, when, bought); err != nil {
		t.Fatal(err)
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
	conn, err := app.OpenSQLite(pantryTestDSN(":memory:"))
	if err != nil {
		t.Fatal(err)
	}
	conn.SetMaxOpenConns(1)
	t.Cleanup(func() { conn.Close() })
	if err := app.RunMigrationsThrough(conn, "030_multi_store.sql"); err != nil {
		t.Fatal(err)
	}
	scope := primaryScope(t, conn)
	when := time.Now().UTC().Format(time.RFC3339Nano)
	expires := time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339Nano)
	if _, err := conn.Exec(`
		INSERT INTO provider_connections (id, user_id, household_id, provider_id, state, access_token, refresh_token, access_token_expires_at, updated_at)
		VALUES ('conn-b9-pre030', 'user-1', ?, 'test-oauth2_authorization_code-server_push', 'connected', 'access-b9', 'refresh-b9', ?, ?)`,
		scope.HouseholdID(), expires, when); err != nil {
		t.Fatal(err)
	}
	if err := app.RunMigrations(conn); err != nil {
		t.Fatal(err)
	}
	handler, env := setupOnDB(t, conn, nil)
	createItemViaStockIn(t, conn, env.ProductStore, "user-1", "prod-b9-cart", "Synthetic B9", "item-b9-cart")
	insertManualShoppingItem(t, conn, "sli-b9-cart", "user-1", "item-b9-cart", 1)
	if err := env.ProductStore.UpsertBarcodeMapping(context.Background(), "8880001112223", "prod-b9-cart", "global", ""); err != nil {
		t.Fatal(err)
	}
	script := carttest.NewScript().
		WithDispositions(cart.DispositionAccepted).
		WithIdentityLookup("8880001112223", cart.ProductIdentity("SKU-B9")).
		WithAddAuthCheck(func(authz string) error {
			if authz != "Bearer access-b9" {
				return fmt.Errorf("unexpected bearer %q", authz)
			}
			return nil
		})
	provider := carttest.NewFake(cart.Capabilities{
		Auth:         cart.AuthOAuth2,
		Delivery:     cart.DeliveryServerPush,
		Confirmation: cart.ConfirmPerRequest,
		Mutation:     cart.MutateAddOnly,
		Identity:     cart.IdentityDerived,
	}, script)
	if err := env.Registry.Register(provider, cart.WithCredentialsConfigured(true)); err != nil {
		t.Fatal(err)
	}
	executeExchange(t, handler, httpExchange{
		method:         http.MethodPost,
		path:           "/api/shopping-list/export",
		body:           `{"provider":"test-oauth2_authorization_code-server_push"}`,
		expectedStatus: http.StatusOK,
		assertions: []assertion{
			{path: "$.sentCount", value: float64(1)},
		},
	})
}

func TestCartExportB9RejectsWrongBearerToken(t *testing.T) {
	conn, err := app.OpenSQLite(pantryTestDSN(":memory:"))
	if err != nil {
		t.Fatal(err)
	}
	conn.SetMaxOpenConns(1)
	t.Cleanup(func() { conn.Close() })
	if err := app.RunMigrationsThrough(conn, "030_multi_store.sql"); err != nil {
		t.Fatal(err)
	}
	scope := primaryScope(t, conn)
	when := time.Now().UTC().Format(time.RFC3339Nano)
	expires := time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339Nano)
	if _, err := conn.Exec(`
		INSERT INTO provider_connections (id, user_id, household_id, provider_id, state, access_token, refresh_token, access_token_expires_at, updated_at)
		VALUES ('conn-b9-pre030', 'user-1', ?, 'test-oauth2_authorization_code-server_push', 'connected', 'wrong-token', 'refresh-b9', ?, ?)`,
		scope.HouseholdID(), expires, when); err != nil {
		t.Fatal(err)
	}
	if err := app.RunMigrations(conn); err != nil {
		t.Fatal(err)
	}
	handler, env := setupOnDB(t, conn, nil)
	createItemViaStockIn(t, conn, env.ProductStore, "user-1", "prod-b9-cart", "Synthetic B9", "item-b9-cart")
	insertManualShoppingItem(t, conn, "sli-b9-cart", "user-1", "item-b9-cart", 1)
	if err := env.ProductStore.UpsertBarcodeMapping(context.Background(), "8880001112223", "prod-b9-cart", "global", ""); err != nil {
		t.Fatal(err)
	}
	script := carttest.NewScript().
		WithDispositions(cart.DispositionAccepted).
		WithIdentityLookup("8880001112223", cart.ProductIdentity("SKU-B9")).
		WithAddAuthCheck(func(authz string) error {
			if authz != "Bearer access-b9" {
				return fmt.Errorf("unexpected bearer")
			}
			return nil
		})
	provider := carttest.NewFake(cart.Capabilities{
		Auth: cart.AuthOAuth2, Delivery: cart.DeliveryServerPush,
		Confirmation: cart.ConfirmPerRequest, Mutation: cart.MutateAddOnly, Identity: cart.IdentityDerived,
	}, script)
	if err := env.Registry.Register(provider, cart.WithCredentialsConfigured(true)); err != nil {
		t.Fatal(err)
	}
	executeExchange(t, handler, httpExchange{
		method: http.MethodPost, path: "/api/shopping-list/export",
		body:           `{"provider":"test-oauth2_authorization_code-server_push"}`,
		expectedStatus: http.StatusOK,
		assertions:     []assertion{{path: "$.sentCount", value: float64(0)}},
	})
}
