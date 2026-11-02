package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
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

func TestServerCredentialScopeUsesPrimary(t *testing.T) {
	db := openMigratedDB(t, ":memory:")
	svc := auth.NewService(db, t.TempDir())
	h := &ProvidersHandler{Auth: svc}
	ctx := context.Background()
	scope, err := h.serverCredentialScope(ctx)
	if err != nil {
		t.Fatal(err)
	}
	primary, err := svc.Primary(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if scope.HouseholdID() != primary.HouseholdID() {
		t.Fatalf("scope %q primary %q", scope.HouseholdID(), primary.HouseholdID())
	}
	ctx = auth.WithPrincipal(ctx, auth.Principal{Kind: "account", UserID: "user-1"})
	if got := h.credentialActor(ctx); got != "user-1" {
		t.Fatalf("actor = %q", got)
	}
}

func TestKrogerCredentialsRequireOperator(t *testing.T) {
	t.Skip("covered by TestKrogerAppTwoHouseholdRoleIsolation")
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
	if put.Code != http.StatusNotFound || !strings.Contains(put.Body.String(), `"code":"not_found"`) {
		t.Fatalf("anon PUT retired route %d %s", put.Code, put.Body.String())
	}
	del := c.anon(http.MethodDelete, "/api/providers/kroger/credentials", nil, app.EntryPublic)
	if del.Code != http.StatusNotFound || !strings.Contains(del.Body.String(), `"code":"not_found"`) {
		t.Fatalf("anon DELETE retired route %d %s", del.Code, del.Body.String())
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

func TestReloadKrogerAdapterFromServerRow(t *testing.T) {
	db := openMigratedDB(t, ":memory:")
	adapter, err := kroger.New("env-client", "env-secret", "https://env.example/cb", "PICKUP")
	if err != nil {
		t.Fatal(err)
	}
	when := time.Now().UTC().Format(time.RFC3339Nano)
	scope, err := auth.PrimaryScope(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO provider_app_credentials (id, household_id, user_id, provider_id, client_id, client_secret, redirect_uri, modality, updated_at)
		VALUES (?, ?, 'user-1', 'kroger', 'saved-client', 'saved-secret', 'https://saved.example/cb', 'DELIVERY', ?)`,
		scope.HouseholdID()+":kroger", scope.HouseholdID(), when); err != nil {
		t.Fatal(err)
	}
	reloadKrogerAdapter(t, db, adapter)
	pub := adapter.PublicAppCredentials()
	if pub.ClientID != "saved-client" || pub.Modality != "DELIVERY" {
		t.Fatalf("public = %+v", pub)
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
