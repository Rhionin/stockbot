package server

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/cart"
	"github.com/Rhionin/stockbot/internal/cart/carttest"
	"github.com/Rhionin/stockbot/internal/cart/connection"
)

func TestShoppingListExportProvisionOutcomes(t *testing.T) {
	tests := []handlerTestCase{
		{
			name: "export reports every staged line once with mixed outcomes",
			setup: func(env testEnv) {
				createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-mix-milk", "Milk", "item-mix-milk")
				createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-mix-cheese", "Farm cheese", "item-mix-cheese")
				createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-mix-oats", "Bulk oats", "item-mix-oats")
				createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-mix-spice", "Organic spice", "item-mix-spice")
				insertManualShoppingItem(env.T, env.DB, "sli-mix-milk", "user-1", "item-mix-milk", 1)
				insertManualShoppingItem(env.T, env.DB, "sli-mix-cheese", "user-1", "item-mix-cheese", 1)
				insertManualShoppingItem(env.T, env.DB, "sli-mix-oats", "user-1", "item-mix-oats", 1)
				insertManualShoppingItem(env.T, env.DB, "sli-mix-spice", "user-1", "item-mix-spice", 1)
				ctx := context.Background()
				if err := env.ProductStore.UpsertBarcodeMapping(ctx, "111", "prod-mix-milk", "global", ""); err != nil {
					env.T.Fatalf("barcode milk: %v", err)
				}
				if err := env.ProductStore.UpsertBarcodeMapping(ctx, "222", "prod-mix-cheese", "global", ""); err != nil {
					env.T.Fatalf("barcode cheese: %v", err)
				}
				if err := env.ProductStore.UpsertBarcodeMapping(ctx, "333", "prod-mix-spice", "global", ""); err != nil {
					env.T.Fatalf("barcode spice: %v", err)
				}
				script := carttest.NewScript().
					WithDispositions(cart.DispositionAccepted, cart.DispositionRejected).
					WithIdentityLookup("111", cart.ProductIdentity("SKU-MILK")).
					WithIdentityLookup("333", cart.ProductIdentity("SKU-SPICE"))
				provider := carttest.NewFake(cart.Capabilities{
					Auth:         cart.AuthNone,
					Delivery:     cart.DeliveryServerPush,
					Confirmation: cart.ConfirmPerRequest,
					Mutation:     cart.MutateAddOnly,
					Identity:     cart.IdentityDerived,
				}, script)
				if err := env.Registry.Register(provider, cart.WithCredentialsConfigured(true), cart.WithBatchSize(1)); err != nil {
					env.T.Fatalf("register: %v", err)
				}
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/shopping-list/export",
				body:           `{"provider":"test-none-server_push"}`,
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.exported", value: float64(1)},
					{path: "$.addedCount", value: float64(1)},
					{path: "$.skippedCount", value: float64(2)},
					{path: "$.failedCount", value: float64(1)},
					{path: "$.unknownCount", value: float64(0)},
				},
				bodyContains: []string{
					`"status":"added"`,
					`"reason":"no_upc"`,
					`"reason":"no_match"`,
					`"reason":"api_error"`,
				},
				bodyExcludes: []string{
					"super-secret",
					"Bearer ",
					"refresh_token",
					"invalid_grant",
				},
			},
		},
		{
			name: "export per-line unavailable is skipped not failed",
			setup: func(env testEnv) {
				createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-unavail", "Store brand", "item-unavail")
				insertManualShoppingItem(env.T, env.DB, "sli-unavail", "user-1", "item-unavail", 1)
				if err := env.ProductStore.UpsertBarcodeMapping(context.Background(), "777", "prod-unavail", "global", ""); err != nil {
					env.T.Fatalf("barcode: %v", err)
				}
				script := carttest.NewScript().
					WithDispositions(cart.DispositionAccepted).
					WithLineResults(map[cart.ProductIdentity]bool{"SKU-NO": false}).
					WithIdentityLookup("777", cart.ProductIdentity("SKU-NO"))
				provider := carttest.NewFake(cart.Capabilities{
					Auth:         cart.AuthNone,
					Delivery:     cart.DeliveryServerPush,
					Confirmation: cart.ConfirmPerLine,
					Mutation:     cart.MutateAddOnly,
					Identity:     cart.IdentityDerived,
				}, script)
				if err := env.Registry.Register(provider, cart.WithCredentialsConfigured(true)); err != nil {
					env.T.Fatalf("register: %v", err)
				}
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/shopping-list/export",
				body:           `{"provider":"test-none-server_push"}`,
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.exported", value: float64(0)},
					{path: "$.skippedCount", value: float64(1)},
					{path: "$.failedCount", value: float64(0)},
					{path: "$.results[0].status", value: "skipped"},
					{path: "$.results[0].reason", value: "unavailable"},
				},
			},
		},
		{
			name: "export oauth auth failure before cart calls returns provider unavailable",
			setup: func(env testEnv) {
				createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-auth", "Milk", "item-auth")
				insertManualShoppingItem(env.T, env.DB, "sli-auth", "user-1", "item-auth", 1)
				if err := env.ProductStore.UpsertBarcodeMapping(context.Background(), "888", "prod-auth", "global", ""); err != nil {
					env.T.Fatalf("barcode: %v", err)
				}
				script := carttest.NewScript().
					WithTokenExchangeError(errors.New("refresh denied")).
					WithIdentityLookup("888", cart.ProductIdentity("SKU-AUTH"))
				provider := carttest.NewFake(cart.Capabilities{
					Auth:         cart.AuthOAuth2,
					Delivery:     cart.DeliveryServerPush,
					Confirmation: cart.ConfirmPerRequest,
					Mutation:     cart.MutateAddOnly,
					Identity:     cart.IdentityDerived,
				}, script)
				if err := env.Registry.Register(provider, cart.WithCredentialsConfigured(true)); err != nil {
					env.T.Fatalf("register: %v", err)
				}
				expired := time.Now().UTC().Add(-time.Hour)
				if err := connection.NewDirectory(env.DB).Write(context.Background(), primaryScope(env.T, env.DB), &connection.Connection{
					Provider:     "test-oauth2_authorization_code-server_push",
					State:        connection.StateConnected,
					AccessToken:  "stale-access",
					RefreshToken: "refresh-me",
					ExpiresAt:    &expired,
				}); err != nil {
					env.T.Fatalf("connection: %v", err)
				}
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/shopping-list/export",
				body:           `{"provider":"test-oauth2_authorization_code-server_push"}`,
				expectedStatus: http.StatusBadGateway,
				assertions: []assertion{
					{path: "$.code", value: "provider_unavailable"},
				},
				bodyExcludes: []string{"refresh-me", "stale-access", "refresh denied"},
			},
		},
	}

	runHandlerTests(t, tests)
}

