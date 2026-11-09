package app

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

func TestMigration031CheckConstraints(t *testing.T) {
	conn, err := OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := RunMigrations(conn); err != nil {
		t.Fatal(err)
	}
	var household string
	if err := conn.QueryRow(`SELECT id FROM households WHERE is_primary = 1`).Scan(&household); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`UPDATE households SET trip_closed_cause = 'bogus' WHERE id = ?`, household); err == nil {
		t.Fatal("trip_closed_cause should reject bogus")
	}
	if _, err := conn.Exec(`UPDATE households SET stock_in_cluster_min = 1 WHERE id = ?`, household); err == nil {
		t.Fatal("stock_in_cluster_min should reject 1")
	}
	if _, err := conn.Exec(`UPDATE households SET stock_in_cluster_min = 51 WHERE id = ?`, household); err == nil {
		t.Fatal("stock_in_cluster_min should reject 51")
	}
}

func TestMigration031FreshAndIdempotent(t *testing.T) {
	conn, err := OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := RunMigrations(conn); err != nil {
		t.Fatalf("first RunMigrations: %v", err)
	}
	assertFull031State(t, conn)
	masterAfterFirst, err := snapshotSqliteMaster(conn)
	if err != nil {
		t.Fatal(err)
	}
	if err := RunMigrations(conn); err != nil {
		t.Fatalf("second RunMigrations: %v", err)
	}
	masterAfterSecond, err := snapshotSqliteMaster(conn)
	if err != nil {
		t.Fatal(err)
	}
	if masterAfterFirst != masterAfterSecond {
		t.Fatal("schema changed on repeat RunMigrations")
	}
}

func TestMigration031PreservesStockInEvents(t *testing.T) {
	conn, err := OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := RunMigrationsThrough(conn, tripScopedSkipsMigration); err != nil {
		t.Fatal(err)
	}
	var household string
	if err := conn.QueryRow(`SELECT id FROM households WHERE is_primary = 1`).Scan(&household); err != nil {
		t.Fatal(err)
	}
	const at = "2026-02-01T08:00:00Z"
	if _, err := conn.Exec(`INSERT INTO stock_in_events (household_id, id, product_id, at) VALUES (?, 1, 'prod-legacy', ?)`,
		household, at); err != nil {
		t.Fatal(err)
	}
	if err := RunMigrations(conn); err != nil {
		t.Fatal(err)
	}
	var storeID sql.NullString
	if err := conn.QueryRow(`SELECT store_id FROM stock_in_events WHERE product_id = 'prod-legacy'`).Scan(&storeID); err != nil {
		t.Fatal(err)
	}
	if storeID.Valid {
		t.Fatalf("store_id should stay null: %v", storeID)
	}
}

