package app

import (
	"database/sql"
	"slices"
	"strings"
	"testing"
)

// Tables and columns introduced only by 030_multi_store.sql (028-era DB has none of these).
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
	"shopping_list_items":     {"run_store_id"},
	"provider_connections":    {"granted_scope", "app_key", "auth_app_key", "auth_return"},
	"provider_app_credentials": {"product_access", "product_access_at"},
}

type pre030SchemaSnapshot struct {
	tables     map[string]struct{}
	columns    map[string]map[string]bool
	migrations []string
}

func capturePre030Schema(conn *sql.DB) (pre030SchemaSnapshot, error) {
	tables, err := sqliteUserTables(conn)
	if err != nil {
		return pre030SchemaSnapshot{}, err
	}
	cols := map[string]map[string]bool{}
	for table := range columnsAddedBy030 {
		c, err := tableColumns(conn, table)
		if err != nil {
			return pre030SchemaSnapshot{}, err
		}
		cols[table] = c
	}
	migs, err := appliedMigrations(conn)
	if err != nil {
		return pre030SchemaSnapshot{}, err
	}
	return pre030SchemaSnapshot{tables: tables, columns: cols, migrations: migs}, nil
}

func assertSchemaMatchesPre030(t *testing.T, conn *sql.DB, want pre030SchemaSnapshot) {
	t.Helper()
	got, err := capturePre030Schema(conn)
	if err != nil {
		t.Fatal(err)
	}
	if !tableSetsEqual(want.tables, got.tables) {
		t.Fatalf("table set changed after rolled-back 030\nwant: %v\ngot:  %v", sortedTableNames(want.tables), sortedTableNames(got.tables))
	}
	for table, wantCols := range want.columns {
		gotCols, ok := got.columns[table]
		if !ok {
			t.Fatalf("missing table %q in snapshot", table)
		}
		for col := range wantCols {
			if gotCols[col] != wantCols[col] {
				t.Fatalf("column %s.%s presence changed", table, col)
			}
		}
	}
	if !slices.Equal(want.migrations, got.migrations) {
		t.Fatalf("schema_migrations changed:\nwant %v\ngot  %v", want.migrations, got.migrations)
	}
	for _, name := range tablesAddedBy030 {
		if _, ok := got.tables[name]; ok {
			t.Fatalf("030 table %q present after rollback", name)
		}
	}
	for table, cols := range columnsAddedBy030 {
		gotCols, err := tableColumns(conn, table)
		if err != nil {
			t.Fatal(err)
		}
		for _, col := range cols {
			if gotCols[strings.ToLower(col)] {
				t.Fatalf("030 column %s.%s present after rollback", table, col)
			}
		}
	}
}

func assertFull030State(t *testing.T, conn *sql.DB) {
	t.Helper()
	tables, err := sqliteUserTables(conn)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range tablesAddedBy030 {
		if _, ok := tables[name]; !ok {
			t.Fatalf("030 table %q missing after successful migration", name)
		}
	}
	for table, cols := range columnsAddedBy030 {
		gotCols, err := tableColumns(conn, table)
		if err != nil {
			t.Fatal(err)
		}
		for _, col := range cols {
			if !gotCols[strings.ToLower(col)] {
				t.Fatalf("030 column %s.%s missing after successful migration", table, col)
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

func TestMigration030RollsBackOnFailure(t *testing.T) {
	conn, err := OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := RunMigrationsThrough(conn, multiStoreMigration); err != nil {
		t.Fatal(err)
	}
	baseline, err := capturePre030Schema(conn)
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(baseline.migrations, multiStoreMigration) {
		t.Fatal("baseline must be pre-030 (028-era)")
	}

	orig := prepareMigrationSQL
	t.Cleanup(func() { prepareMigrationSQL = orig })
	prepareMigrationSQL = func(name, sqlText string) string {
		if name != multiStoreMigration {
			return sqlText
		}
		// Fail partway through 030 (after stores, before product_stores).
		marker := "CREATE TABLE IF NOT EXISTS product_stores"
		idx := strings.Index(sqlText, marker)
		if idx < 0 {
			panic("030 migration shape changed: product_stores block missing")
		}
		return sqlText[:idx] + "CREATE TABLE migration030_fail_probe (x TEXT, x TEXT);\n" + sqlText[idx:]
	}
	if err := RunMigrations(conn); err == nil {
		t.Fatal("expected migration 030 to fail")
	}
	assertSchemaMatchesPre030(t, conn, baseline)

	prepareMigrationSQL = orig
	if err := RunMigrations(conn); err != nil {
		t.Fatalf("rerun after rollback: %v", err)
	}
	assertFull030State(t, conn)
}

func sqliteUserTables(conn *sql.DB) (map[string]struct{}, error) {
	rows, err := conn.Query(`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]struct{}{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out[name] = struct{}{}
	}
	return out, rows.Err()
}

func appliedMigrations(conn *sql.DB) ([]string, error) {
	rows, err := conn.Query(`SELECT filename FROM schema_migrations ORDER BY filename`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

func tableSetsEqual(a, b map[string]struct{}) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if _, ok := b[k]; !ok {
			return false
		}
	}
	return true
}

func sortedTableNames(tables map[string]struct{}) []string {
	out := make([]string, 0, len(tables))
	for name := range tables {
		out = append(out, name)
	}
	slices.Sort(out)
	return out
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
