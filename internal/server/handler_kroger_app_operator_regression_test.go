package server

import (
	"context"
	"database/sql"
	"encoding/json"
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

func TestKrogerAppOperatorHouseholdRegression(t *testing.T) {
	db := openMigratedDB(t, filepath.Join(t.TempDir(), "kroger-app-regression.db"))
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
	operator.house = primaryHouse

	start := operator.authed(http.MethodPost, "/api/operator/household-invites", nil)
	if start.Code != http.StatusCreated {
		t.Fatalf("start invite %d %s", start.Code, start.Body.String())
	}
	var startInvite auth.InviteCreated
	if err := json.Unmarshal(start.Body.Bytes(), &startInvite); err != nil {
		t.Fatal(err)
	}
	birchRec := operator.authed(http.MethodPost, "/api/households", map[string]string{
		"name": "Household Birch", "inviteCode": startInvite.Code,
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
	if _, err := db.Exec(`INSERT INTO memberships (user_id, household_id, role, created_at)
		SELECT ?, ?, 'member', '2026-02-02T00:00:00Z' WHERE NOT EXISTS (
			SELECT 1 FROM memberships WHERE user_id = ? AND household_id = ?
		)`, beaID, primaryHouse, beaID, primaryHouse); err != nil {
		t.Fatal(err)
	}

	seedPut := map[string]string{
		"clientId": "seed-admin", "clientSecret": "seed-secret", "modality": "DELIVERY",
	}
	operator.house = primaryHouse
	if rec := operator.authed(http.MethodPut, "/api/households/"+primaryHouse+"/kroger-app", seedPut); rec.Code != http.StatusOK {
		t.Fatalf("seed primary app %d %s", rec.Code, rec.Body.String())
	}
	operator.house = birchHouse.ID
	snapDB := credentialsDBSnap(t, db)
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

	evilPut := map[string]string{"clientId": "evil-cid", "clientSecret": "evil-sec", "modality": "PICKUP"}
	assertDeniedUnchanged := func(t *testing.T, label string, c *stage1Caller, house string, method string, body any, wantCode int) {
		t.Helper()
		path := "/api/households/" + house + "/kroger-app"
		var rec *httptest.ResponseRecorder
		switch {
		case c.cookie == "":
			rec = c.anon(method, path, body, app.EntryPublic)
		case method == http.MethodDelete:
			rec = c.authed(method, path, nil)
		default:
			rec = c.authed(method, path, body)
		}
		if rec.Code != wantCode {
			t.Fatalf("%s %s code %d want %d body=%s", label, method, rec.Code, wantCode, rec.Body.String())
		}
		if credentialsDBSnap(t, db) != snapDB {
			t.Fatalf("%s %s changed DB snapshot", label, method)
		}
		if cid, ok := primarySavedClientID(t, db); !ok || cid != "seed-admin" {
			t.Fatalf("%s %s primary row changed cid=%q ok=%v", label, method, cid, ok)
		}
	}

	assertDeniedUnchanged(t, "member-birch", member, birchHouse.ID, http.MethodPut, evilPut, http.StatusForbidden)
	assertDeniedUnchanged(t, "member-birch", member, birchHouse.ID, http.MethodDelete, nil, http.StatusForbidden)
	member.house = primaryHouse
	assertDeniedUnchanged(t, "member-primary", member, primaryHouse, http.MethodPut, evilPut, http.StatusForbidden)

	anonCaller := &stage1Caller{t: t, h: handler}
	assertDeniedUnchanged(t, "anon-put", anonCaller, primaryHouse, http.MethodPut, evilPut, http.StatusUnauthorized)

	if _, err := db.Exec(`UPDATE memberships SET role = 'owner' WHERE household_id = ? AND user_id = ?`, primaryHouse, beaID); err != nil {
		t.Fatal(err)
	}
	member.house = primaryHouse
	ownerPut := member.authed(http.MethodPut, "/api/households/"+primaryHouse+"/kroger-app", map[string]string{
		"clientId": "member-owner-app", "clientSecret": "member-owner-secret", "modality": "PICKUP",
	})
	if ownerPut.Code != http.StatusOK {
		t.Fatalf("member owner primary save %d %s", ownerPut.Code, ownerPut.Body.String())
	}
	if cid, ok := primarySavedClientID(t, db); !ok || cid != "member-owner-app" {
		t.Fatalf("primary after member owner save cid=%q ok=%v", cid, ok)
	}
	operator.house = primaryHouse
	if rec := operator.authed(http.MethodPut, "/api/households/"+primaryHouse+"/kroger-app", map[string]string{
		"clientId": "seed-admin", "clientSecret": "seed-secret", "modality": "DELIVERY",
	}); rec.Code != http.StatusOK {
		t.Fatalf("operator restore primary app %d", rec.Code)
	}
	snapDB = credentialsDBSnap(t, db)

	operator.house = birchHouse.ID
	if rec := operator.authed(http.MethodPut, "/api/households/"+birchHouse.ID+"/kroger-app", map[string]string{
		"clientId": "birch-app", "clientSecret": "birch-secret", "modality": "PICKUP",
	}); rec.Code != http.StatusOK {
		t.Fatalf("operator birch app %d %s", rec.Code, rec.Body.String())
	}
	birchScope, err := tenant.New(birchHouse.ID)
	if err != nil {
		t.Fatal(err)
	}
	birchSaved, ok, err := appcred.NewVault(db).Load(context.Background(), birchScope, "kroger")
	if err != nil || !ok || birchSaved.ClientID != "birch-app" {
		t.Fatalf("birch row = %+v ok=%v err=%v", birchSaved, ok, err)
	}
	if cid, ok := primarySavedClientID(t, db); !ok || cid != "seed-admin" {
		t.Fatalf("primary after birch save cid=%q ok=%v", cid, ok)
	}

	providersMember := member.authed(http.MethodGet, "/api/providers", nil)
	if providersMember.Code != http.StatusOK {
		t.Fatalf("member GET providers %d %s", providersMember.Code, providersMember.Body.String())
	}
	body := providersMember.Body.String()
	if strings.Contains(body, "birch-app") || strings.Contains(body, "seed-admin") {
		t.Fatalf("member GET leaked client ids: %s", body)
	}
	if strings.Contains(body, `"credentials"`) {
		t.Fatalf("member GET leaked credentials blob: %s", body)
	}

	reloadKrogerAdapter(t, db, adapter)
	if got := adapter.PublicAppCredentials().ClientID; got != "seed-admin" {
		t.Fatalf("reload from primary row client id = %q", got)
	}

	if rec := operator.authed(http.MethodDelete, "/api/households/"+birchHouse.ID+"/kroger-app", nil); rec.Code != http.StatusOK {
		t.Fatalf("delete birch app %d", rec.Code)
	}
	if _, ok, err := appcred.NewVault(db).Load(context.Background(), birchScope, "kroger"); err != nil || ok {
		t.Fatalf("birch cleared ok=%v err=%v", ok, err)
	}
	if cid, ok := primarySavedClientID(t, db); !ok || cid != "seed-admin" {
		t.Fatalf("primary after birch delete cid=%q ok=%v", cid, ok)
	}
}
