package server

import (
	"context"
	"testing"

	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/Rhionin/stockbot/internal/cart"
	"github.com/Rhionin/stockbot/internal/cart/appcred"
)

func TestProviderCredentialDeleteEnvApplyFailure(t *testing.T) {
	db := openMigratedDB(t, ":memory:")
	svc := auth.NewService(db, t.TempDir())
	registry := cart.NewRegistry()
	failSink := failingApplyProvider{id: "fail-apply-del"}
	if err := registry.Register(failSink); err != nil {
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
		ProviderID: "fail-apply-del", ClientID: "saved", ClientSecret: "stored",
		RedirectURI: "https://saved.example/cb", Modality: "PICKUP",
	}); err != nil {
		t.Fatal(err)
	}
	_, err = h.Handle(Request[struct{}, providerCredentialParams]{
		Context: ctx, PathParams: providerCredentialParams{ProviderID: "fail-apply-del"},
	})
	var httpErr *HTTPError
	if err == nil || !errorsAsHTTP(err, &httpErr) || httpErr.Code != 500 {
		t.Fatalf("err=%v", err)
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
