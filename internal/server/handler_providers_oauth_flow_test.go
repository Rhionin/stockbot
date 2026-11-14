package server

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/Rhionin/stockbot/internal/cart"
	"github.com/Rhionin/stockbot/internal/cart/appcred"
	"github.com/Rhionin/stockbot/internal/cart/connection"
	"github.com/Rhionin/stockbot/internal/tenant"
)

type oauthAppCredProvider struct {
	testAppCredProvider
	exchangeErr error
}

func (p oauthAppCredProvider) AuthorizationScope() string { return "cart.test" }

func (p oauthAppCredProvider) RefreshAccessToken(_ context.Context, _ string) (cart.TokenSet, error) {
	return cart.TokenSet{AccessToken: "access-token", RefreshToken: "refresh-token", ExpiresIn: time.Hour, ReceiptAt: time.Now().UTC()}, nil
}

func (p oauthAppCredProvider) AuthorizationURL(state string) (string, error) {
	return "https://oauth.test/authorize?state=" + url.QueryEscape(state), nil
}

func (p oauthAppCredProvider) ExchangeCode(_ context.Context, code string) (cart.TokenSet, error) {
	if p.exchangeErr != nil {
		return cart.TokenSet{}, p.exchangeErr
	}
	if code != "good-code" {
		return cart.TokenSet{}, errOAuthTestBadCode
	}
	return cart.TokenSet{
		AccessToken:  "access-token",
		RefreshToken: "refresh-token",
		ExpiresIn:    time.Hour,
		ReceiptAt:    time.Now().UTC(),
	}, nil
}

var errOAuthTestBadCode = &oauthTestError{msg: "invalid code"}

type oauthTestError struct{ msg string }

func (e *oauthTestError) Error() string { return e.msg }

func providerOwnerTestCtx(t *testing.T, db *sql.DB) (context.Context, *auth.Service) {
	t.Helper()
	svc := auth.NewService(db, t.TempDir())
	const userID = "provider-route-owner"
	scope := mustPrimaryScope(t, db)
	when := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := db.Exec(
		`INSERT INTO memberships (household_id, user_id, role, created_at) VALUES (?, ?, 'owner', ?)`,
		scope.HouseholdID(), userID, when); err != nil {
		t.Fatal(err)
	}
	ctx := tenant.WithScope(context.Background(), scope)
	ctx = auth.WithPrincipal(ctx, auth.Principal{Kind: "account", UserID: userID})
	return ctx, svc
}

func TestProviderAuthorizeNonKrogerStartsOAuthWhenKrogerAppConfigured(t *testing.T) {
	db := openMigratedDB(t, ":memory:")
	registry := cart.NewRegistry()
	provider := oauthAppCredProvider{testAppCredProvider: testAppCredProvider{id: "oauth-shop"}}
	if err := registry.Register(provider, cart.WithCredentialsConfigured(true)); err != nil {
		t.Fatal(err)
	}
	dir := connection.NewDirectory(db)
	vault := appcred.NewVault(db)
	scope := mustPrimaryScope(t, db)
	if err := vault.Save(context.Background(), scope, appcred.Saved{
		ProviderID: "kroger", ClientID: "hh-k", ClientSecret: "sec", RedirectURI: "https://cb", Modality: "PICKUP",
	}); err != nil {
		t.Fatal(err)
	}
	ctx, svc := providerOwnerTestCtx(t, db)
	h := &ProviderAuthorizeHandler{ProvidersHandler: ProvidersHandler{
		Registry: registry, ConnectionDir: dir, Credentials: vault, Auth: svc,
	}}
	out, err := h.Handle(Request[struct{}, ProviderAuthorizeRequest]{
		Context: ctx, PathParams: ProviderAuthorizeRequest{ProviderID: "oauth-shop"},
	})
	if err != nil || out == nil || !strings.HasPrefix(out.AuthorizationURL, "https://oauth.test/authorize") {
		t.Fatalf("out=%+v err=%v", out, err)
	}
}

