package server

import (
	"net/http"
	"testing"

	"github.com/Rhionin/stockbot/internal/cart"
	"github.com/Rhionin/stockbot/internal/cart/carttest"
	"github.com/Rhionin/stockbot/internal/cart/kroger"
)

const openProvider = "test-none-server_push"

func TestUnknownResolutionAndLedgerReset(t *testing.T) {
	runHandlerTests(t, []handlerTestCase{
		{
			name: "saying it reached the provider lowers the list until the ledger is reset",
			setup: func(env testEnv) {
				registerOpenAndKroger(env)
				createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-milk", "Milk", "item-milk")
				insertManualShoppingItem(env.T, env.DB, "sli-milk", "user-1", "item-milk", 4)
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/shopping-list/items/sli-milk/unknown-resolution",
				body:           `{"reachedProvider":true,"provider":"` + openProvider + `"}`,
				expectedStatus: http.StatusOK,
				assertions:     []assertion{{path: "$.entryId", value: "sli-milk"}},
			},
			afterRequest: exchanges(
				httpExchange{
					method:         "GET",
					path:           "/api/shopping-list",
					query:          map[string]string{"provider": openProvider},
					expectedStatus: http.StatusOK,
					assertions: []assertion{
						{path: "$[0].itemId", value: "item-milk"},
						{path: "$[0].quantity", value: float64(0)},
					},
				},
				httpExchange{
					method:         "GET",
					path:           "/api/providers/" + openProvider + "/ledger",
					expectedStatus: http.StatusOK,
					assertions: []assertion{
						{path: "$.entries[0].itemId", value: "item-milk"},
						{path: "$.entries[0].requested", value: float64(4)},
					},
				},
				httpExchange{
					method:         "POST",
					path:           "/api/providers/nope/ledger/reset",
					expectedStatus: http.StatusNotFound,
					assertions:     []assertion{{path: "$.error", value: `provider "nope" not registered`}},
				},
				httpExchange{
					method:         "POST",
					path:           "/api/providers/kroger/ledger/reset",
					expectedStatus: http.StatusConflict,
					bodyContains:   []string{"not connected"},
				},
				httpExchange{
					method:         "GET",
					path:           "/api/providers/" + openProvider + "/ledger",
					expectedStatus: http.StatusOK,
					assertions:     []assertion{{path: "$.entries[0].requested", value: float64(4)}},
				},
				httpExchange{
					method:         "POST",
					path:           "/api/providers/" + openProvider + "/ledger/reset",
					expectedStatus: http.StatusCreated,
				},
				httpExchange{
					method:         "GET",
					path:           "/api/providers/" + openProvider + "/ledger",
					expectedStatus: http.StatusOK,
					assertions: []assertion{
						{path: "$.entries[0].itemId", value: "item-milk"},
						{path: "$.entries[0].requested", value: float64(0)},
					},
				},
				httpExchange{
					method:         "GET",
					path:           "/api/shopping-list",
					query:          map[string]string{"provider": openProvider},
					expectedStatus: http.StatusOK,
					assertions:     []assertion{{path: "$[0].quantity", value: float64(4)}},
				},
			),
		},
		{
			name: "a line that did not reach the provider stays on the list",
			setup: func(env testEnv) {
				registerOpenAndKroger(env)
				createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-stay", "Bread", "item-stay")
				insertManualShoppingItem(env.T, env.DB, "sli-stay", "user-1", "item-stay", 2)
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/shopping-list/items/sli-stay/unknown-resolution",
				body:           `{"reachedProvider":false}`,
				expectedStatus: http.StatusOK,
				assertions:     []assertion{{path: "$.entryId", value: "sli-stay"}},
			},
			afterRequest: exchanges(
				httpExchange{
					method:         "GET",
					path:           "/api/shopping-list",
					query:          map[string]string{"provider": openProvider},
					expectedStatus: http.StatusOK,
					assertions:     []assertion{{path: "$[0].quantity", value: float64(2)}},
				},
				httpExchange{
					method:         "GET",
					path:           "/api/providers/" + openProvider + "/ledger",
					expectedStatus: http.StatusOK,
					assertions:     []assertion{{path: "$.entries", value: []any{}}},
				},
			),
		},
		{
			name: "a zero quantity still records one requested unit",
			setup: func(env testEnv) {
				registerOpenAndKroger(env)
				createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-zero", "Salt", "item-zero")
				insertManualShoppingItem(env.T, env.DB, "sli-zero", "user-1", "item-zero", 0)
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/shopping-list/items/sli-zero/unknown-resolution",
				body:           `{"reachedProvider":true,"provider":"` + openProvider + `"}`,
				expectedStatus: http.StatusOK,
			},
			afterRequest: exchanges(httpExchange{
				method:         "GET",
				path:           "/api/providers/" + openProvider + "/ledger",
				expectedStatus: http.StatusOK,
				assertions:     []assertion{{path: "$.entries[0].requested", value: float64(1)}},
			}),
		},
		{
			name: "a missing shopping line is not found",
			setup: func(env testEnv) {
				registerOpenAndKroger(env)
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/shopping-list/items/missing/unknown-resolution",
				body:           `{"reachedProvider":true,"provider":"` + openProvider + `"}`,
				expectedStatus: http.StatusNotFound,
				assertions:     []assertion{{path: "$.error", value: "entry not found"}},
			},
		},
		{
			name: "reaching a provider without naming it is a bad request and changes nothing",
			setup: func(env testEnv) {
				registerOpenAndKroger(env)
				createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-noname", "Honey", "item-noname")
				insertManualShoppingItem(env.T, env.DB, "sli-noname", "user-1", "item-noname", 3)
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/shopping-list/items/sli-noname/unknown-resolution",
				body:           `{"reachedProvider":true,"provider":""}`,
				expectedStatus: http.StatusBadRequest,
				assertions:     []assertion{{path: "$.error", value: "missing provider"}},
			},
			afterRequest: exchanges(
				httpExchange{
					method:         "GET",
					path:           "/api/shopping-list",
					query:          map[string]string{"provider": openProvider},
					expectedStatus: http.StatusOK,
					assertions:     []assertion{{path: "$[0].quantity", value: float64(3)}},
				},
				httpExchange{
					method:         "GET",
					path:           "/api/providers/" + openProvider + "/ledger",
					expectedStatus: http.StatusOK,
					assertions:     []assertion{{path: "$.entries", value: []any{}}},
				},
			),
		},
	})
}

func registerOpenAndKroger(env testEnv) {
	env.T.Helper()
	provider := carttest.NewFake(cart.Capabilities{
		Auth:         cart.AuthNone,
		Delivery:     cart.DeliveryServerPush,
		Confirmation: cart.ConfirmPerRequest,
		Mutation:     cart.MutateAddOnly,
		Identity:     cart.IdentityDerived,
	}, nil)
	if err := env.Registry.Register(provider, cart.WithCredentialsConfigured(true)); err != nil {
		env.T.Fatalf("register open provider: %v", err)
	}
	if err := env.Registry.Register(kroger.NewUnconfigured()); err != nil {
		env.T.Fatalf("register kroger: %v", err)
	}
}
