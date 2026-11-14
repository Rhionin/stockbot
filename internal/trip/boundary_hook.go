package trip

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// BoundaryPlanner supplies list_line_changes rows after trip_seq advances inside an open writer txn.
type BoundaryPlanner func(ctx context.Context, householdID string, tripSeq int, tx *sql.Tx) ([]BoundaryChange, error)

var boundaryPlanner BoundaryPlanner

// SetBoundaryPlanner wires trip-advance boundary upserts (W3). Call from server startup.
func SetBoundaryPlanner(fn BoundaryPlanner) {
	boundaryPlanner = fn
}

func writePlannedBoundaryChanges(ctx context.Context, householdID string, tx *sql.Tx, now time.Time) error {
	if boundaryPlanner == nil {
		return nil
	}
	tripSeq, err := currentTripSeq(ctx, tx, householdID)
	if err != nil {
		return err
	}
	changes, err := boundaryPlanner(ctx, householdID, tripSeq, tx)
	if err != nil {
		return fmt.Errorf("could not plan trip boundary: %w", err)
	}
	for _, ch := range changes {
		if err := WriteBoundaryChange(ctx, tx, householdID, tripSeq, now, ch); err != nil {
			return err
		}
	}
	return nil
}