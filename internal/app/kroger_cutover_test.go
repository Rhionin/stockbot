package app

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/Rhionin/stockbot/internal/cart/appcred"
)

func TestKrogerCutoverKeepsPrimarySavedRow(t *testing.T) {
	db := openCutoverDB(t)
	ctx := context.Background()
	primary, err := auth.PrimaryScope(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	when := "2026-03-01T12:00:00Z"
	const wantSnap = "seed-primary|sec|https://primary.test/cb|DELIVERY"
	if _, err := db.Exec(`
		INSERT INTO provider_app_credentials
			(id, household_id, user_id, provider_id, client_id, client_secret, redirect_uri, modality, updated_at)
		VALUES (?, ?, 'user-1', 'kroger', 'seed-primary', 'sec', 'https://primary.test/cb', 'DELIVERY', ?)`,
		primary.HouseholdID()+":kroger", primary.HouseholdID(), when); err != nil {
		t.Fatal(err)
	}
	before := credentialsSnap(t, db, primary.HouseholdID())
	if err := RunKrogerAppCutover(ctx, db, KrogerCutoverEnv{
		ClientID: "env-should-not-overwrite", ClientSecret: "env-sec", RedirectURI: "https://env.test/cb",
	}); err != nil {
		t.Fatal(err)
	}
	after := credentialsSnap(t, db, primary.HouseholdID())
	if after != before || after != wantSnap {
		t.Fatalf("primary row changed:\nbefore %q\nafter %q", before, after)
	}
	assertCutoverFlag(t, db)
}

func TestKrogerCutoverSeedsFromEnvOnce(t *testing.T) {
	db := openCutoverDB(t)
	ctx := context.Background()
	primary, err := auth.PrimaryScope(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	env := KrogerCutoverEnv{
		ClientID: "env-client", ClientSecret: "env-secret", RedirectURI: "https://env.test/cb", Modality: "pickup",
	}
	if err := RunKrogerAppCutover(ctx, db, env); err != nil {
		t.Fatal(err)
	}
	saved, ok, err := appcred.NewVault(db).Load(ctx, primary, "kroger")
	if err != nil || !ok {
		t.Fatalf("load after cutover ok=%v err=%v", ok, err)
	}
	if saved.ClientID != "env-client" || saved.RedirectURI != "https://env.test/cb" || saved.Modality != "PICKUP" {
		t.Fatalf("saved = %+v", saved)
	}
	if err := RunKrogerAppCutover(ctx, db, KrogerCutoverEnv{ClientID: "other", ClientSecret: "x", RedirectURI: "https://other.test/cb"}); err != nil {
		t.Fatal(err)
	}
	saved2, ok, err := appcred.NewVault(db).Load(ctx, primary, "kroger")
	if err != nil || !ok || saved2.ClientID != "env-client" {
		t.Fatalf("second cutover changed row: %+v ok=%v err=%v", saved2, ok, err)
	}
}

func TestKrogerCutoverClearsSecondaryHousehold(t *testing.T) {
	db := openCutoverDB(t)
	ctx := context.Background()
	primary, err := auth.PrimaryScope(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	secondary := "01909999-9999-7000-8000-000000000099"
	if _, err := db.Exec(`INSERT INTO households (id, name, restock_mode, created_at, channel_key, is_primary)
		VALUES (?, 'Secondary', '', '2026-01-01T00:00:00Z', 'abcdef0123456789abcdef0123456789', 0)`, secondary); err != nil {
		t.Fatal(err)
	}
	when := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := db.Exec(`INSERT INTO stores (id, household_id, name, name_key, kind, position, linked_provider, location_id, location_chain, created_at, updated_at)
		VALUES ('store-sec', ?, 'Sec Kroger', 'sec kroger', 'online', 0, 'kroger', '12345678', 'KROGER', ?, ?)`, secondary, when, when); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO provider_connections (id, user_id, household_id, provider_id, state, updated_at)
		VALUES ('conn-sec', 'user-1', ?, 'kroger', 'connected', ?)`, secondary, when); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO provider_app_credentials (id, household_id, user_id, provider_id, client_id, client_secret, redirect_uri, updated_at)
		VALUES (?, ?, 'user-1', 'kroger', 'foreign', 'sec', 'https://foreign.test/cb', ?)`, secondary+":kroger", secondary, when); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO store_checks (household_id, store_id, state, trigger, started_at, lease_until, checked_count, found_count, total_count)
		VALUES (?, 'store-sec', 'done', 'manual', ?, ?, 1, 1, 1)`, secondary, when, when); err != nil {
		t.Fatal(err)
	}
	if err := RunKrogerAppCutover(ctx, db, KrogerCutoverEnv{
		ClientID: "env-client", ClientSecret: "env-secret", RedirectURI: "https://env.test/cb",
	}); err != nil {
		t.Fatal(err)
	}
	var connCount, credCount, checkCount, storeCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM provider_connections WHERE household_id = ? AND provider_id = 'kroger'`, secondary).Scan(&connCount); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM provider_app_credentials WHERE household_id = ? AND provider_id = 'kroger'`, secondary).Scan(&credCount); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM store_checks WHERE household_id = ?`, secondary).Scan(&checkCount); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM stores WHERE household_id = ?`, secondary).Scan(&storeCount); err != nil {
		t.Fatal(err)
	}
	if connCount != 0 || credCount != 0 || checkCount != 0 || storeCount != 1 {
		t.Fatalf("conn=%d cred=%d check=%d store=%d", connCount, credCount, checkCount, storeCount)
	}
	_, ok, err := appcred.NewVault(db).Load(ctx, primary, "kroger")
	if err != nil || !ok {
		t.Fatalf("primary app missing ok=%v err=%v", ok, err)
	}
}

func TestKrogerCutoverSetsPrimaryAppKey(t *testing.T) {
	db := openCutoverDB(t)
	ctx := context.Background()
	primary, err := auth.PrimaryScope(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	when := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := db.Exec(`
		INSERT INTO provider_app_credentials (id, household_id, user_id, provider_id, client_id, client_secret, redirect_uri, updated_at)
		VALUES (?, ?, 'user-1', 'kroger', 'client-for-key', 'sec', 'https://cb.test/c', ?)`,
		primary.HouseholdID()+":kroger", primary.HouseholdID(), when); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO provider_connections (id, user_id, household_id, provider_id, state, updated_at)
		VALUES ('conn-p', 'user-1', ?, 'kroger', 'connected', ?)`, primary.HouseholdID(), when); err != nil {
		t.Fatal(err)
	}
	if err := RunKrogerAppCutover(ctx, db, KrogerCutoverEnv{}); err != nil {
		t.Fatal(err)
	}
	wantKey := krogerAppKey("client-for-key")
	var got sql.NullString
	if err := db.QueryRow(`SELECT app_key FROM provider_connections WHERE id = 'conn-p'`).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if !got.Valid || got.String != wantKey {
		t.Fatalf("app_key = %v want %q", got, wantKey)
	}
}

