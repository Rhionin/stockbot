package server

import (
	"context"
	"testing"

	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/Rhionin/stockbot/internal/cart"
	"github.com/Rhionin/stockbot/internal/cart/appcred"
)

func TestProviderCredentialDeleteClearsSavedWithoutEnvReapply(t *testing.T) {
	db := openMigratedDB(t, ":memory:")
	svc := auth.NewService(db, t.TempDir())
	registry := cart.NewRegistry()
	provider := testAppCredProvider{id: "test-appcred"}
	if err := registry.Register(provider, cart.WithCredentialsConfigured(true)); err != nil {
		t.Fatal(err)
	}
	vault := appcred.NewVault(db)
	h := &ProviderCredentialDeleteHandler{ProvidersHandler: ProvidersHandler{
		Registry: registry, Credentials: vault, Auth: svc,
		EnvFallback: ProviderEnv{
			ClientID: "env", ClientSecret: "sec", RedirectURI: "https://env.example/cb", Modality: "PICKUP",
		},
	}}
	ctx := auth.WithPrincipal(context.Background(), auth.Principal{Kind: "account", UserID: "op-1", Operator: true})
	scope, err := svc.Primary(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := vault.Save(ctx, scope, appcred.Saved{
		ProviderID: "test-appcred", ClientID: "saved", ClientSecret: "stored",
		RedirectURI: "https://saved.example/cb", Modality: "PICKUP",
	}); err != nil {
		t.Fatal(err)
	}
	view, err := h.Handle(Request[struct{}, providerCredentialParams]{
		Context: ctx, PathParams: providerCredentialParams{ProviderID: "test-appcred"},
	})
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if view.Source != "none" || view.ClientID != "test-client" {
		t.Fatalf("view=%+v", view)
	}
	if registry.CredentialsConfigured("test-appcred") {
		t.Fatal("delete should clear credentials-configured")
	}
	has, err := vault.HasApp(ctx, scope, "test-appcred")
	if err != nil || has {
		t.Fatalf("saved row cleared: has=%v err=%v", has, err)
	}
}

func TestProviderCredentialPutNilVaultReturns500(t *testing.T) {
	db := openMigratedDB(t, ":memory:")
	svc := auth.NewService(db, t.TempDir())
	registry := cart.NewRegistry()
	if err := registry.Register(testAppCredProvider{id: "test-appcred"}); err != nil {
		t.Fatal(err)
	}
	h := &ProviderCredentialPutHandler{ProvidersHandler: ProvidersHandler{
		Registry: registry, Credentials: nil, Auth: svc,
	}}
	ctx := auth.WithPrincipal(context.Background(), auth.Principal{Kind: "account", UserID: "op-1", Operator: true})
	_, err := h.Handle(Request[providerCredentialBody, providerCredentialParams]{
		Context: ctx,
		Body: providerCredentialBody{
			ClientID: "c", ClientSecret: "s", RedirectURI: "https://x.example/cb", Modality: "PICKUP",
		},
		PathParams: providerCredentialParams{ProviderID: "test-appcred"},
	})
	var httpErr *HTTPError
	if err == nil || !errorsAsHTTP(err, &httpErr) || httpErr.Code != 500 {
		t.Fatalf("err=%v", err)
	}
}

func TestProviderCredentialDeleteNonOperatorForbidden(t *testing.T) {
	db := openMigratedDB(t, ":memory:")
	svc := auth.NewService(db, t.TempDir())
	registry := cart.NewRegistry()
	if err := registry.Register(testAppCredProvider{id: "test-appcred"}); err != nil {
		t.Fatal(err)
	}
	h := &ProviderCredentialDeleteHandler{ProvidersHandler: ProvidersHandler{
		Registry: registry, Credentials: appcred.NewVault(db), Auth: svc,
	}}
	ctx := auth.WithPrincipal(context.Background(), auth.Principal{Kind: "account", UserID: "user-1"})
	_, err := h.Handle(Request[struct{}, providerCredentialParams]{
		Context: ctx, PathParams: providerCredentialParams{ProviderID: "test-appcred"},
	})
	var httpErr *HTTPError
	if err == nil || !errorsAsHTTP(err, &httpErr) || httpErr.Code != 403 {
		t.Fatalf("err=%v", err)
	}
}

func TestOperatorProviderCredentialKrogerNotRegistered(t *testing.T) {
	db := openMigratedDB(t, ":memory:")
	svc := auth.NewService(db, t.TempDir())
	base := ProvidersHandler{Registry: cart.NewRegistry(), Auth: svc}
	ctx := auth.WithPrincipal(context.Background(), auth.Principal{Kind: "account", UserID: "op-1", Operator: true})
	put := &ProviderCredentialPutHandler{ProvidersHandler: base}
	_, err := put.Handle(Request[providerCredentialBody, providerCredentialParams]{
		Context: ctx,
		Body: providerCredentialBody{
			ClientID: "c", ClientSecret: "s", RedirectURI: "https://x.example/cb", Modality: "PICKUP",
		},
		PathParams: providerCredentialParams{ProviderID: "kroger"},
	})
	var httpErr *HTTPError
	if err == nil || !errorsAsHTTP(err, &httpErr) || httpErr.Code != 404 {
		t.Fatalf("put err=%v", err)
	}
	del := &ProviderCredentialDeleteHandler{ProvidersHandler: base}
	_, err = del.Handle(Request[struct{}, providerCredentialParams]{
		Context: ctx, PathParams: providerCredentialParams{ProviderID: "kroger"},
	})
	if err == nil || !errorsAsHTTP(err, &httpErr) || httpErr.Code != 404 {
		t.Fatalf("delete err=%v", err)
	}
}
