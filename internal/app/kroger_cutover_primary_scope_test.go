package app

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/auth"
)

func TestKrogerCutoverNilDBIsNoOp(t *testing.T) {
	if err := RunKrogerAppCutover(context.Background(), nil, KrogerCutoverEnv{
		ClientID: "a", ClientSecret: "b", RedirectURI: "https://cb.test/x",
	}); err != nil {
		t.Fatal(err)
	}
}

func TestKrogerCutoverPrimaryHouseholdReadErrorRollsBack(t *testing.T) {
	db := openCutoverDB(t)
	ctx := context.Background()
	secondary := "01909999-9999-7000-8000-000000000088"
	when := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := db.Exec(`INSERT INTO households (id, name, restock_mode, created_at, channel_key, is_primary)
		VALUES (?, 'Rollback sec', '', '2026-01-01T00:00:00Z', 'abcdef0123456789abcdef0123456789', 0)`, secondary); err != nil {
		t.Fatal(err)
	}
	seedHouseholdKrogerSideTables(t, db, secondary, "user-rb", "store-rb", "rb-app", when)
	before := sideTableCounts(t, db, secondary)
	if _, err := db.Exec(`ALTER TABLE households RENAME COLUMN is_primary TO is_primary_hidden`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(`ALTER TABLE households RENAME COLUMN is_primary_hidden TO is_primary`)
	})
	err := RunKrogerAppCutover(ctx, db, KrogerCutoverEnv{ClientID: "a", ClientSecret: "b", RedirectURI: "https://cb.test/x"})
	if err == nil {
		t.Fatal("expected primary household read error")
	}
	if !strings.Contains(err.Error(), "read primary household") {
		t.Fatalf("unexpected error: %v", err)
	}
	after := sideTableCounts(t, db, secondary)
	if after != before {
		t.Fatalf("cutover changed secondary state: before %+v after %+v", before, after)
	}
	var flag string
	rowErr := db.QueryRow(`SELECT value FROM app_settings WHERE key = 'kroger_app_cutover_v1'`).Scan(&flag)
	if rowErr != sql.ErrNoRows {
		t.Fatalf("cutover flag should be absent, got %v err=%v", flag, rowErr)
	}
}

func TestKrogerCutoverNonPrimaryOnlyPreservesPrimarySideTables(t *testing.T) {
	db := openCutoverDB(t)
	ctx := context.Background()
	primary, err := auth.PrimaryScope(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	primaryID := primary.HouseholdID()
	secondary := "01909999-9999-7000-8000-000000000099"
	when := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := db.Exec(`INSERT INTO households (id, name, restock_mode, created_at, channel_key, is_primary)
		VALUES (?, 'Secondary', '', '2026-01-01T00:00:00Z', 'abcdef0123456789abcdef0123456789', 0)`, secondary); err != nil {
		t.Fatal(err)
	}
	seedHouseholdKrogerSideTables(t, db, primaryID, "user-primary", "store-primary", "primary-app", when)
	seedHouseholdKrogerSideTables(t, db, secondary, "user-secondary", "store-sec", "secondary-app", when)

	beforePrimary := sideTableCounts(t, db, primaryID)
	beforeSecondary := sideTableCounts(t, db, secondary)
	if beforePrimary.conn != 1 || beforePrimary.avail != 1 || beforePrimary.checks != 1 || beforePrimary.creds != 1 {
		t.Fatalf("primary seed %+v", beforePrimary)
	}
	if beforeSecondary.conn != 1 || beforeSecondary.avail != 1 || beforeSecondary.checks != 1 || beforeSecondary.creds != 1 {
		t.Fatalf("secondary seed %+v", beforeSecondary)
	}

	env := KrogerCutoverEnv{ClientID: "env-client", ClientSecret: "env-secret", RedirectURI: "https://env.test/cb"}
	if err := RunKrogerAppCutover(ctx, db, env); err != nil {
		t.Fatal(err)
	}

	afterPrimary := sideTableCounts(t, db, primaryID)
	if afterPrimary.conn != beforePrimary.conn || afterPrimary.avail != beforePrimary.avail ||
		afterPrimary.checks != beforePrimary.checks || afterPrimary.creds != beforePrimary.creds ||
		afterPrimary.stores != beforePrimary.stores {
		t.Fatalf("primary changed: before %+v after %+v", beforePrimary, afterPrimary)
	}
	var primaryClientID string
	if err := db.QueryRow(`SELECT client_id FROM provider_app_credentials WHERE household_id = ? AND provider_id = 'kroger'`, primaryID).Scan(&primaryClientID); err != nil || primaryClientID != "primary-app" {
		t.Fatalf("primary app row = %q err=%v", primaryClientID, err)
	}

	afterSecondary := sideTableCounts(t, db, secondary)
	if afterSecondary.conn != 0 || afterSecondary.avail != 0 || afterSecondary.checks != 0 || afterSecondary.creds != 0 {
		t.Fatalf("secondary Kroger state not cleared: %+v", afterSecondary)
	}
	if afterSecondary.stores != 1 {
		t.Fatalf("secondary store removed stores=%d", afterSecondary.stores)
	}

	if err := RunKrogerAppCutover(ctx, db, env); err != nil {
		t.Fatal(err)
	}
	afterRerunPrimary := sideTableCounts(t, db, primaryID)
	afterRerunSecondary := sideTableCounts(t, db, secondary)
	if afterRerunPrimary != afterPrimary || afterRerunSecondary != afterSecondary {
		t.Fatalf("rerun changed primary %+v->%+v secondary %+v->%+v",
			afterPrimary, afterRerunPrimary, afterSecondary, afterRerunSecondary)
	}
}

func TestKrogerCutoverFailureRollsBackSecondaryData(t *testing.T) {
	db := openCutoverDB(t)
	ctx := context.Background()
	secondary := "01909999-9999-7000-8000-000000000088"
	when := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := db.Exec(`INSERT INTO households (id, name, restock_mode, created_at, channel_key, is_primary)
		VALUES (?, 'Rollback sec', '', '2026-01-01T00:00:00Z', 'abcdef0123456789abcdef0123456789', 0)`, secondary); err != nil {
		t.Fatal(err)
	}
	seedHouseholdKrogerSideTables(t, db, secondary, "user-rb", "store-rb", "rb-app", when)
	if _, err := db.Exec(`
		CREATE TRIGGER block_cutover_flag_rb
		BEFORE INSERT ON app_settings
		WHEN NEW.key = 'kroger_app_cutover_v1'
		BEGIN
			SELECT RAISE(ABORT, 'inject cutover failure');
		END`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = db.Exec(`DROP TRIGGER IF EXISTS block_cutover_flag_rb`) })

	err := RunKrogerAppCutover(ctx, db, KrogerCutoverEnv{ClientID: "a", ClientSecret: "b", RedirectURI: "https://cb.test/x"})
	if err == nil {
		t.Fatal("expected cutover failure")
	}
	after := sideTableCounts(t, db, secondary)
	if after.conn != 1 || after.avail != 1 || after.checks != 1 || after.creds != 1 {
		t.Fatalf("secondary rolled back with deletes: %+v", after)
	}
}

