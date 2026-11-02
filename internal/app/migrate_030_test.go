package app

import (
	"database/sql"
	"strings"
	"testing"
)

func TestMigration030RollsBackOnFailure(t *testing.T) {
	conn, err := OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := RunMigrationsThrough(conn, multiStoreMigration); err != nil {
		t.Fatal(err)
	}
	orig := prepareMigrationSQL
	t.Cleanup(func() { prepareMigrationSQL = orig })
	prepareMigrationSQL = func(name, sqlText string) string {
		if name == multiStoreMigration {
			return sqlText + "\nCREATE TABLE stores (id TEXT PRIMARY KEY);"
		}
		return sqlText
	}
	if err := RunMigrations(conn); err == nil {
		t.Fatal("expected migration 030 to fail")
	}
	var recorded int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE filename = ?`, multiStoreMigration).Scan(&recorded); err != nil {
		t.Fatal(err)
	}
	if recorded != 0 {
		t.Fatalf("failed migration recorded: %d", recorded)
	}
	cols, err := tableColumns(conn, "shopping_list_items")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cols["run_store_id"]; ok {
		t.Fatal("run_store_id column present after rolled-back 030")
	}
	prepareMigrationSQL = orig
	if err := RunMigrations(conn); err != nil {
		t.Fatalf("rerun after rollback: %v", err)
	}
	if err := conn.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE filename = ?`, multiStoreMigration).Scan(&recorded); err != nil {
		t.Fatal(err)
	}
	if recorded != 1 {
		t.Fatalf("want 030 recorded after successful rerun, got %d", recorded)
	}
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
