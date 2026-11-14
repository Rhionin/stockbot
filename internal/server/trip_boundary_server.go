package server

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/Rhionin/stockbot/internal/tenant"
	"github.com/Rhionin/stockbot/internal/trip"
)

func tripBoundaryForNewCart(ctx context.Context, scope tenant.Scope, tripSeq int, tx *sql.Tx) ([]trip.BoundaryChange, error) {
	if err := scope.Require(); err != nil {
		return nil, err
	}
	if tx == nil {
		return nil, fmt.Errorf("trip boundary requires writer transaction")
	}
	household := scope.HouseholdID()
	rows, err := tx.QueryContext(ctx, `
		SELECT item_id FROM staged_cart_skips
		WHERE household_id = ? AND expires_trip <= ?`,
		household, tripSeq)
	if err != nil {
		return nil, fmt.Errorf("could not load trip skips: %w", err)
	}
	defer rows.Close()
	var out []trip.BoundaryChange
	for rows.Next() {
		var itemID string
		if err := rows.Scan(&itemID); err != nil {
			return nil, err
		}
		if itemID == "" {
			continue
		}
		out = append(out, trip.BoundaryChange{
			LineKey: "item:" + itemID,
			Effect:  string(tripBoundaryEffectAdded),
			Cause:   "new_trip",
		})
	}
	return out, rows.Err()
}

type tripBoundaryEffect string

const tripBoundaryEffectAdded = "added"