func TestShoppingListExportResultEntryIDsUnique(t *testing.T) {
	tests := []handlerTestCase{
		{
			name: "export result entry ids match shopping lines",
			setup: func(env testEnv) {
				createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-a", "A", "item-a")
				createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-b", "B", "item-b")
				insertManualShoppingItem(env.T, env.DB, "sli-a", "user-1", "item-a", 1)
				insertManualShoppingItem(env.T, env.DB, "sli-b", "user-1", "item-b", 1)
				script := carttest.NewScript().
					WithDispositions(cart.DispositionAccepted).
					WithIdentityLookup("001", cart.ProductIdentity("SKU-A")).
					WithIdentityLookup("002", cart.ProductIdentity("SKU-B"))
				if err := env.ProductStore.UpsertBarcodeMapping(context.Background(), "001", "prod-a", "global", ""); err != nil {
					env.T.Fatal(err)
				}
				if err := env.ProductStore.UpsertBarcodeMapping(context.Background(), "002", "prod-b", "global", ""); err != nil {
					env.T.Fatal(err)
				}
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
				method:         "POST",
				path:           "/api/shopping-list/export",
				body:           `{"provider":"test-none-server_push"}`,
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.addedCount", value: float64(2)},
				},
				bodyContains: []string{`"entryId":"sli-a"`, `"entryId":"sli-b"`},
			},
		},
	}
	runHandlerTests(t, tests)
}