func TestProviderAuthorizeNonKrogerNotConfiguredConflict(t *testing.T) {
	db := openMigratedDB(t, ":memory:")
	registry := cart.NewRegistry()
	if err := registry.Register(oauthAppCredProvider{testAppCredProvider: testAppCredProvider{id: "oauth-shop"}}); err != nil {
		t.Fatal(err)
	}
	ctx, svc := providerOwnerTestCtx(t, db)
	h := &ProviderAuthorizeHandler{ProvidersHandler: ProvidersHandler{
		Registry: registry, ConnectionDir: connection.NewDirectory(db), Credentials: appcred.NewVault(db), Auth: svc,
	}}
	_, err := h.Handle(Request[struct{}, ProviderAuthorizeRequest]{
		Context: ctx, PathParams: ProviderAuthorizeRequest{ProviderID: "oauth-shop"},
	})
	var httpErr *HTTPError
	if err == nil || !errorsAsHTTP(err, &httpErr) || httpErr.Code != http.StatusConflict {
		t.Fatalf("err=%v", err)
	}
}

func TestProviderCallbackNonKrogerConnects(t *testing.T) {
	db := openMigratedDB(t, ":memory:")
	registry := cart.NewRegistry()
	if err := registry.Register(oauthAppCredProvider{testAppCredProvider: testAppCredProvider{id: "oauth-shop"}}); err != nil {
		t.Fatal(err)
	}
	dir := connection.NewDirectory(db)
	ctx, svc := providerOwnerTestCtx(t, db)
	scope, _ := tenant.FromContext(ctx)
	state := "fixed-oauth-state"
	if err := dir.GenerateAuthState(ctx, scope, "oauth-shop", state, "", ""); err != nil {
		t.Fatal(err)
	}
	raw, _ := http.NewRequest(http.MethodGet, "/api/providers/oauth-shop/callback?code=good-code&state="+state, nil)
	h := &ProviderCallbackHandler{ProvidersHandler: ProvidersHandler{
		Registry: registry, ConnectionDir: dir, Auth: svc,
	}}
	see, err := h.Handle(Request[struct{}, ProviderCallbackRequest]{
		Context: ctx, RawRequest: raw,
		PathParams: ProviderCallbackRequest{ProviderID: "oauth-shop", Code: "good-code", State: state},
	})
	if err != nil || !strings.Contains(string(see.Location), "/shopping?connected=oauth-shop") {
		t.Fatalf("see=%+v err=%v", see, err)
	}
	conn, err := dir.Read(ctx, scope, "oauth-shop")
	if err != nil || conn == nil || conn.State != connection.StateConnected || conn.AccessToken != "access-token" {
		t.Fatalf("conn=%+v err=%v", conn, err)
	}
}

func TestProviderCallbackNonKrogerProviderErrorDeclines(t *testing.T) {
	db := openMigratedDB(t, ":memory:")
	registry := cart.NewRegistry()
	if err := registry.Register(oauthAppCredProvider{testAppCredProvider: testAppCredProvider{id: "oauth-shop"}}); err != nil {
		t.Fatal(err)
	}
	dir := connection.NewDirectory(db)
	ctx, svc := providerOwnerTestCtx(t, db)
	scope, _ := tenant.FromContext(ctx)
	state := "decline-state"
	if err := dir.GenerateAuthState(ctx, scope, "oauth-shop", state, "", ""); err != nil {
		t.Fatal(err)
	}
	raw := httptest.NewRequest(http.MethodGet, "/api/providers/oauth-shop/callback?error=access_denied&state="+state, nil)
	h := &ProviderCallbackHandler{ProvidersHandler: ProvidersHandler{Registry: registry, ConnectionDir: dir, Auth: svc}}
	_, err := h.Handle(Request[struct{}, ProviderCallbackRequest]{
		Context: ctx, RawRequest: raw,
		PathParams: ProviderCallbackRequest{ProviderID: "oauth-shop", State: state},
	})
	var httpErr *HTTPError
	if err == nil || !errorsAsHTTP(err, &httpErr) || httpErr.Code != http.StatusBadRequest {
		t.Fatalf("err=%v", err)
	}
}

type authNoneProvider struct {
	id cart.ProviderID
}

