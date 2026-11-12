package shopping

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Rhionin/stockbot/internal/tenant"
	"github.com/Rhionin/stockbot/internal/trip"
)

// ErrTripSkipNotFound is returned when no active remove-skip exists for this trip.
var ErrTripSkipNotFound = errors.New("trip remove skip not found")

// UndoRemove clears the current-trip remove skip for itemID and re-plans that
// one auto line. It does not change skips on other trips or lasting target-0 off.
func (s *Store) UndoRemove(ctx context.Context, scope tenant.Scope, itemID string, line PlannedLine) (*ShoppingListItem, error) {
	if err := scope.Require(); err != nil {
		return nil, err
	}
	if itemID == "" {
		return nil, ErrTripSkipNotFound
	}
	household := scope.HouseholdID()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("could not undo remove: %w", err)
	}
	defer tx.Rollback()

	if err := trip.MaintainSkips(ctx, scope, tx, time.Now().UTC()); err != nil {
		return nil, err
	}
	var pantryItem int
	if err := tx.QueryRowContext(ctx,
		`SELECT 1 FROM items WHERE id = ? AND household_id = ?`, itemID, household).Scan(&pantryItem); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrTripSkipNotFound
		}
		return nil, fmt.Errorf("could not undo remove: %w", err)
	}
	replanItem := itemID
	if line.GroupKey != "" {
		if line.ItemID == "" {
			return nil, ErrTripSkipNotFound
		}
		replanItem = line.ItemID
	} else if line.ItemID != "" && line.ItemID != itemID {
		return nil, fmt.Errorf("could not undo remove: mismatched item")
	}
	if line.Quantity < 1 || line.Manual {
		return nil, ErrTripSkipNotFound
	}
	res, err := tx.ExecContext(ctx, `
		DELETE FROM staged_cart_skips
		WHERE household_id = ? AND user_id = ? AND item_id = ?
		  AND expires_trip > (SELECT trip_seq FROM households WHERE id = ?)`,
		household, household, itemID, household)
	if err != nil {
		return nil, fmt.Errorf("could not undo remove: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("could not undo remove: %w", err)
	}
	if n == 0 {
		return nil, ErrTripSkipNotFound
	}
	if err := upsertAutoLine(ctx, scope, tx, replanItem, line.GroupKey, line.Quantity, line.Note); err != nil {
		return nil, err
	}
	if err := s.commitList(ctx, tx, household); err != nil {
		return nil, fmt.Errorf("could not undo remove: %w", err)
	}
	return s.unpurchasedAfterUndo(ctx, scope, line.GroupKey, replanItem)
}

func (s *Store) unpurchasedAfterUndo(ctx context.Context, scope tenant.Scope, groupID, itemID string) (*ShoppingListItem, error) {
	rows, err := s.ListUnpurchased(ctx, scope)
	if err != nil {
		return nil, err
	}
	if groupID != "" {
		for i := range rows {
			if rows[i].GroupID == groupID && rows[i].Source == "auto" {
				return &rows[i], nil
			}
		}
	}
	for i := range rows {
		if rows[i].ItemID == itemID {
			return &rows[i], nil
		}
	}
	return nil, fmt.Errorf("could not undo remove: shopping list line missing")
}
