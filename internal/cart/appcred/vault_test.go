package appcred

import (
	"context"
	"strings"
	"testing"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/Rhionin/stockbot/internal/tenant"
	_ "modernc.org/sqlite"
)

const (
	householdBID         = "0190aaaa-bbbb-7000-8000-0000000000c3"
	householdBChannelKey = "abcdef0123456789abcdef0123456789"
)

func newTestVault(t *testing.T) (*Vault, context.Context, tenant.Scope, tenant.Scope) {
	db, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := app.RunMigrations(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	ctx := context.Background()
	scopeA, err := auth.PrimaryScope(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO households (id, name, restock_mode, created_at, channel_key, is_primary)
		VALUES (?, 'Household B', '', '2026-02-02T00:00:00Z', ?, 0)`,
		householdBID, householdBChannelKey); err != nil {
		t.Fatal(err)
	}
	scopeB, err := tenant.New(householdBID)
	if err != nil {
		t.Fatal(err)
	}
	return NewVault(db), ctx, scopeA, scopeB
}

func TestVaultLoadAndPublicMissingProvider(t *testing.T) {
	vault, ctx, scope, _ := newTestVault(t)
	const provider = "kroger"

	saved, ok, err := vault.Load(ctx, scope, provider)
	if err != nil {
		t.Fatalf("Load err: %v", err)
	}
	if ok || saved != (Saved{}) {
		t.Fatalf("Load ok=%v saved=%+v", ok, saved)
	}

	public, ok, err := vault.Public(ctx, scope, provider)
	if err != nil {
		t.Fatalf("Public err: %v", err)
	}
	if ok || public != (Public{}) {
		t.Fatalf("Public ok=%v public=%+v", ok, public)
	}
}

func TestVaultSaveRequiresSecretOnFirstSave(t *testing.T) {
	vault, ctx, scope, _ := newTestVault(t)

	err := vault.Save(ctx, scope, Saved{
		ProviderID:  "kroger",
		ClientID:    "client",
		RedirectURI: "https://app.example/cb",
		Modality:    "PICKUP",
	})
	if err == nil || !strings.Contains(err.Error(), "client secret is required") {
		t.Fatalf("Save: %v", err)
	}

	saved, ok, loadErr := vault.Load(ctx, scope, "kroger")
	if loadErr != nil {
		t.Fatalf("Load err: %v", loadErr)
	}
	if ok || saved != (Saved{}) {
		t.Fatalf("Load ok=%v saved=%+v", ok, saved)
	}
	public, ok, pubErr := vault.Public(ctx, scope, "kroger")
	if pubErr != nil {
		t.Fatalf("Public err: %v", pubErr)
	}
	if ok || public != (Public{}) {
		t.Fatalf("Public ok=%v public=%+v", ok, public)
	}
}

func TestVaultPublicAndLoadAfterSave(t *testing.T) {
	vault, ctx, scope, _ := newTestVault(t)
	const provider = "kroger"

	if err := vault.Save(ctx, scope, Saved{
		ProviderID:   provider,
		ClientID:     "client-a",
		ClientSecret: "super-secret",
		RedirectURI:  "https://app.example/cb",
		Modality:     "DELIVERY",
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	public, ok, err := vault.Public(ctx, scope, provider)
	if err != nil || !ok {
		t.Fatalf("Public ok=%v err=%v", ok, err)
	}
	if public.ClientID != "client-a" || public.RedirectURI != "https://app.example/cb" ||
		public.Modality != "DELIVERY" || !public.SecretSet || public.Source != "saved" {
		t.Fatalf("public = %+v", public)
	}

	saved, ok, err := vault.Load(ctx, scope, provider)
	if err != nil || !ok {
		t.Fatalf("Load ok=%v err=%v", ok, err)
	}
	if saved.ProviderID != provider || saved.ClientID != "client-a" ||
		saved.ClientSecret != "super-secret" || saved.RedirectURI != "https://app.example/cb" ||
		saved.Modality != "DELIVERY" {
		t.Fatalf("saved = %+v", saved)
	}
}

func TestSaveKeepsSecretWhenOmitted(t *testing.T) {
	vault, ctx, scope, _ := newTestVault(t)
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

func TestVaultClearRemovesSavedRow(t *testing.T) {
	vault, ctx, scope, _ := newTestVault(t)
	const provider = "kroger"

	if err := vault.Save(ctx, scope, Saved{
		ProviderID:   provider,
		ClientID:     "client",
		ClientSecret: "secret",
		RedirectURI:  "https://app.example/cb",
		Modality:     "PICKUP",
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := vault.Clear(ctx, scope, provider); err != nil {
		t.Fatalf("Clear: %v", err)
	}

	_, ok, err := vault.Load(ctx, scope, provider)
	if err != nil || ok {
		t.Fatalf("Load after clear ok=%v err=%v", ok, err)
	}
	_, ok, err = vault.Public(ctx, scope, provider)
	if err != nil || ok {
		t.Fatalf("Public after clear ok=%v err=%v", ok, err)
	}
}

func TestVaultClearMissingProviderIsNoOp(t *testing.T) {
	vault, ctx, scope, _ := newTestVault(t)
	if err := vault.Clear(ctx, scope, "no-such-provider"); err != nil {
		t.Fatalf("Clear missing provider: %v", err)
	}
	if err := vault.Clear(ctx, scope, ""); err != nil {
		t.Fatalf("Clear empty provider: %v", err)
	}
}

func TestVaultHouseholdIsolation(t *testing.T) {
	vault, ctx, scopeA, scopeB := newTestVault(t)
	const provider = "kroger"

	saveA := Saved{
		ProviderID:   provider,
		ClientID:     "household-a-client",
		ClientSecret: "household-a-secret",
		RedirectURI:  "https://a.example/cb",
		Modality:     "PICKUP",
	}
	if err := vault.Save(ctx, scopeA, saveA); err != nil {
		t.Fatalf("Save A: %v", err)
	}

	_, ok, err := vault.Load(ctx, scopeB, provider)
	if err != nil || ok {
		t.Fatalf("B Load before save ok=%v err=%v", ok, err)
	}
	_, ok, err = vault.Public(ctx, scopeB, provider)
	if err != nil || ok {
		t.Fatalf("B Public before save ok=%v err=%v", ok, err)
	}

	saveB := Saved{
		ProviderID:   provider,
		ClientID:     "household-b-client",
		ClientSecret: "household-b-secret",
		RedirectURI:  "https://b.example/cb",
		Modality:     "DELIVERY",
	}
	if err := vault.Save(ctx, scopeB, saveB); err != nil {
		t.Fatalf("Save B: %v", err)
	}

	loadedA, ok, err := vault.Load(ctx, scopeA, provider)
	if err != nil || !ok {
		t.Fatalf("A Load ok=%v err=%v", ok, err)
	}
	if loadedA.ClientID != saveA.ClientID || loadedA.ClientSecret != saveA.ClientSecret ||
		loadedA.RedirectURI != saveA.RedirectURI {
		t.Fatalf("A creds changed after B save: %+v", loadedA)
	}

	if err := vault.Save(ctx, scopeB, Saved{
		ProviderID:  provider,
		ClientID:    "household-b-client-2",
		RedirectURI: "https://b.example/cb2",
		Modality:    "PICKUP",
	}); err != nil {
		t.Fatalf("B update without secret: %v", err)
	}
	loadedB, ok, err := vault.Load(ctx, scopeB, provider)
	if err != nil || !ok {
		t.Fatalf("B Load ok=%v err=%v", ok, err)
	}
	if loadedB.ClientSecret != saveB.ClientSecret || loadedB.ClientID != "household-b-client-2" {
		t.Fatalf("B secret keep failed: %+v", loadedB)
	}
	loadedA, ok, err = vault.Load(ctx, scopeA, provider)
	if err != nil || !ok {
		t.Fatalf("A Load again ok=%v err=%v", ok, err)
	}
	if loadedA.ClientSecret != saveA.ClientSecret {
		t.Fatalf("A secret leaked or changed: %+v", loadedA)
	}

	if err := vault.Save(ctx, scopeA, Saved{
		ProviderID:  provider,
		ClientID:    "household-a-client-2",
		RedirectURI: "https://a.example/cb2",
		Modality:    "DELIVERY",
	}); err != nil {
		t.Fatalf("A update without secret: %v", err)
	}
	loadedA, ok, err = vault.Load(ctx, scopeA, provider)
	if err != nil || !ok || loadedA.ClientSecret != saveA.ClientSecret || loadedA.ClientID != "household-a-client-2" {
		t.Fatalf("A partial update: ok=%v err=%v saved=%+v", ok, err, loadedA)
	}
	loadedB, ok, err = vault.Load(ctx, scopeB, provider)
	if err != nil || !ok || loadedB.ClientSecret != saveB.ClientSecret {
		t.Fatalf("B affected by A update: ok=%v err=%v saved=%+v", ok, err, loadedB)
	}

	if err := vault.Clear(ctx, scopeB, provider); err != nil {
		t.Fatalf("Clear B: %v", err)
	}
	_, ok, err = vault.Load(ctx, scopeB, provider)
	if err != nil || ok {
		t.Fatalf("B Load after clear ok=%v err=%v", ok, err)
	}
	loadedA, ok, err = vault.Load(ctx, scopeA, provider)
	if err != nil || !ok || loadedA.ClientID != "household-a-client-2" {
		t.Fatalf("A Load after B clear ok=%v err=%v saved=%+v", ok, err, loadedA)
	}
}

func TestVaultNotConfigured(t *testing.T) {
	ctx := context.Background()
	scope, err := tenant.New("01900000-0000-7000-8000-000000000001")
	if err != nil {
		t.Fatal(err)
	}
	const provider = "kroger"

	cases := []struct {
		name  string
		vault *Vault
	}{
		{name: "nil receiver", vault: nil},
		{name: "nil database", vault: NewVault(nil)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, ok, err := tc.vault.Load(ctx, scope, provider)
			if err != nil || ok {
				t.Fatalf("Load ok=%v err=%v", ok, err)
			}
			_, ok, err = tc.vault.Public(ctx, scope, provider)
			if err != nil || ok {
				t.Fatalf("Public ok=%v err=%v", ok, err)
			}
			saveErr := tc.vault.Save(ctx, scope, Saved{
				ProviderID:   provider,
				ClientID:     "c",
				ClientSecret: "s",
			})
			if saveErr == nil || !strings.Contains(saveErr.Error(), "credential store is not available") {
				t.Fatalf("Save: %v", saveErr)
			}
			if err := tc.vault.Clear(ctx, scope, provider); err != nil {
				t.Fatalf("Clear: %v", err)
			}
		})
	}
}
