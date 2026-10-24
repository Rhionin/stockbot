package app

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestHouseholdMigrationMovesEveryRowToHouseholdOne(t *testing.T) {
	orig := beforeHouseholdMigration
	t.Cleanup(func() { beforeHouseholdMigration = orig })

	var before map[string]int
	beforeHouseholdMigration = func(db *sql.DB) error {
		if err := seedSyntheticPantry(db); err != nil {
			return err
		}
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		before, err = tableCounts(tx)
		return err
	}

	conn, err := OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := RunMigrations(conn); err != nil {
		t.Fatal(err)
	}

	var household, legacy string
	var primary int
	if err := conn.QueryRow(`SELECT id, legacy_user_id, is_primary FROM households`).Scan(&household, &legacy, &primary); err != nil {
		t.Fatal(err)
	}
	if legacy != "user-1" || primary != 1 {
		t.Fatalf("household legacy=%s primary=%d", legacy, primary)
	}
	parsed, err := uuid.Parse(household)
	if err != nil || parsed.Version() != 7 || parsed.String() != household {
		t.Fatalf("household id %s", household)
	}
	var setting string
	if err := conn.QueryRow(`SELECT value FROM app_settings WHERE key = 'primary_household_id'`).Scan(&setting); err != nil {
		t.Fatal(err)
	}
	if setting != household {
		t.Fatalf("setting %s household %s", setting, household)
	}

	for name, n := range before {
		if name == "schema_migrations" || name == "app_settings" {
			continue
		}
		var got int
		if err := conn.QueryRow(`SELECT COUNT(*) FROM ` + quoteIdent(name)).Scan(&got); err != nil {
			t.Fatal(name, err)
		}
		if got != n {
			t.Errorf("%s before %d after %d", name, n, got)
		}
	}
	var settings int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM app_settings`).Scan(&settings); err != nil {
		t.Fatal(err)
	}
	if settings != before["app_settings"]+1 {
		t.Fatalf("app_settings before %d after %d", before["app_settings"], settings)
	}

	owned := []string{
		"items", "barcodes", "item_instances", "scan_entries", "consumption_events",
		"shopping_list_items", "brand_preferences", "item_deals", "provider_connections",
		"fulfillment_ledger", "shopping_list_entry_adjustments", "provider_app_credentials",
		"product_contributions", "stock_in_events", "supply_overrides", "staged_cart_skips",
		"product_groups", "product_group_members", "group_suggestions", "group_suggestion_members",
		"group_suggestion_dismissals", "stock_moves", "push_subscriptions", "scan_alert_schedule",
		"history_repair", "categories", "category_targets", "item_targets", "item_categories",
		"category_suggestion_dismissals",
	}
	for _, table := range owned {
		var other int
		if err := conn.QueryRow(`SELECT COUNT(*) FROM `+quoteIdent(table)+` WHERE household_id != ?`, household).Scan(&other); err != nil {
			t.Fatalf("%s: %v", table, err)
		}
		if other != 0 {
			t.Errorf("%s has %d rows outside household 1", table, other)
		}
		var n int
		if err := conn.QueryRow(`SELECT COUNT(*) FROM ` + quoteIdent(table)).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			t.Errorf("%s is empty", table)
		}
	}
	for _, table := range []string{"products", "barcode_misses", "product_off_tags"} {
		if columnExists(t, conn, table, "household_id") {
			t.Errorf("%s gained household_id", table)
		}
	}
	var label, kind string
	if err := conn.QueryRow(`SELECT label, kind FROM devices`).Scan(&label, &kind); err != nil {
		t.Fatal(err)
	}
	if label != "This server's scanner" || kind != "direct" {
		t.Fatalf("device %s %s", kind, label)
	}
	var versions int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM change_versions WHERE household_id = ?`, household).Scan(&versions); err != nil {
		t.Fatal(err)
	}
	if versions != 4 {
		t.Fatalf("change versions %d", versions)
	}
	if triggerExists(t, conn, "unshelve_item_on_stock_in") || triggerExists(t, conn, "delete_item_target_with_item") {
		t.Fatal("household triggers remained")
	}
	var actor string
	if err := conn.QueryRow(`SELECT actor_user_id FROM items WHERE id = 'item-sample'`).Scan(&actor); err != nil {
		t.Fatal(err)
	}
	if actor != "user-1" {
		t.Fatalf("actor %s", actor)
	}
	var globalActor sql.NullString
	if err := conn.QueryRow(`SELECT actor_user_id FROM barcodes WHERE barcode = '0000000000000'`).Scan(&globalActor); err != nil {
		t.Fatal(err)
	}
	if globalActor.Valid {
		t.Fatalf("global barcode actor %q", globalActor.String)
	}
}

