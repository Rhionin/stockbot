package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/Rhionin/stockbot/internal/cart"
	"github.com/Rhionin/stockbot/internal/cart/appcred"
	"github.com/Rhionin/stockbot/internal/cart/carttest"
	"github.com/Rhionin/stockbot/internal/cart/connection"
	"github.com/Rhionin/stockbot/internal/cart/kroger"
	"github.com/Rhionin/stockbot/internal/inventory"
	"github.com/Rhionin/stockbot/internal/shopping"
	"github.com/Rhionin/stockbot/internal/tenant"
)

func TestKrogerAppMemberForbiddenBeforeStoreDisabled(t *testing.T) {
	db := openMigratedDB(t, filepath.Join(t.TempDir(), "kroger-member-disabled.db"))
	setupDir := t.TempDir()
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
	owner := &stage1Caller{t: t, h: handler}
	if rec := owner.call(http.MethodGet, "/setup", nil, app.EntryPublic, nil); rec.Code != http.StatusOK {
		t.Fatal(rec.Code)
	}
	code, err := auth.ReadSetupCode(setupDir)
	if err != nil {
		t.Fatal(err)
	}
	owner.call(http.MethodPost, "/setup", map[string]string{
		"setupCode": code, "name": "Owner", "email": "owner-dis@test", "password": "correct horse",
	}, app.EntryPublic, nil)
	login := owner.call(http.MethodPost, "/api/auth/login", map[string]string{
		"email": "owner-dis@test", "password": "correct horse",
	}, app.EntryPublic, nil)
	owner.cookie = cookieFrom(t, login, site.SessionCookie().Name)
	var session auth.SessionView
	if err := json.Unmarshal(login.Body.Bytes(), &session); err != nil || session.ActiveHouseholdID == nil {
		t.Fatal(login.Body.String())
	}
	invite := owner.authed(http.MethodPost, "/api/invites", map[string]string{"role": "member"})
	var created auth.InviteCreated
	if err := json.Unmarshal(invite.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	member := &stage1Caller{t: t, h: handler}
	accept := member.call(http.MethodPost, "/api/invites/accept", map[string]any{
		"code":    created.Code,
		"account": map[string]string{"name": "Mem", "email": "mem-dis@test", "password": "correct horse"},
	}, app.EntryPublic, nil)
	if accept.Code != http.StatusOK {
		t.Fatal(accept.Body.String())
	}
	mLogin := member.call(http.MethodPost, "/api/auth/login", map[string]string{
		"email": "mem-dis@test", "password": "correct horse",
	}, app.EntryPublic, nil)
	member.cookie = cookieFrom(t, mLogin, site.SessionCookie().Name)
	member.house = *session.ActiveHouseholdID
	house := *session.ActiveHouseholdID
	path := "/api/households/" + house + "/kroger-app"
	put := member.authed(http.MethodPut, path, map[string]string{"clientId": "x", "clientSecret": "y"})
	if put.Code != http.StatusForbidden || !strings.Contains(put.Body.String(), `"code":"forbidden_role"`) {
		t.Fatalf("member PUT %d %s", put.Code, put.Body.String())
	}
	del := member.authed(http.MethodDelete, path, nil)
	if del.Code != http.StatusForbidden || !strings.Contains(del.Body.String(), `"code":"forbidden_role"`) {
		t.Fatalf("member DELETE %d %s", del.Code, del.Body.String())
	}
	post := member.authed(http.MethodPost, path+"/test", nil)
	if post.Code != http.StatusForbidden || !strings.Contains(post.Body.String(), `"code":"forbidden_role"`) {
		t.Fatalf("member POST test disabled %d %s", post.Code, post.Body.String())
	}
}

func TestKrogerAppMemberCannotDeleteOrTestWhenEnabled(t *testing.T) {
	caller, _, primary, _ := setupKrogerAppOperator(t)
	setupMemberOnHousehold(t, caller, primary, func(member *stage1Caller) {
		path := "/api/households/" + primary + "/kroger-app"
		del := member.authed(http.MethodDelete, path, nil)
		if del.Code != http.StatusForbidden || !strings.Contains(del.Body.String(), `"code":"forbidden_role"`) {
			t.Fatalf("DELETE %d %s", del.Code, del.Body.String())
		}
		post := member.authed(http.MethodPost, path+"/test", nil)
		if post.Code != http.StatusForbidden || !strings.Contains(post.Body.String(), `"code":"forbidden_role"`) {
			t.Fatalf("POST test %d %s", post.Code, post.Body.String())
		}
	})
}

func TestListProvidersKrogerDisabledShape(t *testing.T) {
	runHandlerTests(t, []handlerTestCase{{
		name: "kroger disabled lists only id displayName status",
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
		},
		afterRequest: func(env testEnv) {
			raw, _ := io.ReadAll(env.Res.Body)
			var list []map[string]json.RawMessage
			if err := json.Unmarshal(raw, &list); err != nil {
				env.T.Fatal(err)
			}
			if len(list) != 1 {
				env.T.Fatalf("len=%d body=%s", len(list), raw)
			}
			entry := list[0]
			if len(entry) != 3 {
				env.T.Fatalf("keys=%d want 3: %s", len(entry), raw)
			}
			for _, key := range []string{"id", "displayName", "status"} {
				if _, ok := entry[key]; !ok {
					env.T.Fatalf("missing %q in %s", key, raw)
				}
			}
			if string(entry["status"]) != `"disabled"` {
				env.T.Fatalf("status=%s", entry["status"])
			}
		},
	}})
}

