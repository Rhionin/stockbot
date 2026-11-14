package trip

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

var faultAfterAdvanceSeq func() error

// SetFaultAfterAdvanceSeqForTest installs a fault hook on the production advance path.
func SetFaultAfterAdvanceSeqForTest(fn func() error) (restore func()) {
	prev := faultAfterAdvanceSeq
	faultAfterAdvanceSeq = fn
	return func() { faultAfterAdvanceSeq = prev }
}

func clearLapsedZeroAdjustments(ctx context.Context, tx *sql.Tx, householdID string, boundaryAt time.Time) error {
	keys, err := collectLapsedZeroLineKeys(ctx, tx, householdID)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		DELETE FROM shopping_list_entry_adjustments
		WHERE household_id = ? AND quantity = 0
		  AND (expires_trip IS NULL OR expires_trip <= (SELECT trip_seq FROM households WHERE id = ?))`,
		householdID, householdID)
	if err != nil {
		return fmt.Errorf("could not clear lapsed zero adjustments: %w", err)
	}
	tripSeq, err := currentTripSeq(ctx, tx, householdID)
	if err != nil {
		return err
	}
	for _, key := range keys {
		ch := BoundaryChange{LineKey: key, Effect: "added", Cause: "new_trip"}
		if err := WriteBoundaryChange(ctx, tx, householdID, tripSeq, boundaryAt, ch); err != nil {
			return err
		}
	}
	return nil
}

func collectLapsedZeroLineKeys(ctx context.Context, tx *sql.Tx, householdID string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT DISTINCT CASE WHEN COALESCE(i.group_id, '') <> '' THEN 'group:' || i.group_id
		                     ELSE 'item:' || i.item_id END
		FROM shopping_list_entry_adjustments a
		JOIN shopping_list_items i ON i.id = a.entry_id AND i.household_id = a.household_id
		JOIN households h ON h.id = a.household_id
		WHERE a.household_id = ? AND a.quantity = 0
		  AND (a.expires_trip IS NULL OR a.expires_trip <= h.trip_seq)
		  AND i.purchased_at IS NULL`,
		householdID)
	if err != nil {
		return nil, fmt.Errorf("could not list lapsed zero lines: %w", err)
	}
	defer rows.Close()
	var keys []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, rows.Err()
}