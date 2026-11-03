package server

import (
	"context"
	"database/sql"
	"net/http"
	"strings"
	"testing"

	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/Rhionin/stockbot/internal/cart"
	"github.com/Rhionin/stockbot/internal/cart/appcred"
	"github.com/Rhionin/stockbot/internal/cart/connection"
	"github.com/Rhionin/stockbot/internal/cart/kroger"
	"github.com/Rhionin/stockbot/internal/tenant"
)

func TestListProvidersKrogerDisabledOmitsCredentialsFields(t *testing.T) {
	registry := cart.NewRegistry()
	if err := registry.Register(kroger.NewUnconfigured()); err != nil {
		t.Fatal(err)
	}
	db := openMigratedDB(t, ":memory:")
	h := &ListProvidersHandler{ProvidersHandler: ProvidersHandler{
		Registry: registry, Credentials: appcred.NewVault(db), EnvFallback: ProviderEnv{Disabled: true},
	}}
	ctx := tenant.WithScope(context.Background(), mustPrimaryScope(t, db))
	out, err := h.Handle(Request[struct{}, struct{}]{Context: ctx})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].ID != "kroger" || out[0].Status != "disabled" {
		t.Fatalf("out=%+v", out)
	}
	if out[0].ConnectionState != "" || out[0].CredentialsConfigured || out[0].Credentials != nil || out[0].Capabilities != nil {
		t.Fatalf("disabled kroger leaked cred fields: %+v", out[0])
	}
}

func TestListProvidersKrogerUsesHouseholdHasApp(t *testing.T) {
	registry := cart.NewRegistry()
	if err := registry.Register(kroger.NewUnconfigured(), cart.WithCredentialsConfigured(false)); err != nil {
		t.Fatal(err)
	}
	db := openMigratedDB(t, ":memory:")
	vault := appcred.NewVault(db)
	scope := mustPrimaryScope(t, db)
	if err := vault.Save(context.Background(), scope, appcred.Saved{
		ProviderID: "kroger", ClientID: "hh", ClientSecret: "sec", RedirectURI: "https://cb", Modality: "PICKUP",
	}); err != nil {
		t.Fatal(err)
	}
	h := &ListProvidersHandler{ProvidersHandler: ProvidersHandler{
		Registry: registry, Credentials: vault, ConnectionDir: connection.NewDirectory(db),
	}}
	ctx := tenant.WithScope(context.Background(), scope)
	out, err := h.Handle(Request[struct{}, struct{}]{Context: ctx})
	if err != nil || len(out) != 1 || !out[0].CredentialsConfigured {
		t.Fatalf("out=%+v err=%v", out, err)
	}
	if out[0].Credentials != nil {
		t.Fatalf("kroger must not expose credentials blob: %+v", out[0])
	}
}

func TestListProvidersNonKrogerIncludesCredentialView(t *testing.T) {
	db := openMigratedDB(t, ":memory:")
	registry := cart.NewRegistry()
	provider := testAppCredProvider{id: "test-appcred"}
	if err := registry.Register(provider, cart.WithCredentialsConfigured(true)); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(kroger.NewUnconfigured()); err != nil {
		t.Fatal(err)
	}
	h := &ListProvidersHandler{ProvidersHandler: ProvidersHandler{
		Registry: registry, Credentials: appcred.NewVault(db), Auth: auth.NewService(db, t.TempDir()),
		EnvFallback: ProviderEnv{ClientID: "env", ClientSecret: "sec", RedirectURI: "https://env/cb", Modality: "PICKUP"},
	}}
	ctx := tenant.WithScope(context.Background(), mustPrimaryScope(t, db))
	out, err := h.Handle(Request[struct{}, struct{}]{Context: ctx})
	if err != nil {
		t.Fatal(err)
	}
	var saw bool
	for _, info := range out {
		if info.ID == "test-appcred" {
			saw = true
			if info.Credentials == nil || info.Credentials.Source != "environment" {
				t.Fatalf("cred view=%+v", info.Credentials)
			}
		}
		if info.ID == "kroger" && info.Credentials != nil {
			t.Fatalf("kroger creds=%+v", info.Credentials)
		}
	}
	if !saw {
		t.Fatal("missing test-appcred")
	}
}

func TestProviderAuthorizeKrogerDisabled(t *testing.T) {
	registry := cart.NewRegistry()
	if err := registry.Register(kroger.NewUnconfigured()); err != nil {
		t.Fatal(err)
	}
	db := openMigratedDB(t, ":memory:")
	h := &ProviderAuthorizeHandler{ProvidersHandler: ProvidersHandler{
		Registry: registry, EnvFallback: ProviderEnv{Disabled: true}, Kroger: kroger.NewUnconfigured(),
	}}
	ctx := tenant.WithScope(context.Background(), mustPrimaryScope(t, db))
	_, err := h.Handle(Request[struct{}, ProviderAuthorizeRequest]{
		Context: ctx, PathParams: ProviderAuthorizeRequest{ProviderID: "kroger"},
	})
	var httpErr *HTTPError
	if err == nil || !errorsAsHTTP(err, &httpErr) || httpErr.Code != http.StatusConflict ||
		httpErr.ErrorCode != codeStoreDisabled {
		t.Fatalf("err=%v", err)
	}
}

