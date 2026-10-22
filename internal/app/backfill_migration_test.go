package app

import (
	"testing"

	_ "modernc.org/sqlite"
)

func TestBackfillSourceMigrationKeepsExistingMoves(t *testing.T) {
	conn, err := OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := applyMigrationsExcept(conn, backfillSourceMigration); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`
		INSERT INTO stock_moves (id, item_id, product_id, direction, quantity, at, source, scan_entry_id, tracked, created_at)
		VALUES ('move-old', 'item-old', 'prod-old', 'out', 2, '2026-04-01T12:00:00Z', 'scan', 'scan-old', 1, '2026-04-01T12:00:01Z')`); err != nil {
		t.Fatal(err)
	}

	if err := RunMigrations(conn); err != nil {
		t.Fatal(err)
	}

	var itemID, source, created string
	var quantity int
	if err := conn.QueryRow(`
		SELECT item_id, quantity, source, created_at FROM stock_moves WHERE id = 'move-old'`).
		Scan(&itemID, &quantity, &source, &created); err != nil {
		t.Fatal(err)
	}
	if itemID != "item-old" || quantity != 2 || source != "scan" || created != "2026-04-01T12:00:01Z" {
		t.Fatalf("kept move = %s %d %s %s", itemID, quantity, source, created)
	}
	if _, err := conn.Exec(`
		INSERT INTO stock_moves (id, item_id, product_id, direction, quantity, at, source)
		VALUES ('move-backfill', 'item-old', 'prod-old', 'in', 2, '2026-04-01T12:00:00Z', 'backfill')`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`
		INSERT INTO stock_moves (id, item_id, product_id, direction, quantity, at, source)
		VALUES ('move-bad', 'item-old', 'prod-old', 'in', 1, '2026-04-01T12:00:00Z', 'imported')`); err == nil {
		t.Fatal("source imported was accepted")
	}
}
