package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/Rhionin/stockbot/internal/cart"
	"github.com/Rhionin/stockbot/internal/cart/connection"
	"github.com/Rhionin/stockbot/internal/cart/kroger"
)

func TestKrogerCallbackInvalidStateRedirectsFailed(t *testing.T) {
	runHandlerTests(t, []handlerTestCase{{
		name: "callback with unknown state",
		setup: func(env testEnv) {
			adapter, err := kroger.New("my-client", "secret", "https://app.example/cb", "PICKUP")
			if err != nil {
				env.T.Fatal(err)
			}
			if err := env.Registry.Register(adapter, cart.WithCredentialsConfigured(true)); err != nil {
				env.T.Fatal(err)
			}
			seedKrogerHouseholdApp(env.T, env.DB, "my-client", "secret", "https://app.example/cb")
		},
		httpExchange: httpExchange{
			method:         http.MethodGet,
			path:           "/api/providers/kroger/callback",
			query:          map[string]string{"code": "abc", "state": "not-a-real-state"},
			expectedStatus: http.StatusSeeOther,
			bodyContains:   []string{"kroger=failed"},
		},
	}})
}

func TestKrogerAuthorizeHouseholdReturn(t *testing.T) {
	runHandlerTests(t, []handlerTestCase{{
		name: "authorize with household return",
		setup: func(env testEnv) {
			adapter, err := kroger.New("my-client", "secret", "https://app.example/cb", "PICKUP")
			if err != nil {
				env.T.Fatal(err)
			}
			if err := env.Registry.Register(adapter, cart.WithCredentialsConfigured(true)); err != nil {
				env.T.Fatal(err)
			}
			seedKrogerHouseholdApp(env.T, env.DB, "my-client", "secret", "https://app.example/cb")
		},
		httpExchange: httpExchange{
			method:         http.MethodGet,
			path:           "/api/providers/kroger/authorize",
			query:          map[string]string{"return": "household"},
			expectedStatus: http.StatusOK,
		},
		afterRequest: func(env testEnv) {
			raw, _ := io.ReadAll(env.Res.Body)
			if !strings.Contains(string(raw), "authorizationUrl") {
				env.T.Fatalf("body %s", raw)
			}
		},
	}})
}

func TestListProvidersKrogerDisabledOmitsCredentials(t *testing.T) {
	runHandlerTests(t, []handlerTestCase{{
		name: "kroger disabled in provider list",
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
			bodyContains:   []string{`"status":"disabled"`},
			bodyExcludes:   []string{`"credentials"`, `"connectionState"`, `"credentialsConfigured"`},
		},
	}})
}

func TestKrogerCallbackTokenExchangeFailure(t *testing.T) {
	runHandlerTests(t, []handlerTestCase{{
		name: "callback maps token exchange failure to failed",
		setup: func(env testEnv) {
			adapter, err := kroger.New("my-client", "secret", "https://app.example/cb", "PICKUP")
			if err != nil {
				env.T.Fatal(err)
			}
			adapter.WithTransport(roundTripFunc(func(req *http.Request) (*http.Response, error) {
				return nil, io.ErrUnexpectedEOF
			}))
			if err := env.Registry.Register(adapter, cart.WithCredentialsConfigured(true)); err != nil {
				env.T.Fatal(err)
			}
			seedKrogerHouseholdApp(env.T, env.DB, "my-client", "secret", "https://app.example/cb")
		},
		httpExchange: httpExchange{
			method:         http.MethodGet,
			path:           "/api/providers/kroger/authorize",
			expectedStatus: http.StatusOK,
		},
		afterRequest: func(env testEnv) {
			var body struct {
				AuthorizationURL string `json:"authorizationUrl"`
			}
			raw, _ := io.ReadAll(env.Res.Body)
			if err := json.Unmarshal(raw, &body); err != nil {
				env.T.Fatal(err)
			}
			parsed, _ := url.Parse(body.AuthorizationURL)
			state := parsed.Query().Get("state")
			executeExchange(env.T, handlerFromEnv(env), httpExchange{
				method:         http.MethodGet,
				path:           "/api/providers/kroger/callback",
				query:          map[string]string{"code": "bad-code", "state": state},
				expectedStatus: http.StatusSeeOther,
				bodyContains:   []string{"kroger=failed"},
			})
		},
	}})
}

func TestKrogerCallbackWhenDisabledRedirectsWithoutOutcome(t *testing.T) {
	runHandlerTests(t, []handlerTestCase{{
		name:    "disabled server callback consumes state and omits kroger query",
		options: []Option{WithProviderEnv(ProviderEnv{Disabled: true})},
		setup: func(env testEnv) {
			if err := env.Registry.Register(kroger.NewUnconfigured()); err != nil {
				env.T.Fatal(err)
			}
			scope := primaryScope(env.T, env.DB)
			if err := connection.NewDirectory(env.DB).GenerateAuthState(
				context.Background(), scope, "kroger", "disabled-state", "app-key", "household"); err != nil {
				env.T.Fatal(err)
			}
		},
		httpExchange: httpExchange{
			method:         http.MethodGet,
			path:           "/api/providers/kroger/callback",
			query:          map[string]string{"code": "ignored", "state": "disabled-state"},
			expectedStatus: http.StatusSeeOther,
		},
		afterRequest: func(env testEnv) {
			loc := env.Res.Header.Get("Location")
			if !strings.Contains(loc, "/household") || strings.Contains(loc, "kroger=") {
				env.T.Fatalf("Location = %q", loc)
			}
		},
	}})
}

func TestKrogerAppDeleteClearsSideTables(t *testing.T) {
	caller, db, primary, _ := setupKrogerAppOperator(t)
	caller.house = primary
	save := caller.authed(http.MethodPut, "/api/households/"+primary+"/kroger-app", map[string]string{
		"clientId": "house-app", "clientSecret": "house-secret", "modality": "PICKUP",
	})
	if save.Code != http.StatusOK {
		t.Fatal(save.Code)
	}
	when := "2026-01-01T00:00:00Z"
	if _, err := db.Exec(`
		INSERT INTO provider_connections (id, user_id, household_id, provider_id, state, updated_at)
		VALUES ('del-conn', 'user-1', ?, 'kroger', 'connected', ?)`, primary, when); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO store_availability (household_id, store_id, product_id, location_id, status, checked_at, status_expires_at)
		VALUES (?, 'store-1', 'prod-1', 'loc-1', 'carried', ?, ?)`, primary, when, when); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO store_checks (household_id, store_id, state, trigger, started_at, lease_until, checked_count, found_count, total_count)
		VALUES (?, 'store-1', 'done', 'manual', ?, ?, 1, 1, 1)`, primary, when, when); err != nil {
		t.Fatal(err)
	}
	del := caller.authed(http.MethodDelete, "/api/households/"+primary+"/kroger-app", nil)
	if del.Code != http.StatusOK {
		t.Fatalf("delete %d %s", del.Code, del.Body.String())
	}
	for _, q := range []string{
		`SELECT COUNT(*) FROM provider_connections WHERE household_id = ? AND provider_id = 'kroger'`,
		`SELECT COUNT(*) FROM store_availability WHERE household_id = ?`,
		`SELECT COUNT(*) FROM store_checks WHERE household_id = ?`,
	} {
		var n int
		if err := db.QueryRow(q, primary).Scan(&n); err != nil || n != 0 {
			t.Fatalf("query %q count=%d err=%v", q, n, err)
		}
	}
}
