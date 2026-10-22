package history

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/Rhionin/stockbot/internal/app"
)

// ErrBackfillLocked is a quantity edit on either move of a backfill pair.
// The server answers with the shared backfill_locked sentence.
var ErrBackfillLocked = errors.New("backfill quantity locked")

// SetQuantity changes how many units a move put on or took off the shelf.
// The returned view is that product's history after the change.
func (j *Journal) SetQuantity(ctx context.Context, userID, id string, quantity int, now time.Time) (View, error) {
	if quantity < 1 {
		return View{}, &InputError{Message: "Quantity has to be at least 1. Undo the move to remove it."}
	}
	if quantity > 999 {
		return View{}, &InputError{Message: "Quantity has to be 999 or less."}
	}
	tx, err := j.db.BeginTx(ctx, nil)
	if err != nil {
		return View{}, err
	}
	defer tx.Rollback()
	if err := ensure(ctx, tx); err != nil {
		return View{}, err
	}
	move, err := loadMove(ctx, tx, userID, id)
	if err != nil {
		return View{}, err
	}
	if quantity != move.Quantity {
		_, paired, pairErr := backfillPartner(ctx, tx, userID, move)
		if pairErr != nil {
			return View{}, pairErr
		}
		if paired {
			return View{}, ErrBackfillLocked
		}
		if move.Direction == "in" {
			err = resizeStockIn(ctx, tx, move, quantity)
		} else {
			err = resizeStockOut(ctx, tx, move, quantity)
		}
		if err != nil {
			return View{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return View{}, err
	}
	return j.Product(ctx, userID, move.ItemID, now)
}

// UndoEffect is one product an undo would change.
// OnHand is the count after the undo. ReshelvesOnUndo is whether that
// undo hides the item again.
type UndoEffect struct {
	ItemID          string
	ProductID       string
	Name            string
	StockInMoveID   string
	StockOutMoveID  string
	OnHand          int
	ReshelvesOnUndo bool
}

// UndoPreview is what an undo would change. It changes nothing.
type UndoPreview struct {
	Effects []UndoEffect
}

// Undo removes a move and reverses what it did to the shelf and the pace.
// The returned view is that product's history afterward.
func (j *Journal) Undo(ctx context.Context, userID, id string, now time.Time) (View, error) {
	tx, err := j.db.BeginTx(ctx, nil)
	if err != nil {
		return View{}, err
	}
	defer tx.Rollback()
	effect, err := applyUndo(ctx, tx, userID, id, true)
	if err != nil {
		return View{}, err
	}
	if err := tx.Commit(); err != nil {
		return View{}, err
	}
	return j.Product(ctx, userID, effect.ItemID, now)
}

// PreviewUndo reports what Undo would change. It runs that same undo on a
// throwaway copy of the pantry, so the pantry itself is only read and the
// re-shelve decision cannot drift from the one Undo commits. The copy skips
// the history_repair update. Repair still runs on the copy when older rows
// would change the result.
func (j *Journal) PreviewUndo(ctx context.Context, userID, id string) (UndoPreview, error) {
	release, err := acquirePreview(ctx)
	if err != nil {
		return UndoPreview{}, err
	}
	defer release()
	clone, cleanup, err := j.snapshot(ctx)
	if err != nil {
		return UndoPreview{}, err
	}
	defer cleanup()
	// The copy's journal is created while the undo writes. Keep that file
	// owner-readable only, the same way the backup creates preview.db.
	var effect UndoEffect
	err = app.WithPrivateFiles(func() error {
		tx, err := clone.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		effect, err = applyUndo(ctx, tx, userID, id, false)
		return err
	})
	if err != nil {
		return UndoPreview{}, previewErr(ctx, err)
	}
	return UndoPreview{Effects: []UndoEffect{effect}}, nil
}

func applyUndo(ctx context.Context, tx *sql.Tx, userID, id string, lockRepair bool) (UndoEffect, error) {
	var err error
	if lockRepair {
		err = ensure(ctx, tx)
	} else {
		err = repair(ctx, tx)
	}
	if err != nil {
		return UndoEffect{}, err
	}
	move, err := loadMove(ctx, tx, userID, id)
	if err != nil {
		return UndoEffect{}, err
	}
	partner, paired, err := backfillPartner(ctx, tx, userID, move)
	if err != nil {
		return UndoEffect{}, err
	}
	var broughtBack string
	var stockInID, stockOutID string
	if paired {
		incoming, outgoing := move, partner
		if incoming.Direction != "in" {
			incoming, outgoing = partner, move
		}
		stockInID, stockOutID = incoming.ID, outgoing.ID
		if incoming.Unshelved {
			broughtBack = incoming.ItemID
		}
		if err = undoStockOut(ctx, tx, outgoing); err != nil {
			return UndoEffect{}, err
		}
		err = undoStockIn(ctx, tx, incoming)
	} else if move.Direction == "in" {
		stockInID = move.ID
		err = undoStockIn(ctx, tx, move)
	} else {
		stockOutID = move.ID
		err = undoStockOut(ctx, tx, move)
	}
	if err != nil {
		return UndoEffect{}, err
	}
	reshelve, err := reshelveItem(ctx, tx, broughtBack)
	if err != nil {
		return UndoEffect{}, err
	}
	if reshelve {
		if _, err := tx.ExecContext(ctx, `UPDATE items SET shelved = 1 WHERE id = ?`, broughtBack); err != nil {
			return UndoEffect{}, err
		}
	}
	var productID, name string
	if err := tx.QueryRowContext(ctx, `
		SELECT i.product_id, p.name
		FROM items i JOIN products p ON p.id = i.product_id
		WHERE i.id = ?`, move.ItemID).Scan(&productID, &name); err != nil {
		return UndoEffect{}, err
	}
	onHand, err := countOnHand(ctx, tx, []string{move.ItemID})
	if err != nil {
		return UndoEffect{}, err
	}
	return UndoEffect{
		ItemID: move.ItemID, ProductID: productID, Name: name,
		StockInMoveID: stockInID, StockOutMoveID: stockOutID,
		OnHand: onHand, ReshelvesOnUndo: reshelve,
	}, nil
}

// reshelveItem reports whether undoing a pair that brought a wiped item back
// hides it again. A later unit that is still on the shelf keeps the item visible.
// Units that were already used do not.
func reshelveItem(ctx context.Context, tx *sql.Tx, itemID string) (bool, error) {
	if itemID == "" {
		return false, nil
	}
	live, err := countQuery(ctx, tx, `
		SELECT COUNT(*) FROM item_instances
		WHERE item_id = ? AND removed_at IS NULL`, itemID)
	if err != nil {
		return false, err
	}
	return live == 0, nil
}

// backfillPartner is the other move from the same backfill commit.
// A backfill stock-in and its stock-out share a scan id, and one of them
// has source backfill. Undoing either one removes both.
func backfillPartner(ctx context.Context, tx *sql.Tx, userID string, move moveRow) (moveRow, bool, error) {
	if !move.ScanEntryID.Valid || move.ScanEntryID.String == "" {
		return moveRow{}, false, nil
	}
	want := ""
	switch move.Source {
	case "backfill":
		want = "scan"
	case "scan":
		want = "backfill"
	default:
		return moveRow{}, false, nil
	}
	var partnerID string
	err := tx.QueryRowContext(ctx, `
		SELECT m.id FROM stock_moves m
		JOIN items i ON i.id = m.item_id
		WHERE m.scan_entry_id = ? AND m.id != ? AND m.source = ? AND i.user_id = ?`,
		move.ScanEntryID.String, move.ID, want, userID).Scan(&partnerID)
	if err == sql.ErrNoRows {
		return moveRow{}, false, nil
	}
	if err != nil {
		return moveRow{}, false, err
	}
	other, err := loadMove(ctx, tx, userID, partnerID)
	if err != nil {
		return moveRow{}, false, err
	}
	return other, true, nil
}

func resizeStockIn(ctx context.Context, tx *sql.Tx, move moveRow, quantity int) error {
	if quantity > move.Quantity {
		return addStockInUnits(ctx, tx, move, quantity-move.Quantity)
	}
	return removeStockInUnits(ctx, tx, move, move.Quantity-quantity)
}

func addStockInUnits(ctx context.Context, tx *sql.Tx, move moveRow, extra int) error {
	var expires sql.NullTime
	_ = tx.QueryRowContext(ctx, `
		SELECT expires_at FROM item_instances WHERE stock_move_id = ? AND expires_at IS NOT NULL LIMIT 1`,
		move.ID).Scan(&expires)
	for i := 0; i < extra; i++ {
		var expiresAt any
		if expires.Valid {
			expiresAt = expires.Time
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO item_instances (id, item_id, stock_in_at, expires_at, stock_move_id)
			VALUES (?, ?, ?, ?, ?)`,
			uuid.NewString(), move.ItemID, move.At, expiresAt, move.ID,
		); err != nil {
			return err
		}
	}
	_, err := tx.ExecContext(ctx, `UPDATE stock_moves SET quantity = quantity + ? WHERE id = ?`, extra, move.ID)
	return err
}

func removeStockInUnits(ctx context.Context, tx *sql.Tx, move moveRow, drop int) error {
	onHand, err := idsOf(ctx, tx, `
		SELECT id FROM item_instances
		WHERE stock_move_id = ? AND removed_at IS NULL
		ORDER BY id`, move.ID)
	if err != nil {
		return err
	}
	if len(onHand) < drop {
		used, err := countQuery(ctx, tx, `
			SELECT COUNT(*) FROM item_instances WHERE stock_move_id = ? AND removed_at IS NOT NULL`, move.ID)
		if err != nil {
			return err
		}
		if used > 0 {
			return &ConflictError{Message: alreadyUsed(used)}
		}
		return &ConflictError{Message: "This stock in can't be lowered to that quantity."}
	}
	for _, id := range onHand[:drop] {
		if _, err := tx.ExecContext(ctx, `DELETE FROM item_instances WHERE id = ?`, id); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE stock_moves SET quantity = quantity - ? WHERE id = ?`, drop, move.ID)
	return err
}

func resizeStockOut(ctx context.Context, tx *sql.Tx, move moveRow, quantity int) error {
	if quantity > move.Quantity {
		return addStockOutUnits(ctx, tx, move, quantity-move.Quantity)
	}
	return removeStockOutUnits(ctx, tx, move, move.Quantity-quantity)
}

func addStockOutUnits(ctx context.Context, tx *sql.Tx, move moveRow, extra int) error {
	onHand, err := countQuery(ctx, tx, `
		SELECT COUNT(*) FROM item_instances WHERE item_id = ? AND removed_at IS NULL`, move.ItemID)
	if err != nil {
		return err
	}
	if onHand < extra {
		return &ConflictError{Message: notEnough(onHand, move.Quantity+extra)}
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT id FROM item_instances
		WHERE item_id = ? AND removed_at IS NULL
		ORDER BY expires_at ASC NULLS LAST
		LIMIT ?`, move.ItemID, extra)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx, `
			UPDATE item_instances
			SET removed_at = ?, removal_reason = 'consumed', removed_by_move_id = ?
			WHERE id = ?`, move.At, move.ID, id); err != nil {
			return err
		}
		if move.Tracked {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO consumption_events (id, item_id, consumed_at, scan_entry_id, stock_move_id)
				VALUES (?, ?, ?, ?, ?)`,
				uuid.NewString(), move.ItemID, move.At, nullableNull(move.ScanEntryID), move.ID,
			); err != nil {
				return err
			}
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE stock_moves SET quantity = quantity + ? WHERE id = ?`, extra, move.ID)
	return err
}

func removeStockOutUnits(ctx context.Context, tx *sql.Tx, move moveRow, drop int) error {
	restored, err := restoreUnits(ctx, tx, move, drop)
	if err != nil {
		return err
	}
	if restored < drop {
		return &ConflictError{Message: "This use can't be lowered because those units are no longer recorded."}
	}
	if move.Tracked {
		if err := deleteConsumptions(ctx, tx, move.ID, drop); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE stock_moves SET quantity = quantity - ? WHERE id = ?`, drop, move.ID)
	return err
}

func undoStockIn(ctx context.Context, tx *sql.Tx, move moveRow) error {
	used, err := countQuery(ctx, tx, `
		SELECT COUNT(*) FROM item_instances WHERE stock_move_id = ? AND removed_at IS NOT NULL`, move.ID)
	if err != nil {
		return err
	}
	if used > 0 {
		return &ConflictError{Message: alreadyUsed(used)}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM item_instances WHERE stock_move_id = ?`, move.ID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM stock_in_events WHERE stock_move_id = ?`, move.ID); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM stock_moves WHERE id = ?`, move.ID)
	return err
}

func undoStockOut(ctx context.Context, tx *sql.Tx, move moveRow) error {
	restored, err := restoreUnits(ctx, tx, move, move.Quantity)
	if err != nil {
		return err
	}
	if restored < move.Quantity {
		return &ConflictError{Message: "This use can't be undone because those units are no longer recorded."}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM consumption_events WHERE stock_move_id = ?`, move.ID); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM stock_moves WHERE id = ?`, move.ID)
	return err
}

func restoreUnits(ctx context.Context, tx *sql.Tx, move moveRow, limit int) (int, error) {
	ids, err := idsOf(ctx, tx, `
		SELECT id FROM item_instances
		WHERE removed_by_move_id = ?
		ORDER BY id
		LIMIT ?`, move.ID, limit)
	if err != nil {
		return 0, err
	}
	if len(ids) < limit {
		more, err := idsOf(ctx, tx, `
			SELECT id FROM item_instances
			WHERE item_id = ? AND removed_at IS NOT NULL AND removed_by_move_id IS NULL
				AND (removal_reason IS NULL OR removal_reason = 'consumed')
			ORDER BY removed_at DESC, id
			LIMIT ?`, move.ItemID, limit-len(ids))
		if err != nil {
			return 0, err
		}
		ids = append(ids, more...)
	}
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx, `
			UPDATE item_instances
			SET removed_at = NULL, removal_reason = NULL, removed_by_move_id = NULL
			WHERE id = ?`, id); err != nil {
			return 0, err
		}
	}
	return len(ids), nil
}

func deleteConsumptions(ctx context.Context, tx *sql.Tx, moveID string, limit int) error {
	ids, err := idsOf(ctx, tx, `
		SELECT id FROM consumption_events WHERE stock_move_id = ? ORDER BY id LIMIT ?`, moveID, limit)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx, `DELETE FROM consumption_events WHERE id = ?`, id); err != nil {
			return err
		}
	}
	return nil
}

func idsOf(ctx context.Context, tx *sql.Tx, query string, args ...any) ([]string, error) {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func countQuery(ctx context.Context, tx *sql.Tx, query string, args ...any) (int, error) {
	var n int
	err := tx.QueryRowContext(ctx, query, args...).Scan(&n)
	return n, err
}

func nullableNull(value sql.NullString) any {
	if !value.Valid || value.String == "" {
		return nil
	}
	return value.String
}

func notEnough(onHand, want int) string {
	if onHand == 1 {
		return fmt.Sprintf("Only 1 is on the shelf, so this can't be %d.", want)
	}
	return fmt.Sprintf("Only %d are on the shelf, so this can't be %d.", onHand, want)
}

func alreadyUsed(used int) string {
	if used == 1 {
		return "1 of these was already used. Undo that use first."
	}
	return fmt.Sprintf("%d of these were already used. Undo those uses first.", used)
}
