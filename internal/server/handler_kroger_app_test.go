package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/Rhionin/stockbot/internal/cart"
	"github.com/Rhionin/stockbot/internal/cart/kroger"
)

func krogerTestTokenTransport() http.RoundTripper {
	return roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if strings.Contains(req.URL.Path, "/oauth2/token") {
			return provisionTestJSONResponse(http.StatusOK, `{"access_token":"throwaway","expires_in":1}`), nil
		}
		return nil, io.ErrUnexpectedEOF
	})
}

func setupKrogerAppOperator(t *testing.T, extra ...Option) (*stage1Caller, *sql.DB, string, *kroger.Adapter) {
	t.Helper()
	setupDir := t.TempDir()
	db := openMigratedDB(t, filepath.Join(t.TempDir(), "kroger-app.db"))
	svc := auth.NewService(db, setupDir)
	site := stage1Site(t)
	registry := cart.NewRegistry()
	adapter, err := kroger.New("env-client", "env-secret", site.AbsoluteURL("/api/providers/kroger/callback"), "PICKUP")
	if err != nil {
		t.Fatal(err)
	}
	adapter.WithTransport(krogerTestTokenTransport())
	if err := registry.Register(adapter, cart.WithCredentialsConfigured(true)); err != nil {
		t.Fatal(err)
	}
	opts := []Option{
		WithStage1(svc, site),
		WithCartRegistry(registry, cart.NewLedger(db)),
		WithProviderEnv(ProviderEnv{
			ClientID: "env-client", ClientSecret: "env-secret",
			RedirectURI: site.AbsoluteURL("/api/providers/kroger/callback"), Modality: "PICKUP",
		}),
	}
	opts = append(opts, extra...)
	handler, _ := setupOnDB(t, db, nil, opts...)
	caller := &stage1Caller{t: t, h: handler}
	if rec := caller.call(http.MethodGet, "/setup", nil, app.EntryPublic, nil); rec.Code != http.StatusOK {
		t.Fatalf("setup page %d", rec.Code)
	}
	code, err := auth.ReadSetupCode(setupDir)
	if err != nil {
		t.Fatal(err)
	}
	if rec := caller.call(http.MethodPost, "/setup", map[string]string{
		"setupCode": code, "name": "Ada Admin", "email": "ada@pantry.test", "password": "correct horse",
	}, app.EntryPublic, nil); rec.Code != http.StatusOK {
		t.Fatalf("setup %d %s", rec.Code, rec.Body.String())
	}
	login := caller.call(http.MethodPost, "/api/auth/login", map[string]string{
		"email": "ada@pantry.test", "password": "correct horse",
	}, app.EntryPublic, nil)
	if login.Code != http.StatusOK {
		t.Fatalf("login %d %s", login.Code, login.Body.String())
	}
	caller.cookie = cookieFrom(t, login, site.SessionCookie().Name)
	var session auth.SessionView
	if err := json.Unmarshal(login.Body.Bytes(), &session); err != nil || session.ActiveHouseholdID == nil {
		t.Fatalf("session %s", login.Body.String())
	}
	return caller, db, *session.ActiveHouseholdID, adapter
}

func TestKrogerAppRetiredCredentialsRoutes404(t *testing.T) {
	runHandlerTests(t, []handlerTestCase{{
		name: "kroger credentials PUT and DELETE are retired",
		setup: func(env testEnv) {
			if err := env.Registry.Register(kroger.NewUnconfigured()); err != nil {
				env.T.Fatal(err)
			}
		},
		httpExchange: httpExchange{
			method: "PUT", path: "/api/providers/kroger/credentials",
			body:           `{"clientId":"x","clientSecret":"y","redirectUri":"https://example.test/cb","modality":"PICKUP"}`,
			expectedStatus: http.StatusNotFound,
			assertions:     []assertion{{path: "$.code", value: "not_found"}},
		},
		afterRequest: exchanges(httpExchange{
			method:         http.MethodDelete,
			path:           "/api/providers/kroger/credentials",
			expectedStatus: http.StatusNotFound,
			assertions:     []assertion{{path: "$.code", value: "not_found"}},
		}),
	}})
}

