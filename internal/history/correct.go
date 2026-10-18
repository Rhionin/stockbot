package history

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// ErrBackfillLocked is a quantity edit on either move of a backfill pair.
// The server answers with the shared backfill_locked sentence.
var ErrBackfillLocked = errors.New("backfill quantity locked")

// SetQuantity changes how many units a move put on or took off the shelf.
// The returned view is that product's history after the change.
func (j *Journal) SetQuantity(ctx context.Context, id string, quantity int, now time.Time) (View, error) {
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
	move, err := loadMove(ctx, tx, id)
	if err != nil {
		return View{}, err
	}
	if quantity != move.Quantity {
		_, paired, pairErr := backfillPartner(ctx, tx, move)
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
	return j.Product(ctx, move.ItemID, now)
}

// Undo removes a move and reverses what it did to the shelf and the pace.
// The returned view is that product's history afterward.
func (j *Journal) Undo(ctx context.Context, id string, now time.Time) (View, error) {
	tx, err := j.db.BeginTx(ctx, nil)
	if err != nil {
		return View{}, err
	}
	defer tx.Rollback()
	if err := ensure(ctx, tx); err != nil {
		return View{}, err
	}
	move, err := loadMove(ctx, tx, id)
	if err != nil {
		return View{}, err
	}
	partner, paired, err := backfillPartner(ctx, tx, move)
	if err != nil {
		return View{}, err
	}
	var reshelve string
	if paired {
		incoming, outgoing := move, partner
		if incoming.Direction != "in" {
			incoming, outgoing = partner, move
		}
		if incoming.Unshelved {
			reshelve = incoming.ItemID
		}
		if err = undoStockOut(ctx, tx, outgoing); err != nil {
			return View{}, err
		}
		err = undoStockIn(ctx, tx, incoming)
	} else if move.Direction == "in" {
		err = undoStockIn(ctx, tx, move)
	} else {
		err = undoStockOut(ctx, tx, move)
	}
	if err != nil {
		return View{}, err
	}
	if reshelve != "" {
		if _, err := tx.ExecContext(ctx, `UPDATE items SET shelved = 1 WHERE id = ?`, reshelve); err != nil {
			return View{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return View{}, err
	}
	return j.Product(ctx, move.ItemID, now)
}

// backfillPartner is the other move from the same backfill commit.
// A backfill stock-in and its stock-out share a scan id, and one of them
// has source backfill. Undoing either one removes both.
func backfillPartner(ctx context.Context, tx *sql.Tx, move moveRow) (moveRow, bool, error) {
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
		SELECT id FROM stock_moves
		WHERE scan_entry_id = ? AND id != ? AND source = ?`,
		move.ScanEntryID.String, move.ID, want).Scan(&partnerID)
	if err == sql.ErrNoRows {
		return moveRow{}, false, nil
	}
	if err != nil {
		return moveRow{}, false, err
	}
	other, err := loadMove(ctx, tx, partnerID)
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
