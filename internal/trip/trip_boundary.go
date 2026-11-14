package trip

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/Rhionin/stockbot/internal/changes"
	"github.com/Rhionin/stockbot/internal/tenant"
)

// BoundaryChange is one list_line_changes row written at trip advance.
type BoundaryChange struct {
	LineKey     string
	Effect      string
	Reason      string
	Cause       string
	FromQty     *int
	ToQty       *int
	N           *int
	ActorUserID *string
}

// TripAdvance runs read-pool plan inputs then one writer transaction for trip boundaries.
type TripAdvance struct {
	DB       *sql.DB
	Now      func() time.Time
	// FaultAfterDeleteChanges aborts the writer txn after stale change delete (T19).
	FaultAfterDeleteChanges func() error
}

// Advance runs trip_seq+1 with boundary upserts in one writer commit.
func (t *TripAdvance) Advance(ctx context.Context, scope tenant.Scope, cause string, boundaryChanges []BoundaryChange) error {
	if err := scope.Require(); err != nil {
		return err
	}
	if t == nil || t.DB == nil {
		return fmt.Errorf("trip advance not configured")
	}
	now := time.Now().UTC()
	if t.Now != nil {
		now = t.Now()
	}
	tx, err := t.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	household := scope.HouseholdID()
	if err := AdvanceSeq(ctx, tx, household, cause, now); err != nil {
		return err
	}
	if t.FaultAfterDeleteChanges != nil {
		if err := t.FaultAfterDeleteChanges(); err != nil {
			return err
		}
	}
	var tripSeq int
	if err := tx.QueryRowContext(ctx, `SELECT trip_seq FROM households WHERE id = ?`, household).Scan(&tripSeq); err != nil {
		return err
	}
	for _, ch := range boundaryChanges {
		if err := upsertBoundaryChange(ctx, tx, household, tripSeq, now, ch); err != nil {
			return err
		}
	}
	if err := changes.Bump(ctx, tx, household, changes.ScopeList); err != nil {
		return err
	}
	return tx.Commit()
}

// WriteBoundaryChange upserts one list_line_changes row at the new trip_seq.
func WriteBoundaryChange(ctx context.Context, tx *sql.Tx, household string, tripSeq int, now time.Time, ch BoundaryChange) error {
	return upsertBoundaryChange(ctx, tx, household, tripSeq, now, ch)
}

func upsertBoundaryChange(ctx context.Context, tx *sql.Tx, household string, tripSeq int, now time.Time, ch BoundaryChange) error {
	fromQty, toQty := ch.FromQty, ch.ToQty
	if ch.Effect == "added" {
		fromQty, toQty = nil, nil
	}
	reason := any(nil)
	if ch.Reason != "" {
		reason = ch.Reason
	}
	actor := any(nil)
	if ch.ActorUserID != nil {
		actor = *ch.ActorUserID
	}
	cause := ch.Cause
	if cause == "" {
		cause = "new_trip"
	}
	at := now.UTC().Format(time.RFC3339Nano)
	_, err := tx.ExecContext(ctx, `
		INSERT INTO list_line_changes (
			household_id, line_key, effect, reason, cause, from_qty, to_qty, n, actor_user_id, at, trip_seq
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(household_id, line_key) DO UPDATE SET
			effect = excluded.effect,
			reason = excluded.reason,
			cause = excluded.cause,
			from_qty = excluded.from_qty,
			to_qty = excluded.to_qty,
			n = excluded.n,
			actor_user_id = excluded.actor_user_id,
			at = excluded.at,
			trip_seq = excluded.trip_seq`,
		household, ch.LineKey, ch.Effect, reason, cause,
		fromQty, toQty, ch.N, actor, at, tripSeq,
	)
	if err != nil {
		return fmt.Errorf("could not write trip boundary change: %w", err)
	}
	return nil
}