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
					{path: "$.addedCount", value: float64(0)},
					{path: "$.sentCount", value: float64(1)},
					{path: "$.entries[0].outcome", value: "confirmed"},
					{path: "$.results[0].status", value: "sent"},
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
		{
			name: "export reports an unknown line when the retailer does not answer",
			setup: func(env testEnv) {
				createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-unk", "Milk", "item-unk")
				insertManualShoppingItem(env.T, env.DB, "sli-unk", "user-1", "item-unk", 1)
				if err := env.ProductStore.UpsertBarcodeMapping(context.Background(), "555", "prod-unk", "global", ""); err != nil {
					env.T.Fatalf("barcode: %v", err)
				}
				script := carttest.NewScript().
					WithDispositions(cart.DispositionIndeterminate).
					WithIdentityLookup("555", cart.ProductIdentity("milk"))
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
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.provider", value: "test-none-server_push"},
					{path: "$.exported", value: float64(0)},
					{path: "$.unknownItems[0]", value: "Milk"},
					{path: "$.entries[0].outcome", value: "unknown"},
					{path: "$.results[0].status", value: "unknown"},
					{path: "$.results[0].reason", value: "request_incomplete"},
				},
			},
		},
		{
			name: "reauth callback clears flag and export succeeds",
			setup: func(env testEnv) {
				createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-recon", "Reconnect", "item-recon")
				insertManualShoppingItem(env.T, env.DB, "sli-recon", "user-1", "item-recon", 1)
				if err := env.ProductStore.UpsertBarcodeMapping(context.Background(), "recon", "prod-recon", "global", ""); err != nil {
					env.T.Fatal(err)
				}
				script := carttest.NewScript().
					WithTokenExchangeSuccess().
					WithIdentityLookup("recon", cart.ProductIdentity("SKU-RC")).
					WithAddOutcomes(carttest.AddOutcome{Result: cart.ProvisionResult{Disposition: cart.DispositionAccepted, Status: 204}})
				provider := carttest.NewFake(cart.Capabilities{
					Auth:         cart.AuthOAuth2,
					Delivery:     cart.DeliveryServerPush,
					Confirmation: cart.ConfirmPerRequest,
					Mutation:     cart.MutateAddOnly,
					Identity:     cart.IdentityDerived,
				}, script)
				if err := env.Registry.Register(provider, cart.WithCredentialsConfigured(true)); err != nil {
					env.T.Fatal(err)
				}
				if err := connection.NewDirectory(env.DB).Write(context.Background(), primaryScope(env.T, env.DB), &connection.Connection{
					Provider:     "test-oauth2_authorization_code-server_push",
					State:        connection.StateReauthRequired,
					RefreshToken: "refresh-recon",
				}); err != nil {
					env.T.Fatal(err)
				}
				if err := connection.NewDirectory(env.DB).GenerateAuthState(context.Background(), primaryScope(env.T, env.DB), "test-oauth2_authorization_code-server_push", "cb-state-recon"); err != nil {
					env.T.Fatal(err)
				}
			},
			httpExchange: httpExchange{
				method:         "GET",
				path:           "/api/providers/test-oauth2_authorization_code-server_push/callback",
				query:          map[string]string{"code": "good-code", "state": "cb-state-recon"},
				expectedStatus: http.StatusSeeOther,
			},
			afterRequest: func(env testEnv) {
				handler := handlerFromEnv(env)
				executeExchange(env.T, handler, httpExchange{
					method:         "POST",
					path:           "/api/shopping-list/export",
					body:           `{"provider":"test-oauth2_authorization_code-server_push"}`,
					expectedStatus: http.StatusOK,
					assertions: []assertion{
						{path: "$.exported", value: float64(1)},
						{path: "$.sentCount", value: float64(1)},
					},
				})
				conn, err := connection.NewDirectory(env.DB).Read(context.Background(), primaryScope(env.T, env.DB), "test-oauth2_authorization_code-server_push")
				if err != nil {
					env.T.Fatal(err)
				}
				if conn.State != connection.StateConnected {
					env.T.Fatalf("connection state = %q, want connected after callback", conn.State)
				}
			},
		},
		{
			name: "export names a product it cannot identify",
			setup: func(env testEnv) {
				createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-noid", "Oats", "item-noid")
				insertManualShoppingItem(env.T, env.DB, "sli-noid", "user-1", "item-noid", 1)
				provider := carttest.NewFake(cart.Capabilities{
					Auth:         cart.AuthNone,
					Delivery:     cart.DeliveryServerPush,
					Confirmation: cart.ConfirmPerRequest,
					Mutation:     cart.MutateAddOnly,
					Identity:     cart.IdentityDerived,
				}, carttest.NewScript())
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
					{path: "$.failedItems[0]", value: "Oats"},
					{path: "$.entries[0].outcome", value: "failed"},
					{path: "$.entries[0].outcomeReason", value: "no_product_identity"},
					{path: "$.results[0].status", value: "skipped"},
					{path: "$.results[0].reason", value: "no_upc"},
				},
			},
		},
		{
			name: "export returns a handoff link",
			setup: func(env testEnv) {
				createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-hand", "Rice", "item-hand")
				insertManualShoppingItem(env.T, env.DB, "sli-hand", "user-1", "item-hand", 1)
				if err := env.ProductStore.UpsertBarcodeMapping(context.Background(), "444", "prod-hand", "global", ""); err != nil {
					env.T.Fatalf("barcode: %v", err)
				}
				script := carttest.NewScript().
					WithDispositions(cart.DispositionAccepted).
					WithIdentityLookup("444", cart.ProductIdentity("rice"))
				provider := carttest.NewFake(cart.Capabilities{
					Auth:         cart.AuthNone,
					Delivery:     cart.DeliveryClientHandoff,
					Confirmation: cart.ConfirmNone,
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
				body:           `{"provider":"test-none-client_handoff"}`,
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.handoff.url", value: "https://example.com/handoff"},
					{path: "$.handoff.expires", value: "30 minutes from now"},
				},
			},
		},
		{
			name: "export refuses a provider that is not connected",
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
				method:         "POST",
				path:           "/api/shopping-list/export",
				body:           `{"provider":"kroger"}`,
				expectedStatus: http.StatusConflict,
				assertions: []assertion{
					{path: "$.code", value: "reconnect_required"},
				},
				bodyContains: []string{"Nothing was sent"},
			},
		},
		{
			name: "export refuses an unknown provider and a provider with no credentials",
			setup: func(env testEnv) {
				push := carttest.NewFake(cart.Capabilities{
					Auth:         cart.AuthNone,
					Delivery:     cart.DeliveryServerPush,
					Confirmation: cart.ConfirmNone,
					Mutation:     cart.MutateAddOnly,
					Identity:     cart.IdentityDerived,
				}, carttest.NewScript())
				handoff := carttest.NewFake(cart.Capabilities{
					Auth:         cart.AuthNone,
					Delivery:     cart.DeliveryClientHandoff,
					Confirmation: cart.ConfirmNone,
					Mutation:     cart.MutateAddOnly,
					Identity:     cart.IdentityDerived,
				}, carttest.NewScript())
				if err := env.Registry.Register(push, cart.WithCredentialsConfigured(true)); err != nil {
					env.T.Fatal(err)
				}
				if err := env.Registry.Register(handoff, cart.WithCredentialsConfigured(true)); err != nil {
					env.T.Fatal(err)
				}
				if err := env.Registry.Register(kroger.NewUnconfigured()); err != nil {
					env.T.Fatal(err)
				}
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/shopping-list/export",
				body:           `{}`,
				expectedStatus: http.StatusBadRequest,
				bodyContains:   []string{"not registered"},
			},
			afterRequest: exchanges(
				httpExchange{
					method:         "POST",
					path:           "/api/shopping-list/export",
					body:           `{"provider":"nope"}`,
					expectedStatus: http.StatusBadRequest,
					bodyContains:   []string{"nope", "not registered"},
				},
				httpExchange{
					method:         "POST",
					path:           "/api/shopping-list/export",
					body:           `{"provider":"kroger"}`,
					expectedStatus: http.StatusConflict,
					bodyContains:   []string{"Kroger is not configured"},
				},
			),
		},
		{
			name: "credentials reject a blank id, a bad modality, and a bad redirect",
			setup: func(env testEnv) {
				if err := env.Registry.Register(kroger.NewUnconfigured()); err != nil {
					env.T.Fatalf("register: %v", err)
				}
			},
			httpExchange: httpExchange{
				method:         "PUT",
				path:           "/api/providers/kroger/credentials",
				body:           `{"clientId":"  ","clientSecret":"secret","redirectUri":"https://pantry.example/cb","modality":"PICKUP"}`,
				expectedStatus: http.StatusUnprocessableEntity,
				bodyContains:   []string{"Client ID is required."},
			},
			afterRequest: exchanges(
				httpExchange{
					method:         "PUT",
					path:           "/api/providers/kroger/credentials",
					body:           `{"clientId":"ui","clientSecret":"secret","redirectUri":"https://pantry.example/cb","modality":"CURBSIDE"}`,
					expectedStatus: http.StatusUnprocessableEntity,
					bodyContains:   []string{"PICKUP or DELIVERY"},
				},
				httpExchange{
					method:         "PUT",
					path:           "/api/providers/kroger/credentials",
					body:           `{"clientId":"ui","clientSecret":"secret","redirectUri":"not a url","modality":"PICKUP"}`,
					expectedStatus: http.StatusUnprocessableEntity,
					bodyContains:   []string{"http or https"},
				},
				httpExchange{
					method:         "PUT",
					path:           "/api/providers/kroger/credentials",
					body:           `{"clientId":"ui","clientSecret":"","redirectUri":"https://pantry.example/cb"}`,
					expectedStatus: http.StatusUnprocessableEntity,
					bodyContains:   []string{"Client secret is required."},
				},
				httpExchange{
					method:         "PUT",
					path:           "/api/providers/nope/credentials",
					body:           `{"clientId":"ui","clientSecret":"secret","redirectUri":"https://pantry.example/cb"}`,
					expectedStatus: http.StatusNotFound,
				},
				httpExchange{
					method:         "GET",
					path:           "/api/providers/missing/authorize",
					expectedStatus: http.StatusNotFound,
					bodyContains:   []string{"not registered"},
				},
				httpExchange{
					method:         "GET",
					path:           "/api/providers/kroger/callback",
					query:          map[string]string{"error": "access_denied", "state": "nope"},
					expectedStatus: http.StatusBadRequest,
					bodyContains:   []string{"declined the connection"},
				},
				httpExchange{
					method:         "GET",
					path:           "/api/providers/kroger/callback",
					query:          map[string]string{"code": "abc", "state": "never-issued"},
					expectedStatus: http.StatusBadRequest,
					bodyContains:   []string{"invalid or expired"},
				},
			),
		},
	}

	runHandlerTests(t, tests)
}
