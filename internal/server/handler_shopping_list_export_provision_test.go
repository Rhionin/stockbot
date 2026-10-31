package server

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/cart"
	"github.com/Rhionin/stockbot/internal/cart/carttest"
	"github.com/Rhionin/stockbot/internal/cart/connection"
	"github.com/Rhionin/stockbot/internal/cart/kroger"
)

var exportProvisionFake carttest.Calls

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
					{path: "$.sentCount", value: float64(1)},
					{path: "$.addedCount", value: float64(0)},
					{path: "$.skippedCount", value: float64(2)},
					{path: "$.failedCount", value: float64(1)},
					{path: "$.unknownCount", value: float64(0)},
				},
				bodyContains: []string{
					`"status":"sent"`,
					`"reason":"no_upc"`,
					`"reason":"no_match"`,
					`"reason":"batch_rejected"`,
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
			name: "export oauth refresh revoked returns reconnect required envelope",
			setup: func(env testEnv) {
				createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-auth", "Milk", "item-auth")
				insertManualShoppingItem(env.T, env.DB, "sli-auth", "user-1", "item-auth", 1)
				if err := env.ProductStore.UpsertBarcodeMapping(context.Background(), "888", "prod-auth", "global", ""); err != nil {
					env.T.Fatalf("barcode: %v", err)
				}
				script := carttest.NewScript().
					WithTokenExchangeError(&cart.OAuthTokenError{StatusCode: 400, OAuthError: "invalid_grant"}).
					WithIdentityLookup("888", cart.ProductIdentity("SKU-AUTH"))
				provider := carttest.NewFake(cart.Capabilities{
					Auth:         cart.AuthOAuth2,
					Delivery:     cart.DeliveryServerPush,
					Confirmation: cart.ConfirmPerRequest,
					Mutation:     cart.MutateAddOnly,
					Identity:     cart.IdentityDerived,
				}, script)
				exportProvisionFake = provider.(carttest.Calls)
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
				expectedStatus: http.StatusConflict,
				assertions: []assertion{
					{path: "$.code", value: "reconnect_required"},
					{path: "$.error", value: "Couldn't connect to your Test Provider account. Nothing was sent."},
					{path: "$.results", absent: true},
				},
				bodyExcludes: []string{"refresh-me", "stale-access", "invalid_grant"},
			},
			afterRequest: func(env testEnv) {
				for _, call := range exportProvisionFake.Calls() {
					if call == "Add" {
						env.T.Fatal("Add was called before reconnect_required failure")
					}
				}
				conn, err := connection.NewDirectory(env.DB).Read(context.Background(), primaryScope(env.T, env.DB), "test-oauth2_authorization_code-server_push")
				if err != nil {
					env.T.Fatal(err)
				}
				if conn.State != connection.StateReauthRequired {
					env.T.Fatalf("connection state = %q, want reauth_required", conn.State)
				}
			},
		},
		{
			name: "second export fails fast after reauth required without token call",
			setup: func(env testEnv) {
				createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-auth3", "Milk", "item-auth3")
				insertManualShoppingItem(env.T, env.DB, "sli-auth3", "user-1", "item-auth3", 1)
				if err := env.ProductStore.UpsertBarcodeMapping(context.Background(), "890", "prod-auth3", "global", ""); err != nil {
					env.T.Fatalf("barcode: %v", err)
				}
				script := carttest.NewScript().
					WithTokenExchangeError(&cart.OAuthTokenError{StatusCode: 400, OAuthError: "invalid_grant"}).
					WithIdentityLookup("890", cart.ProductIdentity("SKU-AUTH3"))
				provider := carttest.NewFake(cart.Capabilities{
					Auth:         cart.AuthOAuth2,
					Delivery:     cart.DeliveryServerPush,
					Confirmation: cart.ConfirmPerRequest,
					Mutation:     cart.MutateAddOnly,
					Identity:     cart.IdentityDerived,
				}, script)
				exportProvisionFake = provider.(carttest.Calls)
				if err := env.Registry.Register(provider, cart.WithCredentialsConfigured(true)); err != nil {
					env.T.Fatalf("register: %v", err)
				}
				expired := time.Now().UTC().Add(-time.Hour)
				if err := connection.NewDirectory(env.DB).Write(context.Background(), primaryScope(env.T, env.DB), &connection.Connection{
					Provider:     "test-oauth2_authorization_code-server_push",
					State:        connection.StateConnected,
					AccessToken:  "stale-access-3",
					RefreshToken: "refresh-me-3",
					ExpiresAt:    &expired,
				}); err != nil {
					env.T.Fatalf("connection: %v", err)
				}
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/shopping-list/export",
				body:           `{"provider":"test-oauth2_authorization_code-server_push"}`,
				expectedStatus: http.StatusConflict,
				assertions: []assertion{
					{path: "$.code", value: "reconnect_required"},
				},
			},
			afterRequest: func(env testEnv) {
				refreshes := 0
				for _, call := range exportProvisionFake.Calls() {
					if call == "RefreshAccessToken" {
						refreshes++
					}
				}
				if refreshes != 1 {
					env.T.Fatalf("after first export calls = %v, want single RefreshAccessToken", exportProvisionFake.Calls())
				}
				handler := handlerFromEnv(env)
				executeExchange(env.T, handler, httpExchange{
					method:         "POST",
					path:           "/api/shopping-list/export",
					body:           `{"provider":"test-oauth2_authorization_code-server_push"}`,
					expectedStatus: http.StatusConflict,
					assertions: []assertion{
						{path: "$.code", value: "reconnect_required"},
					},
				})
				if refreshes != 1 {
					env.T.Fatalf("second export refresh calls = %d, want 1", refreshes)
				}
			},
		},
		{
			name: "export oauth refresh transport failure returns provider unavailable envelope",
			setup: func(env testEnv) {
				createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-auth2", "Bread", "item-auth2")
				insertManualShoppingItem(env.T, env.DB, "sli-auth2", "user-1", "item-auth2", 1)
				if err := env.ProductStore.UpsertBarcodeMapping(context.Background(), "889", "prod-auth2", "global", ""); err != nil {
					env.T.Fatalf("barcode: %v", err)
				}
				script := carttest.NewScript().
					WithTokenExchangeError(&cart.OAuthTokenError{Err: errors.New("connection reset")}).
					WithIdentityLookup("889", cart.ProductIdentity("SKU-AUTH2"))
				provider := carttest.NewFake(cart.Capabilities{
					Auth:         cart.AuthOAuth2,
					Delivery:     cart.DeliveryServerPush,
					Confirmation: cart.ConfirmPerRequest,
					Mutation:     cart.MutateAddOnly,
					Identity:     cart.IdentityDerived,
				}, script)
				exportProvisionFake = provider.(carttest.Calls)
				if err := env.Registry.Register(provider, cart.WithCredentialsConfigured(true)); err != nil {
					env.T.Fatalf("register: %v", err)
				}
				expired := time.Now().UTC().Add(-time.Hour)
				if err := connection.NewDirectory(env.DB).Write(context.Background(), primaryScope(env.T, env.DB), &connection.Connection{
					Provider:     "test-oauth2_authorization_code-server_push",
					State:        connection.StateConnected,
					AccessToken:  "stale-access-2",
					RefreshToken: "refresh-me-2",
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
					{path: "$.error", value: "Test Provider isn't responding. Try again later."},
					{path: "$.results", absent: true},
				},
				bodyExcludes: []string{"refresh-me-2", "stale-access-2", "connection reset"},
			},
			afterRequest: func(env testEnv) {
				for _, call := range exportProvisionFake.Calls() {
					if call == "Add" {
						env.T.Fatal("Add was called on provider_unavailable pre-send failure")
					}
				}
			},
		},
		{
			name: "export oauth refresh 503 returns provider unavailable envelope",
			setup: func(env testEnv) {
				createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-auth4", "Butter", "item-auth4")
				insertManualShoppingItem(env.T, env.DB, "sli-auth4", "user-1", "item-auth4", 1)
				if err := env.ProductStore.UpsertBarcodeMapping(context.Background(), "891", "prod-auth4", "global", ""); err != nil {
					env.T.Fatalf("barcode: %v", err)
				}
				script := carttest.NewScript().
					WithTokenExchangeError(&cart.OAuthTokenError{StatusCode: 503, OAuthError: "server_error"}).
					WithIdentityLookup("891", cart.ProductIdentity("SKU-AUTH4"))
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
					AccessToken:  "stale-access-4",
					RefreshToken: "refresh-me-4",
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
			},
		},
		{
			name: "export cart put 5xx leaves unknown request incomplete not whole request error",
			setup: func(env testEnv) {
				createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-503", "Soup", "item-503")
				insertManualShoppingItem(env.T, env.DB, "sli-503", "user-1", "item-503", 1)
				if err := env.ProductStore.UpsertBarcodeMapping(context.Background(), "503", "prod-503", "global", ""); err != nil {
					env.T.Fatalf("barcode: %v", err)
				}
				script := carttest.NewScript().
					WithTokenExchangeSuccess().
					WithIdentityLookup("503", cart.ProductIdentity("SKU-503")).
					WithAddOutcomes(carttest.AddOutcome{
						Result: cart.ProvisionResult{Disposition: cart.DispositionIndeterminate, Status: 503},
						Err:    errors.New("upstream timeout"),
					})
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
					AccessToken:  "access-503",
					RefreshToken: "refresh-503",
					ExpiresAt:    &expired,
				}); err != nil {
					env.T.Fatalf("connection: %v", err)
				}
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/shopping-list/export",
				body:           `{"provider":"test-oauth2_authorization_code-server_push"}`,
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.exported", value: float64(0)},
					{path: "$.unknownCount", value: float64(1)},
					{path: "$.results[0].reason", value: "request_incomplete"},
					{path: "$.reconnectRequired", absent: true},
				},
				bodyExcludes: []string{"upstream timeout"},
			},
		},
		{
			name: "export cart put 401 before any accept returns reconnect required envelope",
			setup: func(env testEnv) {
				createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-401", "Juice", "item-401")
				insertManualShoppingItem(env.T, env.DB, "sli-401", "user-1", "item-401", 1)
				if err := env.ProductStore.UpsertBarcodeMapping(context.Background(), "401", "prod-401", "global", ""); err != nil {
					env.T.Fatalf("barcode: %v", err)
				}
				script := carttest.NewScript().
					WithTokenExchangeSuccess().
					WithIdentityLookup("401", cart.ProductIdentity("SKU-401")).
					WithAddOutcomes(carttest.AddOutcome{
						Result: cart.ProvisionResult{Disposition: cart.DispositionRejected, Status: http.StatusUnauthorized},
						Err:    errors.New("cart api returned status 401"),
					})
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
					AccessToken:  "access-401",
					RefreshToken: "refresh-401",
					ExpiresAt:    &expired,
				}); err != nil {
					env.T.Fatalf("connection: %v", err)
				}
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/shopping-list/export",
				body:           `{"provider":"test-oauth2_authorization_code-server_push"}`,
				expectedStatus: http.StatusConflict,
				assertions: []assertion{
					{path: "$.code", value: "reconnect_required"},
					{path: "$.results", absent: true},
				},
			},
		},
		{
			name: "export 401 on second put marks later lines failed not unknown",
			setup: func(env testEnv) {
				seedThreeBatchExportItems(env, "401b", "p01b", "p02b", "p03b")
				script := carttest.NewScript().
					WithTokenExchangeSuccess().
					WithIdentityLookup("p01b", cart.ProductIdentity("SKU-P1B")).
					WithIdentityLookup("p02b", cart.ProductIdentity("SKU-P2B")).
					WithIdentityLookup("p03b", cart.ProductIdentity("SKU-P3B")).
					WithAddOutcomes(
						carttest.AddOutcome{Result: cart.ProvisionResult{Disposition: cart.DispositionAccepted, Status: 204}},
						carttest.AddOutcome{
							Result: cart.ProvisionResult{Disposition: cart.DispositionRejected, Status: http.StatusUnauthorized},
							Err:    errors.New("cart api returned status 401"),
						},
					)
				provider := registerOAuthExportFake(env, script, 1)
				exportProvisionFake = provider.(carttest.Calls)
				writeExpiredOAuthConnection(env, "access-401b", "refresh-401b")
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/shopping-list/export",
				body:           `{"provider":"test-oauth2_authorization_code-server_push"}`,
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.exported", value: float64(1)},
					{path: "$.sentCount", value: float64(1)},
					{path: "$.failedCount", value: float64(2)},
					{path: "$.unknownCount", value: float64(0)},
					{path: "$.reconnectRequired", value: true},
					{path: "$.results[0].status", value: "sent"},
					{path: "$.results[1].status", value: "failed"},
					{path: "$.results[1].reason", value: "reconnect_required"},
					{path: "$.results[2].status", value: "failed"},
					{path: "$.results[2].reason", value: "reconnect_required"},
				},
			},
			afterRequest: func(env testEnv) {
				refreshes, adds := 0, 0
				for _, call := range exportProvisionFake.Calls() {
					switch call {
					case "RefreshAccessToken":
						refreshes++
					case "Add":
						adds++
					}
				}
				if refreshes != 1 || adds != 2 {
					env.T.Fatalf("calls = %v, want one RefreshAccessToken and two Add", exportProvisionFake.Calls())
				}
				conn, err := connection.NewDirectory(env.DB).Read(context.Background(), primaryScope(env.T, env.DB), "test-oauth2_authorization_code-server_push")
				if err != nil {
					env.T.Fatal(err)
				}
				if conn.State != connection.StateReauthRequired {
					env.T.Fatalf("connection state = %q, want reauth_required", conn.State)
				}
			},
		},
		{
			name: "export timeout on second put leaves one unknown and later lines failed",
			setup: func(env testEnv) {
				seedThreeBatchExportItems(env, "to", "p01t", "p02t", "p03t")
				script := carttest.NewScript().
					WithTokenExchangeSuccess().
					WithIdentityLookup("p01t", cart.ProductIdentity("SKU-P1T")).
					WithIdentityLookup("p02t", cart.ProductIdentity("SKU-P2T")).
					WithIdentityLookup("p03t", cart.ProductIdentity("SKU-P3T")).
					WithAddOutcomes(
						carttest.AddOutcome{Result: cart.ProvisionResult{Disposition: cart.DispositionAccepted, Status: 204}},
						carttest.AddOutcome{
							Result: cart.ProvisionResult{Disposition: cart.DispositionIndeterminate, Status: 0},
							Err:    errors.New("upstream timeout"),
						},
					)
				provider := registerOAuthExportFake(env, script, 1)
				exportProvisionFake = provider.(carttest.Calls)
				writeExpiredOAuthConnection(env, "access-to", "refresh-to")
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/shopping-list/export",
				body:           `{"provider":"test-oauth2_authorization_code-server_push"}`,
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.exported", value: float64(1)},
					{path: "$.sentCount", value: float64(1)},
					{path: "$.unknownCount", value: float64(1)},
					{path: "$.failedCount", value: float64(1)},
					{path: "$.reconnectRequired", absent: true},
					{path: "$.results[1].status", value: "unknown"},
					{path: "$.results[1].reason", value: "request_incomplete"},
					{path: "$.results[2].status", value: "failed"},
					{path: "$.results[2].reason", value: "request_incomplete"},
				},
				bodyExcludes: []string{"upstream timeout"},
			},
			afterRequest: func(env testEnv) {
				adds := 0
				for _, call := range exportProvisionFake.Calls() {
					if call == "Add" {
						adds++
					}
				}
				if adds != 2 {
					env.T.Fatalf("Add calls = %d, want 2 (no put after timeout batch)", adds)
				}
			},
		},
	}

	runHandlerTests(t, tests)
}

