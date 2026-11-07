package server

// Kill-switch audit (PM ruling): with DISABLE_KROGER on, no live route may reach Kroger HTTP.
// Counting RoundTripper at the adapter boundary; synthetic fixtures only.

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/cart"
	"github.com/Rhionin/stockbot/internal/cart/appcred"
	"github.com/Rhionin/stockbot/internal/cart/connection"
	"github.com/Rhionin/stockbot/internal/cart/kroger"
)

type killSwitchHTTPSpy struct{ calls int }

func (k *killSwitchHTTPSpy) RoundTrip(req *http.Request) (*http.Response, error) {
	k.calls++
	if strings.Contains(req.URL.Path, "/oauth2/token") {
		return provisionTestJSONResponse(http.StatusOK, `{"access_token":"fresh","refresh_token":"r2","expires_in":3600}`), nil
	}
	return provisionTestJSONResponse(http.StatusOK, `{}`), nil
}

func withKillSwitchTimezones(t *testing.T, fn func(t *testing.T)) {
	t.Helper()
	for _, tz := range []string{"UTC", "America/Denver"} {
		t.Run("TZ="+tz, func(t *testing.T) {
			t.Setenv("TZ", tz)
			fn(t)
		})
	}
}

func killSwitchExportFixture(spy *killSwitchHTTPSpy) func(env testEnv) {
	return func(env testEnv) {
		createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-kroger", "Kroger milk", "item-kroger")
		insertManualShoppingItem(env.T, env.DB, "sli-kroger", "user-1", "item-kroger", 1)
		if err := env.ProductStore.UpsertBarcodeMapping(context.Background(), "011110728227", "prod-kroger", "global", ""); err != nil {
			env.T.Fatal(err)
		}
		adapter, err := kroger.New("client", "secret", "https://app/cb", "PICKUP")
		if err != nil {
			env.T.Fatal(err)
		}
		adapter.WithTransport(spy)
		if err := env.Registry.Register(adapter, cart.WithCredentialsConfigured(true)); err != nil {
			env.T.Fatal(err)
		}
		expired := time.Now().UTC().Add(-time.Hour)
		if err := connection.NewDirectory(env.DB).Write(context.Background(), primaryScope(env.T, env.DB), &connection.Connection{
			Provider: "kroger", State: connection.StateConnected,
			AccessToken: "stale", RefreshToken: "refresh", ExpiresAt: &expired,
		}); err != nil {
			env.T.Fatal(err)
		}
		seedKrogerHouseholdApp(env.T, env.DB, "client", "secret", "https://app/cb")
	}
}

func TestKillSwitchAuditExportProviderKrogerZeroCallsWhenDisabled(t *testing.T) {
	withKillSwitchTimezones(t, func(t *testing.T) {
		spy := &killSwitchHTTPSpy{}
		runHandlerTests(t, []handlerTestCase{{
			name:    "export provider kroger disabled",
			options: []Option{WithProviderEnv(ProviderEnv{Disabled: true})},
			setup:   killSwitchExportFixture(spy),
			httpExchange: httpExchange{
				method: "POST", path: "/api/shopping-list/export", body: `{"provider":"kroger"}`,
				expectedStatus: http.StatusConflict,
				assertions:     []assertion{{path: "$.code", value: "store_disabled"}},
			},
		}})
		if spy.calls != 0 {
			t.Fatalf("kroger HTTP calls=%d want 0 with DISABLE_KROGER", spy.calls)
		}
	})
}

func TestKillSwitchAuditExportProviderKrogerEnabledControlReachesKroger(t *testing.T) {
	spy := &killSwitchHTTPSpy{}
	runHandlerTests(t, []handlerTestCase{{
		name:  "export provider kroger enabled",
		setup: killSwitchExportFixture(spy),
		httpExchange: httpExchange{
			method: "POST", path: "/api/shopping-list/export", body: `{"provider":"kroger"}`,
			expectedStatus: http.StatusOK,
		},
	}})
	if spy.calls == 0 {
		t.Fatal("enabled control: expired connection export never reached kroger HTTP")
	}
}

func TestKillSwitchAuditExportEmptyBodyZeroCallsWhenDisabled(t *testing.T) {
	withKillSwitchTimezones(t, func(t *testing.T) {
		spy := &killSwitchHTTPSpy{}
		runHandlerTests(t, []handlerTestCase{{
			name:    "export empty body disabled",
			options: []Option{WithProviderEnv(ProviderEnv{Disabled: true})},
			setup:   killSwitchExportFixture(spy),
			httpExchange: httpExchange{
				method: "POST", path: "/api/shopping-list/export", body: `{}`,
				expectedStatus: http.StatusConflict,
				assertions:     []assertion{{path: "$.code", value: "store_disabled"}},
			},
		}})
		if spy.calls != 0 {
			t.Fatalf("kroger HTTP calls=%d want 0 with DISABLE_KROGER", spy.calls)
		}
	})
}

func TestKillSwitchAuditExportEmptyBodyEnabledControlReachesKroger(t *testing.T) {
	spy := &killSwitchHTTPSpy{}
	runHandlerTests(t, []handlerTestCase{{
		name:  "export empty body enabled",
		setup: killSwitchExportFixture(spy),
		httpExchange: httpExchange{
			method: "POST", path: "/api/shopping-list/export", body: `{}`,
			expectedStatus: http.StatusOK,
		},
	}})
	if spy.calls == 0 {
		t.Fatal("enabled control: SoleConfigured export never reached kroger HTTP")
	}
}

