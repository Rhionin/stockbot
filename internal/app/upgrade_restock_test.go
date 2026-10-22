package app

import (
	"database/sql"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// TestUpgradeRestockTargetsFrom018 applies every migration before 019, seeds the
// rows a household could already have, then runs the 019 file itself.
func TestUpgradeRestockTargetsFrom018(t *testing.T) {
	conn, err := OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := applyMigrationsExcept(conn, "019_restock_targets.sql"); err != nil {
		t.Fatal(err)
	}

	stmts := []string{
		`INSERT INTO products (id, name) VALUES
			('gprod', 'Grouped beans'),
			('wprod', 'Window oats'),
			('qprod', 'Quantity flour'),
			('nprod', 'Quiet zucchini'),
			('tprod', 'Target chips'),
			('rprod', 'Replenish milk'),
			('zprod', 'Zero salt'),
			('bprod', 'Big rice')`,
		`INSERT INTO items (id, user_id, product_id, target_quantity, replenishment_mode) VALUES
			('item-g', 'user-1', 'gprod', 8, 'target'),
			('item-w', 'user-1', 'wprod', NULL, 'target'),
			('item-q', 'user-1', 'qprod', NULL, 'target'),
			('item-n', 'user-1', 'nprod', NULL, 'target'),
			('item-t', 'user-1', 'tprod', 4, 'target'),
			('item-r', 'user-1', 'rprod', 9, 'replenish'),
			('item-z', 'user-1', 'zprod', 0, 'target'),
			('item-b', 'user-1', 'bprod', 1000, 'target')`,
		`INSERT INTO product_groups (id, user_id, name, name_key) VALUES
			('g1', 'user-1', 'Grouped beans', 'grouped-beans'),
			('g2', 'user-1', 'Quiet', 'quiet')`,
		`INSERT INTO product_group_members (product_id, group_id, no_restock) VALUES
			('gprod', 'g1', 0),
			('nprod', 'g2', 1)`,
		`INSERT INTO supply_overrides (product_id, quantity) VALUES ('gprod', 4)`,
		`INSERT INTO supply_overrides (product_id, window_months) VALUES ('wprod', 6)`,
		`INSERT INTO supply_overrides (product_id, quantity) VALUES ('qprod', 3)`,
		`INSERT INTO app_settings (key, value) VALUES ('supply_months', '4')`,
	}
	for _, stmt := range stmts {
		if _, err := conn.Exec(stmt); err != nil {
			t.Fatalf("seed: %v\n%s", err, stmt)
		}
	}

	sqlBytes, err := migrationsFS.ReadFile("migrations/019_restock_targets.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(string(sqlBytes)); err != nil {
		t.Fatalf("019: %v", err)
	}

	rows, err := conn.Query(`SELECT item_id, kind, value FROM item_targets ORDER BY item_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := map[string]string{}
	for rows.Next() {
		var id, kind string
		var value float64
		if err := rows.Scan(&id, &kind, &value); err != nil {
			t.Fatal(err)
		}
		got[id] = kind + " " + strconv.FormatFloat(value, 'f', -1, 64)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"item-w": "months 6",
		"item-q": "amount 3",
		"item-n": "months 0",
		"item-t": "amount 4",
		"item-b": "amount 999",
	}
	if len(got) != len(want) {
		t.Fatalf("item targets: got %#v want %#v", got, want)
	}
	for id, value := range want {
		if got[id] != value {
			t.Fatalf("%s: got %s want %s (all %#v)", id, got[id], value, got)
		}
	}
	var kind, value string
	if err := conn.QueryRow(`SELECT value FROM app_settings WHERE key = 'supply_target_kind'`).Scan(&kind); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow(`SELECT value FROM app_settings WHERE key = 'supply_target_value'`).Scan(&value); err != nil {
		t.Fatal(err)
	}
	if kind != "months" || value != "4" {
		t.Fatalf("household default: %s %s", kind, value)
	}
}

func TestUpgradeIgnoresSupplyMonthsOutsideOneToTwelve(t *testing.T) {
	for _, raw := range []string{"0", "abc", "13", "4.5"} {
		t.Run(raw, func(t *testing.T) {
			conn, err := OpenSQLite(":memory:")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { conn.Close() })
			if err := applyMigrationsExcept(conn, "019_restock_targets.sql"); err != nil {
				t.Fatal(err)
			}
			if _, err := conn.Exec(`INSERT INTO app_settings (key, value) VALUES ('supply_months', ?)`, raw); err != nil {
				t.Fatal(err)
			}
			sqlBytes, err := migrationsFS.ReadFile("migrations/019_restock_targets.sql")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := conn.Exec(string(sqlBytes)); err != nil {
				t.Fatalf("019: %v", err)
			}
			var count int
			if err := conn.QueryRow(`SELECT COUNT(*) FROM app_settings WHERE key IN ('supply_target_kind', 'supply_target_value')`).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != 0 {
				t.Fatalf("supply_months %q was copied into %d household default keys", raw, count)
			}
		})
	}
}

func applyMigrationsExcept(db *sql.DB, skip string) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		filename TEXT PRIMARY KEY,
		applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`); err != nil {
		return err
	}
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		return err
	}
	var files []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") || entry.Name() == skip {
			continue
		}
		files = append(files, entry.Name())
	}
	sort.Strings(files)
	for _, name := range files {
		if name == shoppingChangeMigration {
			if err := snapshotShoppingDB(db); err != nil {
				return err
			}
		}
		if name == restockChangeMigration {
			if err := snapshotRestockDB(db); err != nil {
				return err
			}
		}
		sqlBytes, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}
		if _, err := db.Exec(string(sqlBytes)); err != nil {
			return err
		}
		if _, err := db.Exec(`INSERT INTO schema_migrations (filename) VALUES (?)`, name); err != nil {
			return err
		}
	}
	return nil
}