func seedThreeBatchExportItems(env testEnv, prefix, bc1, bc2, bc3 string) {
	env.T.Helper()
	createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-"+prefix+"1", "One", "item-"+prefix+"1")
	createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-"+prefix+"2", "Two", "item-"+prefix+"2")
	createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-"+prefix+"3", "Three", "item-"+prefix+"3")
	insertManualShoppingItem(env.T, env.DB, "sli-"+prefix+"1", "user-1", "item-"+prefix+"1", 1)
	insertManualShoppingItem(env.T, env.DB, "sli-"+prefix+"2", "user-1", "item-"+prefix+"2", 1)
	insertManualShoppingItem(env.T, env.DB, "sli-"+prefix+"3", "user-1", "item-"+prefix+"3", 1)
	ctx := context.Background()
	for _, pair := range []struct{ bc, prod string }{
		{bc1, "prod-" + prefix + "1"},
		{bc2, "prod-" + prefix + "2"},
		{bc3, "prod-" + prefix + "3"},
	} {
		if err := env.ProductStore.UpsertBarcodeMapping(ctx, pair.bc, pair.prod, "global", ""); err != nil {
			env.T.Fatal(err)
		}
	}
}

func registerOAuthExportFake(env testEnv, script *carttest.Script, batchSize int) cart.Provider {
	env.T.Helper()
	provider := carttest.NewFake(cart.Capabilities{
		Auth:         cart.AuthOAuth2,
		Delivery:     cart.DeliveryServerPush,
		Confirmation: cart.ConfirmPerRequest,
		Mutation:     cart.MutateAddOnly,
		Identity:     cart.IdentityDerived,
	}, script)
	opts := []cart.RegisterOption{cart.WithCredentialsConfigured(true)}
	if batchSize > 0 {
		opts = append(opts, cart.WithBatchSize(batchSize))
	}
	if err := env.Registry.Register(provider, opts...); err != nil {
		env.T.Fatalf("register: %v", err)
	}
	return provider
}

