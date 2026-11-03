package app

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/auth"
)

func TestKrogerCutoverEmptyModalityDefaultsPickup(t *testing.T) {
	db := openCutoverDB(t)
	ctx := context.Background()
	if err := RunKrogerAppCutover(ctx, db, KrogerCutoverEnv{
		ClientID: "env-client", ClientSecret: "env-secret", RedirectURI: "https://env.test/cb", Modality: "  ",
	}); err != nil {
		t.Fatal(err)
	}
	var modality string
	if err := db.QueryRow(`SELECT modality FROM provider_app_credentials WHERE provider_id = 'kroger' LIMIT 1`).Scan(&modality); err != nil {
		t.Fatal(err)
	}
	if modality != "PICKUP" {
		t.Fatalf("modality=%q", modality)
	}
}

func TestKrogerCutoverBackfillsPrimaryConnectionAppKey(t *testing.T) {
	db := openCutoverDB(t)
	ctx := context.Background()
	primary, err := auth.PrimaryScope(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	primaryID := primary.HouseholdID()
	when := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := db.Exec(`
		INSERT INTO provider_app_credentials (id, household_id, user_id, provider_id, client_id, client_secret, redirect_uri, modality, updated_at)
		VALUES (?, ?, ?, 'kroger', 'primary-client', 'sec', 'https://cb.test/c', 'PICKUP', ?)`,
		primaryID+":kroger", primaryID, primaryID, when); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO provider_connections (id, user_id, household_id, provider_id, state, updated_at)
		VALUES ('conn-no-key', ?, ?, 'kroger', 'connected', ?)`, primaryID, primaryID, when); err != nil {
		t.Fatal(err)
	}
	if err := RunKrogerAppCutover(ctx, db, KrogerCutoverEnv{}); err != nil {
		t.Fatal(err)
	}
	wantKey := krogerAppKey("primary-client")
	var gotKey sql.NullString
	if err := db.QueryRow(`SELECT app_key FROM provider_connections WHERE id = 'conn-no-key'`).Scan(&gotKey); err != nil || !gotKey.Valid || gotKey.String != wantKey {
		t.Fatalf("app_key=%v want %q err=%v", gotKey, wantKey, err)
	}
}

func TestKrogerCutoverSkipsWhenFlagAlreadySet(t *testing.T) {
	db := openCutoverDB(t)
	ctx := context.Background()
	secondary := "01909999-9999-7000-8000-000000000077"
	when := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := db.Exec(`INSERT INTO households (id, name, restock_mode, created_at, channel_key, is_primary)
		VALUES (?, 'Keep me', '', '2026-01-01T00:00:00Z', 'abcdef0123456789abcdef0123456789', 0)`, secondary); err != nil {
		t.Fatal(err)
	}
	seedHouseholdKrogerSideTables(t, db, secondary, "user-keep", "store-keep", "keep-app", when)
	if _, err := db.Exec(`INSERT INTO app_settings (key, value) VALUES ('kroger_app_cutover_v1', '1')`); err != nil {
		t.Fatal(err)
	}
	if err := RunKrogerAppCutover(ctx, db, KrogerCutoverEnv{ClientID: "x", ClientSecret: "y", RedirectURI: "https://cb.test/x"}); err != nil {
		t.Fatal(err)
	}
	after := sideTableCounts(t, db, secondary)
	if after.conn != 1 || after.creds != 1 {
		t.Fatalf("cutover ran despite flag: %+v", after)
	}
}