func TestKrogerAppPutTestAppKeysStatusTableDoesNotSave(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{"429", http.StatusTooManyRequests, `{}`},
		{"400", http.StatusBadRequest, `{"error":"invalid_request"}`},
		{"404", http.StatusNotFound, `{}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			caller, db, primary, adapter := setupKrogerAppOperator(t)
			caller.house = primary
			adapter.WithTransport(roundTripFunc(func(req *http.Request) (*http.Response, error) {
				if strings.Contains(req.URL.Path, "/oauth2/token") {
					return jsonResp(tc.status, tc.body), nil
				}
				return nil, io.ErrUnexpectedEOF
			}))
			put := caller.authed(http.MethodPut, "/api/households/"+primary+"/kroger-app", map[string]string{
				"clientId": "new-app", "clientSecret": "new-secret", "modality": "PICKUP",
			})
			if put.Code != http.StatusBadRequest || !strings.Contains(put.Body.String(), `"code":"store_app_keys_rejected"`) {
				t.Fatalf("PUT %d %s", put.Code, put.Body.String())
			}
			var n int
			if err := db.QueryRow(`SELECT COUNT(*) FROM provider_app_credentials WHERE household_id = ? AND provider_id = 'kroger'`, primary).Scan(&n); err != nil || n != 0 {
				t.Fatalf("saved rows=%d err=%v", n, err)
			}
		})
	}
}

func TestKrogerAppSaveAppReplaceRollsBackOnFailure(t *testing.T) {
	saveHookCalls := 0
	caller, db, primary, _ := setupKrogerAppOperator(t, WithKrogerAppAfterSaveTestHook(func() error {
		saveHookCalls++
		if saveHookCalls > 1 {
			return errors.New("injected post-save failure")
		}
		return nil
	}))
	caller.house = primary
	save := caller.authed(http.MethodPut, "/api/households/"+primary+"/kroger-app", map[string]string{
		"clientId": "first-app", "clientSecret": "first-secret", "modality": "PICKUP",
	})
	if save.Code != http.StatusOK {
		t.Fatal(save.Body.String())
	}
	when := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := db.Exec(`
		INSERT INTO provider_connections (id, user_id, household_id, provider_id, state, updated_at)
		VALUES ('conn-replace', 'user-1', ?, 'kroger', 'connected', ?)`, primary, when); err != nil {
		t.Fatal(err)
	}
	replace := caller.authed(http.MethodPut, "/api/households/"+primary+"/kroger-app", map[string]string{
		"clientId": "second-app", "clientSecret": "second-secret", "modality": "PICKUP",
	})
	if replace.Code != http.StatusInternalServerError {
		t.Fatalf("replace %d %s", replace.Code, replace.Body.String())
	}
	if saveHookCalls != 2 {
		t.Fatalf("save hook calls = %d, want 2 (initial save + failed replace)", saveHookCalls)
	}
	var clientID string
	if err := db.QueryRow(`SELECT client_id FROM provider_app_credentials WHERE household_id = ? AND provider_id = 'kroger'`, primary).Scan(&clientID); err != nil || clientID != "first-app" {
		t.Fatalf("client_id=%q err=%v", clientID, err)
	}
	var state string
	if err := db.QueryRow(`SELECT state FROM provider_connections WHERE id = 'conn-replace'`).Scan(&state); err != nil || state != "connected" {
		t.Fatalf("state=%q err=%v", state, err)
	}
	var reauthRows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM provider_connections WHERE household_id = ? AND provider_id = 'kroger' AND state = 'reauth_required'`, primary).Scan(&reauthRows); err != nil || reauthRows != 0 {
		t.Fatalf("reauth_required rows=%d err=%v, want rollback with no reauth", reauthRows, err)
	}
}

