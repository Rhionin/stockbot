package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
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
	"github.com/Rhionin/stockbot/internal/cart/kroger"
	"github.com/Rhionin/stockbot/internal/store"
)

type krogerConnectGateEnv struct {
	*providerConnectGateEnv
	httpSpy *krogerHTTPCallCounter
}

func setupKrogerConnectGateEnv(t *testing.T) *krogerConnectGateEnv {
	t.Helper()
	setupDir := t.TempDir()
	db := openMigratedDB(t, filepath.Join(t.TempDir(), "kroger-owner-gate.db"))
	svc := auth.NewService(db, setupDir)
	site := stage1Site(t)
	registry := cart.NewRegistry()
	spy := &krogerHTTPCallCounter{}
	adapter, err := kroger.New("k-client", "k-secret", "https://cb.test/kroger", "PICKUP")
	if err != nil {
		t.Fatal(err)
	}
	adapter.WithTransport(spy)
	if err := registry.Register(adapter, cart.WithCredentialsConfigured(true)); err != nil {
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
	owner.call(http.MethodPost, "/setup", map[string]string{
		"setupCode": code, "name": "Owner", "email": "kowner-gate@test", "password": "correct horse",
	}, app.EntryPublic, nil)
	login := owner.call(http.MethodPost, "/api/auth/login", map[string]string{
		"email": "kowner-gate@test", "password": "correct horse",
	}, app.EntryPublic, nil)
	owner.cookie = cookieFrom(t, login, site.SessionCookie().Name)
	var session auth.SessionView
	if err := json.Unmarshal(login.Body.Bytes(), &session); err != nil || session.ActiveHouseholdID == nil {
		t.Fatal(login.Body.String())
	}
	house := *session.ActiveHouseholdID
	owner.house = house
	seedKrogerHouseholdApp(t, db, "k-client", "k-secret", "https://cb.test/kroger")

	invite := owner.authed(http.MethodPost, "/api/invites", map[string]string{"role": "member"})
	var created auth.InviteCreated
	if err := json.Unmarshal(invite.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	member := &stage1Caller{t: t, h: handler}
	member.call(http.MethodPost, "/api/invites/accept", map[string]any{
		"code": created.Code,
		"account": map[string]string{"name": "Mem", "email": "kmember-gate@test", "password": "correct horse"},
	}, app.EntryPublic, nil)
	mLogin := member.call(http.MethodPost, "/api/auth/login", map[string]string{
		"email": "kmember-gate@test", "password": "correct horse",
	}, app.EntryPublic, nil)
	member.cookie = cookieFrom(t, mLogin, site.SessionCookie().Name)
	member.house = house

	base := &providerConnectGateEnv{
		t: t, owner: owner, member: member, db: db, house: house, foreignHouse: cedarHousehold(t, db),
	}
	return &krogerConnectGateEnv{providerConnectGateEnv: base, httpSpy: spy}
}

func (e *krogerConnectGateEnv) krogerAuthorizePath() string {
	return "/api/providers/kroger/authorize"
}

func (e *krogerConnectGateEnv) krogerCallbackURL(state, code string) string {
	q := url.Values{"state": {state}}
	if code != "" {
		q.Set("code", code)
	}
	return "/api/providers/kroger/callback?" + q.Encode()
}

func (e *krogerConnectGateEnv) mintKrogerState() string {
	e.t.Helper()
	rec := e.owner.authed(http.MethodGet, e.krogerAuthorizePath(), nil)
	if rec.Code != http.StatusOK {
		e.t.Fatalf("owner kroger authorize %d %s", rec.Code, rec.Body.String())
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

func krogerAuthState(t *testing.T, db *sql.DB, house string) string {
	t.Helper()
	var state sql.NullString
	err := db.QueryRow(
		`SELECT auth_state FROM provider_connections WHERE household_id = ? AND provider_id = 'kroger'`,
		house,
	).Scan(&state)
	if err == sql.ErrNoRows || !state.Valid {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return state.String
}

func TestKrogerProviderConnectRequiresOwner(t *testing.T) {
	e := setupKrogerConnectGateEnv(t)

	memberAuth := e.member.authed(http.MethodGet, e.krogerAuthorizePath(), nil)
	assertForbiddenRole(t, memberAuth)
	if got := krogerAuthState(t, e.db, e.house); got != "" {
		t.Fatalf("member authorize minted kroger state %q", got)
	}

	ownerAuth := e.owner.authed(http.MethodGet, e.krogerAuthorizePath(), nil)
	if ownerAuth.Code != http.StatusOK || !strings.Contains(ownerAuth.Body.String(), "authorizationUrl") {
		t.Fatalf("owner kroger authorize %d %s", ownerAuth.Code, ownerAuth.Body.String())
	}

	state := e.mintKrogerState()
	callbackURL := e.krogerCallbackURL(state, "good-code")
	connBefore, err := connection.NewDirectory(e.db).Read(context.Background(), mustHouseholdScope(t, e.house), "kroger")
	if err != nil {
		t.Fatal(err)
	}
	e.httpSpy.calls = 0
	memberCb := e.member.authed(http.MethodGet, callbackURL, nil)
	if memberCb.Code != http.StatusSeeOther || !strings.Contains(memberCb.Header().Get("Location"), "/shopping?kroger=not_allowed") {
		t.Fatalf("member kroger callback %d loc=%q", memberCb.Code, memberCb.Header().Get("Location"))
	}
	if got := krogerAuthState(t, e.db, e.house); got != "" {
		t.Fatalf("member kroger callback left auth state %q", got)
	}
	if e.httpSpy.calls != 0 {
		t.Fatalf("member kroger callback made %d HTTP calls", e.httpSpy.calls)
	}
	connAfter, err := connection.NewDirectory(e.db).Read(context.Background(), mustHouseholdScope(t, e.house), "kroger")
	if err != nil || connBefore == nil || connAfter == nil || connAfter.State != connBefore.State {
		t.Fatalf("connection changed before=%+v after=%+v err=%v", connBefore, connAfter, err)
	}

	ownerReplay := e.owner.authed(http.MethodGet, callbackURL, nil)
	if ownerReplay.Code != http.StatusSeeOther || !strings.Contains(ownerReplay.Header().Get("Location"), "kroger=failed") {
		t.Fatalf("owner kroger replay %d loc=%q", ownerReplay.Code, ownerReplay.Header().Get("Location"))
	}
}

func TestKrogerAuthorizeMemberForbiddenBeforeStoreDisabled(t *testing.T) {
	setupDir := t.TempDir()
	db := openMigratedDB(t, filepath.Join(t.TempDir(), "kroger-dis-gate.db"))
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
		t.Fatalf("setup page %d", rec.Code)
	}
	code, err := auth.ReadSetupCode(setupDir)
	if err != nil {
		t.Fatal(err)
	}
	owner.call(http.MethodPost, "/setup", map[string]string{
		"setupCode": code, "name": "Owner", "email": "own-dis@test", "password": "correct horse",
	}, app.EntryPublic, nil)
	login := owner.call(http.MethodPost, "/api/auth/login", map[string]string{
		"email": "own-dis@test", "password": "correct horse",
	}, app.EntryPublic, nil)
	owner.cookie = cookieFrom(t, login, site.SessionCookie().Name)
	var session auth.SessionView
	if err := json.Unmarshal(login.Body.Bytes(), &session); err != nil || session.ActiveHouseholdID == nil {
		t.Fatal(login.Body.String())
	}
	house := *session.ActiveHouseholdID
	owner.house = house
	invite := owner.authed(http.MethodPost, "/api/invites", map[string]string{"role": "member"})
	var created auth.InviteCreated
	if err := json.Unmarshal(invite.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	member := &stage1Caller{t: t, h: handler}
	member.call(http.MethodPost, "/api/invites/accept", map[string]any{
		"code": created.Code,
		"account": map[string]string{"name": "Mem", "email": "mem-dis@test", "password": "correct horse"},
	}, app.EntryPublic, nil)
	mLogin := member.call(http.MethodPost, "/api/auth/login", map[string]string{
		"email": "mem-dis@test", "password": "correct horse",
	}, app.EntryPublic, nil)
	member.cookie = cookieFrom(t, mLogin, site.SessionCookie().Name)
	member.house = house

	memberAuth := member.authed(http.MethodGet, "/api/providers/kroger/authorize", nil)
	assertForbiddenRole(t, memberAuth)
	if strings.Contains(memberAuth.Body.String(), `"code":"store_disabled"`) {
		t.Fatalf("member should get forbidden_role before store_disabled: %s", memberAuth.Body.String())
	}
}

func TestProviderConnectMembershipLookupError(t *testing.T) {
	e := setupProviderConnectGateEnv(t)
	req := stage1Request(http.MethodGet, e.authorizePath(), e.owner.cookie, nil, app.EntryPublic, "")
	req.Header.Set("X-Household-Id", e.house)
	req = req.WithContext(auth.WithMembershipHook(req.Context(), func(context.Context, string, string) (string, string, bool, error) {
		return "", "", false, errors.New("membership lookup failed")
	}))
	rec := httptest.NewRecorder()
	e.owner.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("authorize on membership error %d %s", rec.Code, rec.Body.String())
	}
	if got := providerConnectAuthState(t, e.db, e.house); got != "" {
		t.Fatalf("membership error minted auth state %q", got)
	}
}

func TestStoresCanConnectFalseForMember(t *testing.T) {
	e := setupKrogerConnectGateEnv(t)
	ownerList := e.owner.authed(http.MethodGet, "/api/stores", nil)
	if ownerList.Code != http.StatusOK || !strings.Contains(ownerList.Body.String(), `"canConnect":true`) {
		t.Fatalf("owner stores %d %s", ownerList.Code, ownerList.Body.String())
	}
	memberList := e.member.authed(http.MethodGet, "/api/stores", nil)
	if memberList.Code != http.StatusOK || strings.Contains(memberList.Body.String(), `"canConnect":true`) {
		t.Fatalf("member stores %d %s", memberList.Code, memberList.Body.String())
	}
}

func TestStoreCatalogCanConnectHonorsOwnerRole(t *testing.T) {
	db := openMigratedDB(t, ":memory:")
	svc := auth.NewService(db, t.TempDir())
	catalog := store.NewCatalog(db)
	scope := primaryScope(t, db)
	const userID = "store-owner"
	when := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := db.Exec(`INSERT INTO memberships (household_id, user_id, role, created_at) VALUES (?, ?, 'owner', ?)`,
		scope.HouseholdID(), userID, when); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO provider_app_credentials (
			id, user_id, household_id, provider_id, client_id, client_secret, redirect_uri, updated_at
		) VALUES (?, 'user-1', ?, 'kroger', 'cid', 'sec', 'https://example.test/cb', ?)`,
		scope.HouseholdID()+":kroger", scope.HouseholdID(), when); err != nil {
		t.Fatal(err)
	}
	ownerCtx := auth.WithPrincipal(context.Background(), auth.Principal{Kind: "account", UserID: userID})
	out, err := catalog.List(ownerCtx, scope, store.CheckingContext{Auth: svc})
	if err != nil || !out.CanConnect {
		t.Fatalf("owner canConnect=%v err=%v", out.CanConnect, err)
	}
	if _, err := db.Exec(`INSERT INTO memberships (household_id, user_id, role, created_at) VALUES (?, 'mem-1', 'member', ?)`,
		scope.HouseholdID(), when); err != nil {
		t.Fatal(err)
	}
	memberCtx := auth.WithPrincipal(context.Background(), auth.Principal{Kind: "account", UserID: "mem-1"})
	out, err = catalog.List(memberCtx, scope, store.CheckingContext{Auth: svc})
	if err != nil || out.CanConnect {
		t.Fatalf("member canConnect=%v err=%v", out.CanConnect, err)
	}
}