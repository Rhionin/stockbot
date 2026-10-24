package app

import (
	"testing"
)

func TestUpgradeCopiesHouseholdSettingsAndTypedNames(t *testing.T) {
	conn, err := OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := applyMigrationsExcept(conn, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := applyHouseholdMigrations(conn); err != nil {
		t.Fatal(err)
	}

	var primary string
	if err := conn.QueryRow(`SELECT id FROM households WHERE is_primary = 1`).Scan(&primary); err != nil {
		t.Fatal(err)
	}
	const cedar = "0190bbbb-cccc-7000-8000-0000000000e5"
	if _, err := conn.Exec(`
		INSERT INTO households (id, name, restock_mode, created_at, channel_key, is_primary)
		VALUES (?, 'Household Cedar', '', '2026-02-02T00:00:00Z', 'abcdef0123456789abcdef0123456789', 0)`, cedar); err != nil {
		t.Fatal(err)
	}
	const legacyName = "legacy-sentinel-m3n8r beans"
	if _, err := conn.Exec(`
		INSERT INTO products (id, name, source, created_at)
		VALUES ('legacy-sentinel', ?, 'user', '2026-01-01T00:00:00Z')`, legacyName); err != nil {
		t.Fatal(err)
	}
	settings := []struct{ key, value string }{
		{"onboarding_started_at", "2024-05-06T07:08:09Z"},
		{"supply_target_kind", "months"},
		{"supply_target_value", "6"},
		{"default_group_rule", "favorite"},
		{"group_default_rule", "best_deal"},
		{"group_suggestions_seeded_v1", "1"},
		{"product_contributions_enabled", "true"},
	}
	for _, setting := range settings {
		if _, err := conn.Exec(`INSERT INTO app_settings (key, value) VALUES (?, ?)`, setting.key, setting.value); err != nil {
			t.Fatal(err)
		}
	}

	sqlBytes, err := migrationsFS.ReadFile("migrations/023_catalog_overrides.sql")
	if err != nil {
		t.Fatal(err)
	}
	if err := applyMigration(conn, "023_catalog_overrides.sql", string(sqlBytes)); err != nil {
		t.Fatal(err)
	}

	var storedName string
	if err := conn.QueryRow(`SELECT name FROM products WHERE id = 'legacy-sentinel'`).Scan(&storedName); err != nil {
		t.Fatal(err)
	}
	if storedName != "" {
		t.Fatalf("typed catalog name survived migration: %q", storedName)
	}
	var display string
	if err := conn.QueryRow(`
		SELECT display_name FROM household_product_overrides
		WHERE household_id = ? AND product_id = 'legacy-sentinel'`, primary).Scan(&display); err != nil {
		t.Fatal(err)
	}
	if display != legacyName {
		t.Fatalf("primary display name %q", display)
	}
	var leaked int
	if err := conn.QueryRow(`
		SELECT COUNT(*) FROM household_product_overrides
		WHERE household_id = ? AND product_id = 'legacy-sentinel'`, cedar).Scan(&leaked); err != nil {
		t.Fatal(err)
	}
	if leaked != 0 {
		t.Fatalf("cedar received the primary typed name")
	}
	for _, setting := range settings {
		var got string
		if err := conn.QueryRow(`
			SELECT value FROM household_settings WHERE household_id = ? AND key = ?`,
			primary, setting.key).Scan(&got); err != nil {
			t.Fatalf("primary %s: %v", setting.key, err)
		}
		if got != setting.value {
			t.Fatalf("primary %s = %q, want %q", setting.key, got, setting.value)
		}
		var other int
		if err := conn.QueryRow(`
			SELECT COUNT(*) FROM household_settings WHERE household_id = ? AND key = ?`,
			cedar, setting.key).Scan(&other); err != nil {
			t.Fatal(err)
		}
		if other != 0 {
			t.Fatalf("cedar received %s", setting.key)
		}
	}
}
