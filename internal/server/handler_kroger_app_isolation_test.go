package server

import (
	"context"
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

func TestKrogerAppTwoHouseholdRoleIsolation(t *testing.T) {
	db := openMigratedDB(t, filepath.Join(t.TempDir(), "kroger-isolation.db"))
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
		t.Fatal(rec.Code)
	}
	code, err := auth.ReadSetupCode(setupDir)
	if err != nil {
		t.Fatal(err)
	}
	operator.call(http.MethodPost, "/setup", map[string]string{
		"setupCode": code, "name": "Ada Admin", "email": "ada@pantry.test", "password": "correct horse",
	}, app.EntryPublic, nil)
	login := operator.call(http.MethodPost, "/api/auth/login", map[string]string{
		"email": "ada@pantry.test", "password": "correct horse",
	}, app.EntryPublic, nil)
	operator.cookie = cookieFrom(t, login, site.SessionCookie().Name)
	var opSession auth.SessionView
	if err := json.Unmarshal(login.Body.Bytes(), &opSession); err != nil || opSession.ActiveHouseholdID == nil {
		t.Fatal(login.Body.String())
	}
	primaryHouse := *opSession.ActiveHouseholdID
	operator.house = primaryHouse

	if rec := operator.authed(http.MethodPut, "/api/households/"+primaryHouse+"/kroger-app", map[string]string{
		"clientId": "seed-primary", "clientSecret": "seed-secret", "modality": "DELIVERY",
	}); rec.Code != http.StatusOK {
		t.Fatalf("seed primary %d %s", rec.Code, rec.Body.String())
	}
	snap := credentialsDBSnap(t, db)

	start := operator.authed(http.MethodPost, "/api/operator/household-invites", nil)
	if start.Code != http.StatusCreated {
		t.Fatal(start.Code)
	}
	var startInvite auth.InviteCreated
	if err := json.Unmarshal(start.Body.Bytes(), &startInvite); err != nil {
		t.Fatal(err)
	}
	birchRec := operator.authed(http.MethodPost, "/api/households", map[string]string{
		"name": "Household Birch", "inviteCode": startInvite.Code,
	})
	var birchHouse struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(birchRec.Body.Bytes(), &birchHouse); err != nil {
		t.Fatal(err)
	}
	operator.authed(http.MethodPost, "/api/auth/session/household", map[string]string{"householdId": birchHouse.ID})
	join := operator.authed(http.MethodPost, "/api/invites", map[string]string{"role": "member"})
	var joinInvite auth.InviteCreated
	if err := json.Unmarshal(join.Body.Bytes(), &joinInvite); err != nil {
		t.Fatal(err)
	}

	member := &stage1Caller{t: t, h: handler}
	accept := member.call(http.MethodPost, "/api/invites/accept", map[string]any{
		"code":    joinInvite.Code,
		"account": map[string]string{"name": "Bea", "email": "bea@pantry.test", "password": "correct horse"},
	}, app.EntryPublic, nil)
	if accept.Code != http.StatusOK {
		t.Fatal(accept.Body.String())
	}
	memberLogin := member.call(http.MethodPost, "/api/auth/login", map[string]string{
		"email": "bea@pantry.test", "password": "correct horse",
	}, app.EntryPublic, nil)
	member.cookie = cookieFrom(t, memberLogin, site.SessionCookie().Name)
	member.house = birchHouse.ID

	assertSnapUnchanged := func(t *testing.T, label string, c *stage1Caller, house string, method string, body any, want int) {
		t.Helper()
		path := "/api/households/" + house + "/kroger-app"
		var rec *httptest.ResponseRecorder
		switch method {
		case http.MethodDelete:
			rec = c.authed(method, path, nil)
		default:
			rec = c.authed(method, path, body)
		}
		if rec.Code != want {
			t.Fatalf("%s %s %d %s", label, method, rec.Code, rec.Body.String())
		}
		if credentialsDBSnap(t, db) != snap {
			t.Fatalf("%s changed DB", label)
		}
	}

	evil := map[string]string{"clientId": "evil", "clientSecret": "evil", "modality": "PICKUP"}
	assertSnapUnchanged(t, "member-birch", member, primaryHouse, http.MethodPut, evil, http.StatusNotFound)
	member.house = primaryHouse
	assertSnapUnchanged(t, "member-primary", member, primaryHouse, http.MethodPut, evil, http.StatusForbidden)

	operator.house = birchHouse.ID
	if rec := operator.authed(http.MethodPut, "/api/households/"+birchHouse.ID+"/kroger-app", map[string]string{
		"clientId": "birch-app", "clientSecret": "birch-secret", "modality": "PICKUP",
	}); rec.Code != http.StatusOK {
		t.Fatalf("birch save %d %s", rec.Code, rec.Body.String())
	}
	primaryScope, err := auth.PrimaryScope(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	birchScope, err := tenant.New(birchHouse.ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved, ok, err := appcred.NewVault(db).Load(context.Background(), primaryScope, "kroger"); err != nil || !ok || saved.ClientID != "seed-primary" {
		t.Fatalf("primary row = %+v ok=%v err=%v", saved, ok, err)
	}
	if saved, ok, err := appcred.NewVault(db).Load(context.Background(), birchScope, "kroger"); err != nil || !ok || saved.ClientID != "birch-app" {
		t.Fatalf("birch row = %+v ok=%v err=%v", saved, ok, err)
	}

	member.house = birchHouse.ID
	providers := member.authed(http.MethodGet, "/api/providers", nil)
	if strings.Contains(providers.Body.String(), "birch-app") || strings.Contains(providers.Body.String(), "seed-primary") {
		t.Fatalf("providers leaked ids: %s", providers.Body.String())
	}

	operator.house = birchHouse.ID
	if rec := operator.authed(http.MethodDelete, "/api/households/"+birchHouse.ID+"/kroger-app", nil); rec.Code != http.StatusOK {
		t.Fatalf("delete birch %d", rec.Code)
	}
	if _, ok, err := appcred.NewVault(db).Load(context.Background(), birchScope, "kroger"); err != nil || ok {
		t.Fatalf("birch after delete ok=%v err=%v", ok, err)
	}
	if _, ok, err := appcred.NewVault(db).Load(context.Background(), primaryScope, "kroger"); err != nil || !ok {
		t.Fatalf("primary after birch delete ok=%v err=%v", ok, err)
	}
}
