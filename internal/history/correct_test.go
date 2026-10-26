package history

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/changes"
	"github.com/Rhionin/stockbot/internal/inventory"
	"github.com/Rhionin/stockbot/internal/tenant"
)

func TestJournalSetQuantityStockIn(t *testing.T) {
	ctx := t.Context()
	db := openHistoryDB(t)
	scope := scopeOf(t, db)
	household := scope.HouseholdID()
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	at := now.Add(-24 * time.Hour)
	seedCorrectionProduct(t, db, "item-in", "prod-in", "Stock In Test")

	journal := NewJournal(db)
	inID := manualStockIn(t, db, "item-in", "prod-in", 3, at)

	view, err := journal.Product(ctx, scope, "item-in", now)
	if err != nil {
		t.Fatal(err)
	}
	if view.OnHand != 3 {
		t.Fatalf("on hand = %d", view.OnHand)
	}

	if _, err := journal.SetQuantity(ctx, scope, inID, 0, now); !errors.As(err, new(*InputError)) {
		t.Fatalf("zero qty: %v", err)
	}
	if _, err := journal.SetQuantity(ctx, scope, inID, 1000, now); !errors.As(err, new(*InputError)) {
		t.Fatalf("huge qty: %v", err)
	}
	if _, err := journal.SetQuantity(ctx, scope, "missing-move", 2, now); !errors.As(err, new(*NotFoundError)) {
		t.Fatalf("missing move: %v", err)
	}

	before := correctionSnapshot(t, ctx, db, scope, "item-in", inID)
	raised, err := journal.SetQuantity(ctx, scope, inID, 5, now)
	if err != nil {
		t.Fatal(err)
	}
	if raised.OnHand != 5 {
		t.Fatalf("raised on hand = %d", raised.OnHand)
	}
	if move := findMoveByID(raised, inID); move == nil || move.Quantity != 5 {
		t.Fatalf("move qty in view: %+v", move)
	}
	afterBump, err := changes.Version(ctx, db, household, changes.ScopeStock)
	if err != nil {
		t.Fatal(err)
	}
	if afterBump != before.stockVersion+1 {
		t.Fatalf("stock version %d -> %d", before.stockVersion, afterBump)
	}

	outID := manualStockOut(t, db, "item-in", "prod-in", 2, at.Add(time.Hour), false)
	preRefuse := correctionSnapshot(t, ctx, db, scope, "item-in", inID)
	_, err = journal.SetQuantity(ctx, scope, inID, 1, now)
	var conflict *ConflictError
	if !errors.As(err, &conflict) || conflict.Message != alreadyUsed(2) {
		t.Fatalf("shrink below consumed: %v", err)
	}
	snap := correctionSnapshot(t, ctx, db, scope, "item-in", inID)
	if snap != preRefuse {
		t.Fatalf("refused shrink mutated state: before %+v after %+v", preRefuse, snap)
	}
	_ = outID

	unchanged, err := journal.SetQuantity(ctx, scope, inID, 5, now)
	if err != nil || unchanged.OnHand != 3 {
		t.Fatalf("unchanged qty: %+v err %v", unchanged, err)
	}
	versionAfterSame, err := changes.Version(ctx, db, household, changes.ScopeStock)
	if err != nil {
		t.Fatal(err)
	}
	if versionAfterSame != afterBump {
		t.Fatalf("unchanged quantity should not bump stock version; version=%d want %d", versionAfterSame, afterBump)
	}
	// SetQuantity with unchanged quantity skips the bump (quantity == move.Quantity).
	actualQty := moveQuantity(t, db, inID)
	if actualQty != 5 {
		t.Fatalf("move qty = %d", actualQty)
	}

	t.Run("tenant isolation", func(t *testing.T) {
		cedar := insertCedarHousehold(t, db)
		if _, err := journal.SetQuantity(ctx, scope, cedarMoveID(t, db, cedar), 2, now); !errors.As(err, new(*NotFoundError)) {
			t.Fatalf("cross-household set: %v", err)
		}
	})
}