func writeExpiredOAuthConnection(env testEnv, accessToken, refreshToken string) {
	env.T.Helper()
	expired := time.Now().UTC().Add(-time.Hour)
	if err := connection.NewDirectory(env.DB).Write(context.Background(), primaryScope(env.T, env.DB), &connection.Connection{
		Provider:     "test-oauth2_authorization_code-server_push",
		State:        connection.StateConnected,
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ExpiresAt:    &expired,
	}); err != nil {
		env.T.Fatalf("connection: %v", err)
	}
}

func TestShoppingListExportKrogerInvalidGrantFromTokenEndpoint(t *testing.T) {
	tests := []handlerTestCase{{
		name: "kroger adapter maps invalid_grant to reconnect_required export envelope",
		setup: func(env testEnv) {
			createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-kroger", "Kroger milk", "item-kroger")
			insertManualShoppingItem(env.T, env.DB, "sli-kroger", "user-1", "item-kroger", 1)
			if err := env.ProductStore.UpsertBarcodeMapping(context.Background(), "011110728227", "prod-kroger", "global", ""); err != nil {
				env.T.Fatal(err)
			}
			adapter, err := kroger.New("client", "secret", "https://app/cb", "PICKUP")
			if err != nil {
				env.T.Fatal(err)
			}
			if id, ok := adapter.DeriveIdentity("011110728227"); !ok || string(id) != "0001111072822" {
				env.T.Fatalf("DeriveIdentity(011110728227) = %q, %v", id, ok)
			}
			adapter.WithTransport(roundTripFunc(func(req *http.Request) (*http.Response, error) {
				if strings.Contains(req.URL.Path, "/oauth2/token") {
					return provisionTestJSONResponse(http.StatusBadRequest, `{"error":"invalid_grant","error_description":"secret refresh_token=never-leak"}`), nil
				}
				env.T.Fatalf("unexpected HTTP request during pre-send failure: %s", req.URL)
				return nil, nil
			}))
			if err := env.Registry.Register(adapter, cart.WithCredentialsConfigured(true)); err != nil {
				env.T.Fatal(err)
			}
			expired := time.Now().UTC().Add(-time.Hour)
			if err := connection.NewDirectory(env.DB).Write(context.Background(), primaryScope(env.T, env.DB), &connection.Connection{
				Provider:     "kroger",
				State:        connection.StateConnected,
				AccessToken:  "stale-kroger",
				RefreshToken: "refresh-kroger",
				ExpiresAt:    &expired,
			}); err != nil {
				env.T.Fatal(err)
			}
		},
		httpExchange: httpExchange{
			method:         "POST",
			path:           "/api/shopping-list/export",
			body:           `{"provider":"kroger"}`,
			expectedStatus: http.StatusConflict,
			assertions: []assertion{
				{path: "$.code", value: "reconnect_required"},
				{path: "$.error", value: "Couldn't connect to your Kroger account. Nothing was sent."},
				{path: "$.results", absent: true},
			},
			bodyExcludes: []string{"invalid_grant", "refresh_token", "never-leak"},
		},
	}}
	runHandlerTests(t, tests)
}

func provisionTestJSONResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
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
					{path: "$.sentCount", value: float64(2)},
					{path: "$.addedCount", value: float64(0)},
				},
				bodyContains: []string{`"entryId":"sli-a"`, `"entryId":"sli-b"`},
			},
		},
	}
	runHandlerTests(t, tests)
}
