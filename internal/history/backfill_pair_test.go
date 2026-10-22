package history

import (
	"database/sql"
	"errors"
	"testing"
	"time"
)

func TestBackfillPairsOnlyWithTheOppositeSource(t *testing.T) {
	db := openHistoryDB(t)
	ctx := t.Context()
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	at := now.Add(-2 * time.Hour)
	execHistory(t, db, `
		INSERT INTO products (id, name, category, unit_of_measure) VALUES
			('prod-pair', 'Pair', 'Pantry', 'unit'),
			('prod-scans', 'Scans', 'Pantry', 'unit'),
			('prod-hide', 'Hide', 'Pantry', 'unit'),
			('prod-fills', 'Fills', 'Pantry', 'unit')`)
	execHistory(t, db, `
		INSERT INTO items (id, user_id, product_id) VALUES
			('item-pair', 'user-1', 'prod-pair'),
			('item-scans', 'user-1', 'prod-scans'),
			('item-hide', 'user-1', 'prod-hide'),
			('item-fills', 'user-1', 'prod-fills')`)

	journal := NewJournal(db)
	recordBackfillPair(t, db, "item-pair", "prod-pair", "scan-pair", "inst-pair", at, false)

	view, err := journal.Product(ctx, "item-pair", now)
	if err != nil {
		t.Fatal(err)
	}
	inID := moveBy(t, view, "in", "backfill")
	outID := moveBy(t, view, "out", "scan")

	if _, err := journal.SetQuantity(ctx, inID, 2, now); !errors.Is(err, ErrBackfillLocked) {
		t.Fatalf("stock-in edit err = %v", err)
	}
	if _, err := journal.SetQuantity(ctx, outID, 2, now); !errors.Is(err, ErrBackfillLocked) {
		t.Fatalf("stock-out edit err = %v", err)
	}
	if qty := moveQuantity(t, db, inID); qty != 1 {
		t.Fatalf("stock-in quantity = %d", qty)
	}
	if qty := moveQuantity(t, db, outID); qty != 1 {
		t.Fatalf("stock-out quantity = %d", qty)
	}
	if _, err := journal.SetQuantity(ctx, inID, 1, now); err != nil {
		t.Fatalf("same quantity: %v", err)
	}

	undone, err := journal.Undo(ctx, inID, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(undone.Moves) != 0 {
		t.Fatalf("moves after undo = %+v", undone.Moves)
	}
	if shelved := itemShelved(t, db, "item-pair"); shelved != 0 {
		t.Fatal("undo hid an item that was already on the shelf")
	}

	t.Run("two scan moves that share a scan are not a pair", func(t *testing.T) {
		recordHeld(t, db, "item-scans", "prod-scans", "inst-scans", at)
		recordConsumed(t, db, "item-scans", "prod-scans", "scan-shared", "inst-scans-out-1", at, "scan")
		recordConsumed(t, db, "item-scans", "prod-scans", "scan-shared", "inst-scans-out-2", at, "scan")
		recordHeld(t, db, "item-scans", "prod-scans", "inst-scans-left", at)

		view, err := journal.Product(ctx, "item-scans", now)
		if err != nil {
			t.Fatal(err)
		}
		var outs []Move
		for _, move := range view.Moves {
			if move.Direction == "out" && move.Source == "scan" {
				outs = append(outs, move)
			}
		}
		if len(outs) != 2 {
			t.Fatalf("scan outs = %+v", view.Moves)
		}
		if _, err := journal.SetQuantity(ctx, outs[0].ID, 2, now); err != nil {
			t.Fatalf("editable scan out: %v", err)
		}
		if moveQuantity(t, db, outs[1].ID) != 1 {
			t.Fatal("the other scan move was changed")
		}
		if _, err := journal.Undo(ctx, outs[1].ID, now); err != nil {
			t.Fatal(err)
		}
		if moveQuantity(t, db, outs[0].ID) != 2 {
			t.Fatal("undo removed the other scan move")
		}
	})

	t.Run("two backfill moves that share a scan are not a pair", func(t *testing.T) {
		recordHeldMove(t, db, "item-fills", "prod-fills", "scan-fills", "inst-fill-1", at, "backfill", false)
		recordHeldMove(t, db, "item-fills", "prod-fills", "scan-fills", "inst-fill-2", at, "backfill", false)
		view, err := journal.Product(ctx, "item-fills", now)
		if err != nil {
			t.Fatal(err)
		}
		var fills []Move
		for _, move := range view.Moves {
			if move.Source == "backfill" {
				fills = append(fills, move)
			}
		}
		if len(fills) != 2 {
			t.Fatalf("backfills = %+v", view.Moves)
		}
		if _, err := journal.SetQuantity(ctx, fills[0].ID, 2, now); err != nil {
			t.Fatalf("unpaired backfill: %v", err)
		}
		if moveQuantity(t, db, fills[1].ID) != 1 {
			t.Fatal("the other backfill was changed")
		}
	})

	t.Run("undo of a pair that unshelved hides the item again", func(t *testing.T) {
		recordBackfillPair(t, db, "item-hide", "prod-hide", "scan-hide", "inst-hide", at, true)
		view, err := journal.Product(ctx, "item-hide", now)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := journal.Undo(ctx, moveBy(t, view, "out", "scan"), now); err != nil {
			t.Fatal(err)
		}
		if shelved := itemShelved(t, db, "item-hide"); shelved != 1 {
			t.Fatal("undo left the wiped item visible")
		}
		if n := countItemMoves(t, db, "item-hide"); n != 0 {
			t.Fatalf("moves left = %d", n)
		}
	})

	t.Run("undo of a pair that unshelved leaves a later unit", func(t *testing.T) {
		execHistory(t, db, `INSERT INTO products (id, name, category, unit_of_measure) VALUES ('prod-keep', 'Keep', 'Pantry', 'unit')`)
		execHistory(t, db, `INSERT INTO items (id, user_id, product_id) VALUES ('item-keep', 'user-1', 'prod-keep')`)
		recordBackfillPair(t, db, "item-keep", "prod-keep", "scan-keep", "inst-keep", at, true)
		recordHeld(t, db, "item-keep", "prod-keep", "inst-keep-live", at.Add(time.Hour))
		view, err := journal.Product(ctx, "item-keep", now)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := journal.Undo(ctx, moveBy(t, view, "in", "backfill"), now); err != nil {
			t.Fatal(err)
		}
		if shelved := itemShelved(t, db, "item-keep"); shelved != 0 {
			t.Fatal("undo shelved an item that still has a unit")
		}
		var live int
		if err := db.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM item_instances
			WHERE item_id = 'item-keep' AND removed_at IS NULL`).Scan(&live); err != nil {
			t.Fatal(err)
		}
		if live != 1 {
			t.Fatalf("live units = %d", live)
		}
	})
}

func recordBackfillPair(t *testing.T, db *sql.DB, itemID, productID, scanID, instanceID string, at time.Time, unshelved bool) {
	t.Helper()
	recordHeldMove(t, db, itemID, productID, scanID, instanceID, at, "backfill", unshelved)
	markConsumed(t, db, instanceID, at)
	recordConsumedMove(t, db, itemID, productID, scanID, instanceID, at)
}

func recordHeld(t *testing.T, db *sql.DB, itemID, productID, instanceID string, at time.Time) {
	t.Helper()
	recordHeldMove(t, db, itemID, productID, "", instanceID, at, "manual", false)
}

func recordHeldMove(t *testing.T, db *sql.DB, itemID, productID, scanID, instanceID string, at time.Time, source string, unshelved bool) {
	t.Helper()
	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(t.Context(),
		`INSERT INTO item_instances (id, item_id, stock_in_at) VALUES (?, ?, ?)`,
		instanceID, itemID, at); err != nil {
		t.Fatal(err)
	}
	var scan *string
	if scanID != "" {
		scan = &scanID
	}
	if err := AttachStockIn(t.Context(), tx, InNote{
		ItemID: itemID, ProductID: productID, At: at, Source: source,
		ScanEntryID: scan, InstanceIDs: []string{instanceID}, Unshelved: unshelved,
	}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func recordConsumed(t *testing.T, db *sql.DB, itemID, productID, scanID, instanceID string, at time.Time, source string) {
	t.Helper()
	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(t.Context(),
		`INSERT INTO item_instances (id, item_id, stock_in_at, removed_at, removal_reason) VALUES (?, ?, ?, ?, 'consumed')`,
		instanceID, itemID, at, at); err != nil {
		t.Fatal(err)
	}
	// The insert has no stock-in move. Point it at a manual stock-in so repair does not invent one.
	if err := AttachStockIn(t.Context(), tx, InNote{
		ItemID: itemID, ProductID: productID, At: at, Source: "manual",
		InstanceIDs: []string{instanceID},
	}); err != nil {
		t.Fatal(err)
	}
	scan := scanID
	if err := AttachStockOut(t.Context(), tx, OutNote{
		ItemID: itemID, ProductID: productID, At: at, Source: source,
		ScanEntryID: &scan, InstanceID: instanceID,
	}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func recordConsumedMove(t *testing.T, db *sql.DB, itemID, productID, scanID, instanceID string, at time.Time) {
	t.Helper()
	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	scan := scanID
	if err := AttachStockOut(t.Context(), tx, OutNote{
		ItemID: itemID, ProductID: productID, At: at, Source: "scan",
		ScanEntryID: &scan, InstanceID: instanceID,
	}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func markConsumed(t *testing.T, db *sql.DB, instanceID string, at time.Time) {
	t.Helper()
	if _, err := db.ExecContext(t.Context(),
		`UPDATE item_instances SET removed_at = ?, removal_reason = 'consumed' WHERE id = ?`,
		at, instanceID); err != nil {
		t.Fatal(err)
	}
}

func moveBy(t *testing.T, view View, direction, source string) string {
	t.Helper()
	for _, move := range view.Moves {
		if move.Direction == direction && move.Source == source {
			return move.ID
		}
	}
	t.Fatalf("no %s %s move in %+v", source, direction, view.Moves)
	return ""
}

func moveQuantity(t *testing.T, db *sql.DB, id string) int {
	t.Helper()
	var qty int
	if err := db.QueryRowContext(t.Context(), `SELECT quantity FROM stock_moves WHERE id = ?`, id).Scan(&qty); err != nil {
		t.Fatal(err)
	}
	return qty
}

func itemShelved(t *testing.T, db *sql.DB, id string) int {
	t.Helper()
	var shelved int
	if err := db.QueryRowContext(t.Context(), `SELECT shelved FROM items WHERE id = ?`, id).Scan(&shelved); err != nil {
		t.Fatal(err)
	}
	return shelved
}

func countItemMoves(t *testing.T, db *sql.DB, itemID string) int {
	t.Helper()
	var n int
	if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM stock_moves WHERE item_id = ?`, itemID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