func TestKrogerAdapterFallsBackToRegistry(t *testing.T) {
	registry := cart.NewRegistry()
	adapter := kroger.NewUnconfigured()
	if err := registry.Register(adapter); err != nil {
		t.Fatal(err)
	}
	h := &ProvidersHandler{Registry: registry}
	if got := h.krogerAdapter(); got == nil {
		t.Fatal("expected adapter from registry")
	}
	h2 := &ProvidersHandler{Kroger: adapter}
	if got := h2.krogerAdapter(); got != adapter {
		t.Fatal("explicit Kroger field should win")
	}
}

func TestListProvidersNilRegistryEmpty(t *testing.T) {
	h := &ListProvidersHandler{}
	out, err := h.Handle(Request[struct{}, struct{}]{Context: context.Background()})
	if err != nil || len(out) != 0 {
		t.Fatalf("out=%v err=%v", out, err)
	}
}

func TestListProvidersKrogerReflectsConnectionState(t *testing.T) {
	registry := cart.NewRegistry()
	if err := registry.Register(kroger.NewUnconfigured()); err != nil {
		t.Fatal(err)
	}
	db := openMigratedDB(t, ":memory:")
	dir := connection.NewDirectory(db)
	scope := mustPrimaryScope(t, db)
	when := "2026-01-01T00:00:00Z"
	if _, err := db.Exec(`
		INSERT INTO provider_connections (id, user_id, household_id, provider_id, state, updated_at)
		VALUES ('c1', 'u1', ?, 'kroger', 'connected', ?)`, scope.HouseholdID(), when); err != nil {
		t.Fatal(err)
	}
	h := &ListProvidersHandler{ProvidersHandler: ProvidersHandler{
		Registry: registry, Credentials: appcred.NewVault(db), ConnectionDir: dir,
	}}
	ctx := tenant.WithScope(context.Background(), scope)
	out, err := h.Handle(Request[struct{}, struct{}]{Context: ctx})
	if err != nil || len(out) != 1 || out[0].ConnectionState != "connected" {
		t.Fatalf("out=%+v err=%v", out, err)
	}
}

func TestProviderAuthorizeUnknownProvider404(t *testing.T) {
	registry := cart.NewRegistry()
	h := &ProviderAuthorizeHandler{ProvidersHandler: ProvidersHandler{Registry: registry}}
	ctx := tenant.WithScope(context.Background(), mustPrimaryScope(t, openMigratedDB(t, ":memory:")))
	_, err := h.Handle(Request[struct{}, ProviderAuthorizeRequest]{
		Context: ctx, PathParams: ProviderAuthorizeRequest{ProviderID: "missing"},
	})
	var httpErr *HTTPError
	if err == nil || !errorsAsHTTP(err, &httpErr) || httpErr.Code != http.StatusNotFound {
		t.Fatalf("err=%v", err)
	}
}

func TestKrogerAppGetShowsRedirectBeforeConfigured(t *testing.T) {
	caller, _, primary, _ := setupKrogerAppOperator(t)
	caller.house = primary
	get := caller.authed(http.MethodGet, "/api/households/"+primary+"/kroger-app", nil)
	if get.Code != http.StatusOK || !strings.Contains(get.Body.String(), `"redirectUri":"https://pantry.test/api/providers/kroger/callback"`) {
		t.Fatalf("GET before save %d %s", get.Code, get.Body.String())
	}
}

func TestCredentialViewReturnsSavedPrimaryRow(t *testing.T) {
	db := openMigratedDB(t, ":memory:")
	svc := auth.NewService(db, t.TempDir())
	registry := cart.NewRegistry()
	if err := registry.Register(testAppCredProvider{id: "test-appcred"}); err != nil {
		t.Fatal(err)
	}
	vault := appcred.NewVault(db)
	h := &ProvidersHandler{Registry: registry, Credentials: vault, Auth: svc}
	ctx := context.Background()
	scope := mustPrimaryScope(t, db)
	if err := vault.Save(ctx, scope, appcred.Saved{
		ProviderID: "test-appcred", ClientID: "saved", ClientSecret: "sec",
		RedirectURI: "https://saved.example/cb", Modality: "DELIVERY",
	}); err != nil {
		t.Fatal(err)
	}
	provider, _ := registry.Get("test-appcred")
	view, err := h.credentialView(ctx, scope, provider)
	if err != nil || view.Source != "saved" || view.ClientID != "saved" || view.Modality != "DELIVERY" {
		t.Fatalf("view=%+v err=%v", view, err)
	}
}

func TestCredentialViewNonPrimaryHouseholdReturnsNone(t *testing.T) {
	db := openMigratedDB(t, ":memory:")
	svc := auth.NewService(db, t.TempDir())
	registry := cart.NewRegistry()
	if err := registry.Register(testAppCredProvider{id: "test-appcred"}); err != nil {
		t.Fatal(err)
	}
	h := &ProvidersHandler{Registry: registry, Credentials: appcred.NewVault(db), Auth: svc}
	primary := mustPrimaryScope(t, db)
	secondary, err := tenant.New("01909999-9999-7000-8000-000000000099")
	if err != nil {
		t.Fatal(err)
	}
	provider, _ := registry.Get("test-appcred")
	view, err := h.credentialView(context.Background(), secondary, provider)
	if err != nil || view.Source != "none" {
		t.Fatalf("view=%+v err=%v", view, err)
	}
	_ = primary
}

func mustPrimaryScope(t *testing.T, db *sql.DB) tenant.Scope {
	t.Helper()
	scope, err := auth.PrimaryScope(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	return scope
}
