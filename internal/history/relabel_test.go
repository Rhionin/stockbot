package history

import (
	"bytes"
	"database/sql"
	"fmt"
	"log"
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
	household := scopeOf(t, db).HouseholdID()
	execHistory(t, db, `
		INSERT INTO scan_entries (id, user_id, barcode, scanned_at, direction, unit_count, status, product_id, household_id) VALUES
			('scan-exact', ?, 'oats', ?, 'stock_in', 1, 'committed', 'prod', ?),
			('scan-other-product', ?, 'rice', ?, 'stock_in', 1, 'committed', 'other', ?),
			('scan-cancelled', ?, 'oats', ?, 'stock_in', 1, 'cancelled', 'prod', ?),
			('scan-same-second', ?, 'oats', ?, 'stock_in', 1, 'committed', 'prod', ?)`,
		household, exact, household, household, exact, household, household, exact, household, household, sameSecond, household)
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
	household := scopeOf(t, db).HouseholdID()
	execHistory(t, db, `
		INSERT INTO scan_entries (id, user_id, barcode, scanned_at, direction, unit_count, status, product_id, household_id)
		VALUES ('scan-other-time', ?, 'oats', '2026-10-17 19:00:00 +0000 UTC', 'stock_in', 1, 'committed', 'prod', ?)`, household, household)
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
	household := scopeOf(t, db).HouseholdID()
	execHistory(t, db, `
		INSERT INTO scan_entries (id, user_id, barcode, scanned_at, direction, unit_count, status, product_id, household_id) VALUES
			('scan-a', ?, 'oats', ?, 'stock_in', 1, 'committed', 'prod', ?),
			('scan-b', ?, 'oats', ?, 'stock_in', 1, 'committed', 'prod', ?)`,
		household, at, household, household, at, household)
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
	household := scopeOf(t, db).HouseholdID()
	execHistory(t, db, `
		INSERT INTO scan_entries (id, user_id, barcode, scanned_at, direction, unit_count, status, product_id, household_id)
		VALUES ('scan-taken', ?, 'oats', ?, 'stock_in', 1, 'committed', 'prod', ?)`, household, at, household)
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
	household := scopeOf(t, db).HouseholdID()
	execHistory(t, db, `
		INSERT INTO scan_entries (id, user_id, barcode, scanned_at, direction, unit_count, status, product_id, household_id)
		VALUES ('scan-denver', ?, 'oats', ?, 'stock_out', 1, 'committed', 'prod', ?)`, household, denver, household)
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
	household := scopeOf(t, db).HouseholdID()
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3000; i++ {
		at := base.Add(time.Duration(i) * time.Second).Format("2006-01-02 15:04:05 -0700 MST")
		if _, err := tx.Exec(`
			INSERT INTO scan_entries (id, user_id, barcode, scanned_at, direction, unit_count, status, product_id, household_id)
			VALUES (?, ?, 'oats', ?, 'stock_in', 1, 'committed', 'prod', ?)`,
			fmt.Sprintf("scan-%d", i), household, at, household); err != nil {
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

func TestRelabelThreeMovesAcrossTwoProducts(t *testing.T) {
	const (
		oatsIn  = "2026-10-17 18:00:00 +0000 UTC"
		oatsOut = "2026-10-17 18:05:00 +0000 UTC"
		riceIn  = "2026-10-17 18:10:00 +0000 UTC"
	)
	db := openHistoryDB(t)
	seedRelabelHousehold(t, db)
	household := scopeOf(t, db).HouseholdID()
	execHistory(t, db, `
		INSERT INTO scan_entries (id, user_id, barcode, scanned_at, direction, unit_count, status, product_id, household_id) VALUES
			('scan-oats-in', ?, 'oats', ?, 'stock_in', 1, 'committed', 'prod', ?),
			('scan-oats-out', ?, 'oats', ?, 'stock_out', 1, 'committed', 'prod', ?),
			('scan-rice-in', ?, 'rice', ?, 'stock_in', 1, 'committed', 'other', ?)`,
		household, oatsIn, household, household, oatsOut, household, household, riceIn, household)
	execHistory(t, db, `
		INSERT INTO stock_moves (id, item_id, product_id, direction, quantity, at, source, tracked) VALUES
			('move-oats-in', 'item', 'prod', 'in', 1, ?, 'manual', 0),
			('move-oats-out', 'item', 'prod', 'out', 2, ?, 'manual', 0),
			('move-rice-in', 'item-other', 'other', 'in', 4, ?, 'manual', 0)`,
		oatsIn, oatsOut, riceIn)

	repairHistory(t, db)
	want := map[string]string{
		"move-oats-in":  "scan-oats-in",
		"move-oats-out": "scan-oats-out",
		"move-rice-in":  "scan-rice-in",
	}
	for id, scanID := range want {
		got := moveColumns(t, db, id)
		if got.source != "scan" || got.scanID != scanID {
			t.Fatalf("%s = %s %s", id, got.source, got.scanID)
		}
	}

	snapshot := dumpMoves(t, db)
	repairHistory(t, db)
	if again := dumpMoves(t, db); again != snapshot {
		t.Fatalf("rerun changed rows\n%s\n%s", snapshot, again)
	}
}

func TestRelabelIgnoresAnotherUsersScanAtTheSameInstant(t *testing.T) {
	const at = "2026-10-17 18:55:19 +0000 UTC"
	db := openHistoryDB(t)
	seedRelabelHousehold(t, db)
	household := scopeOf(t, db).HouseholdID()
	execHistory(t, db, `
		INSERT INTO scan_entries (id, user_id, barcode, scanned_at, direction, unit_count, status, product_id, household_id) VALUES
			('scan-mine', ?, 'oats', ?, 'stock_in', 1, 'committed', 'prod', ?),
			('scan-theirs', 'user-2', 'oats', ?, 'stock_in', 1, 'committed', 'prod', ?)`,
		household, at, household, at, household)
	execHistory(t, db, `
		INSERT INTO stock_moves (id, item_id, product_id, direction, quantity, at, source, tracked)
		VALUES ('move-mine', 'item', 'prod', 'in', 1, ?, 'manual', 0)`, at)

	repairHistory(t, db)
	got := moveColumns(t, db, "move-mine")
	if got.source != "scan" || got.scanID != "scan-mine" {
		t.Fatalf("own scan = %s %s", got.source, got.scanID)
	}
	var linked int
	if err := db.QueryRow(`SELECT COUNT(*) FROM stock_moves WHERE scan_entry_id = 'scan-theirs'`).Scan(&linked); err != nil {
		t.Fatal(err)
	}
	if linked != 0 {
		t.Fatalf("other user's scan linked %d times", linked)
	}
}

func TestRelabelDoesNotLinkAnotherUsersOnlyScan(t *testing.T) {
	const (
		shelfAt = "2026-10-17 08:00:00 +0000 UTC"
		moveAt  = "2026-10-17 09:00:00 +0000 UTC"
	)
	db := openHistoryDB(t)
	seedRelabelHousehold(t, db)
	household := scopeOf(t, db).HouseholdID()
	execHistory(t, db, `
		INSERT INTO scan_entries (id, user_id, barcode, scanned_at, direction, unit_count, status, product_id, household_id) VALUES
			('scan-shelf', 'user-2', 'oats', ?, 'stock_in', 1, 'committed', 'prod', ?),
			('scan-move', 'user-2', 'oats', ?, 'stock_in', 1, 'committed', 'prod', ?)`,
		shelfAt, household, moveAt, household)
	execHistory(t, db, `
		INSERT INTO item_instances (id, item_id, stock_in_at) VALUES ('inst-shelf', 'item', ?)`, shelfAt)
	execHistory(t, db, `
		INSERT INTO stock_moves (id, item_id, product_id, direction, quantity, at, source, tracked)
		VALUES ('move-lone', 'item', 'prod', 'in', 1, ?, 'manual', 0)`, moveAt)

	repairHistory(t, db)
	lone := moveColumns(t, db, "move-lone")
	if lone.source != "manual" || lone.scanID != "" {
		t.Fatalf("manual move linked to the other user: %s %s", lone.source, lone.scanID)
	}
	var created int
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM stock_moves
		WHERE source = 'manual' AND scan_entry_id IS NULL AND id != 'move-lone'`).Scan(&created); err != nil {
		t.Fatal(err)
	}
	if created != 1 {
		t.Fatalf("shelf row attributed %d moves", created)
	}
	var stolen int
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM stock_moves
		WHERE scan_entry_id IN ('scan-shelf', 'scan-move')`).Scan(&stolen); err != nil {
		t.Fatal(err)
	}
	if stolen != 0 {
		t.Fatalf("other user's scans linked %d times", stolen)
	}
}

func TestRelabelGivesOneScanToTheEarliestMove(t *testing.T) {
	// Two manual moves share one committed scan. The pass reads moves in id
	// order, updates the first, and marks that scan linked, so the later
	// move stays manual. Leaving both manual would drop the only match.
	// The lower id wins, and a second pass changes nothing.
	const at = "2026-10-17 18:55:19 +0000 UTC"
	db := openHistoryDB(t)
	seedRelabelHousehold(t, db)
	household := scopeOf(t, db).HouseholdID()
	execHistory(t, db, `
		INSERT INTO scan_entries (id, user_id, barcode, scanned_at, direction, unit_count, status, product_id, household_id)
		VALUES ('scan-one', ?, 'oats', ?, 'stock_in', 1, 'committed', 'prod', ?)`, household, at, household)
	execHistory(t, db, `
		INSERT INTO stock_moves (id, item_id, product_id, direction, quantity, at, source, tracked) VALUES
			('move-b', 'item', 'prod', 'in', 2, ?, 'manual', 0),
			('move-a', 'item', 'prod', 'in', 1, ?, 'manual', 0)`,
		at, at)

	repairHistory(t, db)
	winner := moveColumns(t, db, "move-a")
	if winner.source != "scan" || winner.scanID != "scan-one" {
		t.Fatalf("earliest move = %s %s", winner.source, winner.scanID)
	}
	loser := moveColumns(t, db, "move-b")
	if loser.source != "manual" || loser.scanID != "" {
		t.Fatalf("later move = %s %s", loser.source, loser.scanID)
	}

	snapshot := dumpMoves(t, db)
	repairHistory(t, db)
	if again := dumpMoves(t, db); again != snapshot {
		t.Fatalf("rerun changed rows\n%s\n%s", snapshot, again)
	}
}

func TestRelabelLeavesMoveThatAlreadyHasAScan(t *testing.T) {
	// move-a sorts first and already points at a scan. The pass must not
	// select it. If it did, it would claim scan-match and move-b would stay
	// manual. The update also refuses a row whose scan_entry_id is set, so
	// move-a's columns stay as they were inserted.
	const (
		at    = "2026-10-17 18:55:19 +0000 UTC"
		older = "2026-10-16 12:00:00 +0000 UTC"
	)
	db := openHistoryDB(t)
	seedRelabelHousehold(t, db)
	household := scopeOf(t, db).HouseholdID()
	execHistory(t, db, `
		INSERT INTO scan_entries (id, user_id, barcode, scanned_at, direction, unit_count, status, product_id, household_id) VALUES
			('scan-kept', ?, 'oats', ?, 'stock_in', 1, 'committed', 'prod', ?),
			('scan-match', ?, 'oats', ?, 'stock_in', 1, 'committed', 'prod', ?)`,
		household, older, household, household, at, household)
	execHistory(t, db, `
		INSERT INTO stock_moves (id, item_id, product_id, direction, quantity, at, source, scan_entry_id, tracked) VALUES
			('move-a', 'item', 'prod', 'in', 5, ?, 'manual', 'scan-kept', 1),
			('move-b', 'item', 'prod', 'in', 1, ?, 'manual', NULL, 0)`,
		at, at)

	before := moveColumns(t, db, "move-a")
	repairHistory(t, db)
	after := moveColumns(t, db, "move-a")
	if after != before {
		t.Fatalf("already linked move changed\nbefore %+v\nafter  %+v", before, after)
	}
	open := moveColumns(t, db, "move-b")
	if open.source != "scan" || open.scanID != "scan-match" {
		t.Fatalf("open move = %s %s", open.source, open.scanID)
	}
}

func TestRelabelSkipsUnparseableScannedAt(t *testing.T) {
	const (
		goodAt  = "2026-10-17 18:00:00 +0000 UTC"
		garbage = "not-a-time"
	)
	var logs bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(previous) })

	db := openHistoryDB(t)
	seedRelabelHousehold(t, db)
	household := scopeOf(t, db).HouseholdID()
	execHistory(t, db, `
		INSERT INTO scan_entries (id, user_id, barcode, scanned_at, direction, unit_count, status, product_id, household_id) VALUES
			('scan-garbage', ?, 'barcode-secret', ?, 'stock_in', 1, 'committed', 'prod', ?),
			('scan-good', ?, 'oats', ?, 'stock_in', 1, 'committed', 'prod', ?)`,
		household, garbage, household, household, goodAt, household)
	execHistory(t, db, `
		INSERT INTO stock_moves (id, item_id, product_id, direction, quantity, at, source, tracked)
		VALUES ('move-good', 'item', 'prod', 'in', 1, ?, 'manual', 0)`, goodAt)

	now := time.Date(2026, 10, 18, 0, 0, 0, 0, time.UTC)
	journal := NewJournal(db)
	view, err := journal.Product(t.Context(), scopeOf(t, db), "item", now)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Moves) != 1 || view.Moves[0].ID != "move-good" || view.Moves[0].Source != "scan" {
		t.Fatalf("history = %+v", view.Moves)
	}
	got := moveColumns(t, db, "move-good")
	if got.source != "scan" || got.scanID != "scan-good" {
		t.Fatalf("good move = %s %s", got.source, got.scanID)
	}
	var stolen int
	if err := db.QueryRow(`SELECT COUNT(*) FROM stock_moves WHERE scan_entry_id = 'scan-garbage'`).Scan(&stolen); err != nil {
		t.Fatal(err)
	}
	if stolen != 0 {
		t.Fatalf("garbage scan linked %d times", stolen)
	}

	corrected, err := journal.SetQuantity(t.Context(), scopeOf(t, db), "move-good", 2, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(corrected.Moves) != 1 || corrected.Moves[0].Quantity != 2 {
		t.Fatalf("correction = %+v", corrected.Moves)
	}

	logged := logs.String()
	if !strings.Contains(logged, "skipped a committed scan whose scanned_at could not be parsed") {
		t.Fatalf("log = %q", logged)
	}
	for _, secret := range []string{garbage, "scan-garbage", "barcode-secret"} {
		if strings.Contains(logged, secret) {
			t.Fatalf("log contains %q: %s", secret, logged)
		}
	}
}

func seedRelabelHousehold(t *testing.T, db *sql.DB) {
	t.Helper()
	household := scopeOf(t, db).HouseholdID()
	execHistory(t, db, `INSERT INTO products (id, name) VALUES ('prod', 'Oats'), ('other', 'Rice')`)
	execHistory(t, db, `INSERT INTO items (id, user_id, product_id, household_id) VALUES ('item', ?, 'prod', ?), ('item-other', ?, 'other', ?)`, household, household, household, household)
}

func repairHistory(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := NewJournal(db).Product(t.Context(), scopeOf(t, db), "item", time.Date(2026, 10, 18, 0, 0, 0, 0, time.UTC)); err != nil {
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