func TestKrogerAppDisabledContract(t *testing.T) {
	setupDir := t.TempDir()
	db := openMigratedDB(t, filepath.Join(t.TempDir(), "kroger-disabled.db"))
	svc := auth.NewService(db, setupDir)
	site := stage1Site(t)
	registry := cart.NewRegistry()
	if err := registry.Register(kroger.NewUnconfigured()); err != nil {
		t.Fatal(err)
	}
	handler, _ := setupOnDB(t, db, nil,
		WithStage1(svc, site),
		WithCartRegistry(registry, cart.NewLedger(db)),
		WithProviderEnv(ProviderEnv{Disabled: true}),
	)
	caller := &stage1Caller{t: t, h: handler}
	if rec := caller.call(http.MethodGet, "/setup", nil, app.EntryPublic, nil); rec.Code != http.StatusOK {
		t.Fatal(rec.Code)
	}
	code, err := auth.ReadSetupCode(setupDir)
	if err != nil {
		t.Fatal(err)
	}
	caller.call(http.MethodPost, "/setup", map[string]string{
		"setupCode": code, "name": "Op", "email": "op@test", "password": "correct horse",
	}, app.EntryPublic, nil)
	login := caller.call(http.MethodPost, "/api/auth/login", map[string]string{
		"email": "op@test", "password": "correct horse",
	}, app.EntryPublic, nil)
	caller.cookie = cookieFrom(t, login, site.SessionCookie().Name)
	scope, err := auth.PrimaryScope(t.Context(), db)
	if err != nil {
		t.Fatal(err)
	}
	house := scope.HouseholdID()
	caller.house = house
	rec := caller.authed(http.MethodGet, "/api/households/"+house+"/kroger-app", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"status":"disabled"`) {
		t.Fatalf("GET disabled %d %s", rec.Code, rec.Body.String())
	}
	put := caller.authed(http.MethodPut, "/api/households/"+house+"/kroger-app", map[string]string{
		"clientId": "a", "clientSecret": "b", "modality": "PICKUP",
	})
	if put.Code != http.StatusConflict || !strings.Contains(put.Body.String(), `"code":"store_disabled"`) {
		t.Fatalf("PUT disabled %d %s", put.Code, put.Body.String())
	}
}

func TestKrogerAppGetForeignHouseholdWhenDisabledReturns404(t *testing.T) {
	setupDir := t.TempDir()
	db := openMigratedDB(t, filepath.Join(t.TempDir(), "kroger-disabled-foreign.db"))
	svc := auth.NewService(db, setupDir)
	if err := svc.Ensure(t.Context()); err != nil {
		t.Fatal(err)
	}
	code, err := auth.ReadSetupCode(setupDir)
	if err != nil {
		t.Fatal(err)
	}
	site := stage1Site(t)
	handler, _ := setupOnDB(t, db, nil, WithStage1(svc, site))
	setup := stage1Do(handler, http.MethodPost, "/setup", "", map[string]string{
		"setupCode": code, "name": "Op", "email": "op@test", "password": "correct horse",
	}, app.EntryPublic, "")
	if setup.Code != http.StatusOK {
		t.Fatal(setup.Body.String())
	}
	primary, err := auth.PrimaryScope(t.Context(), db)
	if err != nil {
		t.Fatal(err)
	}
	var userID string
	if err := db.QueryRow(`SELECT user_id FROM memberships WHERE household_id = ?`, primary.HouseholdID()).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	birch := cedarHousehold(t, db)
	krogerApp := &KrogerAppHandler{DB: db, Auth: svc, Env: ProviderEnv{Disabled: true}}
	ctx := auth.WithPrincipal(t.Context(), auth.Principal{Kind: "account", UserID: userID})
	_, err = krogerApp.Get(Request[struct{}, krogerHouseholdParams]{
		Context: ctx, PathParams: krogerHouseholdParams{ID: birch},
	})
	var he *HTTPError
	if !errors.As(err, &he) || he.Code != http.StatusNotFound {
		t.Fatalf("foreign GET with DISABLE_KROGER want 404 from requireMember before disabled, got err=%v", err)
	}
	view, err := krogerApp.Get(Request[struct{}, krogerHouseholdParams]{
		Context: ctx, PathParams: krogerHouseholdParams{ID: primary.HouseholdID()},
	})
	if err != nil {
		t.Fatal(err)
	}
	disabled, ok := view.(KrogerAppDisabled)
	if !ok || disabled.Status != "disabled" {
		t.Fatalf("member GET want KrogerAppDisabled, got %#v", view)
	}
}

func TestKrogerAppOwnerSaveAndForeignHousehold404(t *testing.T) {
	caller, db, primary, _ := setupKrogerAppOperator(t)
	caller.house = primary
	if rec := caller.authed(http.MethodPut, "/api/providers/kroger/credentials", map[string]string{
		"clientId": "old", "clientSecret": "old", "redirectUri": "https://old.example/cb",
	}); rec.Code != http.StatusNotFound {
		t.Fatalf("retired PUT %d %s", rec.Code, rec.Body.String())
	}
	put := caller.authed(http.MethodPut, "/api/households/"+primary+"/kroger-app", map[string]string{
		"clientId": "house-app", "clientSecret": "house-secret", "modality": "PICKUP",
	})
	if put.Code != http.StatusOK || !strings.Contains(put.Body.String(), `"configured":true`) {
		t.Fatalf("owner PUT %d %s", put.Code, put.Body.String())
	}
	get := caller.authed(http.MethodGet, "/api/households/"+primary+"/kroger-app", nil)
	if get.Code != http.StatusOK || !strings.Contains(get.Body.String(), `"configured":true`) {
		t.Fatalf("GET after save %d %s", get.Code, get.Body.String())
	}
	testPost := caller.authed(http.MethodPost, "/api/households/"+primary+"/kroger-app/test", nil)
	if testPost.Code != http.StatusOK || !strings.Contains(testPost.Body.String(), `"ok":true`) {
		t.Fatalf("POST test saved creds %d %s", testPost.Code, testPost.Body.String())
	}
	if _, err := db.Exec(`
		INSERT INTO provider_connections (id, user_id, household_id, provider_id, state, app_key, updated_at)
		VALUES ('conn-test', 'user-1', ?, 'kroger', 'connected', 'stale', '2026-01-01T00:00:00Z')`, primary); err != nil {
		t.Fatal(err)
	}
	change := caller.authed(http.MethodPut, "/api/households/"+primary+"/kroger-app", map[string]string{
		"clientId": "new-app", "clientSecret": "new-secret", "modality": "DELIVERY",
	})
	if change.Code != http.StatusOK {
		t.Fatalf("change client PUT %d %s", change.Code, change.Body.String())
	}
	var state string
	if err := db.QueryRow(`SELECT state FROM provider_connections WHERE household_id = ? AND provider_id = 'kroger'`, primary).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "reauth_required" {
		t.Fatalf("connection state = %q", state)
	}
	del := caller.authed(http.MethodDelete, "/api/households/"+primary+"/kroger-app", nil)
	if del.Code != http.StatusOK || strings.Contains(del.Body.String(), `"configured":true`) {
		t.Fatalf("DELETE %d %s", del.Code, del.Body.String())
	}
	birch := "01909999-9999-7000-8000-0000000000b1"
	if _, err := db.Exec(`INSERT INTO households (id, name, restock_mode, created_at, channel_key, is_primary)
		VALUES (?, 'Birch', '', '2026-01-01T00:00:00Z', 'abcdef0123456789abcdef0123456789', 0)`, birch); err != nil {
		t.Fatal(err)
	}
	foreign := caller.authed(http.MethodGet, "/api/households/"+birch+"/kroger-app", nil)
	if foreign.Code != http.StatusNotFound {
		t.Fatalf("foreign GET %d %s", foreign.Code, foreign.Body.String())
	}
}

func TestKrogerAppCutoverRunsAtStartup(t *testing.T) {
	db := openMigratedDB(t, filepath.Join(t.TempDir(), "cutover-start.db"))
	if err := app.RunKrogerAppCutover(t.Context(), db, app.KrogerCutoverEnv{
		ClientID: "cut-client", ClientSecret: "cut-secret", RedirectURI: "https://cut.example/cb",
	}); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM app_settings WHERE key = 'kroger_app_cutover_v1' AND value = '1'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("flag count=%d err=%v", n, err)
	}
}

func TestKrogerAppPutModalityOnly(t *testing.T) {
	caller, _, primary, _ := setupKrogerAppOperator(t)
	caller.house = primary
	save := caller.authed(http.MethodPut, "/api/households/"+primary+"/kroger-app", map[string]string{
		"clientId": "house-app", "clientSecret": "house-secret", "modality": "PICKUP",
	})
	if save.Code != http.StatusOK {
		t.Fatalf("initial save %d", save.Code)
	}
	mod := caller.authed(http.MethodPut, "/api/households/"+primary+"/kroger-app", map[string]string{
		"modality": "DELIVERY",
	})
	if mod.Code != http.StatusOK || !strings.Contains(mod.Body.String(), `"modality":"DELIVERY"`) {
		t.Fatalf("modality-only %d %s", mod.Code, mod.Body.String())
	}
}

func TestKrogerAppPutRejectsBadKeys(t *testing.T) {
	caller, _, primary, adapter := setupKrogerAppOperator(t)
	caller.house = primary
	adapter.WithTransport(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if strings.Contains(req.URL.Path, "/oauth2/token") {
			return jsonResp(http.StatusUnauthorized, `{"error":"invalid_client"}`), nil
		}
		return nil, io.ErrUnexpectedEOF
	}))
	put := caller.authed(http.MethodPut, "/api/households/"+primary+"/kroger-app", map[string]string{
		"clientId": "bad", "clientSecret": "bad", "modality": "PICKUP",
	})
	if put.Code != http.StatusBadRequest || !strings.Contains(put.Body.String(), `"code":"store_app_keys_rejected"`) {
		t.Fatalf("PUT bad keys %d %s", put.Code, put.Body.String())
	}
}

func jsonResp(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}
}
