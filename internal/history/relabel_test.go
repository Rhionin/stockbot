package history

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestRelabelOneExactScan(t *testing.T) {
	const (
		exact      = "2026-10-17 18:55:19.032067103 +0000 UTC m=+12.3"
		sameSecond = "2026-10-17 18:55:19.9 +0000 UTC"
		created    = "2026-01-01 00:00:00"
	)
	db := openHistoryDB(t)
	seedRelabelHousehold(t, db)
	execHistory(t, db, `
		INSERT INTO scan_entries (id, user_id, barcode, scanned_at, direction, unit_count, status, product_id) VALUES
			('scan-exact', 'user-1', 'oats', ?, 'stock_in', 1, 'committed', 'prod'),
			('scan-other-product', 'user-1', 'rice', ?, 'stock_in', 1, 'committed', 'other'),
			('scan-cancelled', 'user-1', 'oats', ?, 'stock_in', 1, 'cancelled', 'prod'),
			('scan-same-second', 'user-1', 'oats', ?, 'stock_in', 1, 'committed', 'prod')`,
		exact, exact, exact, sameSecond)
	execHistory(t, db, `
		INSERT INTO stock_moves (id, item_id, product_id, direction, quantity, at, source, tracked, created_at)
		VALUES ('move-one', 'item', 'prod', 'in', 7, ?, 'manual', 1, ?)`,
		exact, created)

	before := moveColumns(t, db, "move-one")
	repairHistory(t, db)
	after := moveColumns(t, db, "move-one")
	if after.source != "scan" || after.scanID != "scan-exact" {
		t.Fatalf("relabel = %s %s", after.source, after.scanID)
	}
	for _, decoy := range []string{"scan-other-product", "scan-cancelled", "scan-same-second"} {
		if after.scanID == decoy {
			t.Fatalf("manual move linked to %s", decoy)
		}
	}
	if after.rest != before.rest {
		t.Fatalf("other columns changed\nbefore %s\nafter  %s", before.rest, after.rest)
	}

	snapshot := dumpMoves(t, db)
	repairHistory(t, db)
	if again := dumpMoves(t, db); again != snapshot {
		t.Fatalf("rerun changed rows\n%s\n%s", snapshot, again)
	}
}

func TestRelabelLeavesZeroMatches(t *testing.T) {
	db := openHistoryDB(t)
	seedRelabelHousehold(t, db)
	execHistory(t, db, `
		INSERT INTO scan_entries (id, user_id, barcode, scanned_at, direction, unit_count, status, product_id)
		VALUES ('scan-other-time', 'user-1', 'oats', '2026-10-17 19:00:00 +0000 UTC', 'stock_in', 1, 'committed', 'prod')`)
	execHistory(t, db, `
		INSERT INTO stock_moves (id, item_id, product_id, direction, quantity, at, source, tracked)
		VALUES ('move-none', 'item', 'prod', 'in', 1, '2026-10-17 12:00:00 +0000 UTC', 'manual', 0)`)

	repairHistory(t, db)
	got := moveColumns(t, db, "move-none")
	if got.source != "manual" || got.scanID != "" {
		t.Fatalf("zero matches became %s %s", got.source, got.scanID)
	}
}

func TestRelabelLeavesMultipleMatches(t *testing.T) {
	const at = "2026-10-17 18:55:19.032067103 +0000 UTC m=+12.3"
	db := openHistoryDB(t)
	seedRelabelHousehold(t, db)
	execHistory(t, db, `
		INSERT INTO scan_entries (id, user_id, barcode, scanned_at, direction, unit_count, status, product_id) VALUES
			('scan-a', 'user-1', 'oats', ?, 'stock_in', 1, 'committed', 'prod'),
			('scan-b', 'user-1', 'oats', ?, 'stock_in', 1, 'committed', 'prod')`,
		at, at)
	execHistory(t, db, `
		INSERT INTO stock_moves (id, item_id, product_id, direction, quantity, at, source, tracked)
		VALUES ('move-many', 'item', 'prod', 'in', 2, ?, 'manual', 0)`, at)

	repairHistory(t, db)
	got := moveColumns(t, db, "move-many")
	if got.source != "manual" || got.scanID != "" {
		t.Fatalf("multiple matches became %s %s", got.source, got.scanID)
	}
}

func TestRelabelLeavesScanAlreadyLinked(t *testing.T) {
	const at = "2026-10-17 18:55:19 +0000 UTC"
	db := openHistoryDB(t)
	seedRelabelHousehold(t, db)
	execHistory(t, db, `
		INSERT INTO scan_entries (id, user_id, barcode, scanned_at, direction, unit_count, status, product_id)
		VALUES ('scan-taken', 'user-1', 'oats', ?, 'stock_in', 1, 'committed', 'prod')`, at)
	execHistory(t, db, `
		INSERT INTO stock_moves (id, item_id, product_id, direction, quantity, at, source, scan_entry_id, tracked) VALUES
			('move-keeper', 'item', 'prod', 'in', 1, ?, 'scan', 'scan-taken', 0),
			('move-late', 'item', 'prod', 'in', 3, ?, 'manual', NULL, 0)`,
		at, at)

	repairHistory(t, db)
	got := moveColumns(t, db, "move-late")
	if got.source != "manual" || got.scanID != "" {
		t.Fatalf("linked scan was reused: %s %s", got.source, got.scanID)
	}
	keeper := moveColumns(t, db, "move-keeper")
	if keeper.source != "scan" || keeper.scanID != "scan-taken" {
		t.Fatalf("keeper changed: %s %s", keeper.source, keeper.scanID)
	}
}

