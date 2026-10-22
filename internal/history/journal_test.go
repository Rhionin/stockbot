package history

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/app"
	_ "modernc.org/sqlite"
)

func TestJournalReadsRepairedEventsAndCorrections(t *testing.T) {
	ctx := t.Context()
	db := openHistoryDB(t)
	t0 := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	t0out := t0.Add(time.Hour)
	t1 := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)
	t3 := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	t4 := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)
	now := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	expires := t1.Add(30 * 24 * time.Hour)

	execHistory(t, db, `
		INSERT INTO products (id, name, category, unit_of_measure) VALUES
			('prod-oats', 'Rolled Oats', 'Pantry', 'unit'),
			('prod-beans', 'Beans', 'Pantry', 'unit'),
			('prod-bare', 'Bare', 'Pantry', 'unit')`)
	execHistory(t, db, `
		INSERT INTO items (id, user_id, product_id) VALUES
			('item-oats', 'user-1', 'prod-oats'),
			('item-beans', 'user-1', 'prod-beans')`)
	execHistory(t, db, `
		INSERT INTO product_groups (id, user_id, name, name_key, rule) VALUES
			('grp-staples', 'user-1', 'Staples', 'staples', 'same_as_ran_out'),
			('grp-solo', 'user-1', 'Solo', 'solo', 'same_as_ran_out'),
			('grp-empty', 'user-1', 'Empty', 'empty', 'same_as_ran_out')`)
	execHistory(t, db, `
		INSERT INTO product_group_members (product_id, group_id) VALUES
			('prod-oats', 'grp-staples'),
			('prod-beans', 'grp-staples'),
			('prod-bare', 'grp-solo')`)
	execHistory(t, db, `INSERT INTO app_settings (key, value) VALUES ('onboarding_started_at', 'yesterday')`)
	execHistory(t, db, `
		INSERT INTO scan_entries (id, user_id, barcode, scanned_at, direction, unit_count, status, product_id) VALUES
			('scan-in', 'user-1', 'oats', ?, 'stock_in', 2, 'committed', 'prod-oats'),
			('scan-out', 'user-1', 'oats', ?, 'stock_out', 1, 'committed', 'prod-oats')`, t1, t4)
	execHistory(t, db, `INSERT INTO stock_in_events (product_id, at) VALUES ('prod-oats', ?)`, t1)
	execHistory(t, db, `
		INSERT INTO item_instances (id, item_id, stock_in_at, expires_at, removed_at, removal_reason) VALUES
			('inst-e', 'item-oats', ?, NULL, ?, 'manual'),
			('inst-a', 'item-oats', ?, ?, NULL, NULL),
			('inst-b', 'item-oats', ?, NULL, NULL, NULL),
			('inst-c', 'item-oats', ?, NULL, ?, 'consumed'),
			('inst-d', 'item-oats', ?, NULL, ?, 'consumed'),
			('inst-g', 'item-oats', ?, NULL, NULL, NULL),
			('inst-f', 'item-oats', ?, NULL, ?, 'consumed')`,
		t0, t0out, t1, expires, t1, t2, t3, t2, t3, t2, t4, t4)
	execHistory(t, db, `
		INSERT INTO consumption_events (id, item_id, consumed_at, scan_entry_id) VALUES
			('use-c', 'item-oats', ?, NULL),
			('use-d', 'item-oats', ?, NULL),
			('use-f', 'item-oats', ?, 'scan-out')`, t3, t3, t4)

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := AttachStockIn(ctx, tx, InNote{}); err != nil {
		t.Fatal(err)
	}
	if err := AttachStockOut(ctx, tx, OutNote{}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	journal := NewJournal(db)
	if _, err := journal.Household(ctx, LedgerFilter{Direction: "sideways"}); err == nil {
		t.Fatal("expected a rejected direction")
	}
	ledger, err := journal.Household(ctx, LedgerFilter{Direction: " in ", Query: "staples"})
	if err != nil {
		t.Fatal(err)
	}
	if ledger.SetupAt != "" || len(ledger.Moves) == 0 || ledger.Moves[0].ProductName == "" {
		t.Fatalf("ledger %+v", ledger)
	}
	outs, err := journal.Household(ctx, LedgerFilter{Direction: "out", Query: "oats"})
	if err != nil || len(outs.Moves) == 0 || outs.Moves[0].Direction != "out" {
		t.Fatalf("outs %+v err %v", outs, err)
	}

	view, err := journal.Product(ctx, householdUser, "item-oats", now)
	if err != nil {
		t.Fatal(err)
	}
	if view.OnHand != 3 || view.Name != "Rolled Oats" || len(view.Moves) != 7 {
		t.Fatalf("opened %+v", view)
	}
	inScanned := findMove(t, view, t1, "in")
	inUsed := findMove(t, view, t2, "in")
	outBatch := findMove(t, view, t3, "out")
	outScan := findMove(t, view, t4, "out")
	inRemoved := findMove(t, view, t0, "in")
	inLatest := findMove(t, view, t4, "in")
	if inScanned.Quantity != 2 || inScanned.Source != "scan" || inUsed.Quantity != 3 || outBatch.Quantity != 2 || outScan.Source != "scan" || inRemoved.Quantity != 1 {
		t.Fatalf("moves %+v", view.Moves)
	}

	staples, err := journal.Group(ctx, "grp-staples", now)
	if err != nil || staples.Detail != "2 products" || staples.OnHand != 3 {
		t.Fatalf("staples %+v err %v", staples, err)
	}
	solo, err := journal.Group(ctx, "grp-solo", now)
	if err != nil || solo.Detail != "" || solo.OnHand != 0 || len(solo.Moves) != 0 {
		t.Fatalf("solo %+v err %v", solo, err)
	}
	empty, err := journal.Group(ctx, "grp-empty", now)
	if err != nil || empty.OnHand != 0 {
		t.Fatalf("empty %+v err %v", empty, err)
	}
	if _, err := journal.Product(ctx, householdUser, "missing", now); !errors.As(err, new(*NotFoundError)) {
		t.Fatalf("missing product: %v", err)
	}
	if _, err := journal.Group(ctx, "missing", now); !errors.As(err, new(*NotFoundError)) {
		t.Fatalf("missing group: %v", err)
	}

	if _, err := journal.SetQuantity(ctx, householdUser, inScanned.ID, 0, now); !errors.As(err, new(*InputError)) {
		t.Fatalf("zero quantity: %v", err)
	}
	if _, err := journal.SetQuantity(ctx, householdUser, inScanned.ID, 1000, now); !errors.As(err, new(*InputError)) {
		t.Fatalf("huge quantity: %v", err)
	}
	if _, err := journal.SetQuantity(ctx, householdUser, "missing-move", 1, now); !errors.As(err, new(*NotFoundError)) {
		t.Fatalf("missing move: %v", err)
	}
	if _, err := journal.SetQuantity(ctx, householdUser, inUsed.ID, 1, now); !errors.As(err, new(*ConflictError)) {
		t.Fatalf("used stock in: %v", err)
	}
	if _, err := journal.Undo(ctx, householdUser, inRemoved.ID, now); !errors.As(err, new(*ConflictError)) {
		t.Fatalf("undo used stock in: %v", err)
	}

	raised, err := journal.SetQuantity(ctx, householdUser, inScanned.ID, 4, now)
	if err != nil || raised.OnHand != 5 {
		t.Fatalf("raise in: %+v err %v", raised, err)
	}
	usedMore, err := journal.SetQuantity(ctx, householdUser, outBatch.ID, 4, now)
	if err != nil || usedMore.OnHand != 3 {
		t.Fatalf("raise out: %+v err %v", usedMore, err)
	}
	restored, err := journal.SetQuantity(ctx, householdUser, outBatch.ID, 2, now)
	if err != nil || restored.OnHand != 5 {
		t.Fatalf("lower out: %+v err %v", restored, err)
	}
	if _, err := journal.SetQuantity(ctx, householdUser, outScan.ID, 8, now); !errors.As(err, new(*ConflictError)) {
		t.Fatalf("not enough: %v", err)
	}
	drained, err := journal.SetQuantity(ctx, householdUser, outBatch.ID, 6, now)
	if err != nil || drained.OnHand != 1 {
		t.Fatalf("drain: %+v err %v", drained, err)
	}
	if _, err := journal.SetQuantity(ctx, householdUser, outScan.ID, 3, now); !errors.As(err, new(*ConflictError)) {
		t.Fatalf("one left: %v", err)
	}
	putBack, err := journal.Undo(ctx, householdUser, outScan.ID, now)
	if err != nil || putBack.OnHand != 2 {
		t.Fatalf("undo out: %+v err %v", putBack, err)
	}
	cleared, err := journal.Undo(ctx, householdUser, inLatest.ID, now)
	if err != nil || cleared.OnHand != 1 {
		t.Fatalf("undo in: %+v err %v", cleared, err)
	}
	same, err := journal.SetQuantity(ctx, householdUser, inUsed.ID, inUsed.Quantity, now)
	if err != nil || same.OnHand != 1 {
		t.Fatalf("unchanged quantity: %+v err %v", same, err)
	}
}

func findMove(t *testing.T, view View, at time.Time, direction string) Move {
	t.Helper()
	stamp := at.UTC().Format(time.RFC3339)
	for _, move := range view.Moves {
		if move.At == stamp && move.Direction == direction {
			return move
		}
	}
	t.Fatalf("no %s move at %s in %+v", direction, stamp, view.Moves)
	return Move{}
}

func openHistoryDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	if err := app.RunMigrations(db); err != nil {
		t.Fatal(err)
	}
	return db
}

func execHistory(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.ExecContext(t.Context(), query, args...); err != nil {
		t.Fatalf("exec: %v", err)
	}
}
