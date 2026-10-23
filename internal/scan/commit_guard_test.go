package scan

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestCommitReachesTheRowsAffectedGuard(t *testing.T) {
	queue, db := newTestQueueInternal(t)
	ctx := context.Background()
	at := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	const productID = "prod-guard"
	if _, err := db.Exec(`INSERT INTO products (id, name, category, unit_of_measure) VALUES (?, 'Guard', 'Pantry', 'unit')`, productID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO items (id, user_id, product_id) VALUES ('item-guard', 'user-1', ?)`, productID); err != nil {
		t.Fatal(err)
	}
	direction := StockIn
	product := productID
	entry, err := queue.CreateScanEntry(ctx, ScanEntry{
		ID: "scan-guard", UserID: "user-1", Barcode: "scan-guard", ScannedAt: at,
		Direction: &direction, UnitCount: 1, ProductID: &product, Status: Pending,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE scan_entries SET status = ? WHERE id = ?`, string(Committed), entry.ID); err != nil {
		t.Fatal(err)
	}

	// requireOpenScan would reject this committed scan before the update.
	// Skip that check so CommitStockIn reaches the rows-affected guard.
	openScanCheck = func(context.Context, *sql.Tx, string) error { return nil }
	t.Cleanup(func() { openScanCheck = requireOpenScan })

	err = queue.CommitStockIn(ctx, entry)
	if !errors.Is(err, ErrNotPending) {
		t.Fatalf("commit err = %v, want ErrNotPending", err)
	}
	var status string
	if err := db.QueryRow(`SELECT status FROM scan_entries WHERE id = ?`, entry.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != string(Committed) {
		t.Fatalf("status = %s", status)
	}
	var instances int
	if err := db.QueryRow(`SELECT COUNT(*) FROM item_instances WHERE item_id = 'item-guard'`).Scan(&instances); err != nil {
		t.Fatal(err)
	}
	if instances != 0 {
		t.Fatalf("instances = %d, want the failed commit rolled back", instances)
	}
}

func TestBackfillMoveReadsAndInstanceInsertFail(t *testing.T) {
	at := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)

	t.Run("instance insert", func(t *testing.T) {
		queue, db := newTestQueueInternal(t)
		entry := pendingBackfill(t, db, queue, "scan-insert", at)
		if _, err := db.Exec(`
			CREATE TRIGGER fail_instance_insert BEFORE INSERT ON item_instances
			BEGIN
				SELECT RAISE(ABORT, 'injected instance failure');
			END`); err != nil {
			t.Fatal(err)
		}
		_, err := queue.CommitBackfill(context.Background(), entry)
		if err == nil || !strings.Contains(err.Error(), "create instance") {
			t.Fatalf("err = %v", err)
		}
		assertBackfillRolledBack(t, db, entry.ID)
	})

	t.Run("stock-in move id", func(t *testing.T) {
		queue, db := newTestQueueInternal(t)
		entry := pendingBackfill(t, db, queue, "scan-in-id", at)
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		backfillMoveRead = func(direction string) {
			if direction == "in" {
				cancel()
			}
		}
		t.Cleanup(func() { backfillMoveRead = nil })
		_, err := queue.CommitBackfill(ctx, entry)
		if err == nil || !strings.Contains(err.Error(), "read backfill stock-in") || !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v", err)
		}
		assertBackfillRolledBack(t, db, entry.ID)
	})

	t.Run("stock-out move id", func(t *testing.T) {
		queue, db := newTestQueueInternal(t)
		entry := pendingBackfill(t, db, queue, "scan-out-id", at)
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		backfillMoveRead = func(direction string) {
			if direction == "out" {
				cancel()
			}
		}
		t.Cleanup(func() { backfillMoveRead = nil })
		_, err := queue.CommitBackfill(ctx, entry)
		if err == nil || !strings.Contains(err.Error(), "read backfill stock-out") || !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v", err)
		}
		assertBackfillRolledBack(t, db, entry.ID)
	})
}

func TestBackfillScanUpdateAndCommitFail(t *testing.T) {
	at := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)

	t.Run("scan update", func(t *testing.T) {
		queue, db := newTestQueueInternal(t)
		db.SetMaxOpenConns(1)
		entry := pendingBackfill(t, db, queue, "scan-update", at)
		if _, err := db.Exec(`
			CREATE TRIGGER fail_scan_update BEFORE UPDATE ON scan_entries
			WHEN NEW.status = 'committed' AND OLD.status != 'committed'
			BEGIN
				SELECT RAISE(ABORT, 'injected scan update failure');
			END`); err != nil {
			t.Fatal(err)
		}
		_, err := queue.CommitBackfill(context.Background(), entry)
		if err == nil || !strings.Contains(err.Error(), "update scan entry") {
			t.Fatalf("err = %v", err)
		}
		assertBackfillRolledBack(t, db, entry.ID)
	})

	t.Run("commit", func(t *testing.T) {
		queue, db := newTestQueueInternal(t)
		db.SetMaxOpenConns(1)
		entry := pendingBackfill(t, db, queue, "scan-commit", at)
		if _, err := db.Exec(`PRAGMA foreign_keys = ON`); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`
			CREATE TABLE commit_parent (id TEXT PRIMARY KEY);
			CREATE TABLE commit_child (
				id INTEGER PRIMARY KEY,
				parent_id TEXT REFERENCES commit_parent(id) DEFERRABLE INITIALLY DEFERRED
			);
			CREATE TRIGGER fail_backfill_commit AFTER UPDATE ON scan_entries
			BEGIN
				INSERT INTO commit_child (parent_id) VALUES ('missing');
			END`); err != nil {
			t.Fatal(err)
		}
		_, err := queue.CommitBackfill(context.Background(), entry)
		if err == nil || !strings.Contains(err.Error(), "commit transaction") {
			t.Fatalf("err = %v", err)
		}
		assertBackfillRolledBack(t, db, entry.ID)
	})
}

func pendingBackfill(t *testing.T, db *sql.DB, queue *Queue, scanID string, at time.Time) *ScanEntry {
	t.Helper()
	productID := "prod-" + scanID
	itemID := "item-" + scanID
	if _, err := db.Exec(`INSERT INTO products (id, name, category, unit_of_measure) VALUES (?, 'Oats', 'Pantry', 'unit')`, productID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO items (id, user_id, product_id) VALUES (?, 'user-1', ?)`, itemID, productID); err != nil {
		t.Fatal(err)
	}
	direction := StockOut
	entry, err := queue.CreateScanEntry(context.Background(), ScanEntry{
		ID: scanID, UserID: "user-1", Barcode: scanID, ScannedAt: at,
		Direction: &direction, UnitCount: 1, ProductID: &productID, Status: Pending,
	})
	if err != nil {
		t.Fatal(err)
	}
	return entry
}

func assertBackfillRolledBack(t *testing.T, db *sql.DB, scanID string) {
	t.Helper()
	var status string
	if err := db.QueryRow(`SELECT status FROM scan_entries WHERE id = ?`, scanID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != string(Pending) {
		t.Fatalf("status = %s", status)
	}
	var moves int
	if err := db.QueryRow(`SELECT COUNT(*) FROM stock_moves`).Scan(&moves); err != nil {
		t.Fatal(err)
	}
	if moves != 0 {
		t.Fatalf("moves = %d", moves)
	}
}
