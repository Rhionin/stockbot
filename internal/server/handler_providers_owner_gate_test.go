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

func TestProviderConnectRequiresOwner(t *testing.T) {
	routes := []struct {
		name string
		run  func(t *testing.T, e *providerConnectGateEnv)
	}{
		{name: "authorize", run: testProviderConnectOwnerGateAuthorize},
		{name: "callback", run: testProviderConnectOwnerGateCallback},
		{name: "disconnect", run: testProviderConnectOwnerGateDisconnect},
	}
	for _, route := range routes {
		t.Run(route.name, func(t *testing.T) {
			route.run(t, setupProviderConnectGateEnv(t))
		})
	}
}

func testProviderConnectOwnerGateAuthorize(t *testing.T, e *providerConnectGateEnv) {
	t.Helper()
	path := e.authorizePath()

	member := e.member.authed(http.MethodGet, path, nil)
	assertForbiddenRole(t, member)
	if got := providerConnectAuthState(t, e.db, e.house); got != "" {
		t.Fatalf("member authorize minted auth state %q", got)
	}

	anon := e.owner.anon(http.MethodGet, path, nil, app.EntryPublic)
	if anon.Code != http.StatusUnauthorized || !strings.Contains(anon.Body.String(), `"code":"unauthorized"`) {
		t.Fatalf("anon authorize %d %s", anon.Code, anon.Body.String())
	}

	foreign := e.owner.call(http.MethodGet, path, nil, app.EntryPublic, map[string]string{
		"X-Household-Id": e.foreignHouse,
	})
	if foreign.Code != http.StatusNotFound || !strings.Contains(foreign.Body.String(), `"code":"not_found"`) {
		t.Fatalf("foreign authorize %d %s", foreign.Code, foreign.Body.String())
	}

	owner := e.owner.authed(http.MethodGet, path, nil)
	if owner.Code != http.StatusOK || !strings.Contains(owner.Body.String(), "authorizationUrl") {
		t.Fatalf("owner authorize %d %s", owner.Code, owner.Body.String())
	}
}

func testProviderConnectOwnerGateCallback(t *testing.T, e *providerConnectGateEnv) {
	t.Helper()
	state := e.mintAuthState()
	url := e.callbackURL(state, "good-code")

	connBefore, err := connection.NewDirectory(e.db).Read(context.Background(), mustHouseholdScope(t, e.house), providerConnectGateID)
	if err != nil {
		t.Fatal(err)
	}
	member := e.member.authed(http.MethodGet, url, nil)
	if member.Code != http.StatusSeeOther ||
		!strings.Contains(member.Header().Get("Location"), "/shopping?"+providerConnectGateID+"=not_allowed") {
		t.Fatalf("member callback %d loc=%q", member.Code, member.Header().Get("Location"))
	}
	if got := providerConnectAuthState(t, e.db, e.house); got != "" {
		t.Fatalf("member callback left auth state %q", got)
	}
	connAfter, err := connection.NewDirectory(e.db).Read(context.Background(), mustHouseholdScope(t, e.house), providerConnectGateID)
	if err != nil || connBefore == nil || connAfter == nil || connAfter.State != connBefore.State {
		t.Fatalf("member callback changed connection before=%+v after=%+v err=%v", connBefore, connAfter, err)
	}

	ownerAfterMember := e.owner.authed(http.MethodGet, url, nil)
	if ownerAfterMember.Code != http.StatusBadRequest ||
		!strings.Contains(ownerAfterMember.Body.String(), "invalid or expired authorization state") {
		t.Fatalf("owner replay after member denial %d %s", ownerAfterMember.Code, ownerAfterMember.Body.String())
	}

	e2 := setupProviderConnectGateEnv(t)
	state = e2.mintAuthState()
	url = e2.callbackURL(state, "good-code")

	anon := e2.owner.anon(http.MethodGet, url, nil, app.EntryPublic)
	if anon.Code != http.StatusUnauthorized || !strings.Contains(anon.Body.String(), `"code":"unauthorized"`) {
		t.Fatalf("anon callback %d %s", anon.Code, anon.Body.String())
	}

	foreign := e2.owner.call(http.MethodGet, url, nil, app.EntryPublic, map[string]string{
		"X-Household-Id": e2.foreignHouse,
	})
	if foreign.Code != http.StatusNotFound || !strings.Contains(foreign.Body.String(), `"code":"not_found"`) {
		t.Fatalf("foreign callback %d %s", foreign.Code, foreign.Body.String())
	}

	owner := e2.owner.authed(http.MethodGet, url, nil)
	if owner.Code != http.StatusSeeOther ||
		!strings.Contains(owner.Header().Get("Location"), "/shopping?connected="+providerConnectGateID) {
		t.Fatalf("owner callback %d loc=%q", owner.Code, owner.Header().Get("Location"))
	}
}

func testProviderConnectOwnerGateDisconnect(t *testing.T, e *providerConnectGateEnv) {
	t.Helper()
	seedProviderConnectConnection(t, e.db, e.house)
	path := e.disconnectPath()

	member := e.member.authed(http.MethodDelete, path, nil)
	assertForbiddenRole(t, member)
	conn, err := connection.NewDirectory(e.db).Read(context.Background(), mustHouseholdScope(t, e.house), providerConnectGateID)
	if err != nil || conn == nil || conn.State != connection.StateConnected {
		t.Fatalf("member disconnect cleared connection conn=%+v err=%v", conn, err)
	}

	anon := e.owner.anon(http.MethodDelete, path, nil, app.EntryPublic)
	if anon.Code != http.StatusUnauthorized || !strings.Contains(anon.Body.String(), `"code":"unauthorized"`) {
		t.Fatalf("anon disconnect %d %s", anon.Code, anon.Body.String())
	}

	foreign := e.owner.call(http.MethodDelete, path, nil, app.EntryPublic, map[string]string{
		"X-Household-Id": e.foreignHouse,
	})
	if foreign.Code != http.StatusNotFound || !strings.Contains(foreign.Body.String(), `"code":"not_found"`) {
		t.Fatalf("foreign disconnect %d %s", foreign.Code, foreign.Body.String())
	}

	owner := e.owner.authed(http.MethodDelete, path, nil)
	if owner.Code != http.StatusCreated {
		t.Fatalf("owner disconnect %d %s", owner.Code, owner.Body.String())
	}
	conn, err = connection.NewDirectory(e.db).Read(context.Background(), mustHouseholdScope(t, e.house), providerConnectGateID)
	if err != nil || conn == nil || conn.State != connection.StateDisconnected {
		t.Fatalf("owner disconnect did not disconnect conn=%+v err=%v", conn, err)
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