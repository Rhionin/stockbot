package history

import (
	"context"
	"database/sql"
	"time"

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
	// Undoing the pair hides the item again.
	Unshelved bool
}

// OutNote is one stock-out that just took a unit off the shelf.
type OutNote struct {
	ItemID         string
	ProductID      string
	At             time.Time
	Source         string
	ScanEntryID    *string
	Tracked        bool
	InstanceID     string
	InstanceIDs    []string
	Quantity       int
	ConsumptionID  string
	ConsumptionIDs []string
}

// AttachStockIn records the move and points the new instances (and the
// stock-in event, when this restock counts toward pace) at it.
func AttachStockIn(ctx context.Context, tx *sql.Tx, note InNote) error {
	if len(note.InstanceIDs) == 0 {
		return nil
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
		INSERT INTO stock_moves (id, item_id, product_id, direction, quantity, at, source, scan_entry_id, tracked, unshelved)
		VALUES (?, ?, ?, 'in', ?, ?, ?, ?, ?, ?)`,
		id, note.ItemID, note.ProductID, len(note.InstanceIDs), note.At, note.Source, nullableString(note.ScanEntryID), tracked, unshelved,
	); err != nil {
		return err
	}
	for _, instanceID := range note.InstanceIDs {
		if _, err := tx.ExecContext(ctx, `
			UPDATE item_instances SET stock_move_id = ? WHERE id = ?`,
			id, instanceID,
		); err != nil {
			return err
		}
	}
	if !note.Tracked {
		return nil
	}
	_, err := tx.ExecContext(ctx, `
		UPDATE stock_in_events SET stock_move_id = ?
		WHERE id = (
			SELECT id FROM stock_in_events
			WHERE product_id = ? AND at = ? AND stock_move_id IS NULL
			ORDER BY id DESC
			LIMIT 1
		)`, id, note.ProductID, note.At)
	return err
}

// AttachStockOut records the move and points the removed unit (and the
// consumption row, when this use counts toward pace) at it.
func AttachStockOut(ctx context.Context, tx *sql.Tx, note OutNote) error {
	instances := note.InstanceIDs
	if note.InstanceID != "" {
		instances = append(instances, note.InstanceID)
	}
	if len(instances) == 0 {
		return nil
	}
	quantity := note.Quantity
	if quantity < 1 {
		quantity = len(instances)
	}
	id := uuid.NewString()
	tracked := 0
	if note.Tracked {
		tracked = 1
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO stock_moves (id, item_id, product_id, direction, quantity, at, source, scan_entry_id, tracked)
		VALUES (?, ?, ?, 'out', ?, ?, ?, ?, ?)`,
		id, note.ItemID, note.ProductID, quantity, note.At, note.Source, nullableString(note.ScanEntryID), tracked,
	); err != nil {
		return err
	}
	for _, instanceID := range instances {
		if _, err := tx.ExecContext(ctx, `
			UPDATE item_instances SET removed_by_move_id = ? WHERE id = ?`,
			id, instanceID,
		); err != nil {
			return err
		}
	}
	consumptions := note.ConsumptionIDs
	if note.ConsumptionID != "" {
		consumptions = append(consumptions, note.ConsumptionID)
	}
	for _, consumptionID := range consumptions {
		if _, err := tx.ExecContext(ctx, `
			UPDATE consumption_events SET stock_move_id = ? WHERE id = ?`,
			id, consumptionID,
		); err != nil {
			return err
		}
	}
	return nil
}

func nullableString(value *string) any {
	if value == nil || *value == "" {
		return nil
	}
	return *value
}
