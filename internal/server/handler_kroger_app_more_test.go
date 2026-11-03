package server

import (
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/Rhionin/stockbot/internal/cart"
	"github.com/Rhionin/stockbot/internal/cart/appcred"
	"github.com/Rhionin/stockbot/internal/cart/kroger"
	"github.com/Rhionin/stockbot/internal/tenant"
)

func TestKrogerAuthorizeWithoutStoreApp(t *testing.T) {
	runHandlerTests(t, []handlerTestCase{{
		name: "authorize returns kroger_not_connected when household has no app",
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

func TestKrogerProvidersCredentialsConfiguredPerHousehold(t *testing.T) {
	runHandlerTests(t, []handlerTestCase{{
		name: "GET providers reflects household app not server registry alone",
		setup: func(env testEnv) {
			seedKrogerHouseholdApp(env.T, env.DB, "hh-client", "secret", "https://app.example/cb")
			if err := env.Registry.Register(kroger.NewUnconfigured(), cart.WithCredentialsConfigured(false)); err != nil {
				env.T.Fatal(err)
			}
		},
		httpExchange: httpExchange{
			method:         http.MethodGet,
			path:           "/api/providers",
			expectedStatus: http.StatusOK,
			assertions:     []assertion{{path: "$[0].credentialsConfigured", value: true}},
		},
	}})
}

func TestKrogerAppMemberCannotWrite(t *testing.T) {
	db := openMigratedDB(t, filepath.Join(t.TempDir(), "kroger-member.db"))
	setupDir := t.TempDir()
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
	handler, _ := setupOnDB(t, db, nil,
		WithStage1(svc, site),
		WithCartRegistry(registry, cart.NewLedger(db)),
		WithProviderEnv(ProviderEnv{
			ClientID: "env-client", ClientSecret: "env-secret",
			RedirectURI: site.AbsoluteURL("/api/providers/kroger/callback"), Modality: "PICKUP",
		}),
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
		"setupCode": code, "name": "Owner", "email": "owner@test", "password": "correct horse",
	}, app.EntryPublic, nil)
	login := owner.call(http.MethodPost, "/api/auth/login", map[string]string{
		"email": "owner@test", "password": "correct horse",
	}, app.EntryPublic, nil)
	owner.cookie = cookieFrom(t, login, site.SessionCookie().Name)
	var session auth.SessionView
	if err := json.Unmarshal(login.Body.Bytes(), &session); err != nil || session.ActiveHouseholdID == nil {
		t.Fatal(login.Body.String())
	}
	owner.house = *session.ActiveHouseholdID
	invite := owner.authed(http.MethodPost, "/api/invites", map[string]string{"role": "member"})
	var created auth.InviteCreated
	if err := json.Unmarshal(invite.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	member := &stage1Caller{t: t, h: handler}
	accept := member.call(http.MethodPost, "/api/invites/accept", map[string]any{
		"code":    created.Code,
		"account": map[string]string{"name": "Mem", "email": "mem@test", "password": "correct horse"},
	}, app.EntryPublic, nil)
	if accept.Code != http.StatusOK {
		t.Fatal(accept.Body.String())
	}
	mLogin := member.call(http.MethodPost, "/api/auth/login", map[string]string{
		"email": "mem@test", "password": "correct horse",
	}, app.EntryPublic, nil)
	member.cookie = cookieFrom(t, mLogin, site.SessionCookie().Name)
	member.house = *session.ActiveHouseholdID
	get := member.authed(http.MethodGet, "/api/households/"+*session.ActiveHouseholdID+"/kroger-app", nil)
	if get.Code != http.StatusOK || strings.Contains(get.Body.String(), `"canManage":true`) {
		t.Fatalf("member GET %d %s", get.Code, get.Body.String())
	}
	put := member.authed(http.MethodPut, "/api/households/"+*session.ActiveHouseholdID+"/kroger-app", map[string]string{
		"clientId": "x", "clientSecret": "y",
	})
	if put.Code != http.StatusForbidden || !strings.Contains(put.Body.String(), `"code":"forbidden_role"`) {
		t.Fatalf("member PUT %d %s", put.Code, put.Body.String())
	}
}

func TestKrogerAppDeleteIdempotent(t *testing.T) {
	caller, _, primary, _ := setupKrogerAppOperator(t)
	caller.house = primary
	save := caller.authed(http.MethodPut, "/api/households/"+primary+"/kroger-app", map[string]string{
		"clientId": "house-app", "clientSecret": "house-secret", "modality": "PICKUP",
	})
	if save.Code != http.StatusOK {
		t.Fatalf("save %d", save.Code)
	}
	del1 := caller.authed(http.MethodDelete, "/api/households/"+primary+"/kroger-app", nil)
	del2 := caller.authed(http.MethodDelete, "/api/households/"+primary+"/kroger-app", nil)
	if del1.Code != http.StatusOK || del2.Code != http.StatusOK {
		t.Fatalf("delete %d %d", del1.Code, del2.Code)
	}
	if strings.Contains(del2.Body.String(), `"configured":true`) {
		t.Fatalf("second delete still configured: %s", del2.Body.String())
	}
}

func TestKrogerAppTestWithoutSavedApp(t *testing.T) {
	caller, _, primary, _ := setupKrogerAppOperator(t)
	caller.house = primary
	post := caller.authed(http.MethodPost, "/api/households/"+primary+"/kroger-app/test", nil)
	if post.Code != http.StatusConflict || !strings.Contains(post.Body.String(), `"code":"kroger_not_connected"`) {
		t.Fatalf("POST test empty %d %s", post.Code, post.Body.String())
	}
}

func TestKrogerAppUnreachableOnSave(t *testing.T) {
	caller, _, primary, adapter := setupKrogerAppOperator(t)
	caller.house = primary
	adapter.WithTransport(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if strings.Contains(req.URL.Path, "/oauth2/token") {
			return nil, io.ErrUnexpectedEOF
		}
		return nil, io.ErrUnexpectedEOF
	}))
	put := caller.authed(http.MethodPut, "/api/households/"+primary+"/kroger-app", map[string]string{
		"clientId": "house-app", "clientSecret": "house-secret", "modality": "PICKUP",
	})
	if put.Code != http.StatusServiceUnavailable || !strings.Contains(put.Body.String(), `"code":"store_app_unreachable"`) {
		t.Fatalf("PUT unreachable %d %s", put.Code, put.Body.String())
	}
}

func TestKrogerAppPutNoProductAccessSaves(t *testing.T) {
	caller, db, primary, adapter := setupKrogerAppOperator(t)
	caller.house = primary
	adapter.WithTransport(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if strings.Contains(req.URL.Path, "/oauth2/token") {
			return jsonResp(http.StatusForbidden, `{"error":"invalid_scope"}`), nil
		}
		return nil, io.ErrUnexpectedEOF
	}))
	put := caller.authed(http.MethodPut, "/api/households/"+primary+"/kroger-app", map[string]string{
		"clientId": "limited-app", "clientSecret": "limited-secret", "modality": "PICKUP",
	})
	if put.Code != http.StatusOK || !strings.Contains(put.Body.String(), `"code":"no_product_access"`) {
		t.Fatalf("PUT no_product_access %d %s", put.Code, put.Body.String())
	}
	var pa sql.NullInt64
	if err := db.QueryRow(`SELECT product_access FROM provider_app_credentials WHERE household_id = ? AND provider_id = 'kroger'`, primary).Scan(&pa); err != nil || !pa.Valid || pa.Int64 != 0 {
		t.Fatalf("product_access = %v err=%v", pa, err)
	}
}

