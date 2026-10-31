package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
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
	var memberSession auth.SessionView
	if err := json.Unmarshal(memberLogin.Body.Bytes(), &memberSession); err != nil || memberSession.ActiveHouseholdID == nil {
		t.Fatal("member session")
	}
	member.house = *memberSession.ActiveHouseholdID

	before := adapter.PublicAppCredentials().ClientID
	putBody := map[string]string{
		"clientId": "birch-ui", "clientSecret": "birch-secret",
		"redirectUri": "https://birch.example/cb", "modality": "DELIVERY",
	}
	denied := member.authed(http.MethodPut, "/api/providers/kroger/credentials", putBody)
	if denied.Code != http.StatusForbidden ||
		!strings.Contains(denied.Body.String(), auth.CodeForbiddenOperator) ||
		!strings.Contains(denied.Body.String(), auth.SentenceForbiddenOperator) {
		t.Fatalf("member PUT %d %s", denied.Code, denied.Body.String())
	}
	if strings.Contains(denied.Body.String(), primaryHouse) {
		t.Fatalf("403 leaked primary household id")
	}

	delDenied := member.authed(http.MethodDelete, "/api/providers/kroger/credentials", nil)
	if delDenied.Code != http.StatusForbidden || !strings.Contains(delDenied.Body.String(), auth.CodeForbiddenOperator) {
		t.Fatalf("member DELETE %d %s", delDenied.Code, delDenied.Body.String())
	}

	if after := adapter.PublicAppCredentials().ClientID; after != before {
		t.Fatalf("live client id changed %q -> %q", before, after)
	}
	birchScope, err := tenant.New(birchHouse.ID)
	if err != nil {
		t.Fatal(err)
	}
	saved, ok, err := appcred.NewVault(db).Load(context.Background(), birchScope, "kroger")
	if err != nil || ok || saved.ClientID != "" {
		t.Fatalf("birch row written ok=%v err=%v saved=%+v", ok, err, saved)
	}

	primaryScope, err := auth.PrimaryScope(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	birchAdminPut := operator.authed(http.MethodPut, "/api/providers/kroger/credentials", map[string]string{
		"clientId": "birch-session-admin", "clientSecret": "admin-secret",
		"redirectUri": "https://birch-session.example/cb", "modality": "DELIVERY",
	})
	if birchAdminPut.Code != http.StatusOK {
		t.Fatalf("admin PUT on birch session %d %s", birchAdminPut.Code, birchAdminPut.Body.String())
	}
	savedPrimary, ok, err := appcred.NewVault(db).Load(context.Background(), primaryScope, "kroger")
	if err != nil || !ok || savedPrimary.ClientID != "birch-session-admin" {
		t.Fatalf("primary row after birch-session PUT ok=%v err=%v saved=%+v", ok, err, savedPrimary)
	}
	if saved, ok, err := appcred.NewVault(db).Load(context.Background(), birchScope, "kroger"); err != nil || ok || saved.ClientID != "" {
		t.Fatalf("birch household row ok=%v err=%v saved=%+v", ok, err, saved)
	}
	if got := adapter.PublicAppCredentials().ClientID; got != "birch-session-admin" {
		t.Fatalf("live client id = %q", got)
	}
	reloadKrogerAdapter(t, db, adapter)
	if got := adapter.PublicAppCredentials().ClientID; got != "birch-session-admin" {
		t.Fatalf("after restart client id = %q", got)
	}
	providersMember := member.authed(http.MethodGet, "/api/providers", nil)
	if providersMember.Code != http.StatusOK {
		t.Fatalf("member GET providers %d %s", providersMember.Code, providersMember.Body.String())
	}
	if strings.Contains(providersMember.Body.String(), `"source":"environment"`) {
		t.Fatalf("member providers leaked environment source: %s", providersMember.Body.String())
	}

	birchDel := operator.authed(http.MethodDelete, "/api/providers/kroger/credentials", nil)
	if birchDel.Code != http.StatusOK {
		t.Fatalf("admin DELETE on birch session %d %s", birchDel.Code, birchDel.Body.String())
	}
	if _, ok, err := appcred.NewVault(db).Load(context.Background(), primaryScope, "kroger"); err != nil || ok {
		t.Fatalf("primary row after birch-session DELETE ok=%v err=%v", ok, err)
	}
	if got := adapter.PublicAppCredentials().ClientID; got != "env-client" {
		t.Fatalf("after birch-session delete live client id = %q", got)
	}

	reloadKrogerAdapter(t, db, adapter)
	if got := adapter.PublicAppCredentials().ClientID; got != before {
		t.Fatalf("after restart client id = %q want %q", got, before)
	}

	if rec := operator.authed(http.MethodPost, "/api/auth/session/household", map[string]string{"householdId": primaryHouse}); rec.Code != http.StatusOK {
		t.Fatalf("switch primary %d %s", rec.Code, rec.Body.String())
	}
	adminPut := operator.authed(http.MethodPut, "/api/providers/kroger/credentials", map[string]string{
		"clientId": "admin-ui", "clientSecret": "admin-secret",
		"redirectUri": "https://admin.example/cb", "modality": "DELIVERY",
	})
	if adminPut.Code != http.StatusOK {
		t.Fatalf("admin PUT %d %s", adminPut.Code, adminPut.Body.String())
	}
	if got := adapter.PublicAppCredentials().ClientID; got != "admin-ui" {
		t.Fatalf("live client id = %q", got)
	}
	reloadKrogerAdapter(t, db, adapter)
	if got := adapter.PublicAppCredentials().ClientID; got != "admin-ui" {
		t.Fatalf("after restart client id = %q", got)
	}

	adminDel := operator.authed(http.MethodDelete, "/api/providers/kroger/credentials", nil)
	if adminDel.Code != http.StatusOK || !strings.Contains(adminDel.Body.String(), `"source":"environment"`) {
		t.Fatalf("admin DELETE %d %s", adminDel.Code, adminDel.Body.String())
	}
	if got := adapter.PublicAppCredentials().ClientID; got != "env-client" {
		t.Fatalf("after delete live client id = %q", got)
	}
	reloadKrogerAdapter(t, db, adapter)
	if got := adapter.PublicAppCredentials().ClientID; got != "env-client" {
		t.Fatalf("after restart env client id = %q", got)
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
	rec := c.anon(http.MethodPut, "/api/providers/kroger/credentials", map[string]string{
		"clientId": "x", "clientSecret": "y", "redirectUri": "https://x.example/cb",
	}, app.EntryPublic)
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), auth.CodeUnauthorized) {
		t.Fatalf("anon PUT %d %s", rec.Code, rec.Body.String())
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
