package server

import (
	"context"
	"net/http"
	"testing"

	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/Rhionin/stockbot/internal/cart"
	"github.com/Rhionin/stockbot/internal/cart/appcred"
)

type testAppCredProvider struct {
	id cart.ProviderID
}

func (p testAppCredProvider) ID() cart.ProviderID { return p.id }
func (p testAppCredProvider) DisplayName() string { return "Test app cred" }
func (p testAppCredProvider) Capabilities() cart.Capabilities {
	return cart.Capabilities{Auth: cart.AuthOAuth2, Delivery: cart.DeliveryServerPush,
		Confirmation: cart.ConfirmPerRequest, Mutation: cart.MutateAddOnly, Identity: cart.IdentityDerived}
}
func (p testAppCredProvider) ApplyAppCredentials(clientID, clientSecret, redirectURI, modality string) error {
	return nil
}
func (p testAppCredProvider) ClearAppCredentials() {}
func (p testAppCredProvider) PublicAppCredentials() cart.AppCredentials {
	return cart.AppCredentials{ClientID: "test-client", RedirectURI: "https://test.example/cb", Modality: "PICKUP", SecretSet: true}
}

func TestNonKrogerProviderCredentialPutAndDelete(t *testing.T) {
	db := openMigratedDB(t, ":memory:")
	svc := auth.NewService(db, t.TempDir())
	registry := cart.NewRegistry()
	provider := testAppCredProvider{id: "test-appcred"}
	if err := registry.Register(provider, cart.WithCredentialsConfigured(false)); err != nil {
		t.Fatal(err)
	}
	vault := appcred.NewVault(db)
	h := &ProvidersHandler{
		Registry:    registry,
		Credentials: vault,
		Auth:        svc,
		EnvFallback: ProviderEnv{ClientID: "env", ClientSecret: "env", RedirectURI: "https://env.example/cb", Modality: "PICKUP"},
	}
	ctx := auth.WithPrincipal(context.Background(), auth.Principal{Kind: "account", UserID: "op-1", Operator: true})
	put := &ProviderCredentialPutHandler{ProvidersHandler: *h}
	view, err := put.Handle(Request[providerCredentialBody, providerCredentialParams]{
		Context: ctx,
		Body: providerCredentialBody{
			ClientID: "cid", ClientSecret: "sec", RedirectURI: "https://example.test/cb", Modality: "PICKUP",
		},
		PathParams: providerCredentialParams{ProviderID: "test-appcred"},
	})
	if err != nil {
		t.Fatalf("PUT: %v", err)
	}
	if view.ClientID != "cid" || view.Source != "saved" {
		t.Fatalf("view = %+v", view)
	}
	if !registry.CredentialsConfigured("test-appcred") {
		t.Fatal("registry not configured after PUT")
	}
	del := &ProviderCredentialDeleteHandler{ProvidersHandler: *h}
	out, err := del.Handle(Request[struct{}, providerCredentialParams]{
		Context:    ctx,
		PathParams: providerCredentialParams{ProviderID: "test-appcred"},
	})
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	if out.Source != "none" {
		t.Fatalf("after delete view = %+v", out)
	}
	scope, err := svc.Primary(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := vault.Load(ctx, scope, "test-appcred"); err != nil || ok {
		t.Fatalf("row should be cleared ok=%v err=%v", ok, err)
	}
}

type plainProvider struct {
	id cart.ProviderID
}

func (p plainProvider) ID() cart.ProviderID { return p.id }
func (p plainProvider) DisplayName() string { return "Plain" }
func (p plainProvider) Capabilities() cart.Capabilities {
	return cart.Capabilities{Auth: cart.AuthOAuth2, Delivery: cart.DeliveryServerPush,
		Confirmation: cart.ConfirmPerRequest, Mutation: cart.MutateAddOnly, Identity: cart.IdentityDerived}
}

func TestProviderCredentialPutKrogerRetired(t *testing.T) {
	h := &ProviderCredentialPutHandler{ProvidersHandler: ProvidersHandler{Auth: auth.NewService(openMigratedDB(t, ":memory:"), t.TempDir())}}
	ctx := auth.WithPrincipal(context.Background(), auth.Principal{Kind: "account", UserID: "op-1", Operator: true})
	_, err := h.Handle(Request[providerCredentialBody, providerCredentialParams]{
		Context:    ctx,
		Body:       providerCredentialBody{ClientID: "x", ClientSecret: "y", RedirectURI: "https://x.example/cb"},
		PathParams: providerCredentialParams{ProviderID: "kroger"},
	})
	var httpErr *HTTPError
	if err == nil || !errorsAsHTTP(err, &httpErr) || httpErr.Code != http.StatusNotFound {
		t.Fatalf("err = %v", err)
	}
}

func TestProviderCredentialPutValidationAndSinkErrors(t *testing.T) {
	db := openMigratedDB(t, ":memory:")
	svc := auth.NewService(db, t.TempDir())
	registry := cart.NewRegistry()
	if err := registry.Register(testAppCredProvider{id: "test-appcred"}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(plainProvider{id: "plain"}); err != nil {
		t.Fatal(err)
	}
	h := &ProviderCredentialPutHandler{ProvidersHandler: ProvidersHandler{
		Registry: registry, Credentials: appcred.NewVault(db), Auth: svc,
	}}
	op := auth.WithPrincipal(context.Background(), auth.Principal{Kind: "account", UserID: "op-1", Operator: true})

	try := func(body providerCredentialBody, providerID string, wantCode int) {
		t.Helper()
		_, err := h.Handle(Request[providerCredentialBody, providerCredentialParams]{
			Context: op, Body: body, PathParams: providerCredentialParams{ProviderID: providerID},
		})
		var httpErr *HTTPError
		if err == nil || !errorsAsHTTP(err, &httpErr) || httpErr.Code != wantCode {
			t.Fatalf("provider=%s body=%+v err=%v", providerID, body, err)
		}
	}

	try(providerCredentialBody{ClientSecret: "s", RedirectURI: "https://x.example/cb"}, "test-appcred", http.StatusUnprocessableEntity)
	try(providerCredentialBody{ClientID: "c", ClientSecret: "s", RedirectURI: "not-a-url", Modality: "PICKUP"}, "test-appcred", http.StatusUnprocessableEntity)
	try(providerCredentialBody{ClientID: "c", ClientSecret: "s", RedirectURI: "https://x.example/cb", Modality: "SHIP"}, "test-appcred", http.StatusUnprocessableEntity)
	try(providerCredentialBody{ClientID: "c", ClientSecret: "s", RedirectURI: "https://x.example/cb"}, "missing", http.StatusNotFound)
	try(providerCredentialBody{ClientID: "c", ClientSecret: "s", RedirectURI: "https://x.example/cb"}, "plain", http.StatusUnprocessableEntity)
	h.EnvFallback = ProviderEnv{Disabled: true}
	try(providerCredentialBody{ClientID: "c", ClientSecret: "s", RedirectURI: "https://x.example/cb"}, "test-appcred", http.StatusConflict)
}

func TestProviderCredentialPutSecretReuseAndApplyFailure(t *testing.T) {
	db := openMigratedDB(t, ":memory:")
	svc := auth.NewService(db, t.TempDir())
	registry := cart.NewRegistry()
	badSink := testAppCredProvider{id: "bad-apply"}
	if err := registry.Register(badSink); err != nil {
		t.Fatal(err)
	}
	vault := appcred.NewVault(db)
	h := &ProviderCredentialPutHandler{ProvidersHandler: ProvidersHandler{
		Registry: registry, Credentials: vault, Auth: svc,
	}}
	ctx := auth.WithPrincipal(context.Background(), auth.Principal{Kind: "account", UserID: "op-1", Operator: true})
	scope, err := svc.Primary(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := vault.Save(ctx, scope, appcred.Saved{
		ProviderID: "bad-apply", ClientID: "saved", ClientSecret: "stored-secret",
		RedirectURI: "https://saved.example/cb", Modality: "PICKUP",
	}); err != nil {
		t.Fatal(err)
	}
	_, err = h.Handle(Request[providerCredentialBody, providerCredentialParams]{
		Context: ctx,
		Body: providerCredentialBody{
			ClientID: "saved", RedirectURI: "https://saved.example/cb", Modality: "DELIVERY",
		},
		PathParams: providerCredentialParams{ProviderID: "bad-apply"},
	})
	if err != nil {
		t.Fatalf("modality-only update: %v", err)
	}
	failSink := failingApplyProvider{id: "fail-apply"}
	if err := registry.Register(failSink); err != nil {
		t.Fatal(err)
	}
	h.Registry = registry
	_, err = h.Handle(Request[providerCredentialBody, providerCredentialParams]{
		Context: ctx,
		Body: providerCredentialBody{
			ClientID: "x", ClientSecret: "y", RedirectURI: "https://x.example/cb",
		},
		PathParams: providerCredentialParams{ProviderID: "fail-apply"},
	})
	var httpErr *HTTPError
	if err == nil || !errorsAsHTTP(err, &httpErr) || httpErr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("apply failure err=%v", err)
	}
}

type failingApplyProvider struct {
	id cart.ProviderID
}

func (p failingApplyProvider) ID() cart.ProviderID { return p.id }
func (p failingApplyProvider) DisplayName() string { return "Fail apply" }
func (p failingApplyProvider) Capabilities() cart.Capabilities {
	return cart.Capabilities{Auth: cart.AuthOAuth2, Delivery: cart.DeliveryServerPush,
		Confirmation: cart.ConfirmPerRequest, Mutation: cart.MutateAddOnly, Identity: cart.IdentityDerived}
}
func (p failingApplyProvider) ApplyAppCredentials(string, string, string, string) error {
	return errCredentialStore
}
func (p failingApplyProvider) ClearAppCredentials() {}
func (p failingApplyProvider) PublicAppCredentials() cart.AppCredentials {
	return cart.AppCredentials{}
}

func TestProviderCredentialDeleteClearsRegistryWhenNoEnv(t *testing.T) {
	db := openMigratedDB(t, ":memory:")
	svc := auth.NewService(db, t.TempDir())
	registry := cart.NewRegistry()
	provider := testAppCredProvider{id: "test-appcred"}
	if err := registry.Register(provider, cart.WithCredentialsConfigured(true)); err != nil {
		t.Fatal(err)
	}
	vault := appcred.NewVault(db)
	h := &ProvidersHandler{Registry: registry, Credentials: vault, Auth: svc}
	ctx := auth.WithPrincipal(context.Background(), auth.Principal{Kind: "account", UserID: "op-1", Operator: true})
	scope, err := svc.Primary(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := vault.Save(ctx, scope, appcred.Saved{
		ProviderID: "test-appcred", ClientID: "cid", ClientSecret: "sec",
		RedirectURI: "https://example.test/cb", Modality: "PICKUP",
	}); err != nil {
		t.Fatal(err)
	}
	del := &ProviderCredentialDeleteHandler{ProvidersHandler: *h}
	out, err := del.Handle(Request[struct{}, providerCredentialParams]{
		Context: ctx, PathParams: providerCredentialParams{ProviderID: "test-appcred"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Source != "none" || registry.CredentialsConfigured("test-appcred") {
		t.Fatalf("view=%+v configured=%v", out, registry.CredentialsConfigured("test-appcred"))
	}
}

func TestProviderCredentialPutRejectsNonOperator(t *testing.T) {
	db := openMigratedDB(t, ":memory:")
	svc := auth.NewService(db, t.TempDir())
	registry := cart.NewRegistry()
	if err := registry.Register(testAppCredProvider{id: "test-appcred"}); err != nil {
		t.Fatal(err)
	}
	h := &ProviderCredentialPutHandler{ProvidersHandler: ProvidersHandler{
		Registry: registry, Credentials: appcred.NewVault(db), Auth: svc,
	}}
	ctx := auth.WithPrincipal(context.Background(), auth.Principal{Kind: "account", UserID: "user-1"})
	_, err := h.Handle(Request[providerCredentialBody, providerCredentialParams]{
		Context:    ctx,
		Body:       providerCredentialBody{ClientID: "x", ClientSecret: "y", RedirectURI: "https://x.example/cb"},
		PathParams: providerCredentialParams{ProviderID: "test-appcred"},
	})
	var httpErr *HTTPError
	if err == nil || !errorsAsHTTP(err, &httpErr) || httpErr.Code != http.StatusForbidden {
		t.Fatalf("err = %v", err)
	}
}