func TestKrogerAppPutRejectsBadModality(t *testing.T) {
	caller, _, primary, _ := setupKrogerAppOperator(t)
	caller.house = primary
	put := caller.authed(http.MethodPut, "/api/households/"+primary+"/kroger-app", map[string]string{
		"clientId": "house-app", "clientSecret": "house-secret", "modality": "CURBSIDE",
	})
	if put.Code != http.StatusBadRequest || !strings.Contains(put.Body.String(), `"code":"validation"`) {
		t.Fatalf("PUT bad modality %d %s", put.Code, put.Body.String())
	}
}

func TestKrogerAppIsolationSurvivesHandlerReload(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "kroger-reload.db")
	setupDir := t.TempDir()
	db := openMigratedDB(t, dbPath)
	svc := auth.NewService(db, setupDir)
	site := stage1Site(t)
	buildHandler := func() http.Handler {
		registry := cart.NewRegistry()
		adapter, err := kroger.New("env-client", "env-secret", site.AbsoluteURL("/api/providers/kroger/callback"), "PICKUP")
		if err != nil {
			t.Fatal(err)
		}
		adapter.WithTransport(krogerTestTokenTransport())
		if err := registry.Register(adapter, cart.WithCredentialsConfigured(true)); err != nil {
			t.Fatal(err)
		}
		h, _ := setupOnDB(t, db, nil,
			WithStage1(svc, site),
			WithCartRegistry(registry, cart.NewLedger(db)),
			WithProviderEnv(ProviderEnv{
				ClientID: "env-client", ClientSecret: "env-secret",
				RedirectURI: site.AbsoluteURL("/api/providers/kroger/callback"), Modality: "PICKUP",
			}),
		)
		return h
	}
	handler := buildHandler()
	caller := &stage1Caller{t: t, h: handler}
	if rec := caller.call(http.MethodGet, "/setup", nil, app.EntryPublic, nil); rec.Code != http.StatusOK {
		t.Fatal(rec.Code)
	}
	code, err := auth.ReadSetupCode(setupDir)
	if err != nil {
		t.Fatal(err)
	}
	caller.call(http.MethodPost, "/setup", map[string]string{
		"setupCode": code, "name": "Owner", "email": "owner-reload@test", "password": "correct horse",
	}, app.EntryPublic, nil)
	login := caller.call(http.MethodPost, "/api/auth/login", map[string]string{
		"email": "owner-reload@test", "password": "correct horse",
	}, app.EntryPublic, nil)
	caller.cookie = cookieFrom(t, login, site.SessionCookie().Name)
	var session auth.SessionView
	if err := json.Unmarshal(login.Body.Bytes(), &session); err != nil || session.ActiveHouseholdID == nil {
		t.Fatal(login.Body.String())
	}
	primary := *session.ActiveHouseholdID
	caller.house = primary
	if rec := caller.authed(http.MethodPut, "/api/households/"+primary+"/kroger-app", map[string]string{
		"clientId": "reload-app", "clientSecret": "reload-secret", "modality": "PICKUP",
	}); rec.Code != http.StatusOK {
		t.Fatalf("save %d %s", rec.Code, rec.Body.String())
	}
	birch := "01909999-9999-7000-8000-0000000000b2"
	when := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := db.Exec(`INSERT INTO households (id, name, restock_mode, created_at, channel_key, is_primary)
		VALUES (?, 'Birch reload', '', '2026-01-01T00:00:00Z', 'abcdef0123456789abcdef0123456789', 0)`, birch); err != nil {
		t.Fatal(err)
	}
	if session.User == nil {
		t.Fatal("missing session user")
	}
	if _, err := db.Exec(`INSERT INTO memberships (household_id, user_id, role, created_at) VALUES (?, ?, 'owner', ?)`,
		birch, session.User.ID, when); err != nil {
		t.Fatal(err)
	}
	caller2 := &stage1Caller{t: t, h: buildHandler()}
	caller2.cookie = caller.cookie
	caller2.house = birch
	if rec := caller2.authed(http.MethodPut, "/api/households/"+birch+"/kroger-app", map[string]string{
		"clientId": "birch-reload", "clientSecret": "birch-secret", "modality": "DELIVERY",
	}); rec.Code != http.StatusOK {
		t.Fatalf("birch save after reload %d %s", rec.Code, rec.Body.String())
	}
	primaryScope, _ := tenant.New(primary)
	birchScope, _ := tenant.New(birch)
	vault := appcred.NewVault(db)
	savedPrimary, ok, err := vault.Load(t.Context(), primaryScope, "kroger")
	if err != nil || !ok || savedPrimary.ClientID != "reload-app" {
		t.Fatalf("primary after reload %+v ok=%v err=%v", savedPrimary, ok, err)
	}
	savedBirch, ok, err := vault.Load(t.Context(), birchScope, "kroger")
	if err != nil || !ok || savedBirch.ClientID != "birch-reload" {
		t.Fatalf("birch after reload %+v ok=%v err=%v", savedBirch, ok, err)
	}
}

func TestKrogerCallbackAccessDenied(t *testing.T) {
	runHandlerTests(t, []handlerTestCase{{
		name: "callback maps access_denied to cancelled",
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
			parsed, err := url.Parse(body.AuthorizationURL)
			if err != nil {
				env.T.Fatal(err)
			}
			state := parsed.Query().Get("state")
			handler := handlerFromEnv(env)
			executeExchange(env.T, handler, httpExchange{
				method:         http.MethodGet,
				path:           "/api/providers/kroger/callback",
				query:          map[string]string{"error": "access_denied", "state": state},
				expectedStatus: http.StatusSeeOther,
				bodyContains:   []string{"kroger=cancelled"},
			})
		},
	}})
}
