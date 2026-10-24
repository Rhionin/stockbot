package server

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/cart"
	"github.com/Rhionin/stockbot/internal/cart/carttest"
	"github.com/Rhionin/stockbot/internal/cart/connection"
	"github.com/Rhionin/stockbot/internal/cart/kroger"
)

func TestCartHTTP(t *testing.T) {
	tests := []handlerTestCase{
		{
			name: "providers list is empty when nothing is registered",
			httpExchange: httpExchange{
				method:         "GET",
				path:           "/api/providers",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$", value: []interface{}{}},
				},
			},
		},
		{
			name: "unconfigured kroger is listed as disconnected",
			setup: func(env testEnv) {
				if err := env.Registry.Register(kroger.NewUnconfigured()); err != nil {
					env.T.Fatalf("register: %v", err)
				}
			},
			httpExchange: httpExchange{
				method:         "GET",
				path:           "/api/providers",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$[0].id", value: "kroger"},
					{path: "$[0].displayName", value: "Kroger"},
					{path: "$[0].connectionState", value: "disconnected"},
					{path: "$[0].credentialsConfigured", value: false},
					{path: "$[0].capabilities.auth", value: "oauth2_authorization_code"},
					{path: "$[0].capabilities.confirmation", value: "per_request"},
				},
			},
		},
		{
			name: "authorize refuses an unconfigured provider",
			setup: func(env testEnv) {
				if err := env.Registry.Register(kroger.NewUnconfigured()); err != nil {
					env.T.Fatalf("register: %v", err)
				}
			},
			httpExchange: httpExchange{
				method:         "GET",
				path:           "/api/providers/kroger/authorize",
				expectedStatus: http.StatusConflict,
				bodyContains:   []string{"not configured"},
			},
		},
		{
			name: "authorize returns the provider authorization URL",
			setup: func(env testEnv) {
				adapter, err := kroger.New("my-client", "secret", "https://app.example/cb", "PICKUP")
				if err != nil {
					env.T.Fatalf("kroger.New: %v", err)
				}
				if err := env.Registry.Register(adapter, cart.WithCredentialsConfigured(true)); err != nil {
					env.T.Fatalf("register: %v", err)
				}
			},
			httpExchange: httpExchange{
				method:         "GET",
				path:           "/api/providers/kroger/authorize",
				expectedStatus: http.StatusOK,
				bodyContains: []string{
					"https://api.kroger.com/v1/connect/oauth2/authorize",
					"client_id=my-client",
					"response_type=code",
					"cart.basic:write",
				},
			},
		},
		{
			name: "export names an unknown provider as a bad request",
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/shopping-list/export",
				body:           `{"provider":"nope"}`,
				expectedStatus: http.StatusBadRequest,
				bodyContains:   []string{"not registered"},
			},
		},
		{
			name: "export names an unconfigured provider as a conflict",
			setup: func(env testEnv) {
				if err := env.Registry.Register(kroger.NewUnconfigured()); err != nil {
					env.T.Fatalf("register: %v", err)
				}
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/shopping-list/export",
				body:           `{"provider":"kroger"}`,
				expectedStatus: http.StatusConflict,
				bodyContains:   []string{"not configured"},
			},
		},
		{
			name: "replenishment mode rejects an unknown value",
			setup: func(env testEnv) {
				createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-mode", "Oats", "item-mode")
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/items/item-mode/replenishment-mode",
				body:           `{"mode":"weekly"}`,
				expectedStatus: http.StatusUnprocessableEntity,
			},
		},
		{
			name: "replenishment mode records replenish",
			setup: func(env testEnv) {
				createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-mode-ok", "Rice", "item-mode-ok")
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/items/item-mode-ok/replenishment-mode",
				body:           `{"mode":"replenish"}`,
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.mode", value: "replenish"},
				},
			},
		},
		{
			name: "adjustment rejects a quantity above 999",
			setup: func(env testEnv) {
				createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-adj", "Beans", "item-adj")
				insertManualShoppingItem(env.T, env.DB, "sli-adj", "user-1", "item-adj", 2)
			},
			httpExchange: httpExchange{
				method:         "PUT",
				path:           "/api/shopping-list/items/sli-adj/adjustment",
				query:          map[string]string{"provider": "kroger"},
				body:           `{"adjustment":1000}`,
				expectedStatus: http.StatusUnprocessableEntity,
			},
		},
		{
			name: "adjustment is the provision quantity on the next list read",
			setup: func(env testEnv) {
				createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-adj-ok", "Beans", "item-adj-ok")
				insertManualShoppingItem(env.T, env.DB, "sli-adj-ok", "user-1", "item-adj-ok", 2)
			},
			httpExchange: httpExchange{
				method:         "PUT",
				path:           "/api/shopping-list/items/sli-adj-ok/adjustment",
				query:          map[string]string{"provider": "kroger"},
				body:           `{"adjustment":4}`,
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.adjustment", value: float64(4)},
				},
			},
			afterRequest: exchanges(httpExchange{
				method:         "GET",
				path:           "/api/shopping-list",
				query:          map[string]string{"provider": "kroger"},
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$[0].itemId", value: "item-adj-ok"},
					{path: "$[0].quantity", value: float64(4)},
					{path: "$[0].computedQuantity", value: float64(2)},
					{path: "$[0].adjustment", value: float64(4)},
					{path: "$[0].provider", value: "kroger"},
				},
			}),
		},
		{
			name: "disconnect clears the connection and leaves the provider listed",
			setup: func(env testEnv) {
				adapter, err := kroger.New("my-client", "secret", "https://app.example/cb", "PICKUP")
				if err != nil {
					env.T.Fatalf("kroger.New: %v", err)
				}
				if err := env.Registry.Register(adapter, cart.WithCredentialsConfigured(true)); err != nil {
					env.T.Fatalf("register: %v", err)
				}
				if err := connection.NewDirectory(env.DB).Write(context.Background(), primaryScope(env.T, env.DB), &connection.Connection{
					Provider:    "kroger",
					State:       connection.StateConnected,
					AccessToken: "should-not-leak",
				}); err != nil {
					env.T.Fatalf("connect: %v", err)
				}
			},
			httpExchange: httpExchange{
				method:         "DELETE",
				path:           "/api/providers/kroger/connection",
				expectedStatus: http.StatusCreated,
			},
			afterRequest: exchanges(httpExchange{
				method:         "GET",
				path:           "/api/providers",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$[0].connectionState", value: "disconnected"},
					{path: "$[0].credentialsConfigured", value: true},
				},
				bodyContains: []string{"disconnected"},
			}),
		},
		{
			name: "provision accepts a line and advances the ledger",
			setup: func(env testEnv) {
				createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-prov", "Milk", "item-prov")
				insertManualShoppingItem(env.T, env.DB, "sli-prov", "user-1", "item-prov", 2)
				if err := env.ProductStore.UpsertBarcodeMapping(context.Background(), "111", "prod-prov", "global", ""); err != nil {
					env.T.Fatalf("barcode: %v", err)
				}
				script := carttest.NewScript().
					WithDispositions(cart.DispositionAccepted).
					WithIdentityLookup("111", cart.ProductIdentity("0001111111111"))
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
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/shopping-list/export",
				body:           `{"provider":"test-none-server_push"}`,
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.exported", value: float64(1)},
					{path: "$.provider", value: "test-none-server_push"},
					{path: "$.entries[0].outcome", value: "confirmed"},
					{path: "$.entries[0].name", value: "Milk"},
				},
			},
			afterRequest: exchanges(httpExchange{
				method:         "GET",
				path:           "/api/providers/test-none-server_push/ledger",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.entries[0].itemId", value: "item-prov"},
					{path: "$.entries[0].requested", value: float64(2)},
				},
			}),
		},
		{
			name: "configured provider exports one line per pooled store-brand need",
			setup: func(env testEnv) {
				createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-pool-gv", "Great Value Cut Green Beans", "item-pool-gv")
				createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-pool-kr", "Kroger Cut Green Beans", "item-pool-kr")
				createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-pool-corn", "Kroger Whole Kernel Corn", "item-pool-corn")
				beginUsing(env.T, env.DB, time.Now())
				setSupplyQuantity(env.T, env.DB, "prod-pool-gv", 4)
				setSupplyQuantity(env.T, env.DB, "prod-pool-corn", 2)
				ctx := context.Background()
				if err := env.ProductStore.UpsertBarcodeMapping(ctx, "000111111117", "prod-pool-gv", "global", ""); err != nil {
					env.T.Fatalf("barcode beans: %v", err)
				}
				if err := env.ProductStore.UpsertBarcodeMapping(ctx, "000222222224", "prod-pool-corn", "global", ""); err != nil {
					env.T.Fatalf("barcode corn: %v", err)
				}
				script := carttest.NewScript().
					WithDispositions(cart.DispositionAccepted).
					WithIdentityLookup("000111111117", cart.ProductIdentity("beans")).
					WithIdentityLookup("000222222224", cart.ProductIdentity("corn"))
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
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/shopping-list/fill",
				expectedStatus: http.StatusOK,
			},
			afterRequest: exchanges(httpExchange{
				method:         "POST",
				path:           "/api/shopping-list/export",
				body:           `{"provider":"test-none-server_push"}`,
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.exported", value: float64(2)},
				},
			}),
		},
		{
			name: "saving credentials does not echo the client secret",
			setup: func(env testEnv) {
				if err := env.Registry.Register(kroger.NewUnconfigured()); err != nil {
					env.T.Fatalf("register: %v", err)
				}
			},
			httpExchange: httpExchange{
				method:         "PUT",
				path:           "/api/providers/kroger/credentials",
				body:           `{"clientId":"ui-client","clientSecret":"super-secret-value","redirectUri":"https://pantry.example/api/providers/kroger/callback","modality":"PICKUP"}`,
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.clientId", value: "ui-client"},
					{path: "$.secretSet", value: true},
					{path: "$.source", value: "saved"},
					{path: "$.clientSecret", absent: true},
				},
				bodyExcludes: []string{"super-secret-value"},
			},
			afterRequest: exchanges(
				httpExchange{
					method:         "GET",
					path:           "/api/providers",
					expectedStatus: http.StatusOK,
					assertions: []assertion{
						{path: "$[0].credentialsConfigured", value: true},
						{path: "$[0].credentials.clientId", value: "ui-client"},
						{path: "$[0].credentials.secretSet", value: true},
						{path: "$[0].credentials.clientSecret", absent: true},
					},
					bodyExcludes: []string{"super-secret-value"},
				},
				httpExchange{
					method:         "PUT",
					path:           "/api/providers/kroger/credentials",
					body:           `{"clientId":"ui-client","clientSecret":"","redirectUri":"https://pantry.example/cb2","modality":"DELIVERY"}`,
					expectedStatus: http.StatusOK,
					assertions: []assertion{
						{path: "$.redirectUri", value: "https://pantry.example/cb2"},
						{path: "$.modality", value: "DELIVERY"},
						{path: "$.secretSet", value: true},
						{path: "$.clientSecret", absent: true},
					},
					bodyExcludes: []string{"super-secret-value"},
				},
				httpExchange{
					method:         "GET",
					path:           "/api/providers/kroger/authorize",
					expectedStatus: http.StatusOK,
					bodyContains:   []string{"client_id=ui-client"},
					bodyExcludes:   []string{"super-secret-value"},
				},
				httpExchange{
					method:         "DELETE",
					path:           "/api/providers/kroger/credentials",
					expectedStatus: http.StatusOK,
					assertions: []assertion{
						{path: "$.source", value: "none"},
						{path: "$.secretSet", value: false},
						{path: "$.clientSecret", absent: true},
					},
					bodyExcludes: []string{"super-secret-value"},
				},
			),
		},
		{
			name: "export refuses a brand that is not the line",
			setup: func(env testEnv) {
				createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-swap-gv", "Great Value Cut Green Beans", "item-swap-gv")
				createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-swap-kr", "Kroger Cut Green Beans", "item-swap-kr")
				beginUsing(env.T, env.DB, time.Now())
				setSupplyQuantity(env.T, env.DB, "prod-swap-gv", 4)
				if err := env.ProductStore.UpsertBarcodeMapping(context.Background(), "000333333331", "prod-swap-kr", "global", ""); err != nil {
					env.T.Fatalf("barcode: %v", err)
				}
				script := carttest.NewScript().
					WithDispositions(cart.DispositionAccepted).
					WithIdentityLookup("000333333331", cart.ProductIdentity("sale-beans"))
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
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/shopping-list/fill",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$[0].itemId", value: "item-swap-gv"},
					{path: "$[0].quantity", value: float64(3)},
				},
			},
			afterRequest: exchanges(
				httpExchange{
					method:         "POST",
					path:           "/api/shopping-list/export",
					body:           `{"provider":"test-none-server_push","useItemIds":{"item-swap-gv":"item-swap-kr"}}`,
					expectedStatus: http.StatusUnprocessableEntity,
					assertions: []assertion{
						{path: "$.error", value: "Choose a product on this line."},
					},
				},
			),
		},
	}

	runHandlerTests(t, tests)
}
