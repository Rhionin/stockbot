package cart

import (
	"context"
	"testing"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/Rhionin/stockbot/internal/cart/appcred"
	"github.com/Rhionin/stockbot/internal/cart/connection"
	"github.com/Rhionin/stockbot/internal/tenant"
)

func TestHouseholdPushModalityUsesScopedVaultOnly(t *testing.T) {
	db, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := app.RunMigrations(db); err != nil {
		t.Fatal(err)
	}
	scope, err := auth.PrimaryScope(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}

	vault := appcred.NewVault(db)
	if err := vault.Save(context.Background(), scope, appcred.Saved{
		ProviderID: "kroger", ClientID: "house-id", ClientSecret: "secret",
		RedirectURI: "https://app/cb", Modality: "DELIVERY", ActorUserID: "user-1",
	}); err != nil {
		t.Fatal(err)
	}

	engine := NewEngine(NewRegistry(), NewLedger(db), connection.NewTokenBroker(connection.NewDirectory(db)))
	engine.SetCredentialVault(vault)

	if mod := engine.householdPushModality(context.Background(), scope, ProviderID("kroger")); mod != "DELIVERY" {
		t.Fatalf("modality = %q want DELIVERY", mod)
	}

	otherHouse := "01909999-9999-7000-8000-000000000099"
	if _, err := db.Exec(`INSERT INTO households (id, name, restock_mode, created_at, channel_key, is_primary)
		VALUES (?, 'Other', '', '2026-01-01T00:00:00Z', 'abcdef0123456789abcdef0123456789', 0)`, otherHouse); err != nil {
		t.Fatal(err)
	}
	otherScope, err := tenant.New(otherHouse)
	if err != nil {
		t.Fatal(err)
	}
	if err := vault.Save(context.Background(), otherScope, appcred.Saved{
		ProviderID: "kroger", ClientID: "other-id", ClientSecret: "other-secret",
		RedirectURI: "https://other/cb", Modality: "PICKUP", ActorUserID: "user-1",
	}); err != nil {
		t.Fatal(err)
	}
	if mod := engine.householdPushModality(context.Background(), scope, ProviderID("kroger")); mod != "DELIVERY" {
		t.Fatalf("other household modality leaked: %q", mod)
	}
	if mod := engine.householdPushModality(context.Background(), otherScope, ProviderID("kroger")); mod != "PICKUP" {
		t.Fatalf("other scope = %q want PICKUP", mod)
	}
}

func TestHouseholdPushModalityEmptyWhenNoSavedApp(t *testing.T) {
	db, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := app.RunMigrations(db); err != nil {
		t.Fatal(err)
	}
	scope, err := auth.PrimaryScope(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	engine := NewEngine(NewRegistry(), NewLedger(db), connection.NewTokenBroker(connection.NewDirectory(db)))
	engine.SetCredentialVault(appcred.NewVault(db))
	if mod := engine.householdPushModality(context.Background(), scope, ProviderID("kroger")); mod != "" {
		t.Fatalf("modality = %q want empty", mod)
	}
}