func TestKrogerAppTestRateLimitPerHousehold(t *testing.T) {
	caller, _, primary, _ := setupKrogerAppOperator(t)
	caller.house = primary
	save := caller.authed(http.MethodPut, "/api/households/"+primary+"/kroger-app", map[string]string{
		"clientId": "rate-app", "clientSecret": "rate-secret", "modality": "PICKUP",
	})
	if save.Code != http.StatusOK {
		t.Fatal(save.Body.String())
	}
	path := "/api/households/" + primary + "/kroger-app/test"
	for i := 0; i < 9; i++ {
		rec := caller.authed(http.MethodPost, path, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("test %d: %d %s", i+2, rec.Code, rec.Body.String())
		}
	}
	blocked := caller.authed(http.MethodPost, path, nil)
	if blocked.Code != http.StatusTooManyRequests || blocked.Header().Get("Retry-After") == "" || !strings.Contains(blocked.Body.String(), `"code":"rate_limited"`) {
		t.Fatalf("11th test %d retry=%q %s", blocked.Code, blocked.Header().Get("Retry-After"), blocked.Body.String())
	}
}

func TestKrogerAppTestRateLimitRetryAfterPinned(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "kroger-rate-retry.db")
	setupDir := t.TempDir()
	db := openMigratedDB(t, dbPath)
	svc := auth.NewService(db, setupDir)
	start := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	svc.SetClock(func() time.Time { return start })
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
	handler, _ := setupOnDB(t, db, nil,
		WithStage1(svc, site),
		WithCartRegistry(registry, cart.NewLedger(db)),
		WithProviderEnv(ProviderEnv{
			ClientID: "env-client", ClientSecret: "env-secret",
			RedirectURI: site.AbsoluteURL("/api/providers/kroger/callback"), Modality: "PICKUP",
		}),
	)
	caller := krogerTestOwner(t, handler, setupDir, site, "rate-retry@test")
	caller.house = caller.primary
	save := caller.authed(http.MethodPut, "/api/households/"+caller.primary+"/kroger-app", map[string]string{
		"clientId": "rate-app", "clientSecret": "rate-secret", "modality": "PICKUP",
	})
	if save.Code != http.StatusOK {
		t.Fatal(save.Body.String())
	}
	path := "/api/households/" + caller.primary + "/kroger-app/test"
	for i := 0; i < 9; i++ {
		if rec := caller.authed(http.MethodPost, path, nil); rec.Code != http.StatusOK {
			t.Fatalf("test %d: %d %s", i+2, rec.Code, rec.Body.String())
		}
	}
	blocked := caller.authed(http.MethodPost, path, nil)
	if blocked.Code != http.StatusTooManyRequests {
		t.Fatalf("11th test %d %s", blocked.Code, blocked.Body.String())
	}
	if blocked.Header().Get("Retry-After") != "3600" {
		t.Fatalf("first block Retry-After = %q, want 3600", blocked.Header().Get("Retry-After"))
	}
	svc.SetClock(func() time.Time { return start.Add(59*time.Minute + 59*time.Second) })
	almost := caller.authed(http.MethodPost, path, nil)
	if almost.Code != http.StatusTooManyRequests {
		t.Fatalf("still limited at 59m59s %d %s", almost.Code, almost.Body.String())
	}
	if almost.Header().Get("Retry-After") != "1" {
		t.Fatalf("Retry-After at 59m59s = %q, want 1", almost.Header().Get("Retry-After"))
	}
}