type sideCounts struct {
	conn, avail, checks, creds, stores int
}

func sideTableCounts(t *testing.T, db *sql.DB, householdID string) sideCounts {
	t.Helper()
	var c sideCounts
	queries := []struct {
		dest *int
		q    string
	}{
		{&c.conn, `SELECT COUNT(*) FROM provider_connections WHERE household_id = ? AND provider_id = 'kroger'`},
		{&c.avail, `SELECT COUNT(*) FROM store_availability WHERE household_id = ?`},
		{&c.checks, `SELECT COUNT(*) FROM store_checks WHERE household_id = ?`},
		{&c.creds, `SELECT COUNT(*) FROM provider_app_credentials WHERE household_id = ? AND provider_id = 'kroger'`},
		{&c.stores, `SELECT COUNT(*) FROM stores WHERE household_id = ?`},
	}
	for _, item := range queries {
		if err := db.QueryRow(item.q, householdID).Scan(item.dest); err != nil {
			t.Fatal(err)
		}
	}
	return c
}

func seedHouseholdKrogerSideTables(t *testing.T, db *sql.DB, householdID, userID, storeID, clientID, when string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO stores (id, household_id, name, name_key, kind, position, linked_provider, location_id, location_chain, created_at, updated_at)
		VALUES (?, ?, 'Kroger', 'kroger', 'online', 0, 'kroger', '12345678', 'KROGER', ?, ?)`, storeID, householdID, when, when); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO provider_connections (id, user_id, household_id, provider_id, state, updated_at)
		VALUES (?, ?, ?, 'kroger', 'connected', ?)`, storeID+":conn", userID, householdID, when); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO provider_app_credentials (id, household_id, user_id, provider_id, client_id, client_secret, redirect_uri, updated_at)
		VALUES (?, ?, ?, 'kroger', ?, 'sec', 'https://cb.test/c', ?)`, householdID+":kroger", householdID, userID, clientID, when); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO store_availability (household_id, store_id, product_id, location_id, status, checked_at, status_expires_at)
		VALUES (?, ?, 'prod-1', '12345678', 'carried', ?, ?)`, householdID, storeID, when, when); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO store_checks (household_id, store_id, state, trigger, started_at, lease_until, checked_count, found_count, total_count)
		VALUES (?, ?, 'done', 'manual', ?, ?, 1, 1, 1)`, householdID, storeID, when, when); err != nil {
		t.Fatal(err)
	}
}
