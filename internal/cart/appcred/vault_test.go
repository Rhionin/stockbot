package appcred

import (
	"context"
	"testing"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/auth"
	_ "modernc.org/sqlite"
)

func TestSaveKeepsSecretWhenOmitted(t *testing.T) {
	db, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := app.RunMigrations(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	vault := NewVault(db)
	ctx := context.Background()
	scope, err := auth.PrimaryScope(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if err := vault.Save(ctx, scope, Saved{
		ProviderID:   "kroger",
		ClientID:     "client",
		ClientSecret: "kept-secret",
		RedirectURI:  "https://app.example/cb",
		Modality:     "PICKUP",
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := vault.Save(ctx, scope, Saved{
		ProviderID:  "kroger",
		ClientID:    "client-2",
		RedirectURI: "https://app.example/cb2",
		Modality:    "DELIVERY",
	}); err != nil {
		t.Fatalf("update: %v", err)
	}
	saved, ok, err := vault.Load(ctx, scope, "kroger")
	if err != nil || !ok {
		t.Fatalf("load ok=%v err=%v", ok, err)
	}
	if saved.ClientSecret != "kept-secret" || saved.ClientID != "client-2" {
		t.Fatalf("saved = %+v", saved)
	}
	public, ok, err := vault.Public(ctx, scope, "kroger")
	if err != nil || !ok {
		t.Fatalf("public ok=%v err=%v", ok, err)
	}
	if public.ClientID != "client-2" || !public.SecretSet || public.Source != "saved" {
		t.Fatalf("public = %+v", public)
	}
}