func TestHouseholdMigrationAbortsOutsideTheFirstHousehold(t *testing.T) {
	orig := beforeHouseholdMigration
	t.Cleanup(func() { beforeHouseholdMigration = orig })
	beforeHouseholdMigration = func(db *sql.DB) error {
		_, err := db.Exec(`INSERT INTO products (id, name) VALUES ('prod-sample', 'Sample oats')`)
		if err != nil {
			return err
		}
		_, err = db.Exec(`INSERT INTO items (id, user_id, product_id) VALUES ('item-other', 'other-user', 'prod-sample')`)
		return err
	}

	conn, err := OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	err = RunMigrations(conn)
	if err == nil || !strings.Contains(err.Error(), "items.user_id: 1 rows outside the first household") {
		t.Fatalf("RunMigrations err = %v", err)
	}
	if strings.Contains(err.Error(), "other-user") || strings.Contains(err.Error(), "Sample oats") {
		t.Fatalf("abort named a row: %v", err)
	}
	if tableExists(t, conn, "households") {
		t.Fatal("households table remained after abort")
	}
	var recorded int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE filename = '022a_household_tables.sql'`).Scan(&recorded); err != nil {
		t.Fatal(err)
	}
	if recorded != 0 {
		t.Fatalf("022a recorded: %d", recorded)
	}
	var userID string
	if err := conn.QueryRow(`SELECT user_id FROM items WHERE id = 'item-other'`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if userID != "other-user" {
		t.Fatalf("row changed to %s", userID)
	}
}

func TestHouseholdMigrationFailureRollsBackEveryStep(t *testing.T) {
	origSQL := prepareMigrationSQL
	origSeed := beforeHouseholdMigration
	t.Cleanup(func() {
		prepareMigrationSQL = origSQL
		beforeHouseholdMigration = origSeed
	})
	prepareMigrationSQL = func(name, sqlText string) string {
		if name != "022c_household_backfill.sql" {
			return sqlText
		}
		return sqlText + "\nSELECT * FROM household_migration_should_not_exist;\n"
	}

	conn, err := OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	err = RunMigrations(conn)
	if err == nil || !strings.Contains(err.Error(), "022c_household_backfill.sql") {
		t.Fatalf("RunMigrations err = %v", err)
	}
	if tableExists(t, conn, "households") {
		t.Fatal("households remained after rollback")
	}
	var recorded int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE filename LIKE '022%' AND filename != '022_backfill_unshelve.sql'`).Scan(&recorded); err != nil {
		t.Fatal(err)
	}
	if recorded != 0 {
		t.Fatalf("household steps recorded: %d", recorded)
	}

	prepareMigrationSQL = origSQL
	if err := RunMigrations(conn); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if !tableExists(t, conn, "households") {
		t.Fatal("retry did not add households")
	}
}

func TestHouseholdMigrationDoesNotSnapshotThePantryFile(t *testing.T) {
	dir := t.TempDir()
	conn := openFileDB(t, filepath.Join(dir, "pantry.db"))
	if err := RunMigrations(conn); err != nil {
		t.Fatal(err)
	}
	matches, err := filepath.Glob(filepath.Join(dir, "pantry-pre-households-*.db"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("household snapshot files: %v", matches)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), "household") {
			t.Fatalf("unexpected file %s", entry.Name())
		}
	}
}

