package connection

import (
	"context"
	"errors"
	"testing"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/Rhionin/stockbot/internal/cart/appcred"
	"github.com/Rhionin/stockbot/internal/tenant"
)

func testVaultScope(t *testing.T) (*appcred.Vault, context.Context, tenant.Scope) {
	t.Helper()
	db, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := app.RunMigrations(db); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	scope, err := auth.PrimaryScope(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	return appcred.NewVault(db), ctx, scope
}

func TestAppForRequiresVaultAndApp(t *testing.T) {
	vault, ctx, scope := testVaultScope(t)
	_, _, err := AppFor(ctx, nil, scope, nil)
	if err == nil || err.Error() != "credential store is not available" {
		t.Fatalf("nil vault: %v", err)
	}
	_, _, err = AppFor(ctx, vault, scope, nil)
	if err == nil || err.Error() != "no store app configured" {
		t.Fatalf("missing app: %v", err)
	}
	if err := vault.Save(ctx, scope, appcred.Saved{
		ProviderID: "kroger", ClientID: "client-a", ClientSecret: "sec",
		RedirectURI: "https://cb", Modality: "PICKUP", ActorUserID: "u1",
	}); err != nil {
		t.Fatal(err)
	}
	saved, key, err := AppFor(ctx, vault, scope, nil)
	if err != nil || saved.ClientID != "client-a" || key != appcred.AppKey("client-a") {
		t.Fatalf("load: saved=%+v key=%q err=%v", saved, key, err)
	}
	conn := &Connection{AppKey: "wrong-key"}
	_, _, err = AppFor(ctx, vault, scope, conn)
	if !errors.Is(err, ErrAppMismatch) {
		t.Fatalf("mismatch: %v", err)
	}
	conn.AppKey = key
	if _, _, err = AppFor(ctx, vault, scope, conn); err != nil {
		t.Fatalf("matching key: %v", err)
	}
}

func TestAppForRejectsEmptyScope(t *testing.T) {
	vault, ctx, _ := testVaultScope(t)
	_, _, err := AppFor(ctx, vault, tenant.Scope{}, nil)
	if err == nil {
		t.Fatal("expected scope error")
	}
}
