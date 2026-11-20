package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/Rhionin/stockbot/internal/cart"
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