func TestKrogerAppTestRateLimitPerHouseholdOtherHouseholdUnblocked(t *testing.T) {
	caller, db, primary, _ := setupKrogerAppOperator(t)
	caller.house = primary
	save := caller.authed(http.MethodPut, "/api/households/"+primary+"/kroger-app", map[string]string{
		"clientId": "rate-app", "clientSecret": "rate-secret", "modality": "PICKUP",
	})
	if save.Code != http.StatusOK {
		t.Fatal(save.Body.String())
	}
	path := "/api/households/" + primary + "/kroger-app/test"
	for i := 0; i < 9; i++ {
		rec := caller.authed(http.MethodPost, path, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("test %d: %d %s", i+2, rec.Code, rec.Body.String())
		}
	}
	blocked := caller.authed(http.MethodPost, path, nil)
	if blocked.Code != http.StatusTooManyRequests {
		t.Fatalf("11th test %d %s", blocked.Code, blocked.Body.String())
	}
	birch := seedSecondHousehold(t, db, caller, "01909999-9999-7000-8000-0000000000c3")
	caller2 := &stage1Caller{t: t, h: caller.h, cookie: caller.cookie, house: birch}
	saveBirch := caller2.authed(http.MethodPut, "/api/households/"+birch+"/kroger-app", map[string]string{
		"clientId": "birch-app", "clientSecret": "birch-secret", "modality": "PICKUP",
	})
	if saveBirch.Code != http.StatusOK {
		t.Fatalf("birch save %d %s", saveBirch.Code, saveBirch.Body.String())
	}
	if rec := caller2.authed(http.MethodPost, "/api/households/"+birch+"/kroger-app/test", nil); rec.Code != http.StatusOK {
		t.Fatalf("other household test %d %s", rec.Code, rec.Body.String())
	}
}

func TestKrogerAppTestRateLimitWindowResets(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "kroger-rate-reset.db")
	setupDir := t.TempDir()
	db := openMigratedDB(t, dbPath)
	svc := auth.NewService(db, setupDir)
	start := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	svc.SetClock(func() time.Time { return start })
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
	handler, _ := setupOnDB(t, db, nil,
		WithStage1(svc, site),
		WithCartRegistry(registry, cart.NewLedger(db)),
		WithProviderEnv(ProviderEnv{
			ClientID: "env-client", ClientSecret: "env-secret",
			RedirectURI: site.AbsoluteURL("/api/providers/kroger/callback"), Modality: "PICKUP",
		}),
	)
	caller := krogerTestOwner(t, handler, setupDir, site, "rate-reset@test")
	caller.house = caller.primary
	save := caller.authed(http.MethodPut, "/api/households/"+caller.primary+"/kroger-app", map[string]string{
		"clientId": "rate-app", "clientSecret": "rate-secret", "modality": "PICKUP",
	})
	if save.Code != http.StatusOK {
		t.Fatal(save.Body.String())
	}
	path := "/api/households/" + caller.primary + "/kroger-app/test"
	for i := 0; i < 9; i++ {
		if rec := caller.authed(http.MethodPost, path, map[string]string{"clientId": "x", "clientSecret": "y"}); rec.Code != http.StatusOK {
			t.Fatalf("test %d: %d", i+2, rec.Code)
		}
	}
	if rec := caller.authed(http.MethodPost, path, map[string]string{"clientId": "x", "clientSecret": "y"}); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("blocked %d", rec.Code)
	}
	svc.SetClock(func() time.Time { return start.Add(time.Hour + time.Minute) })
	if rec := caller.authed(http.MethodPost, path, map[string]string{"clientId": "x", "clientSecret": "y"}); rec.Code != http.StatusOK {
		t.Fatalf("after window %d %s", rec.Code, rec.Body.String())
	}
}

