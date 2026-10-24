package app

import (
	"strings"
	"testing"
	"time"
)

func TestHouseholdKeysAllowTheSameValueInTwoHouseholds(t *testing.T) {
	conn, err := OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := RunMigrations(conn); err != nil {
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
	if _, err := conn.Exec(`INSERT INTO products (id, name) VALUES ('prod-key', 'Key oats')`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO items (id, user_id, product_id, household_id) VALUES ('item-key', ?, 'prod-key', ?)`, primary, primary); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`DELETE FROM history_repair`); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)

	inserts := []struct {
		name  string
		query string
		args  []any
	}{
		{"supply_overrides", `INSERT INTO supply_overrides (household_id, product_id, quantity) VALUES (?, 'prod-key', 2)`, nil},
		{"product_group_members", `INSERT INTO product_group_members (household_id, product_id, group_id) VALUES (?, 'prod-key', 'group-key')`, nil},
		{"scan_alert_schedule", `INSERT INTO scan_alert_schedule (household_id, direction, due_at, scan_ids) VALUES (?, 'stock_in', '2026-04-01T00:00:00Z', '[]')`, nil},
		{"push_subscriptions", `INSERT INTO push_subscriptions (household_id, endpoint, p256dh, auth) VALUES (?, 'https://example.test/push', 'key', 'auth')`, nil},
		{"fulfillment_ledger", `INSERT INTO fulfillment_ledger (household_id, provider_id, item_id, ledger_boundary) VALUES (?, 'kroger', 'item-key', ?)`, []any{at}},
		{"barcodes", `INSERT INTO barcodes (household_id, barcode, product_id, source, user_id) VALUES (?, '000111222333', 'prod-key', 'user_override', '')`, nil},
		{"history_repair", `INSERT INTO history_repair (household_id, id) VALUES (?, 1)`, nil},
		{"stock_in_events", `INSERT INTO stock_in_events (household_id, id, product_id, at) VALUES (?, 7, 'prod-key', ?)`, []any{at}},
	}
	for _, household := range []string{primary, cedar} {
		for _, insert := range inserts {
			args := append([]any{household}, insert.args...)
			if _, err := conn.Exec(insert.query, args...); err != nil {
				t.Fatalf("%s household %s: %v", insert.name, household, err)
			}
		}
	}
	for _, insert := range inserts {
		if _, err := conn.Exec(insert.query, append([]any{primary}, insert.args...)...); err == nil {
			t.Fatalf("%s accepted a second row for the same household", insert.name)
		}
	}

	var defaults string
	rows, err := conn.Query(`SELECT sql FROM sqlite_master WHERE sql LIKE '%user-1%'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var sqlText string
		if err := rows.Scan(&sqlText); err != nil {
			t.Fatal(err)
		}
		defaults += sqlText
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(defaults, "user-1") {
		t.Fatalf("schema still defaults an account id:\n%s", defaults)
	}
}