func killSwitchCallbackFixture(spy *killSwitchHTTPSpy) func(env testEnv) {
	return func(env testEnv) {
		adapter, err := kroger.New("client", "secret", "https://app/cb", "PICKUP")
		if err != nil {
			env.T.Fatal(err)
		}
		adapter.WithTransport(spy)
		if err := env.Registry.Register(adapter, cart.WithCredentialsConfigured(true)); err != nil {
			env.T.Fatal(err)
		}
		seedKrogerHouseholdApp(env.T, env.DB, "client", "secret", "https://app/cb")
		scope := primaryScope(env.T, env.DB)
		if err := connection.NewDirectory(env.DB).GenerateAuthState(
			context.Background(), scope, "kroger", "ks-state", appcred.AppKey("client"), "household"); err != nil {
			env.T.Fatal(err)
		}
	}
}

func TestKillSwitchAuditCallbackZeroCallsWhenDisabled(t *testing.T) {
	withKillSwitchTimezones(t, func(t *testing.T) {
		spy := &killSwitchHTTPSpy{}
		runHandlerTests(t, []handlerTestCase{{
			name:    "callback disabled",
			options: []Option{WithProviderEnv(ProviderEnv{Disabled: true})},
			setup:   killSwitchCallbackFixture(spy),
			httpExchange: httpExchange{
				method: http.MethodGet, path: "/api/providers/kroger/callback",
				query:          map[string]string{"code": "c", "state": "ks-state"},
				expectedStatus: http.StatusSeeOther,
			},
		}})
		if spy.calls != 0 {
			t.Fatalf("kroger HTTP calls=%d want 0 with DISABLE_KROGER", spy.calls)
		}
	})
}

func TestKillSwitchAuditCallbackEnabledControlReachesKroger(t *testing.T) {
	spy := &killSwitchHTTPSpy{}
	runHandlerTests(t, []handlerTestCase{{
		name:  "callback enabled",
		setup: killSwitchCallbackFixture(spy),
		httpExchange: httpExchange{
			method: http.MethodGet, path: "/api/providers/kroger/callback",
			query:          map[string]string{"code": "c", "state": "ks-state"},
			expectedStatus: http.StatusSeeOther,
		},
	}})
	if spy.calls == 0 {
		t.Fatal("enabled control: callback never reached kroger HTTP (ExchangeCode)")
	}
}

func TestKillSwitchAuditKrogerAppPutZeroCallsWhenDisabled(t *testing.T) {
	spy := &killSwitchHTTPSpy{}
	caller, db, primary, adapter := setupKrogerAppOperator(t, WithProviderEnv(ProviderEnv{Disabled: true}))
	adapter.WithTransport(spy)
	caller.house = primary
	seedKrogerHouseholdApp(t, db, "house-client", "house-secret", "https://app.example/cb")
	path := "/api/households/" + primary + "/kroger-app"
	put := caller.authed(http.MethodPut, path, map[string]string{
		"clientId": "new-client", "clientSecret": "new-secret", "modality": "PICKUP",
	})
	if spy.calls != 0 {
		t.Fatalf("kroger HTTP calls=%d want 0 with DISABLE_KROGER", spy.calls)
	}
	if put.Code != http.StatusConflict || !strings.Contains(put.Body.String(), `"code":"store_disabled"`) {
		t.Fatalf("PUT %d %s", put.Code, put.Body.String())
	}
}

func TestKillSwitchAuditKrogerAppPutEnabledControlReachesKroger(t *testing.T) {
	spy := &killSwitchHTTPSpy{}
	caller, _, primary, adapter := setupKrogerAppOperator(t)
	adapter.WithTransport(spy)
	caller.house = primary
	rec := caller.authed(http.MethodPut, "/api/households/"+primary+"/kroger-app", map[string]string{
		"clientId": "new-client", "clientSecret": "new-secret", "modality": "PICKUP",
	})
	if spy.calls == 0 {
		t.Fatalf("enabled control: PUT never reached kroger HTTP (status %d %s)", rec.Code, rec.Body.String())
	}
}

func TestKillSwitchAuditKrogerAppTestZeroCallsWhenDisabled(t *testing.T) {
	spy := &killSwitchHTTPSpy{}
	caller, db, primary, adapter := setupKrogerAppOperator(t, WithProviderEnv(ProviderEnv{Disabled: true}))
	adapter.WithTransport(spy)
	caller.house = primary
	seedKrogerHouseholdApp(t, db, "house-client", "house-secret", "https://app.example/cb")
	path := "/api/households/" + primary + "/kroger-app/test"
	post := caller.authed(http.MethodPost, path, map[string]string{
		"clientId": "new-client", "clientSecret": "new-secret",
	})
	if spy.calls != 0 {
		t.Fatalf("kroger HTTP calls=%d want 0 with DISABLE_KROGER", spy.calls)
	}
	if post.Code != http.StatusConflict || !strings.Contains(post.Body.String(), `"code":"store_disabled"`) {
		t.Fatalf("POST test %d %s", post.Code, post.Body.String())
	}
}

func TestKillSwitchAuditKrogerAppTestEnabledControlReachesKroger(t *testing.T) {
	spy := &killSwitchHTTPSpy{}
	caller, _, primary, adapter := setupKrogerAppOperator(t)
	adapter.WithTransport(spy)
	caller.house = primary
	rec := caller.authed(http.MethodPost, "/api/households/"+primary+"/kroger-app/test", map[string]string{
		"clientId": "new-client", "clientSecret": "new-secret",
	})
	if spy.calls == 0 {
		t.Fatalf("enabled control: POST test never reached kroger HTTP (status %d %s)", rec.Code, rec.Body.String())
	}
}
