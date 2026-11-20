package server

import (
	"context"
	"database/sql"
	"encoding/json"
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
	"github.com/Rhionin/stockbot/internal/cart/connection"
	"github.com/Rhionin/stockbot/internal/tenant"
)

const providerConnectGateID = "oauth-shop"

type providerConnectGateEnv struct {
	t            *testing.T
	owner        *stage1Caller
	member       *stage1Caller
	db           *sql.DB
	house        string
	foreignHouse string
}

func setupProviderConnectGateEnv(t *testing.T) *providerConnectGateEnv {
	t.Helper()
	setupDir := t.TempDir()
	db := openMigratedDB(t, filepath.Join(t.TempDir(), "provider-owner-gate.db"))
	svc := auth.NewService(db, setupDir)
	site := stage1Site(t)
	registry := cart.NewRegistry()
	if err := registry.Register(
		oauthAppCredProvider{testAppCredProvider: testAppCredProvider{id: providerConnectGateID}},
		cart.WithCredentialsConfigured(true),
	); err != nil {
		t.Fatal(err)
	}
	handler, _ := setupOnDB(t, db, nil,
		WithStage1(svc, site),
		WithCartRegistry(registry, cart.NewLedger(db)),
	)
	owner := &stage1Caller{t: t, h: handler}
	if rec := owner.call(http.MethodGet, "/setup", nil, app.EntryPublic, nil); rec.Code != http.StatusOK {
		t.Fatalf("setup page %d", rec.Code)
	}
	code, err := auth.ReadSetupCode(setupDir)
	if err != nil {
		t.Fatal(err)
	}
	if rec := owner.call(http.MethodPost, "/setup", map[string]string{
		"setupCode": code, "name": "Owner", "email": "owner-gate@test", "password": "correct horse",
	}, app.EntryPublic, nil); rec.Code != http.StatusOK {
		t.Fatalf("setup %d %s", rec.Code, rec.Body.String())
	}
	login := owner.call(http.MethodPost, "/api/auth/login", map[string]string{
		"email": "owner-gate@test", "password": "correct horse",
	}, app.EntryPublic, nil)
	if login.Code != http.StatusOK {
		t.Fatalf("login %d %s", login.Code, login.Body.String())
	}
	owner.cookie = cookieFrom(t, login, site.SessionCookie().Name)
	var session auth.SessionView
	if err := json.Unmarshal(login.Body.Bytes(), &session); err != nil || session.ActiveHouseholdID == nil {
		t.Fatalf("session %s", login.Body.String())
	}
	house := *session.ActiveHouseholdID
	owner.house = house

	when := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := db.Exec(`
		INSERT INTO provider_app_credentials (
			id, user_id, household_id, provider_id, client_id, client_secret, redirect_uri, updated_at
		) VALUES (?, 'user-1', ?, 'kroger', 'k-id', 'k-secret', 'https://cb.test/kroger', ?)`,
		house+":kroger", house, when); err != nil {
		t.Fatal(err)
	}

	invite := owner.authed(http.MethodPost, "/api/invites", map[string]string{"role": "member"})
	var created auth.InviteCreated
	if err := json.Unmarshal(invite.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	member := &stage1Caller{t: t, h: handler}
	accept := member.call(http.MethodPost, "/api/invites/accept", map[string]any{
		"code": created.Code,
		"account": map[string]string{
			"name": "Mem", "email": "member-gate@test", "password": "correct horse",
		},
	}, app.EntryPublic, nil)
	if accept.Code != http.StatusOK {
		t.Fatal(accept.Body.String())
	}
	mLogin := member.call(http.MethodPost, "/api/auth/login", map[string]string{
		"email": "member-gate@test", "password": "correct horse",
	}, app.EntryPublic, nil)
	member.cookie = cookieFrom(t, mLogin, site.SessionCookie().Name)
	member.house = house

	return &providerConnectGateEnv{
		t:            t,
		owner:        owner,
		member:       member,
		db:           db,
		house:        house,
		foreignHouse: cedarHousehold(t, db),
	}
}

func (e *providerConnectGateEnv) authorizePath() string {
	return "/api/providers/" + providerConnectGateID + "/authorize"
}

func (e *providerConnectGateEnv) callbackURL(state, code string) string {
	q := url.Values{"state": {state}}
	if code != "" {
		q.Set("code", code)
	}
	return "/api/providers/" + providerConnectGateID + "/callback?" + q.Encode()
}

func (e *providerConnectGateEnv) disconnectPath() string {
	return "/api/providers/" + providerConnectGateID + "/connection"
}

func (e *providerConnectGateEnv) mintAuthState() string {
	e.t.Helper()
	rec := e.owner.authed(http.MethodGet, e.authorizePath(), nil)
	if rec.Code != http.StatusOK {
		e.t.Fatalf("owner authorize %d %s", rec.Code, rec.Body.String())
	}
	var body struct {
		AuthorizationURL string `json:"authorizationUrl"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		e.t.Fatal(err)
	}
	parsed, err := url.Parse(body.AuthorizationURL)
	if err != nil {
		e.t.Fatal(err)
	}
	state := parsed.Query().Get("state")
	if state == "" {
		e.t.Fatalf("missing state in %q", body.AuthorizationURL)
	}
	return state
}

func providerConnectAuthState(t *testing.T, db *sql.DB, house string) string {
	t.Helper()
	var state sql.NullString
	err := db.QueryRow(
		`SELECT auth_state FROM provider_connections WHERE household_id = ? AND provider_id = ?`,
		house, providerConnectGateID,
	).Scan(&state)
	if err == sql.ErrNoRows {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	if !state.Valid {
		return ""
	}
	return state.String
}

func assertForbiddenRole(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code != http.StatusForbidden ||
		!strings.Contains(rec.Body.String(), `"code":"forbidden_role"`) ||
		!strings.Contains(rec.Body.String(), auth.SentenceForbidden) {
		t.Fatalf("forbidden_role %d %s", rec.Code, rec.Body.String())
	}
}

func seedProviderConnectConnection(t *testing.T, db *sql.DB, house string) {
	t.Helper()
	scope, err := tenant.New(house)
	if err != nil {
		t.Fatal(err)
	}
	dir := connection.NewDirectory(db)
	if err := dir.Write(context.Background(), scope, &connection.Connection{
		Provider:    providerConnectGateID,
		State:       connection.StateConnected,
		AccessToken: "tok",
	}); err != nil {
		t.Fatal(err)
	}
}

func TestProviderConnectOwnerGateAnonAndForeign(t *testing.T) {
	e := setupProviderConnectGateEnv(t)
	state := e.mintAuthState()
	callbackURL := e.callbackURL(state, "good-code")
	seedProviderConnectConnection(t, e.db, e.house)

	for _, route := range []struct {
		name   string
		method string
		path   string
	}{
		{"authorize", http.MethodGet, e.authorizePath()},
		{"callback", http.MethodGet, callbackURL},
		{"disconnect", http.MethodDelete, e.disconnectPath()},
	} {
		t.Run(route.name, func(t *testing.T) {
			anon := e.owner.anon(route.method, route.path, nil, app.EntryPublic)
			if anon.Code != http.StatusUnauthorized || !strings.Contains(anon.Body.String(), `"code":"unauthorized"`) {
				t.Fatalf("anon %s %d %s", route.name, anon.Code, anon.Body.String())
			}
			foreign := e.owner.call(route.method, route.path, nil, app.EntryPublic, map[string]string{
				"X-Household-Id": e.foreignHouse,
			})
			if foreign.Code != http.StatusNotFound || !strings.Contains(foreign.Body.String(), `"code":"not_found"`) {
				t.Fatalf("foreign %s %d %s", route.name, foreign.Code, foreign.Body.String())
			}
		})
	}
}

func mustHouseholdScope(t *testing.T, id string) tenant.Scope {
	t.Helper()
	scope, err := tenant.New(id)
	if err != nil {
		t.Fatal(err)
	}
	return scope
}