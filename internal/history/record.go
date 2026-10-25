package history

import (
	"context"
	"database/sql"
	"time"

	"github.com/Rhionin/stockbot/internal/tenant"
	"github.com/google/uuid"
)

// InNote is one stock-in that just put units on the shelf.
type InNote struct {
	ItemID      string
	ProductID   string
	At          time.Time
	Source      string
	ScanEntryID *string
	Tracked     bool
	InstanceIDs []string
	// Unshelved is set when this stock-in put a wiped item back on the shelf.
	// Undoing the pair hides the item again when no live unit remains.
	Unshelved bool
}

// OutNote is one stock-out that just took one or more units off the shelf.
// InstanceIDs is the units this move removed. A single InstanceID is the
// same note with one unit, which is what older callers and history repair write.
type OutNote struct {
	ItemID         string
	ProductID      string
	At             time.Time
	Source         string
	ScanEntryID    *string
	Tracked        bool
	InstanceID     string
	InstanceIDs    []string
	ConsumptionID  string
	ConsumptionIDs []string
}

// AttachStockIn records the move and points the new instances (and the
// stock-in event, when this restock counts toward pace) at it.
func AttachStockIn(ctx context.Context, scope tenant.Scope, tx *sql.Tx, note InNote) (string, error) {
	if err := scope.Require(); err != nil {
		return "", err
	}
	if len(note.InstanceIDs) == 0 {
		return "", nil
	}
	id := uuid.NewString()
	tracked := 0
	if note.Tracked {
		tracked = 1
	}
	unshelved := 0
	if note.Unshelved {
		unshelved = 1
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO stock_moves (id, item_id, product_id, direction, quantity, at, source, scan_entry_id, tracked, unshelved, household_id)
		VALUES (?, ?, ?, 'in', ?, ?, ?, ?, ?, ?, ?)`,
		id, note.ItemID, note.ProductID, len(note.InstanceIDs), note.At, note.Source, nullableString(note.ScanEntryID), tracked, unshelved, scope.HouseholdID(),
	); err != nil {
		return "", err
	}
	for _, instanceID := range note.InstanceIDs {
		if _, err := tx.ExecContext(ctx, `
			UPDATE item_instances SET stock_move_id = ? WHERE id = ? AND household_id = ?`,
			id, instanceID, scope.HouseholdID(),
		); err != nil {
			return "", err
		}
	}
	if !note.Tracked {
		return id, nil
	}
	_, err := tx.ExecContext(ctx, `
		UPDATE stock_in_events SET stock_move_id = ?
		WHERE household_id = ? AND id = (
			SELECT id FROM stock_in_events
			WHERE household_id = ? AND product_id = ? AND at = ? AND stock_move_id IS NULL
			ORDER BY id DESC
			LIMIT 1
		)`, id, scope.HouseholdID(), scope.HouseholdID(), note.ProductID, note.At)
	if err != nil {
		return "", err
	}
	return id, nil
}

// AttachStockOut records the move and points every removed unit (and each
// consumption row, when this use counts toward pace) at it. Quantity is the
// number of units, so undoing the move puts all of them back.
func AttachStockOut(ctx context.Context, scope tenant.Scope, tx *sql.Tx, note OutNote) (string, error) {
	if err := scope.Require(); err != nil {
		return "", err
	}
	ids := note.InstanceIDs
	if len(ids) == 0 && note.InstanceID != "" {
		ids = []string{note.InstanceID}
	}
	if len(ids) == 0 {
		return "", nil
	}
	consumptionIDs := note.ConsumptionIDs
	if len(consumptionIDs) == 0 && note.ConsumptionID != "" {
		consumptionIDs = []string{note.ConsumptionID}
	}
	id := uuid.NewString()
	tracked := 0
	if note.Tracked {
		tracked = 1
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO stock_moves (id, item_id, product_id, direction, quantity, at, source, scan_entry_id, tracked, household_id)
		VALUES (?, ?, ?, 'out', ?, ?, ?, ?, ?, ?)`,
		id, note.ItemID, note.ProductID, len(ids), note.At, note.Source, nullableString(note.ScanEntryID), tracked, scope.HouseholdID(),
	); err != nil {
		return "", err
	}
	for _, instanceID := range ids {
		if _, err := tx.ExecContext(ctx, `
			UPDATE item_instances SET removed_by_move_id = ? WHERE id = ? AND household_id = ?`,
			id, instanceID, scope.HouseholdID(),
		); err != nil {
			return "", err
		}
	}
	for _, consumptionID := range consumptionIDs {
		if consumptionID == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE consumption_events SET stock_move_id = ? WHERE id = ? AND household_id = ?`,
			id, consumptionID, scope.HouseholdID(),
		); err != nil {
			return "", err
		}
	}
	return id, nil
}

func nullableString(value *string) any {
	if value == nil || *value == "" {
		return nil
	}
	return *value
}