func (p authNoneProvider) ID() cart.ProviderID { return p.id }
func (p authNoneProvider) DisplayName() string { return "No auth" }
func (p authNoneProvider) Capabilities() cart.Capabilities {
	return cart.Capabilities{Auth: cart.AuthNone, Delivery: cart.DeliveryServerPush,
		Confirmation: cart.ConfirmPerRequest, Mutation: cart.MutateAddOnly, Identity: cart.IdentityDerived}
}

func TestProviderAuthorizeNonKrogerBadAuthType(t *testing.T) {
	registry := cart.NewRegistry()
	if err := registry.Register(authNoneProvider{id: "no-auth-shop"}); err != nil {
		t.Fatal(err)
	}
	h := &ProviderAuthorizeHandler{ProvidersHandler: ProvidersHandler{Registry: registry}}
	ctx := tenant.WithScope(context.Background(), mustPrimaryScope(t, openMigratedDB(t, ":memory:")))
	_, err := h.Handle(Request[struct{}, ProviderAuthorizeRequest]{
		Context: ctx, PathParams: ProviderAuthorizeRequest{ProviderID: "no-auth-shop"},
	})
	var httpErr *HTTPError
	if err == nil || !errorsAsHTTP(err, &httpErr) || httpErr.Code != http.StatusBadRequest {
		t.Fatalf("err=%v", err)
	}
}

func TestProviderCallbackNonKrogerInvalidState(t *testing.T) {
	db := openMigratedDB(t, ":memory:")
	registry := cart.NewRegistry()
	if err := registry.Register(oauthAppCredProvider{testAppCredProvider: testAppCredProvider{id: "oauth-shop"}}); err != nil {
		t.Fatal(err)
	}
	dir := connection.NewDirectory(db)
	ctx, svc := providerOwnerTestCtx(t, db)
	raw := httptest.NewRequest(http.MethodGet, "/api/providers/oauth-shop/callback?code=good-code&state=wrong", nil)
	h := &ProviderCallbackHandler{ProvidersHandler: ProvidersHandler{Registry: registry, ConnectionDir: dir, Auth: svc}}
	_, err := h.Handle(Request[struct{}, ProviderCallbackRequest]{
		Context: ctx, RawRequest: raw,
		PathParams: ProviderCallbackRequest{ProviderID: "oauth-shop", Code: "good-code", State: "wrong"},
	})
	var httpErr *HTTPError
	if err == nil || !errorsAsHTTP(err, &httpErr) || httpErr.Code != http.StatusBadRequest {
		t.Fatalf("err=%v", err)
	}
}

func TestProviderCallbackNonKrogerExchangeFailure(t *testing.T) {
	db := openMigratedDB(t, ":memory:")
	registry := cart.NewRegistry()
	if err := registry.Register(oauthAppCredProvider{
		testAppCredProvider: testAppCredProvider{id: "oauth-shop"},
		exchangeErr:         errOAuthTestBadCode,
	}); err != nil {
		t.Fatal(err)
	}
	dir := connection.NewDirectory(db)
	ctx, svc := providerOwnerTestCtx(t, db)
	scope, _ := tenant.FromContext(ctx)
	state := "exchange-fail-state"
	if err := dir.GenerateAuthState(ctx, scope, "oauth-shop", state, "", ""); err != nil {
		t.Fatal(err)
	}
	raw := httptest.NewRequest(http.MethodGet, "/api/providers/oauth-shop/callback?code=bad&state="+state, nil)
	h := &ProviderCallbackHandler{ProvidersHandler: ProvidersHandler{Registry: registry, ConnectionDir: dir, Auth: svc}}
	_, err := h.Handle(Request[struct{}, ProviderCallbackRequest]{
		Context: ctx, RawRequest: raw,
		PathParams: ProviderCallbackRequest{ProviderID: "oauth-shop", Code: "bad", State: state},
	})
	var httpErr *HTTPError
	if err == nil || !errorsAsHTTP(err, &httpErr) || httpErr.Code != http.StatusBadGateway {
		t.Fatalf("err=%v", err)
	}
}
