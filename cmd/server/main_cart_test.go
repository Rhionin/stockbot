package main

import (
	"context"
	"database/sql"
	"testing"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/Rhionin/stockbot/internal/cart/appcred"
	"github.com/Rhionin/stockbot/internal/cart/kroger"
	_ "modernc.org/sqlite"
)

func newCartDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := app.RunMigrations(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func TestLoadCartRegistryUnconfiguredStillListsKroger(t *testing.T) {
	t.Setenv("DISABLE_KROGER", "")
	t.Setenv("KROGER_CLIENT_ID", "  ")
	t.Setenv("KROGER_CLIENT_SECRET", "")
	t.Setenv("KROGER_REDIRECT_URI", "")
	t.Setenv("KROGER_MODALITY", "")
	t.Setenv("KROGER_BATCH_SIZE", "")

	registry, ledger, _ := loadCartRegistry(newCartDB(t))
	if ledger == nil {
		t.Fatal("ledger is nil")
	}
	provider, ok := registry.Get("kroger")
	if !ok {
		t.Fatal("kroger should be registered when credentials are absent")
	}
	if provider.DisplayName() != "Kroger" {
		t.Fatalf("display name %q", provider.DisplayName())
	}
	if registry.CredentialsConfigured("kroger") {
		t.Fatal("kroger should not be credentials-configured")
	}
}

func TestLoadCartRegistryConfigured(t *testing.T) {
	t.Setenv("DISABLE_KROGER", "false")
	t.Setenv("KROGER_CLIENT_ID", "id")
	t.Setenv("KROGER_CLIENT_SECRET", "secret")
	t.Setenv("KROGER_REDIRECT_URI", "https://app.example/cb")
	t.Setenv("KROGER_MODALITY", "delivery")
	t.Setenv("KROGER_BATCH_SIZE", "10")

	registry, _, _ := loadCartRegistry(newCartDB(t))
	if !registry.CredentialsConfigured("kroger") {
		t.Fatal("kroger should be credentials-configured")
	}
	if got := registry.BatchSize("kroger"); got != 10 {
		t.Fatalf("batch size %d, want 10", got)
	}
	id, ok := registry.SoleConfigured()
	if !ok || id != "kroger" {
		t.Fatalf("sole configured = %q, %v", id, ok)
	}
}

func TestLoadCartRegistryInvalidModalityStaysConfigured(t *testing.T) {
	t.Setenv("DISABLE_KROGER", "")
	t.Setenv("KROGER_CLIENT_ID", "id")
	t.Setenv("KROGER_CLIENT_SECRET", "secret")
	t.Setenv("KROGER_REDIRECT_URI", "https://app.example/cb")
	t.Setenv("KROGER_MODALITY", "ship")
	t.Setenv("KROGER_BATCH_SIZE", "nope")

	registry, _, _ := loadCartRegistry(newCartDB(t))
	if !registry.CredentialsConfigured("kroger") {
		t.Fatal("invalid modality should still leave Kroger configured")
	}
	if got := registry.BatchSize("kroger"); got != 50 {
		t.Fatalf("batch size %d, want default 50", got)
	}
}

func TestLoadCartRegistrySavedCredentialsOverrideEnv(t *testing.T) {
	t.Setenv("DISABLE_KROGER", "")
	t.Setenv("KROGER_CLIENT_ID", "env-client")
	t.Setenv("KROGER_CLIENT_SECRET", "env-secret")
	t.Setenv("KROGER_REDIRECT_URI", "https://env.example/cb")
	t.Setenv("KROGER_MODALITY", "PICKUP")

	db := newCartDB(t)
	scope, err := auth.PrimaryScope(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if err := appcred.NewVault(db).Save(context.Background(), scope, appcred.Saved{
		ProviderID:   "kroger",
		ClientID:     "saved-client",
		ClientSecret: "saved-secret",
		RedirectURI:  "https://saved.example/cb",
		Modality:     "DELIVERY",
	}); err != nil {
		t.Fatalf("save: %v", err)
	}

	registry, _, envCreds := loadCartRegistry(db)
	if !registry.CredentialsConfigured("kroger") {
		t.Fatal("saved credentials should configure Kroger")
	}
	provider, ok := registry.Get("kroger")
	if !ok {
		t.Fatal("kroger missing")
	}
	adapter, ok := provider.(*kroger.Adapter)
	if !ok {
		t.Fatalf("provider type %T", provider)
	}
	public := adapter.PublicAppCredentials()
	if public.ClientID != "saved-client" || public.RedirectURI != "https://saved.example/cb" || public.Modality != "DELIVERY" || !public.SecretSet {
		t.Fatalf("public credentials = %+v", public)
	}
	if envCreds.ClientID != "env-client" || envCreds.ClientSecret != "env-secret" {
		t.Fatalf("env fallback = %+v", envCreds)
	}
}

func TestLoadCartRegistryDropsUnusableSavedCredentials(t *testing.T) {
	t.Setenv("DISABLE_KROGER", "")
	t.Setenv("KROGER_CLIENT_ID", "")
	t.Setenv("KROGER_CLIENT_SECRET", "")
	t.Setenv("KROGER_REDIRECT_URI", "")
	t.Setenv("KROGER_MODALITY", "")

	db := newCartDB(t)
	scope, err := auth.PrimaryScope(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if err := appcred.NewVault(db).Save(context.Background(), scope, appcred.Saved{
		ProviderID:   "kroger",
		ClientID:     "saved-client",
		ClientSecret: "saved-secret",
		RedirectURI:  "https://saved.example/cb",
		Modality:     "SHIP",
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	registry, _, _ := loadCartRegistry(db)
	if registry.CredentialsConfigured("kroger") {
		t.Fatal("saved credentials with a bad modality should not configure Kroger")
	}
}

func TestLoadCartRegistryDisabledIsNotConfigured(t *testing.T) {
	t.Setenv("DISABLE_KROGER", "true")
	t.Setenv("KROGER_CLIENT_ID", "id")
	t.Setenv("KROGER_CLIENT_SECRET", "secret")
	t.Setenv("KROGER_REDIRECT_URI", "https://app.example/cb")
	t.Setenv("KROGER_MODALITY", "PICKUP")
	t.Setenv("KROGER_BATCH_SIZE", "50")

	registry, _, _ := loadCartRegistry(newCartDB(t))
	if _, ok := registry.Get("kroger"); !ok {
		t.Fatal("disabled kroger should still be listed")
	}
	if registry.CredentialsConfigured("kroger") {
		t.Fatal("DISABLE_KROGER should clear credentials-configured")
	}
}
