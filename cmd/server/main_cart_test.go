package main

import (
	"context"
	"database/sql"
	"log"
	"strings"
	"testing"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/Rhionin/stockbot/internal/cart/appcred"
	"github.com/Rhionin/stockbot/internal/cart/kroger"
	"github.com/Rhionin/stockbot/internal/tenant"
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

func TestLoadCartRegistryUsesOperatorSavedRow(t *testing.T) {
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
	operatorID := "01909999-9999-7000-8000-0000000000op"
	if _, err := db.Exec(`
		INSERT INTO users (id, name, email_norm, pw_hash, idp, operator, created_at)
		VALUES (?, 'Op', 'op@pantry.test', 'hash', 'local', 1, '2026-02-02T00:00:00Z')`, operatorID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO memberships (user_id, household_id, role, created_at) VALUES (?, ?, 'owner', '2026-02-02T00:00:00Z')`, operatorID, scope.HouseholdID()); err != nil {
		t.Fatal(err)
	}
	if err := appcred.NewVault(db).Save(context.Background(), scope, appcred.Saved{
		ProviderID:   "kroger",
		ClientID:     "operator-saved",
		ClientSecret: "operator-secret",
		RedirectURI:  "https://op.example/cb",
		Modality:     "DELIVERY",
		ActorUserID:  operatorID,
	}); err != nil {
		t.Fatal(err)
	}

	registry, _, _ := loadCartRegistry(db)
	provider, ok := registry.Get("kroger")
	if !ok {
		t.Fatal("missing kroger")
	}
	adapter := provider.(*kroger.Adapter)
	if got := adapter.PublicAppCredentials().ClientID; got != "operator-saved" {
		t.Fatalf("client id = %q", got)
	}
}

func TestLoadCartRegistryIgnoresForeignHouseholdSavedCreds(t *testing.T) {
	t.Setenv("DISABLE_KROGER", "")
	t.Setenv("KROGER_CLIENT_ID", "env-client")
	t.Setenv("KROGER_CLIENT_SECRET", "env-secret")
	t.Setenv("KROGER_REDIRECT_URI", "https://env.example/cb")
	t.Setenv("KROGER_MODALITY", "PICKUP")

	db := newCartDB(t)
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
	if err := appcred.NewVault(db).Save(context.Background(), birchScope, appcred.Saved{
		ProviderID:   "kroger",
		ClientID:     "birch-only",
		ClientSecret: "birch-secret",
		RedirectURI:  "https://birch.example/cb",
		Modality:     "DELIVERY",
		ActorUserID:  "member-user",
	}); err != nil {
		t.Fatal(err)
	}

	registry, _, _ := loadCartRegistry(db)
	provider, ok := registry.Get("kroger")
	if !ok {
		t.Fatal("kroger adapter missing")
	}
	adapter, ok := provider.(*kroger.Adapter)
	if !ok {
		t.Fatal("kroger adapter type")
	}
	if got := adapter.PublicAppCredentials().ClientID; got != "env-client" {
		t.Fatalf("client id = %q want env-client", got)
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

func TestLoadCartRegistryLogsCredentialSource(t *testing.T) {
	t.Setenv("DISABLE_KROGER", "")
	t.Setenv("KROGER_CLIENT_ID", "env-client")
	t.Setenv("KROGER_CLIENT_SECRET", "env-secret")
	t.Setenv("KROGER_REDIRECT_URI", "https://env.example/cb")
	t.Setenv("KROGER_MODALITY", "PICKUP")

	var logs strings.Builder
	prev := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(prev) })

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
		t.Fatal(err)
	}
	loadCartRegistry(db)
	out := logs.String()
	if !strings.Contains(out, "using saved row for household "+scope.HouseholdID()) {
		t.Fatalf("log missing saved household: %q", out)
	}
	if strings.Contains(out, "env-secret") || strings.Contains(out, "saved-secret") {
		t.Fatalf("log leaked secret: %q", out)
	}

	logs.Reset()
	loadCartRegistry(newCartDB(t))
	out = logs.String()
	if !strings.Contains(out, "using environment") {
		t.Fatalf("log missing environment source: %q", out)
	}
}