func TestJournalSetQuantityStockInDecreaseAndExpiry(t *testing.T) {
	ctx := t.Context()
	db := openHistoryDB(t)
	scope := scopeOf(t, db)
	now := time.Date(2026, 9, 2, 8, 0, 0, 0, time.UTC)
	at := now.Add(-time.Hour)
	expires := at.Add(14 * 24 * time.Hour)
	seedCorrectionProduct(t, db, "item-exp", "prod-exp", "Expiry")
	inID := manualStockInWithExpiry(t, db, "item-exp", "prod-exp", 4, at, expires)
	journal := NewJournal(db)
	shrunk, err := journal.SetQuantity(ctx, scope, inID, 2, now)
	if err != nil || shrunk.OnHand != 2 {
		t.Fatalf("shrink unused stock in: %+v err %v", shrunk, err)
	}
	grown, err := journal.SetQuantity(ctx, scope, inID, 3, now)
	if err != nil || grown.OnHand != 3 {
		t.Fatalf("grow stock in: %+v err %v", grown, err)
	}
	var withExpiry int
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM item_instances
		WHERE household_id = ? AND stock_move_id = ? AND expires_at IS NOT NULL`,
		scope.HouseholdID(), inID).Scan(&withExpiry); err != nil {
		t.Fatal(err)
	}
	if withExpiry < 1 {
		t.Fatal("added units should copy expiry from the move")
	}
}

func TestJournalSetQuantityStockInNotEnoughWithoutUsed(t *testing.T) {
	ctx := t.Context()
	db := openHistoryDB(t)
	scope := scopeOf(t, db)
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	at := now.Add(-time.Hour)
	seedCorrectionProduct(t, db, "item-gap", "prod-gap", "Gap")
	inID := manualStockIn(t, db, "item-gap", "prod-gap", 3, at)
	// Drop two live instances without marking them used on this move.
	var dropIDs []string
	rows, err := db.QueryContext(ctx, `
		SELECT id FROM item_instances
		WHERE household_id = ? AND stock_move_id = ? AND removed_at IS NULL
		ORDER BY id LIMIT 2`, scope.HouseholdID(), inID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		dropIDs = append(dropIDs, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatal(err)
	}
	rows.Close()
	for _, id := range dropIDs {
		execHistory(t, db, `DELETE FROM item_instances WHERE id = ? AND household_id = ?`, id, scope.HouseholdID())
	}
	journal := NewJournal(db)
	_, err = journal.SetQuantity(ctx, scope, inID, 1, now)
	var conflict *ConflictError
	if !errors.As(err, &conflict) || conflict.Message != "This stock in can't be lowered to that quantity." {
		t.Fatalf("generic shrink refusal: %v", err)
	}
	if qty := moveQuantity(t, db, inID); qty != 3 {
		t.Fatalf("quantity = %d", qty)
	}
}

func TestJournalSetQuantityStockOut(t *testing.T) {
	ctx := t.Context()
	db := openHistoryDB(t)
	scope := scopeOf(t, db)
	household := scope.HouseholdID()
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	at := now.Add(-2 * time.Hour)
	seedCorrectionProduct(t, db, "item-out", "prod-out", "Stock Out Test")
	manualStockIn(t, db, "item-out", "prod-out", 5, at)
	journal := NewJournal(db)
	outID := manualStockOut(t, db, "item-out", "prod-out", 1, at.Add(30*time.Minute), false)

	before, err := changes.Version(ctx, db, household, changes.ScopeStock)
	if err != nil {
		t.Fatal(err)
	}
	more, err := journal.SetQuantity(ctx, scope, outID, 3, now)
	if err != nil || more.OnHand != 2 {
		t.Fatalf("raise out: %+v err %v", more, err)
	}
	if findMoveByID(more, outID) == nil || findMoveByID(more, outID).Quantity != 3 {
		t.Fatalf("out quantity in view: %+v", more.Moves)
	}
	after, err := changes.Version(ctx, db, household, changes.ScopeStock)
	if err != nil || after != before+1 {
		t.Fatalf("version %d -> %d", before, after)
	}

	if _, err := journal.SetQuantity(ctx, scope, outID, 10, now); !errors.As(err, new(*ConflictError)) {
		t.Fatalf("not enough on shelf: %v", err)
	}

	less, err := journal.SetQuantity(ctx, scope, outID, 1, now)
	if err != nil || less.OnHand != 4 {
		t.Fatalf("lower out: %+v err %v", less, err)
	}
}

func TestJournalSetQuantityTrackedStockOut(t *testing.T) {
	ctx := t.Context()
	db := openHistoryDB(t)
	scope := scopeOf(t, db)
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	at := now.Add(-time.Hour)
	seedCorrectionProduct(t, db, "item-track", "prod-track", "Tracked")
	execHistory(t, db, `INSERT INTO stock_in_events (id, product_id, at, household_id) VALUES (1, 'prod-track', ?, ?)`, at, scope.HouseholdID())
	manualStockIn(t, db, "item-track", "prod-track", 4, at)
	journal := NewJournal(db)
	view, err := journal.Product(ctx, scope, "item-track", now)
	if err != nil {
		t.Fatal(err)
	}
	outID := manualStockOut(t, db, "item-track", "prod-track", 2, at.Add(30*time.Minute), true)
	_ = view
	raised, err := journal.SetQuantity(ctx, scope, outID, 3, now)
	if err != nil || raised.OnHand != 1 {
		t.Fatalf("tracked raise: %+v err %v", raised, err)
	}
	var consumptions int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM consumption_events WHERE stock_move_id = ?`, outID).Scan(&consumptions); err != nil {
		t.Fatal(err)
	}
	if consumptions != 3 {
		t.Fatalf("consumption rows = %d", consumptions)
	}
	lowered, err := journal.SetQuantity(ctx, scope, outID, 1, now)
	if err != nil || lowered.OnHand != 3 {
		t.Fatalf("tracked lower: %+v err %v", lowered, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM consumption_events WHERE stock_move_id = ?`, outID).Scan(&consumptions); err != nil {
		t.Fatal(err)
	}
	if consumptions != 1 {
		t.Fatalf("consumption rows after lower = %d", consumptions)
	}
}

func TestJournalSetQuantityBackfillLocked(t *testing.T) {
	ctx := t.Context()
	db := openHistoryDB(t)
	scope := scopeOf(t, db)
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	at := now.Add(-time.Hour)
	seedCorrectionProduct(t, db, "item-bf", "prod-bf", "Backfill")
	recordBackfillPair(t, db, "item-bf", "prod-bf", "scan-bf", "inst-bf", at, false)
	journal := NewJournal(db)
	view, err := journal.Product(ctx, scope, "item-bf", now)
	if err != nil {
		t.Fatal(err)
	}
	inID := moveBy(t, view, "in", "backfill")
	outID := moveBy(t, view, "out", "scan")
	version, err := changes.Version(ctx, db, scope.HouseholdID(), changes.ScopeStock)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{inID, outID} {
		if _, err := journal.SetQuantity(ctx, scope, id, 2, now); !errors.Is(err, ErrBackfillLocked) {
			t.Fatalf("resize %s: %v", id, err)
		}
	}
	after, err := changes.Version(ctx, db, scope.HouseholdID(), changes.ScopeStock)
	if err != nil || after != version {
		t.Fatalf("version changed: %d -> %d", version, after)
	}
}

func TestJournalUndoStockMoves(t *testing.T) {
	ctx := t.Context()
	db := openHistoryDB(t)
	scope := scopeOf(t, db)
	household := scope.HouseholdID()
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	at := now.Add(-3 * time.Hour)
	seedCorrectionProduct(t, db, "item-undo", "prod-undo", "Undo Me")
	inID := manualStockIn(t, db, "item-undo", "prod-undo", 3, at)
	journal := NewJournal(db)

	version, err := changes.Version(ctx, db, household, changes.ScopeStock)
	if err != nil {
		t.Fatal(err)
	}
	undone, err := journal.Undo(ctx, scope, inID, now)
	if err != nil || undone.OnHand != 0 || len(undone.Moves) != 0 {
		t.Fatalf("undo stock in: %+v err %v", undone, err)
	}
	after, err := changes.Version(ctx, db, household, changes.ScopeStock)
	if err != nil || after != version+1 {
		t.Fatalf("version %d -> %d", version, after)
	}
	if _, err := journal.Undo(ctx, scope, inID, now); !errors.As(err, new(*NotFoundError)) {
		t.Fatalf("double undo: %v", err)
	}

	inID = manualStockIn(t, db, "item-undo", "prod-undo", 2, at.Add(time.Hour))
	outID := manualStockOut(t, db, "item-undo", "prod-undo", 1, at.Add(2*time.Hour), false)
	if _, err := journal.Undo(ctx, scope, inID, now); !errors.As(err, new(*ConflictError)) {
		t.Fatalf("undo used stock in: %v", err)
	}
	restored, err := journal.Undo(ctx, scope, outID, now)
	if err != nil || restored.OnHand != 2 {
		t.Fatalf("undo stock out: %+v err %v", restored, err)
	}
	cleared, err := journal.Undo(ctx, scope, inID, now)
	if err != nil || cleared.OnHand != 0 {
		t.Fatalf("undo stock in after out: %+v err %v", cleared, err)
	}
}

func TestJournalSetQuantityStockOutNotEnoughSingular(t *testing.T) {
	ctx := t.Context()
	db := openHistoryDB(t)
	scope := scopeOf(t, db)
	now := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	at := now.Add(-time.Hour)
	seedCorrectionProduct(t, db, "item-one", "prod-one", "One Left")
	manualStockIn(t, db, "item-one", "prod-one", 1, at)
	outID := manualStockOut(t, db, "item-one", "prod-one", 1, at.Add(30*time.Minute), false)
	journal := NewJournal(db)
	_, err := journal.SetQuantity(ctx, scope, outID, 2, now)
	var conflict *ConflictError
	if !errors.As(err, &conflict) || conflict.Message != notEnough(0, 2) {
		t.Fatalf("singular not enough: %v", err)
	}
}

func TestJournalUndoBackfillPair(t *testing.T) {
	ctx := t.Context()
	db := openHistoryDB(t)
	scope := scopeOf(t, db)
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	at := now.Add(-time.Hour)
	seedCorrectionProduct(t, db, "item-pair-undo", "prod-pair-undo", "Pair Undo")
	recordBackfillPair(t, db, "item-pair-undo", "prod-pair-undo", "scan-pair-undo", "inst-pair-undo", at, false)
	journal := NewJournal(db)
	view, err := journal.Product(ctx, scope, "item-pair-undo", now)
	if err != nil {
		t.Fatal(err)
	}
	outID := moveBy(t, view, "out", "scan")
	undone, err := journal.Undo(ctx, scope, outID, now)
	if err != nil || undone.OnHand != 0 || len(undone.Moves) != 0 {
		t.Fatalf("undo pair from out: %+v err %v", undone, err)
	}
	if n := countItemMoves(t, db, "item-pair-undo"); n != 0 {
		t.Fatalf("moves left = %d", n)
	}
}

func TestJournalPreviewUndo(t *testing.T) {
	dir := t.TempDir()
	db := openHistoryFile(t, filepath.Join(dir, "pantry.db"))
	ctx := t.Context()
	scope := scopeOf(t, db)
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	at := now.Add(-2 * time.Hour)
	seedCorrectionProduct(t, db, "item-prev", "prod-prev", "Preview")
	inID := manualStockIn(t, db, "item-prev", "prod-prev", 2, at)
	outID := manualStockOut(t, db, "item-prev", "prod-prev", 1, at.Add(time.Hour), false)
	journal := NewJournal(db)

	t.Run("valid stock-in undo preview", func(t *testing.T) {
		// Fresh stock-in with no consumption.
		onlyIn := manualStockIn(t, db, "item-prev", "prod-prev", 1, at.Add(2*time.Hour))
		before := correctionSnapshot(t, ctx, db, scope, "item-prev", onlyIn)
		preview, err := journal.PreviewUndo(ctx, scope, onlyIn)
		if err != nil {
			t.Fatal(err)
		}
		if len(preview.Effects) != 1 || preview.Effects[0].OnHand != 1 || preview.Effects[0].StockInMoveID != onlyIn {
			t.Fatalf("preview = %+v", preview)
		}
		after := correctionSnapshot(t, ctx, db, scope, "item-prev", onlyIn)
		if after != before {
			t.Fatalf("preview mutated state: before %+v after %+v", before, after)
		}
	})

	t.Run("refused stock-in undo already used", func(t *testing.T) {
		before := correctionSnapshot(t, ctx, db, scope, "item-prev", inID)
		_, err := journal.PreviewUndo(ctx, scope, inID)
		if !errors.As(err, new(*ConflictError)) {
			t.Fatalf("preview used in: %v", err)
		}
		after := correctionSnapshot(t, ctx, db, scope, "item-prev", inID)
		if after != before {
			t.Fatalf("preview mutated state: before %+v after %+v", before, after)
		}
	})

	t.Run("valid stock-out undo preview", func(t *testing.T) {
		before := correctionSnapshot(t, ctx, db, scope, "item-prev", outID)
		preview, err := journal.PreviewUndo(ctx, scope, outID)
		if err != nil {
			t.Fatal(err)
		}
		if len(preview.Effects) != 1 || preview.Effects[0].OnHand != 3 || preview.Effects[0].StockOutMoveID != outID {
			t.Fatalf("preview = %+v", preview)
		}
		after := correctionSnapshot(t, ctx, db, scope, "item-prev", outID)
		if after != before {
			t.Fatalf("preview mutated state: before %+v after %+v", before, after)
		}
	})

	t.Run("unknown move", func(t *testing.T) {
		before := correctionSnapshot(t, ctx, db, scope, "item-prev", inID)
		_, err := journal.PreviewUndo(ctx, scope, "missing-move")
		if !errors.As(err, new(*NotFoundError)) {
			t.Fatalf("preview missing: %v", err)
		}
		after := correctionSnapshot(t, ctx, db, scope, "item-prev", inID)
		if after != before {
			t.Fatalf("preview mutated state")
		}
	})

	t.Run("cross-household move", func(t *testing.T) {
		cedar := insertCedarHousehold(t, db)
		moveID := cedarMoveID(t, db, cedar)
		before := correctionSnapshot(t, ctx, db, scope, "item-prev", inID)
		_, err := journal.PreviewUndo(ctx, scope, moveID)
		if !errors.As(err, new(*NotFoundError)) {
			t.Fatalf("preview cross-household: %v", err)
		}
		after := correctionSnapshot(t, ctx, db, scope, "item-prev", inID)
		if after != before {
			t.Fatalf("preview mutated state")
		}
	})
}

func TestJournalSetQuantityStockOutMissingUnits(t *testing.T) {
	ctx := t.Context()
	db := openHistoryDB(t)
	scope := scopeOf(t, db)
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	at := now.Add(-time.Hour)
	seedCorrectionProduct(t, db, "item-miss", "prod-miss", "Missing Units")
	manualStockIn(t, db, "item-miss", "prod-miss", 2, at)
	outID := manualStockOut(t, db, "item-miss", "prod-miss", 2, at.Add(30*time.Minute), false)
	execHistory(t, db, `
		DELETE FROM item_instances
		WHERE household_id = ? AND removed_by_move_id = ?`, scope.HouseholdID(), outID)
	journal := NewJournal(db)
	_, err := journal.SetQuantity(ctx, scope, outID, 1, now)
	var conflict *ConflictError
	if !errors.As(err, &conflict) || conflict.Message != "This use can't be lowered because those units are no longer recorded." {
		t.Fatalf("lower out missing units: %v", err)
	}
}

func TestJournalUndoStockOutMissingUnits(t *testing.T) {
	ctx := t.Context()
	db := openHistoryDB(t)
	scope := scopeOf(t, db)
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	at := now.Add(-time.Hour)
	seedCorrectionProduct(t, db, "item-undomiss", "prod-undomiss", "Undo Missing")
	manualStockIn(t, db, "item-undomiss", "prod-undomiss", 1, at)
	outID := manualStockOut(t, db, "item-undomiss", "prod-undomiss", 1, at.Add(30*time.Minute), false)
	execHistory(t, db, `
		DELETE FROM item_instances
		WHERE household_id = ? AND removed_by_move_id = ?`, scope.HouseholdID(), outID)
	journal := NewJournal(db)
	_, err := journal.Undo(ctx, scope, outID, now)
	var conflict *ConflictError
	if !errors.As(err, &conflict) || conflict.Message != "This use can't be undone because those units are no longer recorded." {
		t.Fatalf("undo out missing units: %v", err)
	}
}

func TestJournalSetQuantityTrackedScanStockOut(t *testing.T) {
	ctx := t.Context()
	db := openHistoryDB(t)
	scope := scopeOf(t, db)
	household := scope.HouseholdID()
	now := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	at := now.Add(-time.Hour)
	seedCorrectionProduct(t, db, "item-scan", "prod-scan", "Scan Out")
	execHistory(t, db, `INSERT INTO stock_in_events (id, product_id, at, household_id) VALUES (2, 'prod-scan', ?, ?)`, at, household)
	execHistory(t, db, `
		INSERT INTO scan_entries (id, user_id, barcode, scanned_at, direction, unit_count, status, product_id, household_id)
		VALUES ('scan-out-track', ?, 'scan', ?, 'stock_out', 1, 'committed', 'prod-scan', ?)`, household, at, household)
	manualStockIn(t, db, "item-scan", "prod-scan", 3, at)
	scan := "scan-out-track"
	outID := trackedScanStockOut(t, db, "item-scan", "prod-scan", 1, at.Add(30*time.Minute), scan)
	journal := NewJournal(db)
	raised, err := journal.SetQuantity(ctx, scope, outID, 2, now)
	if err != nil || raised.OnHand != 1 {
		t.Fatalf("tracked scan raise: %+v err %v", raised, err)
	}
}

func TestJournalUndoCrossHousehold(t *testing.T) {
	ctx := t.Context()
	db := openHistoryDB(t)
	scope := scopeOf(t, db)
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	cedar := insertCedarHousehold(t, db)
	journal := NewJournal(db)
	if _, err := journal.Undo(ctx, scope, cedarMoveID(t, db, cedar), now); !errors.As(err, new(*NotFoundError)) {
		t.Fatalf("cross undo: %v", err)
	}
}

// --- helpers ---

type correctionSnap struct {
	onHand       int
	moveQty      int
	stockVersion int64
	moveCount    int
}

func correctionSnapshot(t *testing.T, ctx context.Context, db *sql.DB, scope tenant.Scope, itemID, moveID string) correctionSnap {
	t.Helper()
	var onHand int
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM item_instances
		WHERE household_id = ? AND item_id = ? AND removed_at IS NULL`, scope.HouseholdID(), itemID).Scan(&onHand); err != nil {
		t.Fatal(err)
	}
	qty := moveQuantity(t, db, moveID)
	version, err := changes.Version(ctx, db, scope.HouseholdID(), changes.ScopeStock)
	if err != nil {
		t.Fatal(err)
	}
	var moveCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM stock_moves WHERE household_id = ?`, scope.HouseholdID()).Scan(&moveCount); err != nil {
		t.Fatal(err)
	}
	return correctionSnap{onHand: onHand, moveQty: qty, stockVersion: version, moveCount: moveCount}
}

func seedCorrectionProduct(t *testing.T, db *sql.DB, itemID, productID, name string) {
	t.Helper()
	scope := scopeOf(t, db)
	household := scope.HouseholdID()
	execHistory(t, db, `INSERT INTO products (id, name, category, unit_of_measure) VALUES (?, ?, 'Pantry', 'unit')`, productID, name)
	execHistory(t, db, `INSERT INTO items (id, user_id, product_id, household_id) VALUES (?, ?, ?, ?)`, itemID, household, productID, household)
}

func manualStockInWithExpiry(t *testing.T, db *sql.DB, itemID, productID string, qty int, at, expires time.Time) string {
	t.Helper()
	scope := scopeOf(t, db)
	var ids []string
	for i := 0; i < qty; i++ {
		ids = append(ids, fmt.Sprintf("inst-exp-%s-%d-%d", itemID, at.Unix(), i))
	}
	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, id := range ids {
		if _, err := tx.ExecContext(t.Context(),
			`INSERT INTO item_instances (id, item_id, stock_in_at, expires_at, household_id) VALUES (?, ?, ?, ?, ?)`,
			id, itemID, at, expires, scope.HouseholdID()); err != nil {
			t.Fatal(err)
		}
	}
	if err := inventory.UnshelveItem(t.Context(), tx, scope, itemID); err != nil {
		t.Fatal(err)
	}
	moveID, err := AttachStockIn(t.Context(), scope, tx, InNote{
		ItemID: itemID, ProductID: productID, At: at, Source: "manual", InstanceIDs: ids,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return moveID
}

func manualStockIn(t *testing.T, db *sql.DB, itemID, productID string, qty int, at time.Time) string {
	t.Helper()
	scope := scopeOf(t, db)
	var ids []string
	for i := 0; i < qty; i++ {
		ids = append(ids, fmt.Sprintf("inst-in-%s-%d-%d", itemID, at.Unix(), i))
	}
	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, id := range ids {
		if _, err := tx.ExecContext(t.Context(),
			`INSERT INTO item_instances (id, item_id, stock_in_at, household_id) VALUES (?, ?, ?, ?)`,
			id, itemID, at, scope.HouseholdID()); err != nil {
			t.Fatal(err)
		}
	}
	if err := inventory.UnshelveItem(t.Context(), tx, scope, itemID); err != nil {
		t.Fatal(err)
	}
	moveID, err := AttachStockIn(t.Context(), scope, tx, InNote{
		ItemID: itemID, ProductID: productID, At: at, Source: "manual", InstanceIDs: ids,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return moveID
}

func trackedScanStockOut(t *testing.T, db *sql.DB, itemID, productID string, qty int, at time.Time, scanID string) string {
	t.Helper()
	scope := scopeOf(t, db)
	rows, err := db.QueryContext(t.Context(), `
		SELECT id FROM item_instances
		WHERE household_id = ? AND item_id = ? AND removed_at IS NULL
		ORDER BY id LIMIT ?`, scope.HouseholdID(), itemID, qty)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatal(err)
	}
	rows.Close()
	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var consumptionIDs []string
	for _, id := range ids {
		if _, err := tx.ExecContext(t.Context(), `
			UPDATE item_instances
			SET removed_at = ?, removal_reason = 'consumed'
			WHERE id = ? AND household_id = ?`, at, id, scope.HouseholdID()); err != nil {
			t.Fatal(err)
		}
		cid := fmt.Sprintf("use-scan-%s", id)
		if _, err := tx.ExecContext(t.Context(), `
			INSERT INTO consumption_events (id, item_id, consumed_at, scan_entry_id, household_id) VALUES (?, ?, ?, ?, ?)`,
			cid, itemID, at, scanID, scope.HouseholdID()); err != nil {
			t.Fatal(err)
		}
		consumptionIDs = append(consumptionIDs, cid)
	}
	scan := scanID
	moveID, err := AttachStockOut(t.Context(), scope, tx, OutNote{
		ItemID: itemID, ProductID: productID, At: at, Source: "scan",
		ScanEntryID: &scan, Tracked: true, InstanceIDs: ids, ConsumptionIDs: consumptionIDs,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return moveID
}

func manualStockOut(t *testing.T, db *sql.DB, itemID, productID string, qty int, at time.Time, tracked bool) string {
	t.Helper()
	scope := scopeOf(t, db)
	rows, err := db.QueryContext(t.Context(), `
		SELECT id FROM item_instances
		WHERE household_id = ? AND item_id = ? AND removed_at IS NULL
		ORDER BY id LIMIT ?`, scope.HouseholdID(), itemID, qty)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatal(err)
	}
	rows.Close()
	if len(ids) < qty {
		t.Fatalf("need %d instances, found %d", qty, len(ids))
	}
	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var consumptionIDs []string
	for _, id := range ids {
		if _, err := tx.ExecContext(t.Context(), `
			UPDATE item_instances
			SET removed_at = ?, removal_reason = 'consumed'
			WHERE id = ? AND household_id = ?`, at, id, scope.HouseholdID()); err != nil {
			t.Fatal(err)
		}
		if tracked {
			cid := fmt.Sprintf("use-%s-%s", itemID, id)
			if _, err := tx.ExecContext(t.Context(), `
				INSERT INTO consumption_events (id, item_id, consumed_at, household_id) VALUES (?, ?, ?, ?)`,
				cid, itemID, at, scope.HouseholdID()); err != nil {
				t.Fatal(err)
			}
			consumptionIDs = append(consumptionIDs, cid)
		}
	}
	moveID, err := AttachStockOut(t.Context(), scope, tx, OutNote{
		ItemID: itemID, ProductID: productID, At: at, Source: "manual",
		InstanceIDs: ids, Tracked: tracked, ConsumptionIDs: consumptionIDs,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return moveID
}

func findMoveByID(view View, id string) *Move {
	for i := range view.Moves {
		if view.Moves[i].ID == id {
			return &view.Moves[i]
		}
	}
	return nil
}

const cedarHouseholdID = "0190bbbb-cccc-7000-8000-0000000000e5"

func insertCedarHousehold(t *testing.T, db *sql.DB) tenant.Scope {
	t.Helper()
	execHistory(t, db, `
		INSERT INTO households (id, name, created_at, channel_key, is_primary)
		VALUES (?, 'Household Cedar', '2026-01-01T00:00:00Z', '0123456789abcdef0123456789abcdef', 0)`, cedarHouseholdID)
	scope, err := tenant.New(cedarHouseholdID)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	execHistory(t, db, `INSERT INTO products (id, name, category, unit_of_measure) VALUES ('prod-cedar', 'Cedar', 'Pantry', 'unit')`)
	execHistory(t, db, `INSERT INTO items (id, user_id, product_id, household_id) VALUES ('item-cedar', ?, 'prod-cedar', ?)`, cedarHouseholdID, cedarHouseholdID)
	execHistory(t, db, `
		INSERT INTO stock_moves (id, item_id, product_id, direction, quantity, at, source, tracked, household_id) VALUES
			('move-cedar', 'item-cedar', 'prod-cedar', 'in', 1, ?, 'manual', 0, ?)`, at, cedarHouseholdID)
	return scope
}

func cedarMoveID(t *testing.T, db *sql.DB, _ tenant.Scope) string {
	t.Helper()
	return "move-cedar"
}
