package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/Rhionin/stockbot/internal/cart"
	"github.com/Rhionin/stockbot/internal/cart/appcred"
	"github.com/Rhionin/stockbot/internal/cart/kroger"
	"github.com/Rhionin/stockbot/internal/tenant"
)

func credentialsDBSnap(t *testing.T, db *sql.DB) string {
	t.Helper()
	rows, err := db.Query(`SELECT id, household_id, user_id, COALESCE(actor_user_id,'<nil>'), provider_id, client_id, redirect_uri, modality, updated_at FROM provider_app_credentials ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var b strings.Builder
	for rows.Next() {
		var c [9]string
		if err := rows.Scan(&c[0], &c[1], &c[2], &c[3], &c[4], &c[5], &c[6], &c[7], &c[8]); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintln(&b, strings.Join(c[:], "|"))
	}
	return b.String()
}

func primarySavedClientID(t *testing.T, db *sql.DB) (string, bool) {
	t.Helper()
	scope, err := auth.PrimaryScope(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	saved, ok, err := appcred.NewVault(db).Load(context.Background(), scope, "kroger")
	if err != nil {
		t.Fatal(err)
	}
	return saved.ClientID, ok
}

func TestKrogerCredentialsRequireOperator(t *testing.T) {
	t.Setenv("KROGER_CLIENT_ID", "env-client")
	t.Setenv("KROGER_CLIENT_SECRET", "env-secret")
	t.Setenv("KROGER_REDIRECT_URI", "https://env.example/cb")
	t.Setenv("KROGER_MODALITY", "PICKUP")

	db := openMigratedDB(t, filepath.Join(t.TempDir(), "kroger-creds.db"))
	setupDir := t.TempDir()
	svc := auth.NewService(db, setupDir)
	site := stage1Site(t)
	registry := cart.NewRegistry()
	adapter, err := kroger.New("env-client", "env-secret", "https://env.example/cb", "PICKUP")
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(adapter, cart.WithCredentialsConfigured(true)); err != nil {
		t.Fatal(err)
	}
	handler, _ := setupOnDB(t, db, nil,
		WithStage1(svc, site),
		WithCartRegistry(registry, cart.NewLedger(db)),
		WithProviderEnv(ProviderEnv{
			ClientID:     "env-client",
			ClientSecret: "env-secret",
			RedirectURI:  "https://env.example/cb",
			Modality:     "PICKUP",
		}),
	)
	operator := &stage1Caller{t: t, h: handler}
	if rec := operator.call(http.MethodGet, "/setup", nil, app.EntryPublic, nil); rec.Code != http.StatusOK {
		t.Fatalf("setup page %d", rec.Code)
	}
	code, err := auth.ReadSetupCode(setupDir)
	if err != nil {
		t.Fatal(err)
	}
	if rec := operator.call(http.MethodPost, "/setup", map[string]string{
		"setupCode": code, "name": "Ada Admin", "email": "ada@pantry.test", "password": "correct horse",
	}, app.EntryPublic, nil); rec.Code != http.StatusOK {
		t.Fatalf("setup %d %s", rec.Code, rec.Body.String())
	}
	login := operator.call(http.MethodPost, "/api/auth/login", map[string]string{
		"email": "ada@pantry.test", "password": "correct horse",
	}, app.EntryPublic, nil)
	if login.Code != http.StatusOK {
		t.Fatalf("login %d %s", login.Code, login.Body.String())
	}
	operator.cookie = cookieFrom(t, login, site.SessionCookie().Name)
	var opSession auth.SessionView
	if err := json.Unmarshal(login.Body.Bytes(), &opSession); err != nil || opSession.ActiveHouseholdID == nil {
		t.Fatalf("session %s", login.Body.String())
	}
	primaryHouse := *opSession.ActiveHouseholdID

	start := operator.authed(http.MethodPost, "/api/operator/household-invites", nil)
	if start.Code != http.StatusCreated {
		t.Fatalf("start invite %d %s", start.Code, start.Body.String())
	}
	var startInvite auth.InviteCreated
	if err := json.Unmarshal(start.Body.Bytes(), &startInvite); err != nil {
		t.Fatal(err)
	}
	birchRec := operator.authed(http.MethodPost, "/api/households", map[string]string{
		"name":       "Household Birch",
		"inviteCode": startInvite.Code,
	})
	if birchRec.Code != http.StatusCreated {
		t.Fatalf("create birch %d %s", birchRec.Code, birchRec.Body.String())
	}
	var birchHouse struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(birchRec.Body.Bytes(), &birchHouse); err != nil {
		t.Fatal(err)
	}
	if rec := operator.authed(http.MethodPost, "/api/auth/session/household", map[string]string{"householdId": birchHouse.ID}); rec.Code != http.StatusOK {
		t.Fatalf("switch birch %d %s", rec.Code, rec.Body.String())
	}
	operator.house = birchHouse.ID

	join := operator.authed(http.MethodPost, "/api/invites", map[string]string{"role": "member"})
	if join.Code != http.StatusCreated {
		t.Fatalf("join invite %d %s", join.Code, join.Body.String())
	}
	var joinInvite auth.InviteCreated
	if err := json.Unmarshal(join.Body.Bytes(), &joinInvite); err != nil {
		t.Fatal(err)
	}

	member := &stage1Caller{t: t, h: handler}
	accept := member.call(http.MethodPost, "/api/invites/accept", map[string]any{
		"code": joinInvite.Code,
		"account": map[string]string{
			"name": "Bea Member", "email": "bea@pantry.test", "password": "correct horse",
		},
	}, app.EntryPublic, nil)
	if accept.Code != http.StatusOK {
		t.Fatalf("accept %d %s", accept.Code, accept.Body.String())
	}
	memberLogin := member.call(http.MethodPost, "/api/auth/login", map[string]string{
		"email": "bea@pantry.test", "password": "correct horse",
	}, app.EntryPublic, nil)
	if memberLogin.Code != http.StatusOK {
		t.Fatalf("member login %d %s", memberLogin.Code, memberLogin.Body.String())
	}
	member.cookie = cookieFrom(t, memberLogin, site.SessionCookie().Name)
	member.house = birchHouse.ID

	var beaID string
	if err := db.QueryRow(`SELECT id FROM users WHERE email_norm = 'bea@pantry.test'`).Scan(&beaID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO memberships (user_id, household_id, role, created_at) VALUES (?, ?, 'member', '2026-02-02T00:00:00Z')`, beaID, primaryHouse); err != nil {
		t.Fatal(err)
	}

	seedPut := map[string]string{
		"clientId": "seed-admin", "clientSecret": "seed-secret",
		"redirectUri": "https://seed.example/cb", "modality": "DELIVERY",
	}
	if rec := operator.authed(http.MethodPut, "/api/providers/kroger/credentials", seedPut); rec.Code != http.StatusOK {
		t.Fatalf("seed admin put %d %s", rec.Code, rec.Body.String())
	}
	snapDB := credentialsDBSnap(t, db)
	liveClient := adapter.PublicAppCredentials().ClientID
	primaryScope, err := auth.PrimaryScope(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	var operatorUserID string
	if err := db.QueryRow(`SELECT id FROM users WHERE email_norm = 'ada@pantry.test'`).Scan(&operatorUserID); err != nil {
		t.Fatal(err)
	}
	var actorUserID sql.NullString
	if err := db.QueryRow(`SELECT actor_user_id FROM provider_app_credentials WHERE household_id = ? AND provider_id = 'kroger'`, primaryScope.HouseholdID()).Scan(&actorUserID); err != nil {
		t.Fatal(err)
	}
	if !actorUserID.Valid || actorUserID.String != operatorUserID {
		t.Fatalf("actor_user_id = %v want operator %q", actorUserID, operatorUserID)
	}

	evilPut := map[string]string{
		"clientId": "evil-cid", "clientSecret": "evil-sec",
		"redirectUri": "https://evil.example/cb", "modality": "PICKUP",
	}
	assertDeniedUnchanged := func(t *testing.T, label string, c *stage1Caller, method string, body any, wantCode int) {
		t.Helper()
		var rec *httptest.ResponseRecorder
		if c.cookie == "" {
			r := c.anon(method, "/api/providers/kroger/credentials", body, app.EntryPublic)
			rec = r
		} else {
			r := c.authed(method, "/api/providers/kroger/credentials", body)
			rec = r
		}
		if rec.Code != wantCode {
			t.Fatalf("%s %s code %d want %d body=%s", label, method, rec.Code, wantCode, rec.Body.String())
		}
		if credentialsDBSnap(t, db) != snapDB {
			t.Fatalf("%s %s changed DB snapshot", label, method)
		}
		if got := adapter.PublicAppCredentials().ClientID; got != liveClient {
			t.Fatalf("%s %s changed live client id %q want %q", label, method, got, liveClient)
		}
		if cid, ok := primarySavedClientID(t, db); !ok || cid != "seed-admin" {
			t.Fatalf("%s %s primary row changed cid=%q ok=%v", label, method, cid, ok)
		}
	}

	member.house = birchHouse.ID
	assertDeniedUnchanged(t, "member-birch", member, http.MethodPut, evilPut, http.StatusForbidden)
	assertDeniedUnchanged(t, "member-birch", member, http.MethodDelete, nil, http.StatusForbidden)

	if _, err := db.Exec(`UPDATE memberships SET role = 'owner' WHERE household_id = ? AND user_id = ?`, birchHouse.ID, beaID); err != nil {
		t.Fatal(err)
	}
	assertDeniedUnchanged(t, "owner-birch", member, http.MethodPut, evilPut, http.StatusForbidden)

	member.house = primaryHouse
	assertDeniedUnchanged(t, "member-primary", member, http.MethodPut, evilPut, http.StatusForbidden)
	if _, err := db.Exec(`UPDATE memberships SET role = 'owner' WHERE household_id = ? AND user_id = ?`, primaryHouse, beaID); err != nil {
		t.Fatal(err)
	}
	assertDeniedUnchanged(t, "owner-primary", member, http.MethodPut, evilPut, http.StatusForbidden)

	member.house = birchHouse.ID
	anonCaller := &stage1Caller{t: t, h: handler}
	assertDeniedUnchanged(t, "anon-put", anonCaller, http.MethodPut, evilPut, http.StatusUnauthorized)
	assertDeniedUnchanged(t, "anon-delete", anonCaller, http.MethodDelete, nil, http.StatusUnauthorized)

	operator.house = birchHouse.ID
	put2 := map[string]string{
		"clientId": "admin2-cid", "clientSecret": "admin2-sec",
		"redirectUri": "https://admin2.example/cb", "modality": "PICKUP",
	}
	if rec := operator.authed(http.MethodPut, "/api/providers/kroger/credentials", put2); rec.Code != http.StatusOK {
		t.Fatalf("admin birch-session PUT %d %s", rec.Code, rec.Body.String())
	}
	var rowHouse string
	if err := db.QueryRow(`SELECT household_id FROM provider_app_credentials WHERE provider_id = 'kroger'`).Scan(&rowHouse); err != nil {
		t.Fatal(err)
	}
	if rowHouse != primaryScope.HouseholdID() {
		t.Fatalf("credential row household %q want primary %q", rowHouse, primaryScope.HouseholdID())
	}
	if got := adapter.PublicAppCredentials().ClientID; got != "admin2-cid" {
		t.Fatalf("live client id = %q", got)
	}
	reloadKrogerAdapter(t, db, adapter)
	if got := adapter.PublicAppCredentials().ClientID; got != "admin2-cid" {
		t.Fatalf("after restart client id = %q", got)
	}

	providersMember := member.authed(http.MethodGet, "/api/providers", nil)
	if providersMember.Code != http.StatusOK {
		t.Fatalf("member GET providers %d %s", providersMember.Code, providersMember.Body.String())
	}
	body := providersMember.Body.String()
	if strings.Contains(body, `"source":"environment"`) || strings.Contains(body, "admin2-cid") || strings.Contains(body, "env-client") {
		t.Fatalf("member GET leaked configured creds: %s", body)
	}

	providersPrimary := operator.authed(http.MethodPost, "/api/auth/session/household", map[string]string{"householdId": primaryHouse})
	if providersPrimary.Code != http.StatusOK {
		t.Fatalf("switch primary %d", providersPrimary.Code)
	}
	operator.house = primaryHouse
	getPrimary := operator.authed(http.MethodGet, "/api/providers", nil)
	if getPrimary.Code != http.StatusOK || strings.Contains(getPrimary.Body.String(), `"source":"environment"`) {
		t.Fatalf("primary GET with saved row must not be environment: %s", getPrimary.Body.String())
	}
	if !strings.Contains(getPrimary.Body.String(), `"source":"saved"`) {
		t.Fatalf("primary GET want saved source: %s", getPrimary.Body.String())
	}

	operator.house = birchHouse.ID
	if rec := operator.authed(http.MethodDelete, "/api/providers/kroger/credentials", nil); rec.Code != http.StatusOK {
		t.Fatalf("admin DELETE birch session %d %s", rec.Code, rec.Body.String())
	}
	if _, ok, err := appcred.NewVault(db).Load(context.Background(), primaryScope, "kroger"); err != nil || ok {
		t.Fatalf("primary row after delete ok=%v err=%v", ok, err)
	}
	if got := adapter.PublicAppCredentials().ClientID; got != "env-client" {
		t.Fatalf("after delete live client id = %q", got)
	}
}

func TestCredentialViewNonPrimaryIgnoresLeftoverSavedRow(t *testing.T) {
	db := openMigratedDB(t, ":memory:")
	const birch = "0190aaaa-bbbb-7000-8000-0000000000c3"
	if _, err := db.Exec(`
		INSERT INTO households (id, name, restock_mode, created_at, channel_key, is_primary)
		VALUES (?, 'Household Birch', '', '2026-02-02T00:00:00Z', 'abcdef0123456789abcdef0123456789', 0)`, birch); err != nil {
		t.Fatal(err)
	}
	birchScope, err := tenant.New(birch)
	if err != nil {
		t.Fatal(err)
	}
	vault := appcred.NewVault(db)
	if err := vault.Save(context.Background(), birchScope, appcred.Saved{
		ProviderID: "kroger", ClientID: "leftover-birch", ClientSecret: "secret",
		RedirectURI: "https://birch.example/cb", Modality: "DELIVERY",
	}); err != nil {
		t.Fatal(err)
	}
	registry := cart.NewRegistry()
	adapter, err := kroger.New("env-client", "env-secret", "https://env.example/cb", "PICKUP")
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(adapter, cart.WithCredentialsConfigured(true)); err != nil {
		t.Fatal(err)
	}
	h := &ProvidersHandler{
		Registry:    registry,
		Credentials: vault,
		Auth:        auth.NewService(db, t.TempDir()),
		EnvFallback: ProviderEnv{ClientID: "env-client", ClientSecret: "env-secret", RedirectURI: "https://env.example/cb", Modality: "PICKUP"},
	}
	ctx := tenant.WithScope(context.Background(), birchScope)
	view, err := h.credentialView(ctx, birchScope, adapter)
	if err != nil {
		t.Fatal(err)
	}
	if view.Source != "none" || view.ClientID != "" || strings.Contains(view.RedirectURI, "birch") {
		t.Fatalf("view = %+v", view)
	}
}

func TestKrogerCredentialsUnauthenticated(t *testing.T) {
	db := openMigratedDB(t, filepath.Join(t.TempDir(), "creds.db"))
	svc := auth.NewService(db, t.TempDir())
	registry := cart.NewRegistry()
	if err := registry.Register(kroger.NewUnconfigured()); err != nil {
		t.Fatal(err)
	}
	handler, _ := setupOnDB(t, db, nil, WithStage1(svc, stage1Site(t)), WithCartRegistry(registry, cart.NewLedger(db)))
	c := &stage1Caller{t: t, h: handler}
	put := c.anon(http.MethodPut, "/api/providers/kroger/credentials", map[string]string{
		"clientId": "x", "clientSecret": "y", "redirectUri": "https://x.example/cb",
	}, app.EntryPublic)
	if put.Code != http.StatusUnauthorized || !strings.Contains(put.Body.String(), auth.CodeUnauthorized) {
		t.Fatalf("anon PUT %d %s", put.Code, put.Body.String())
	}
	del := c.anon(http.MethodDelete, "/api/providers/kroger/credentials", nil, app.EntryPublic)
	if del.Code != http.StatusUnauthorized || !strings.Contains(del.Body.String(), auth.CodeUnauthorized) {
		t.Fatalf("anon DELETE %d %s", del.Code, del.Body.String())
	}
	if del.Body.String() == put.Body.String() && strings.Contains(del.Body.String(), auth.CodeForbiddenOperator) {
		t.Fatal("anon DELETE must be unauthorized not forbidden_operator")
	}
}

func TestRequireServerOperator(t *testing.T) {
	if err := (&ProvidersHandler{}).requireServerOperator(context.Background()); err != nil {
		t.Fatalf("legacy without auth service: %v", err)
	}
	db := openMigratedDB(t, ":memory:")
	h := &ProvidersHandler{Auth: auth.NewService(db, t.TempDir())}
	if err := h.requireServerOperator(context.Background()); err == nil {
		t.Fatal("auth enabled without session expected unauthorized")
	}
	ctx := auth.WithPrincipal(context.Background(), auth.Principal{Kind: "account", UserID: "user-1", Operator: true})
	if err := h.requireServerOperator(ctx); err != nil {
		t.Fatalf("operator principal: %v", err)
	}
	const dbOperatorID = "user-db-operator"
	if _, err := db.Exec(`
		INSERT INTO users (id, name, email_norm, pw_hash, idp, operator, created_at)
		VALUES (?, 'Ops', 'ops@example.test', 'x', 'local', 1, '2026-04-04T00:00:00Z')`, dbOperatorID); err != nil {
		t.Fatal(err)
	}
	ctx = auth.WithPrincipal(context.Background(), auth.Principal{Kind: "account", UserID: dbOperatorID, Operator: false})
	if err := h.requireServerOperator(ctx); err != nil {
		t.Fatalf("db operator without flag: %v", err)
	}
	ctx = auth.WithPrincipal(context.Background(), auth.Principal{Kind: "account", UserID: "user-1"})
	err := h.requireServerOperator(ctx)
	var httpErr *HTTPError
	if err == nil || !errorsAsHTTP(err, &httpErr) || httpErr.Code != http.StatusForbidden || httpErr.ErrorCode != auth.CodeForbiddenOperator {
		t.Fatalf("non-operator: %v", err)
	}
	ctx = auth.WithPrincipal(context.Background(), auth.Principal{Kind: "account"})
	if err := h.requireServerOperator(ctx); err == nil {
		t.Fatal("missing user id expected unauthorized")
	}
}

func errorsAsHTTP(err error, target **HTTPError) bool {
	if err == nil {
		return false
	}
	var he *HTTPError
	if errors.As(err, &he) {
		*target = he
		return true
	}
	return false
}

func reloadKrogerAdapter(t *testing.T, db *sql.DB, adapter *kroger.Adapter) {
	t.Helper()
	saved, _, ok, err := appcred.NewVault(db).LoadServerProvider(context.Background(), "kroger")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		if err := adapter.ApplyAppCredentials("env-client", "env-secret", "https://env.example/cb", "PICKUP"); err != nil {
			t.Fatal(err)
		}
		return
	}
	if err := adapter.ApplyAppCredentials(saved.ClientID, saved.ClientSecret, saved.RedirectURI, saved.Modality); err != nil {
		t.Fatal(err)
	}
}
