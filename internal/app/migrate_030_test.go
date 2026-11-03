package app

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

func TestMigration030RollsBackOnFailure(t *testing.T) {
	cases := []struct {
		name              string
		fileDB            bool
		assertAfterALTERs bool
		prepare           func(t *testing.T, conn *sql.DB)
	}{
		{
			name: "mid migration in memory",
			prepare: func(t *testing.T, conn *sql.DB) {
				t.Helper()
				inject030FailAfter(t, "CREATE TABLE IF NOT EXISTS product_stores")
			},
		},
		{
			name:              "after last ALTER in memory",
			assertAfterALTERs: true,
			prepare: func(t *testing.T, conn *sql.DB) {
				t.Helper()
				marker := "ALTER TABLE provider_app_credentials ADD COLUMN product_access_at TEXT"
				inject030FailAfter(t, marker)
			},
		},
		{
			name: "schema_migrations insert in memory",
			prepare: func(t *testing.T, conn *sql.DB) {
				t.Helper()
				if _, err := conn.Exec(`
					CREATE TRIGGER migration030_block_version_insert
					BEFORE INSERT ON schema_migrations
					WHEN NEW.filename = '` + multiStoreMigration + `'
					BEGIN
						SELECT RAISE(ABORT, 'inject migration030 version insert failure');
					END`); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name:              "after last ALTER on WAL file database",
			fileDB:            true,
			assertAfterALTERs: true,
			prepare: func(t *testing.T, conn *sql.DB) {
				t.Helper()
				marker := "ALTER TABLE provider_app_credentials ADD COLUMN product_access_at TEXT"
				inject030FailAfter(t, marker)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			conn, cleanup := openPre030Database(t, tc.fileDB)
			defer cleanup()
			baseline, err := snapshotSqliteMaster(conn)
			if err != nil {
				t.Fatal(err)
			}
			rowCountsBefore, err := snapshotTableRowCounts(conn)
			if err != nil {
				t.Fatal(err)
			}
			var recorded int
			if err := conn.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE filename = ?`, multiStoreMigration).Scan(&recorded); err != nil {
				t.Fatal(err)
			}
			if recorded != 0 {
				t.Fatal("baseline must be pre-030")
			}
			tc.prepare(t, conn)
			if err := RunMigrations(conn); err == nil {
				t.Fatal("expected migration 030 to fail")
			}
			cleanupMigration030TestArtifacts(t, conn)
			got, err := snapshotSqliteMaster(conn)
			if err != nil {
				t.Fatal(err)
			}
			if got != baseline {
				t.Fatalf("sqlite_master changed after rolled-back 030\nwant:\n%s\ngot:\n%s", baseline, got)
			}
			rowCountsAfter, err := snapshotTableRowCounts(conn)
			if err != nil {
				t.Fatal(err)
			}
			assertTableRowCountsEqual(t, rowCountsBefore, rowCountsAfter)
			if tc.assertAfterALTERs {
				assert030ColumnsAbsent(t, conn)
			}
			if err := RunMigrations(conn); err != nil {
				t.Fatalf("rerun after rollback: %v", err)
			}
			assertFull030State(t, conn)
		})
	}
}

func openPre030Database(t *testing.T, file bool) (*sql.DB, func()) {
	t.Helper()
	if file {
		path := filepath.Join(t.TempDir(), "pantry.db")
		writer, reader, err := OpenPantry(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := RunMigrationsThrough(writer, multiStoreMigration); err != nil {
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
	if err := RunMigrationsThrough(conn, multiStoreMigration); err != nil {
		conn.Close()
		t.Fatal(err)
	}
	return conn, func() { conn.Close() }
}

func inject030FailAfter(t *testing.T, marker string) {
	t.Helper()
	orig := prepareMigrationSQL
	t.Cleanup(func() { prepareMigrationSQL = orig })
	prepareMigrationSQL = func(name, sqlText string) string {
		if name != multiStoreMigration {
			return sqlText
		}
		idx := strings.Index(sqlText, marker)
		if idx < 0 {
			t.Fatalf("030 migration missing marker %q", marker)
		}
		end := idx + len(marker)
		return sqlText[:end] + ";\nCREATE TABLE migration030_fail_probe (x TEXT, x TEXT);\n" + sqlText[end:]
	}
}

func cleanupMigration030TestArtifacts(t *testing.T, conn *sql.DB) {
	t.Helper()
	prepareMigrationSQL = func(_ string, sqlText string) string { return sqlText }
	if _, err := conn.Exec(`DROP TRIGGER IF EXISTS migration030_block_version_insert`); err != nil {
		t.Fatal(err)
	}
}

func snapshotSqliteMaster(conn *sql.DB) (string, error) {
	rows, err := conn.Query(`SELECT type, name, tbl_name, sql FROM sqlite_master ORDER BY type, name`)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var b strings.Builder
	for rows.Next() {
		var typ, name, tblName string
		var ddl sql.NullString
		if err := rows.Scan(&typ, &name, &tblName, &ddl); err != nil {
			return "", err
		}
		b.WriteString(typ)
		b.WriteByte('\t')
		b.WriteString(name)
		b.WriteByte('\t')
		b.WriteString(tblName)
		b.WriteByte('\t')
		b.WriteString(ddl.String)
		b.WriteByte('\n')
	}
	return b.String(), rows.Err()
}

func snapshotTableRowCounts(conn *sql.DB) (map[string]int, error) {
	rows, err := conn.Query(`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		tables = append(tables, name)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make(map[string]int, len(tables))
	for _, table := range tables {
		var n int
		if err := conn.QueryRow(`SELECT COUNT(*) FROM ` + quoteSQLiteIdent(table)).Scan(&n); err != nil {
			return nil, err
		}
		out[table] = n
	}
	return out, nil
}

func quoteSQLiteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func assertTableRowCountsEqual(t *testing.T, want, got map[string]int) {
	t.Helper()
	for table, n := range want {
		if got[table] != n {
			t.Fatalf("table %q row count: before %d after %d", table, n, got[table])
		}
	}
	for table, n := range got {
		if _, ok := want[table]; !ok {
			t.Fatalf("unexpected table %q with %d rows after rollback", table, n)
		}
	}
}

func assert030ColumnsAbsent(t *testing.T, conn *sql.DB) {
	t.Helper()
	for table, cols := range columnsAddedBy030 {
		gotCols, err := tableColumns(conn, table)
		if err != nil {
			t.Fatal(err)
		}
		for _, col := range cols {
			if gotCols[strings.ToLower(col)] {
				t.Fatalf("030 column %s.%s still present after rollback", table, col)
			}
		}
	}
}

func assertFull030State(t *testing.T, conn *sql.DB) {
	t.Helper()
	for _, name := range tablesAddedBy030 {
		var one int
		if err := conn.QueryRow(`SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = ?`, name).Scan(&one); err != nil {
			t.Fatalf("030 table %q missing: %v", name, err)
		}
	}
	for table, cols := range columnsAddedBy030 {
		gotCols, err := tableColumns(conn, table)
		if err != nil {
			t.Fatal(err)
		}
		for _, col := range cols {
			if !gotCols[strings.ToLower(col)] {
				t.Fatalf("030 column %s.%s missing", table, col)
			}
		}
	}
	var recorded int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE filename = ?`, multiStoreMigration).Scan(&recorded); err != nil {
		t.Fatal(err)
	}
	if recorded != 1 {
		t.Fatalf("want 030 recorded after successful migration, got %d", recorded)
	}
}

var tablesAddedBy030 = []string{
	"stores",
	"product_stores",
	"store_trips",
	"store_availability",
	"store_checks",
	"kroger_api_window",
	"kroger_household_usage",
}

var columnsAddedBy030 = map[string][]string{
	"shopping_list_items":      {"run_store_id"},
	"provider_connections":     {"granted_scope", "app_key", "auth_app_key", "auth_return"},
	"provider_app_credentials": {"product_access", "product_access_at"},
}

func tableColumns(conn *sql.DB, table string) (map[string]bool, error) {
	rows, err := conn.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var cid, notnull, pk int
		var name, colType string
		var dflt any
		if err := rows.Scan(&cid, &name, &colType, &notnull, &dflt, &pk); err != nil {
			return nil, err
		}
		out[strings.ToLower(name)] = true
	}
	return out, rows.Err()
}