func TestRelabelMatchesTheSameInstantInEitherZone(t *testing.T) {
	later, _ := dstPair(t)
	denver := later.Format("2006-01-02 15:04:05 -0700 MST")
	utc := later.UTC().Format("2006-01-02 15:04:05 -0700 MST")
	db := openHistoryDB(t)
	seedRelabelHousehold(t, db)
	execHistory(t, db, `
		INSERT INTO scan_entries (id, user_id, barcode, scanned_at, direction, unit_count, status, product_id)
		VALUES ('scan-denver', 'user-1', 'oats', ?, 'stock_out', 1, 'committed', 'prod')`, denver)
	execHistory(t, db, `
		INSERT INTO stock_moves (id, item_id, product_id, direction, quantity, at, source, tracked)
		VALUES ('move-utc', 'item', 'prod', 'out', 1, ?, 'manual', 0)`, utc)

	repairHistory(t, db)
	got := moveColumns(t, db, "move-utc")
	if got.source != "scan" || got.scanID != "scan-denver" {
		t.Fatalf("zone match = %s %s (scan %s move %s)", got.source, got.scanID, denver, utc)
	}
	if !strings.Contains(got.rest, utc) {
		t.Fatalf("move at changed: %s", got.rest)
	}
}

func TestRelabelThreeThousandScans(t *testing.T) {
	db := openHistoryDB(t)
	seedRelabelHousehold(t, db)
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3000; i++ {
		at := base.Add(time.Duration(i) * time.Second).Format("2006-01-02 15:04:05 -0700 MST")
		if _, err := tx.Exec(`
			INSERT INTO scan_entries (id, user_id, barcode, scanned_at, direction, unit_count, status, product_id)
			VALUES (?, 'user-1', 'oats', ?, 'stock_in', 1, 'committed', 'prod')`,
			fmt.Sprintf("scan-%d", i), at); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
	}
	hit := base.Add(1500 * time.Second).Format("2006-01-02 15:04:05 -0700 MST")
	if _, err := tx.Exec(`
		INSERT INTO stock_moves (id, item_id, product_id, direction, quantity, at, source, tracked)
		VALUES ('move-hit', 'item', 'prod', 'in', 1, ?, 'manual', 0)`, hit); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		at := time.Date(2019, 1, 1, 0, 0, i, 0, time.UTC).Format("2006-01-02 15:04:05 -0700 MST")
		if _, err := tx.Exec(`
			INSERT INTO stock_moves (id, item_id, product_id, direction, quantity, at, source, tracked)
			VALUES (?, 'item', 'prod', 'in', 1, ?, 'manual', 0)`,
			fmt.Sprintf("move-miss-%d", i), at); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	started := time.Now()
	repairHistory(t, db)
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("repair took %s", elapsed)
	}
	hitMove := moveColumns(t, db, "move-hit")
	if hitMove.source != "scan" || hitMove.scanID != "scan-1500" {
		t.Fatalf("hit = %s %s", hitMove.source, hitMove.scanID)
	}
	miss := moveColumns(t, db, "move-miss-0")
	if miss.source != "manual" || miss.scanID != "" {
		t.Fatalf("miss = %s %s", miss.source, miss.scanID)
	}
}

func seedRelabelHousehold(t *testing.T, db *sql.DB) {
	t.Helper()
	execHistory(t, db, `INSERT INTO products (id, name) VALUES ('prod', 'Oats'), ('other', 'Rice')`)
	execHistory(t, db, `INSERT INTO items (id, user_id, product_id) VALUES ('item', 'user-1', 'prod'), ('item-other', 'user-1', 'other')`)
}

func repairHistory(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := NewJournal(db).Product(t.Context(), "item", time.Date(2026, 10, 18, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
}

type moveCols struct {
	source string
	scanID string
	rest   string
}

func moveColumns(t *testing.T, db *sql.DB, id string) moveCols {
	t.Helper()
	var item, product, direction, quantity, at, source, scanID, tracked, created string
	err := db.QueryRow(`
		SELECT item_id, product_id, direction, CAST(quantity AS TEXT), CAST(at AS TEXT),
			source, COALESCE(scan_entry_id, ''), CAST(tracked AS TEXT), CAST(created_at AS TEXT)
		FROM stock_moves WHERE id = ?`, id).Scan(
		&item, &product, &direction, &quantity, &at, &source, &scanID, &tracked, &created)
	if err != nil {
		t.Fatal(err)
	}
	return moveCols{
		source: source,
		scanID: scanID,
		rest:   strings.Join([]string{item, product, direction, quantity, at, tracked, created}, "|"),
	}
}

func dumpMoves(t *testing.T, db *sql.DB) string {
	t.Helper()
	rows, err := db.Query(`
		SELECT id, item_id, product_id, direction, CAST(quantity AS TEXT), CAST(at AS TEXT),
			source, COALESCE(scan_entry_id, ''), CAST(tracked AS TEXT), CAST(created_at AS TEXT)
		FROM stock_moves ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var id, item, product, direction, quantity, at, source, scanID, tracked, created string
		if err := rows.Scan(&id, &item, &product, &direction, &quantity, &at, &source, &scanID, &tracked, &created); err != nil {
			t.Fatal(err)
		}
		lines = append(lines, strings.Join([]string{id, item, product, direction, quantity, at, source, scanID, tracked, created}, "|"))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return strings.Join(lines, "\n")
}