func TestMigration031PreservesExistingSkips(t *testing.T) {
	conn, err := OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := RunMigrationsThrough(conn, tripScopedSkipsMigration); err != nil {
		t.Fatal(err)
	}
	var household string
	if err := conn.QueryRow(`SELECT id FROM households WHERE is_primary = 1`).Scan(&household); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO staged_cart_skips (user_id, item_id, household_id) VALUES (?, 'item-legacy', ?)`,
		household, household); err != nil {
		t.Fatal(err)
	}
	if err := RunMigrations(conn); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}
	var n int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM staged_cart_skips WHERE item_id = 'item-legacy'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("legacy skip row count: %d", n)
	}
	var expires int
	var actor, removed sql.NullString
	if err := conn.QueryRow(`SELECT expires_trip, actor_user_id, removed_at FROM staged_cart_skips WHERE item_id = 'item-legacy'`).
		Scan(&expires, &actor, &removed); err != nil {
		t.Fatal(err)
	}
	if expires != 1 {
		t.Fatalf("expires_trip: %d", expires)
	}
	if actor.Valid || removed.Valid {
		t.Fatalf("legacy actor/time should be null: %v %v", actor, removed)
	}
}

func TestMigration031RollsBackOnFailure(t *testing.T) {
	cases := []struct {
		name   string
		fileDB bool
		marker string
	}{
		{name: "after households ALTER in memory", marker: "ALTER TABLE households ADD COLUMN trip_seq"},
		{name: "after last statement in memory", marker: "CREATE INDEX IF NOT EXISTS idx_stock_moves_household_at"},
		{name: "after last statement on WAL file", fileDB: true, marker: "CREATE INDEX IF NOT EXISTS idx_stock_moves_household_at"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			conn, cleanup := openPre031Database(t, tc.fileDB)
			defer cleanup()
			baseline, err := snapshotSqliteMaster(conn)
			if err != nil {
				t.Fatal(err)
			}
			inject031FailAfter(t, tc.marker)
			if err := RunMigrations(conn); err == nil {
				t.Fatal("expected migration 031 to fail")
			}
			cleanupMigration031TestArtifacts(t, conn)
			got, err := snapshotSqliteMaster(conn)
			if err != nil {
				t.Fatal(err)
			}
			if got != baseline {
				t.Fatalf("sqlite_master changed after rolled-back 031")
			}
			if err := RunMigrations(conn); err != nil {
				t.Fatalf("rerun after rollback: %v", err)
			}
			assertFull031State(t, conn)
		})
	}
}

func openPre031Database(t *testing.T, file bool) (*sql.DB, func()) {
	t.Helper()
	if file {
		path := filepath.Join(t.TempDir(), "pantry.db")
		writer, reader, err := OpenPantry(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := RunMigrationsThrough(writer, tripScopedSkipsMigration); err != nil {
			t.Fatal(err)
		}
		return writer, func() {
			writer.Close()
			reader.Close()
		}
	}
	conn, err := OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := RunMigrationsThrough(conn, tripScopedSkipsMigration); err != nil {
		conn.Close()
		t.Fatal(err)
	}
	return conn, func() { conn.Close() }
}

func inject031FailAfter(t *testing.T, marker string) {
	t.Helper()
	orig := prepareMigrationSQL
	t.Cleanup(func() { prepareMigrationSQL = orig })
	prepareMigrationSQL = func(name, sqlText string) string {
		if name != tripScopedSkipsMigration {
			return sqlText
		}
		idx := strings.Index(sqlText, marker)
		if idx < 0 {
			t.Fatalf("031 migration missing marker %q", marker)
		}
		end := idx + len(marker)
		return sqlText[:end] + ";\nCREATE TABLE migration031_fail_probe (x TEXT, x TEXT);\n" + sqlText[end:]
	}
}

func cleanupMigration031TestArtifacts(t *testing.T, conn *sql.DB) {
	t.Helper()
	prepareMigrationSQL = func(_ string, sqlText string) string { return sqlText }
}

func assertFull031State(t *testing.T, conn *sql.DB) {
	t.Helper()
	var tripSeq int
	if err := conn.QueryRow(`SELECT trip_seq FROM households WHERE is_primary = 1`).Scan(&tripSeq); err != nil {
		t.Fatal(err)
	}
	if tripSeq != 0 {
		t.Fatalf("trip_seq default: %d", tripSeq)
	}
	var closedCause, closedAt sql.NullString
	if err := conn.QueryRow(`SELECT trip_closed_cause, trip_closed_at FROM households WHERE is_primary = 1`).
		Scan(&closedCause, &closedAt); err != nil {
		t.Fatal(err)
	}
	if closedCause.Valid || closedAt.Valid {
		t.Fatalf("trip close columns should be null: %v %v", closedCause, closedAt)
	}
	householdCols, err := tableColumns(conn, "households")
	if err != nil {
		t.Fatal(err)
	}
	for _, col := range []string{"trip_closed_at", "trip_closed_cause"} {
		if !householdCols[strings.ToLower(col)] {
			t.Fatalf("missing households.%s", col)
		}
	}
	eventCols, err := tableColumns(conn, "stock_in_events")
	if err != nil {
		t.Fatal(err)
	}
	if !eventCols["store_id"] {
		t.Fatal("missing stock_in_events.store_id")
	}
	var one int
	for _, table := range []string{"stock_in_clusters", "list_line_changes", "cart_line_results", "attention_marks"} {
		if err := conn.QueryRow(`SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&one); err != nil {
			t.Fatalf("missing table %s: %v", table, err)
		}
	}
	var clusterMin int
	if err := conn.QueryRow(`SELECT stock_in_cluster_min FROM households WHERE is_primary = 1`).Scan(&clusterMin); err != nil {
		t.Fatal(err)
	}
	if clusterMin != 5 {
		t.Fatalf("stock_in_cluster_min default: %d", clusterMin)
	}
	cols, err := tableColumns(conn, "staged_cart_skips")
	if err != nil {
		t.Fatal(err)
	}
	for _, col := range []string{"expires_trip", "removed_at"} {
		if !cols[strings.ToLower(col)] {
			t.Fatalf("missing staged_cart_skips.%s", col)
		}
	}
	var recorded int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE filename = ?`, tripScopedSkipsMigration).Scan(&recorded); err != nil {
		t.Fatal(err)
	}
	if recorded != 1 {
		t.Fatalf("want 031 recorded, got %d", recorded)
	}
}
