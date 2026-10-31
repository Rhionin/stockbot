package appcred

import (
	"context"
	"database/sql"
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

func newTestVault(t *testing.T) (*Vault, *sql.DB, context.Context, tenant.Scope, tenant.Scope) {
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
	return NewVault(db), db, ctx, scopeA, scopeB
}

func TestVaultLoadAndPublicMissingProvider(t *testing.T) {
	vault, _, ctx, scope, _ := newTestVault(t)
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
	vault, _, ctx, scope, _ := newTestVault(t)

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
	vault, _, ctx, scope, _ := newTestVault(t)
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
	vault, _, ctx, scope, _ := newTestVault(t)
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
	vault, _, ctx, scope, _ := newTestVault(t)
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
	vault, _, ctx, scope, _ := newTestVault(t)
	if err := vault.Clear(ctx, scope, "no-such-provider"); err != nil {
		t.Fatalf("Clear missing provider: %v", err)
	}
	if err := vault.Clear(ctx, scope, ""); err != nil {
		t.Fatalf("Clear empty provider: %v", err)
	}
}

func TestVaultHouseholdIsolation(t *testing.T) {
	vault, _, ctx, scopeA, scopeB := newTestVault(t)
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

func TestVaultLoadServerProviderRules(t *testing.T) {
	vault, db, ctx, scopeA, scopeB := newTestVault(t)
	operatorID := "01909999-9999-7000-8000-0000000000op"
	if _, err := db.Exec(`
		INSERT INTO users (id, name, email_norm, pw_hash, idp, operator, created_at)
		VALUES (?, 'Op', 'op@pantry.test', 'hash', 'local', 1, '2026-02-02T00:00:00Z')`, operatorID); err != nil {
		t.Fatal(err)
	}
	var primaryID string
	if err := db.QueryRow(`SELECT id FROM households WHERE is_primary = 1`).Scan(&primaryID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO memberships (user_id, household_id, role, created_at) VALUES (?, ?, 'owner', '2026-02-02T00:00:00Z')`, operatorID, primaryID); err != nil {
		t.Fatal(err)
	}

	if err := vault.Save(ctx, scopeB, Saved{
		ProviderID:   "kroger",
		ClientID:     "birch-only",
		ClientSecret: "secret",
		RedirectURI:  "https://birch.example/cb",
		Modality:     "DELIVERY",
		ActorUserID:  "member-user",
	}); err != nil {
		t.Fatal(err)
	}
	_, _, ok, err := vault.LoadServerProvider(ctx, "kroger")
	if err != nil || ok {
		t.Fatalf("foreign row loaded ok=%v err=%v", ok, err)
	}

	if err := vault.Save(ctx, scopeA, Saved{
		ProviderID:   "kroger",
		ClientID:     "legacy-primary",
		ClientSecret: "secret",
		RedirectURI:  "https://primary.example/cb",
		Modality:     "PICKUP",
	}); err != nil {
		t.Fatal(err)
	}
	saved, _, ok, err := vault.LoadServerProvider(ctx, "kroger")
	if err != nil || !ok || saved.ClientID != "legacy-primary" {
		t.Fatalf("primary legacy ok=%v err=%v saved=%+v", ok, err, saved)
	}

	memberID := "01908888-8888-7000-8000-0000000000mb"
	if _, err := db.Exec(`
		INSERT INTO users (id, name, email_norm, pw_hash, idp, operator, created_at)
		VALUES (?, 'Mem', 'mem@pantry.test', 'hash', 'local', 0, '2026-02-02T00:00:00Z')`, memberID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO memberships (user_id, household_id, role, created_at) VALUES (?, ?, 'member', '2026-02-02T00:00:00Z')`, memberID, primaryID); err != nil {
		t.Fatal(err)
	}
	if err := vault.Save(ctx, scopeA, Saved{
		ProviderID:   "kroger",
		ClientID:     "member-primary",
		ClientSecret: "secret",
		RedirectURI:  "https://member.example/cb",
		Modality:     "PICKUP",
		ActorUserID:  memberID,
	}); err != nil {
		t.Fatal(err)
	}
	saved, _, ok, err = vault.LoadServerProvider(ctx, "kroger")
	if err != nil || ok {
		t.Fatalf("member actor on primary should not configure server ok=%v err=%v saved=%+v", ok, err, saved)
	}

	if err := vault.Save(ctx, scopeA, Saved{
		ProviderID:   "kroger",
		ClientID:     "operator-primary",
		ClientSecret: "secret",
		RedirectURI:  "https://op.example/cb",
		Modality:     "DELIVERY",
		ActorUserID:  operatorID,
	}); err != nil {
		t.Fatal(err)
	}
	saved, _, ok, err = vault.LoadServerProvider(ctx, "kroger")
	if err != nil || !ok || saved.ClientID != "operator-primary" {
		t.Fatalf("operator row ok=%v err=%v saved=%+v", ok, err, saved)
	}
}

func TestVaultLoadServerProviderLegacyMigrationActor(t *testing.T) {
	vault, db, ctx, _, scopeB := newTestVault(t)
	operatorID := "01909999-9999-7000-8000-0000000000op"
	if _, err := db.Exec(`
		INSERT INTO users (id, name, email_norm, pw_hash, idp, operator, created_at)
		VALUES (?, 'Op', 'op@pantry.test', 'hash', 'local', 1, '2026-02-02T00:00:00Z')`, operatorID); err != nil {
		t.Fatal(err)
	}
	var primaryID, legacyID string
	if err := db.QueryRow(`SELECT id, legacy_user_id FROM households WHERE is_primary = 1`).Scan(&primaryID, &legacyID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO memberships (user_id, household_id, role, created_at) VALUES (?, ?, 'owner', '2026-02-02T00:00:00Z')`, operatorID, primaryID); err != nil {
		t.Fatal(err)
	}
	const now = "2026-02-02T00:00:00Z"
	if _, err := db.Exec(`
		INSERT INTO provider_app_credentials
			(id, household_id, user_id, actor_user_id, provider_id, client_id, client_secret, redirect_uri, modality, updated_at)
		VALUES (?, ?, ?, ?, 'kroger', 'legacy-migrated', 'secret', 'https://legacy.example/cb', 'PICKUP', ?)`,
		primaryID+":kroger", primaryID, primaryID, legacyID, now); err != nil {
		t.Fatal(err)
	}
	saved, hid, ok, err := vault.LoadServerProvider(ctx, "kroger")
	if err != nil || !ok || saved.ClientID != "legacy-migrated" || hid != primaryID {
		t.Fatalf("legacy actor on primary ok=%v err=%v hid=%q saved=%+v", ok, err, hid, saved)
	}

	if _, err := db.Exec(`UPDATE households SET legacy_user_id = ? WHERE id = ?`, "legacy-b", scopeB.HouseholdID()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO provider_app_credentials
			(id, household_id, user_id, actor_user_id, provider_id, client_id, client_secret, redirect_uri, modality, updated_at)
		VALUES (?, ?, ?, 'legacy-b', 'kroger', 'birch-legacy-shape', 'secret', 'https://birch.example/cb', 'DELIVERY', ?)`,
		scopeB.HouseholdID()+":kroger", scopeB.HouseholdID(), scopeB.HouseholdID(), now); err != nil {
		t.Fatal(err)
	}
	_, _, ok, err = vault.LoadServerProvider(ctx, "kroger")
	if err != nil || !ok || saved.ClientID != "legacy-migrated" {
		t.Fatalf("non-primary legacy-shaped row must not win ok=%v err=%v", ok, err)
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