func seedSyntheticPantry(db *sql.DB) error {
	_, err := db.Exec(`
		INSERT INTO products (id, name) VALUES ('prod-sample', 'Sample oats'), ('prod-other', 'Sample rice');
		INSERT INTO items (id, user_id, product_id) VALUES ('item-sample', 'user-1', 'prod-sample');
		INSERT INTO barcodes (barcode, product_id, source, user_id) VALUES
			('0000000000000', 'prod-sample', 'global', ''),
			('0000000000001', 'prod-sample', 'user_override', 'user-1');
		INSERT INTO item_instances (id, item_id, stock_in_at) VALUES ('inst-sample', 'item-sample', '2026-01-01T00:00:00Z');
		INSERT INTO scan_entries (id, user_id, barcode, scanned_at, direction, status) VALUES
			('scan-sample', 'user-1', '0000000000001', '2026-01-01T00:00:00Z', 'stock_in', 'committed');
		INSERT INTO consumption_events (id, item_id, consumed_at) VALUES ('cons-sample', 'item-sample', '2026-01-02T00:00:00Z');
		INSERT INTO shopping_list_items (id, user_id, item_id, quantity, source) VALUES
			('line-sample', 'user-1', 'item-sample', 1, 'manual');
		INSERT INTO brand_preferences (user_id, need_key, item_id) VALUES ('user-1', 'sample-oats', 'item-sample');
		INSERT INTO item_deals (user_id, item_id, label) VALUES ('user-1', 'item-sample', 'sample');
		INSERT INTO provider_connections (id, user_id, provider_id) VALUES ('conn-sample', 'user-1', 'sample');
		INSERT INTO fulfillment_ledger (provider_id, item_id, ledger_boundary) VALUES ('sample', 'item-sample', '2026-01-01T00:00:00Z');
		INSERT INTO shopping_list_entry_adjustments (entry_id, provider_id, quantity) VALUES ('line-sample', 'sample', 1);
		INSERT INTO provider_app_credentials (id, user_id, provider_id, client_id, client_secret, redirect_uri) VALUES
			('cred-sample', 'user-1', 'sample', 'client', 'secret', 'https://example.test/callback');
		INSERT INTO product_contributions (id, product_id, barcode, external_source, status) VALUES
			('contrib-sample', 'prod-sample', '0000000000001', 'openfoodfacts', 'submitted');
		INSERT INTO stock_in_events (product_id, at) VALUES ('prod-sample', '2026-01-01T00:00:00Z');
		INSERT INTO supply_overrides (product_id, window_months) VALUES ('prod-other', 2);
		INSERT INTO staged_cart_skips (user_id, item_id) VALUES ('user-1', 'item-sample');
		INSERT INTO product_groups (id, user_id, name, name_key, rule) VALUES
			('group-sample', 'user-1', 'Sample grains', 'sample grains', 'same_as_ran_out');
		INSERT INTO product_group_members (product_id, group_id) VALUES ('prod-sample', 'group-sample');
		INSERT INTO group_suggestions (id, user_id, kind, title) VALUES
			('sug-sample', 'user-1', 'looks_alike', 'Sample grains');
		INSERT INTO group_suggestion_members (suggestion_id, product_id) VALUES ('sug-sample', 'prod-sample');
		INSERT INTO group_suggestion_dismissals (user_id, product_id_a, product_id_b) VALUES
			('user-1', 'prod-other', 'prod-sample');
		INSERT INTO stock_moves (id, item_id, product_id, direction, quantity, at, source) VALUES
			('move-sample', 'item-sample', 'prod-sample', 'in', 1, '2026-01-01T00:00:00Z', 'manual');
		INSERT INTO push_subscriptions (endpoint, p256dh, auth, user_id) VALUES
			('https://example.test/push', 'key', 'auth', 'user-1');
		INSERT INTO scan_alert_schedule (direction, due_at, scan_ids) VALUES
			('stock_in', '2026-01-01T00:00:00Z', '[]');
		INSERT INTO categories (id, household_id, name, name_key) VALUES
			('cat-sample', 'user-1', 'Sample shelf', 'sample shelf'),
			('cat-blank', '', 'Sample blank', 'sample blank');
		INSERT INTO category_targets (household_id, category_id, kind, value) VALUES ('user-1', 'cat-sample', 'months', 1);
		INSERT INTO item_targets (household_id, item_id, kind, value) VALUES ('user-1', 'item-sample', 'amount', 2);
		INSERT INTO item_categories (household_id, item_id, category_id, source) VALUES
			('user-1', 'item-sample', 'cat-sample', 'user');
		INSERT INTO category_suggestion_dismissals (household_id, item_id, category_key) VALUES
			('', 'item-sample', 'sample blank');
		INSERT INTO product_off_tags (product_id) VALUES ('prod-sample');
		INSERT INTO barcode_misses (barcode, checked_at) VALUES ('0000000000099', '2026-01-01T00:00:00Z');
	`)
	return err
}