func TestShoppingListExportEmptyBodyMultipleRegistryConfigured(t *testing.T) {
	_, env := setupTestWithDB(t)
	push := carttest.NewFake(cart.Capabilities{
		Auth: cart.AuthNone, Delivery: cart.DeliveryServerPush,
		Confirmation: cart.ConfirmNone, Mutation: cart.MutateAddOnly, Identity: cart.IdentityDerived,
	}, carttest.NewScript())
	handoff := carttest.NewFake(cart.Capabilities{
		Auth: cart.AuthNone, Delivery: cart.DeliveryClientHandoff,
		Confirmation: cart.ConfirmNone, Mutation: cart.MutateAddOnly, Identity: cart.IdentityDerived,
	}, carttest.NewScript())
	if err := env.Registry.Register(push, cart.WithCredentialsConfigured(true)); err != nil {
		t.Fatal(err)
	}
	if err := env.Registry.Register(handoff, cart.WithCredentialsConfigured(true)); err != nil {
		t.Fatal(err)
	}
	seedDefaultExportApp(t, env.DB, string(push.ID()))
	seedDefaultExportApp(t, env.DB, string(handoff.ID()))
	handler := handlerFromEnv(env)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/shopping-list/export", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	withRequestScope(t, env.DB, handler).ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `provider \"\" is not registered`) {
		t.Fatalf("export %d %s", rec.Code, rec.Body.String())
	}
}