func TestKrogerCutoverIncompleteEnvCopiesNothing(t *testing.T) {
	db := openCutoverDB(t)
	ctx := context.Background()
	primary, err := auth.PrimaryScope(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if err := RunKrogerAppCutover(ctx, db, KrogerCutoverEnv{ClientID: "only-id"}); err != nil {
		t.Fatal(err)
	}
	_, ok, err := appcred.NewVault(db).Load(ctx, primary, "kroger")
	if err != nil || ok {
		t.Fatalf("expected no app row ok=%v err=%v", ok, err)
	}
}

func TestKrogerCutoverIdempotentFlag(t *testing.T) {
	db := openCutoverDB(t)
	ctx := context.Background()
	env := KrogerCutoverEnv{ClientID: "a", ClientSecret: "b", RedirectURI: "https://cb.test/x"}
	if err := RunKrogerAppCutover(ctx, db, env); err != nil {
		t.Fatal(err)
	}
	assertCutoverFlag(t, db)
	if err := RunKrogerAppCutover(ctx, db, env); err != nil {
		t.Fatal(err)
	}
}

func TestKrogerCutoverFailureRollsBack(t *testing.T) {
	db := openCutoverDB(t)
	ctx := context.Background()
	if _, err := db.Exec(`
		CREATE TRIGGER block_cutover_flag
		BEFORE INSERT ON app_settings
		WHEN NEW.key = 'kroger_app_cutover_v1'
		BEGIN
			SELECT RAISE(ABORT, 'inject cutover failure');
		END`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = db.Exec(`DROP TRIGGER IF EXISTS block_cutover_flag`) })
	err := RunKrogerAppCutover(ctx, db, KrogerCutoverEnv{ClientID: "a", ClientSecret: "b", RedirectURI: "https://cb.test/x"})
	if err == nil {
		t.Fatal("expected cutover failure")
	}
	var flag string
	rowErr := db.QueryRow(`SELECT value FROM app_settings WHERE key = 'kroger_app_cutover_v1'`).Scan(&flag)
	if rowErr != sql.ErrNoRows {
		t.Fatalf("cutover flag should be absent, got %v err=%v", flag, rowErr)
	}
}

func openCutoverDB(t *testing.T) *sql.DB {
	t.Helper()
	conn, err := OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	conn.SetMaxOpenConns(1)
	t.Cleanup(func() { conn.Close() })
	if err := RunMigrations(conn); err != nil {
		t.Fatal(err)
	}
	return conn
}

func credentialsSnap(t *testing.T, db *sql.DB, householdID string) string {
	t.Helper()
	var clientID, secret, redirect, modality string
	err := db.QueryRow(`
		SELECT client_id, client_secret, redirect_uri, modality FROM provider_app_credentials
		WHERE household_id = ? AND provider_id = 'kroger'`, householdID).Scan(&clientID, &secret, &redirect, &modality)
	if err == sql.ErrNoRows {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return clientID + "|" + secret + "|" + redirect + "|" + modality
}

func assertCutoverFlag(t *testing.T, db *sql.DB) {
	t.Helper()
	var v string
	if err := db.QueryRow(`SELECT value FROM app_settings WHERE key = 'kroger_app_cutover_v1'`).Scan(&v); err != nil || v != "1" {
		t.Fatalf("cutover flag = %q err=%v", v, err)
	}
}
