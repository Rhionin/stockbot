package server

import (
	"net/http"
	"testing"

	"github.com/Rhionin/stockbot/internal/cart"
	"github.com/Rhionin/stockbot/internal/cart/kroger"
)

func TestKrogerAppHTTPRetiredCredentialsAndAuthorize(t *testing.T) {
	runHandlerTests(t, []handlerTestCase{
		{
			name: "DELETE retired kroger credentials route",
			setup: func(env testEnv) {
				if err := env.Registry.Register(kroger.NewUnconfigured()); err != nil {
					env.T.Fatal(err)
				}
			},
			httpExchange: httpExchange{
				method:         http.MethodDelete,
				path:           "/api/providers/kroger/credentials",
				expectedStatus: http.StatusNotFound,
				assertions:     []assertion{{path: "$.code", value: "not_found"}},
			},
		},
		{
			name: "GET providers kroger disabled",
			options: []Option{
				WithProviderEnv(ProviderEnv{Disabled: true}),
			},
			setup: func(env testEnv) {
				if err := env.Registry.Register(kroger.NewUnconfigured()); err != nil {
					env.T.Fatal(err)
				}
			},
			httpExchange: httpExchange{
				method:         http.MethodGet,
				path:           "/api/providers",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$[0].connectionState", value: "disabled"},
				},
			},
		},
	})
}

func TestKrogerAuthorizeWithoutHouseholdApp(t *testing.T) {
	runHandlerTests(t, []handlerTestCase{{
		name: "authorize before household app saved",
		setup: func(env testEnv) {
			if err := env.Registry.Register(kroger.NewUnconfigured(), cart.WithCredentialsConfigured(false)); err != nil {
				env.T.Fatal(err)
			}
		},
		httpExchange: httpExchange{
			method:         http.MethodGet,
			path:           "/api/providers/kroger/authorize",
			expectedStatus: http.StatusConflict,
			assertions: []assertion{
				{path: "$.code", value: "kroger_not_connected"},
				{path: "$.checkReason", value: "not_configured"},
			},
		},
	}})
}