func TestShoppingListExportUnknownProviderRejected(t *testing.T) {
	_, env := setupTestWithDB(t)
	registry := cart.NewRegistry()
	adapter, err := kroger.New("house-client", "house-secret", "https://app.example/cb", "PICKUP")
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(adapter); err != nil {
		t.Fatal(err)
	}
	seedKrogerHouseholdApp(t, env.DB, "house-client", "house-secret", "https://app.example/cb")
	handler := &ShoppingListExportHandler{
		ShoppingList: shopping.NewStore(env.DB),
		Pantry:       inventory.NewPantry(env.DB),
		Registry:     registry,
		Credentials:  appcred.NewVault(env.DB),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/shopping-list/export", HandleJSON(handler.Handle))
	scoped := withRequestScope(t, env.DB, mux)
	rec := postExportKroger(t, scoped, `{"provider":"not-registered"}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `provider \"not-registered\" is not registered`) {
		t.Fatalf("export %d %s", rec.Code, rec.Body.String())
	}
}

func TestShoppingListExportEmptyBodySingleHouseholdAppNoOp(t *testing.T) {
	_, env := setupTestWithDB(t)
	registry := cart.NewRegistry()
	adapter, err := kroger.New("house-client", "house-secret", "https://app.example/cb", "PICKUP")
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(adapter); err != nil {
		t.Fatal(err)
	}
	seedKrogerHouseholdApp(t, env.DB, "house-client", "house-secret", "https://app.example/cb")
	handler := &ShoppingListExportHandler{
		ShoppingList: shopping.NewStore(env.DB),
		Pantry:       inventory.NewPantry(env.DB),
		Registry:     registry,
		Credentials:  appcred.NewVault(env.DB),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/shopping-list/export", HandleJSON(handler.Handle))
	scoped := withRequestScope(t, env.DB, mux)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/shopping-list/export", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	scoped.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"exported":0`) {
		t.Fatalf("export %d %s", rec.Code, rec.Body.String())
	}
}

func TestShoppingListExportKrogerSavedAppNotConnected(t *testing.T) {
	_, env := setupTestWithDB(t)
	registry := cart.NewRegistry()
	adapter, err := kroger.New("house-client", "house-secret", "https://app.example/cb", "PICKUP")
	if err != nil {
		t.Fatal(err)
	}
	httpSpy := &krogerHTTPCallCounter{}
	adapter.WithTransport(httpSpy)
	if err := registry.Register(adapter); err != nil {
		t.Fatal(err)
	}
	seedKrogerHouseholdApp(t, env.DB, "house-client", "house-secret", "https://app.example/cb")
	prov := &countingProvisioner{}
	handler := &ShoppingListExportHandler{
		ShoppingList: shopping.NewStore(env.DB),
		Pantry:       inventory.NewPantry(env.DB),
		Registry:     registry,
		Credentials:  appcred.NewVault(env.DB),
		Provisioner:  prov,
		Connections:  connection.NewDirectory(env.DB),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/shopping-list/export", HandleJSON(handler.Handle))
	scoped := withRequestScope(t, env.DB, mux)
	rec := postExportKroger(t, scoped, `{"provider":"kroger"}`)
	if rec.Code != http.StatusConflict || (!strings.Contains(rec.Body.String(), "not connected") && !strings.Contains(rec.Body.String(), "reconnect_required")) {
		t.Fatalf("export %d %s", rec.Code, rec.Body.String())
	}
	if prov.calls != 0 || httpSpy.calls != 0 {
		t.Fatalf("Provision=%d HTTP=%d", prov.calls, httpSpy.calls)
	}
}

func TestKrogerCallbackRejectsMismatchedAuthAppKey(t *testing.T) {
	runHandlerTests(t, []handlerTestCase{{
		name: "callback fails when saved app no longer matches auth_app_key",
		setup: func(env testEnv) {
			adapter, err := kroger.New("my-client", "secret", "https://app.example/cb", "PICKUP")
			if err != nil {
				env.T.Fatal(err)
			}
			adapter.WithTransport(krogerTestTokenTransport())
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
			scope := primaryScope(env.T, env.DB)
			if _, err := env.DB.Exec(`
				UPDATE provider_app_credentials SET client_id = 'replaced-client' WHERE household_id = ? AND provider_id = 'kroger'`,
				scope.HouseholdID()); err != nil {
				env.T.Fatal(err)
			}
			parsed, err := url.Parse(body.AuthorizationURL)
			if err != nil {
				env.T.Fatal(err)
			}
			state := parsed.Query().Get("state")
			executeExchange(env.T, handlerFromEnv(env), httpExchange{
				method:         http.MethodGet,
				path:           "/api/providers/kroger/callback",
				query:          map[string]string{"code": "abc", "state": state},
				expectedStatus: http.StatusSeeOther,
				bodyContains:   []string{"kroger=failed"},
			})
			var n int
			if err := env.DB.QueryRow(`SELECT COUNT(*) FROM provider_connections WHERE household_id = ? AND provider_id = 'kroger' AND state = 'connected'`, scope.HouseholdID()).Scan(&n); err != nil || n != 0 {
				env.T.Fatalf("connected rows=%d err=%v", n, err)
			}
		},
	}})
}

type countingProvisioner struct {
	calls int
}

func (c *countingProvisioner) Provision(context.Context, tenant.Scope, cart.ProviderID) (cart.ProvisionReport, error) {
	c.calls++
	return cart.ProvisionReport{}, nil
}

type krogerHTTPCallCounter struct {
	calls int
}

func (k *krogerHTTPCallCounter) RoundTrip(req *http.Request) (*http.Response, error) {
	k.calls++
	return provisionTestJSONResponse(http.StatusOK, `{"access_token":"x","expires_in":3600}`), nil
}

func postExportKroger(t *testing.T, scoped http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/shopping-list/export", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	scoped.ServeHTTP(rec, req)
	return rec
}

func TestShoppingListExportKrogerHouseholdAppProvisionsOnce(t *testing.T) {
	_, env := setupTestWithDB(t)
	registry := cart.NewRegistry()
	adapter, err := kroger.New("house-client", "house-secret", "https://app.example/cb", "PICKUP")
	if err != nil {
		t.Fatal(err)
	}
	httpSpy := &krogerHTTPCallCounter{}
	adapter.WithTransport(httpSpy)
	if err := registry.Register(adapter); err != nil {
		t.Fatal(err)
	}
	seedKrogerHouseholdApp(t, env.DB, "house-client", "house-secret", "https://app.example/cb")
	scope := primaryScope(t, env.DB)
	when := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := env.DB.Exec(`
		INSERT INTO provider_connections (id, user_id, household_id, provider_id, state, updated_at)
		VALUES ('conn-export', 'user-1', ?, 'kroger', 'connected', ?)`, scope.HouseholdID(), when); err != nil {
		t.Fatal(err)
	}
	prov := &countingProvisioner{}
	handler := &ShoppingListExportHandler{
		ShoppingList: shopping.NewStore(env.DB),
		Pantry:       inventory.NewPantry(env.DB),
		Registry:     registry,
		Credentials:  appcred.NewVault(env.DB),
		Provisioner:  prov,
		Connections:  connection.NewDirectory(env.DB),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/shopping-list/export", HandleJSON(handler.Handle))
	scoped := withRequestScope(t, env.DB, mux)
	rec := postExportKroger(t, scoped, `{"provider":"kroger"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("export %d %s", rec.Code, rec.Body.String())
	}
	if prov.calls != 1 {
		t.Fatalf("Provision calls=%d want 1", prov.calls)
	}
	if httpSpy.calls != 0 {
		t.Fatalf("kroger HTTP calls=%d want 0", httpSpy.calls)
	}
}

func TestShoppingListExportKrogerEnvOnlyNotConfigured(t *testing.T) {
	_, env := setupTestWithDB(t)
	registry := cart.NewRegistry()
	adapter, err := kroger.New("env-client", "env-secret", "https://env.example/cb", "PICKUP")
	if err != nil {
		t.Fatal(err)
	}
	httpSpy := &krogerHTTPCallCounter{}
	adapter.WithTransport(httpSpy)
	if err := registry.Register(adapter, cart.WithCredentialsConfigured(true)); err != nil {
		t.Fatal(err)
	}
	prov := &countingProvisioner{}
	handler := &ShoppingListExportHandler{
		ShoppingList: shopping.NewStore(env.DB),
		Pantry:       inventory.NewPantry(env.DB),
		Registry:     registry,
		Credentials:  appcred.NewVault(env.DB),
		Provisioner:  prov,
		Env: ProviderEnv{
			ClientID: "env-client", ClientSecret: "env-secret", RedirectURI: "https://env.example/cb", Modality: "PICKUP",
		},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/shopping-list/export", HandleJSON(handler.Handle))
	scoped := withRequestScope(t, env.DB, mux)
	rec := postExportKroger(t, scoped, `{"provider":"kroger"}`)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "not configured") {
		t.Fatalf("export %d %s", rec.Code, rec.Body.String())
	}
	if prov.calls != 0 {
		t.Fatalf("Provision calls=%d want 0", prov.calls)
	}
	if httpSpy.calls != 0 {
		t.Fatalf("kroger HTTP calls=%d want 0", httpSpy.calls)
	}
}

func TestShoppingListExportVaultErrorWhenAppSaved(t *testing.T) {
	_, env := setupTestWithDB(t)
	registry := cart.NewRegistry()
	adapter, err := kroger.New("house-client", "house-secret", "https://app.example/cb", "PICKUP")
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(adapter); err != nil {
		t.Fatal(err)
	}
	seedKrogerHouseholdApp(t, env.DB, "house-client", "house-secret", "https://app.example/cb")
	scope := primaryScope(t, env.DB)
	handler := &ShoppingListExportHandler{
		ShoppingList: shopping.NewStore(env.DB),
		Pantry:       inventory.NewPantry(env.DB),
		Registry:     registry,
		Credentials:  appcred.NewVault(env.DB),
	}
	if err := env.DB.Close(); err != nil {
		t.Fatal(err)
	}
	ctx := tenant.WithScope(context.Background(), scope)
	_, err = handler.Handle(Request[shoppingListExportRequest, struct{}]{
		Context: ctx,
		Body:    shoppingListExportRequest{Provider: "kroger"},
	})
	var he *HTTPError
	if !errors.As(err, &he) || he.Code != http.StatusInternalServerError {
		t.Fatalf("Handle err=%v", err)
	}
}

func TestShoppingListExportKrogerDisabledWithSavedApp(t *testing.T) {
	_, env := setupTestWithDB(t)
	registry := cart.NewRegistry()
	adapter, err := kroger.New("house-client", "house-secret", "https://app.example/cb", "PICKUP")
	if err != nil {
		t.Fatal(err)
	}
	httpSpy := &krogerHTTPCallCounter{}
	adapter.WithTransport(httpSpy)
	if err := registry.Register(adapter); err != nil {
		t.Fatal(err)
	}
	seedKrogerHouseholdApp(t, env.DB, "house-client", "house-secret", "https://app.example/cb")
	prov := &countingProvisioner{}
	handler := &ShoppingListExportHandler{
		ShoppingList: shopping.NewStore(env.DB),
		Pantry:       inventory.NewPantry(env.DB),
		Registry:     registry,
		Credentials:  appcred.NewVault(env.DB),
		Provisioner:  prov,
		Env:          ProviderEnv{Disabled: true},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/shopping-list/export", HandleJSON(handler.Handle))
	scoped := withRequestScope(t, env.DB, mux)
	rec := postExportKroger(t, scoped, `{"provider":"kroger"}`)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"code":"store_disabled"`) {
		t.Fatalf("export %d %s", rec.Code, rec.Body.String())
	}
	if prov.calls != 0 {
		t.Fatalf("Provision calls=%d want 0", prov.calls)
	}
	if httpSpy.calls != 0 {
		t.Fatalf("kroger HTTP calls=%d want 0", httpSpy.calls)
	}
}

func setupMemberOnHousehold(t *testing.T, owner *stage1Caller, house string, fn func(*stage1Caller)) {
	t.Helper()
	invite := owner.authed(http.MethodPost, "/api/invites", map[string]string{"role": "member"})
	var created auth.InviteCreated
	if err := json.Unmarshal(invite.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	member := &stage1Caller{t: t, h: owner.h}
	accept := member.call(http.MethodPost, "/api/invites/accept", map[string]any{
		"code":    created.Code,
		"account": map[string]string{"name": "Mem", "email": "mem-" + house + "@test", "password": "correct horse"},
	}, app.EntryPublic, nil)
	if accept.Code != http.StatusOK {
		t.Fatal(accept.Body.String())
	}
	mLogin := member.call(http.MethodPost, "/api/auth/login", map[string]string{
		"email": "mem-" + house + "@test", "password": "correct horse",
	}, app.EntryPublic, nil)
	member.cookie = cookieFrom(t, mLogin, stage1Site(t).SessionCookie().Name)
	member.house = house
	fn(member)
}

func seedSecondHousehold(t *testing.T, db *sql.DB, caller *stage1Caller, id string) string {
	t.Helper()
	when := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := db.Exec(`INSERT INTO households (id, name, restock_mode, created_at, channel_key, is_primary)
		VALUES (?, 'Second', '', '2026-01-01T00:00:00Z', 'abcdef0123456789abcdef0123456789', 0)`, id); err != nil {
		t.Fatal(err)
	}
	var userID string
	if err := db.QueryRow(`SELECT id FROM users LIMIT 1`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO memberships (household_id, user_id, role, created_at) VALUES (?, ?, 'owner', ?)`, id, userID, when); err != nil {
		t.Fatal(err)
	}
	return id
}

type krogerTestOwnerCaller struct {
	*stage1Caller
	primary string
}

func krogerTestOwner(t *testing.T, handler http.Handler, setupDir string, site app.RuntimeConfig, email string) *krogerTestOwnerCaller {
	t.Helper()
	caller := &stage1Caller{t: t, h: handler}
	if rec := caller.call(http.MethodGet, "/setup", nil, app.EntryPublic, nil); rec.Code != http.StatusOK {
		t.Fatal(rec.Code)
	}
	code, err := auth.ReadSetupCode(setupDir)
	if err != nil {
		t.Fatal(err)
	}
	caller.call(http.MethodPost, "/setup", map[string]string{
		"setupCode": code, "name": "Op", "email": email, "password": "correct horse",
	}, app.EntryPublic, nil)
	login := caller.call(http.MethodPost, "/api/auth/login", map[string]string{
		"email": email, "password": "correct horse",
	}, app.EntryPublic, nil)
	caller.cookie = cookieFrom(t, login, site.SessionCookie().Name)
	var session auth.SessionView
	if err := json.Unmarshal(login.Body.Bytes(), &session); err != nil || session.ActiveHouseholdID == nil {
		t.Fatal(login.Body.String())
	}
	caller.house = *session.ActiveHouseholdID
	return &krogerTestOwnerCaller{stage1Caller: caller, primary: *session.ActiveHouseholdID}
}
