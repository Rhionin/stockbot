package shopping

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/Rhionin/stockbot/internal/tenant"
)

// Effect is why a line changed on the automatic list.
type Effect string

const (
	EffectAdded      Effect = "added"
	EffectAmountUp   Effect = "amount_up"
	EffectAmountDown Effect = "amount_down"
	EffectTakenOff   Effect = "taken_off"
)

// Cause is what triggered an automatic list write.
type Cause string

const (
	CauseTakenOut      Cause = "taken_out"
	CausePutAway       Cause = "put_away"
	CauseTargetChanged Cause = "target_changed"
	CauseGroupChanged  Cause = "group_changed"
	CauseNewTrip       Cause = "new_trip"
	CauseSetupFinished Cause = "setup_finished"
	CauseRestockedHere Cause = "restocked_here"
	CauseListUpdated   Cause = "list_updated"
	CauseUsageLearned  Cause = "usage_learned"
)

// TakenOffReason explains a taken_off effect.
type TakenOffReason string

const (
	TakenOffOnHandMeetsPar   TakenOffReason = "on_hand_meets_par"
	TakenOffTargetZero       TakenOffReason = "target_zero"
	TakenOffNothingToReplace TakenOffReason = "nothing_to_replace"
)

// LineChange is the latest automatic change for one line key.
type LineChange struct {
	LineKey          string
	Effect           Effect
	Reason           TakenOffReason
	Cause            Cause
	FromQty          *int
	ToQty            *int
	N                *int
	ActorUserID      *string
	ActorDisplayName *string
	At               time.Time
	TripSeq          int
}

func (e Effect) Validate() error {
	switch e {
	case EffectAdded, EffectAmountUp, EffectAmountDown, EffectTakenOff:
		return nil
	default:
		return fmt.Errorf("invalid list line effect")
	}
}

func (c Cause) Validate() error {
	switch c {
	case CauseTakenOut, CausePutAway, CauseTargetChanged, CauseGroupChanged,
		CauseNewTrip, CauseSetupFinished, CauseRestockedHere, CauseListUpdated, CauseUsageLearned:
		return nil
	default:
		return fmt.Errorf("invalid list line cause")
	}
}

func (r TakenOffReason) Validate() error {
	switch r {
	case TakenOffOnHandMeetsPar, TakenOffTargetZero, TakenOffNothingToReplace:
		return nil
	default:
		return fmt.Errorf("invalid taken off reason")
	}
}

func (c LineChange) Validate() error {
	if err := c.Effect.Validate(); err != nil {
		return err
	}
	if err := c.Cause.Validate(); err != nil {
		return err
	}
	if c.Effect == EffectTakenOff && c.Reason != "" {
		if err := c.Reason.Validate(); err != nil {
			return err
		}
	}
	if c.LineKey == "" {
		return errors.New("line key is required")
	}
	return nil
}

// ItemLineKey builds the planner key for an item line.
func ItemLineKey(itemID string) string { return "item:" + itemID }

// GroupLineKey builds the planner key for a group line.
func GroupLineKey(groupID string) string { return "group:" + groupID }

// ListLineChanges loads every change row for the household in one query.
func (s *Store) ListLineChanges(ctx context.Context, scope tenant.Scope) (map[string]LineChange, error) {
	if err := scope.Require(); err != nil {
		return nil, err
	}
	var exists int
	if err := s.db.QueryRowContext(ctx, `
		SELECT 1 FROM sqlite_master WHERE type='table' AND name='list_line_changes'`).Scan(&exists); err != nil || exists == 0 {
		return map[string]LineChange{}, nil
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT line_key, effect, COALESCE(reason,''), cause, from_qty, to_qty, n, actor_user_id, at, trip_seq
		FROM list_line_changes WHERE household_id = ?`, scope.HouseholdID())
	if err != nil {
		return nil, fmt.Errorf("could not load list changes: %w", err)
	}
	defer rows.Close()
	out := map[string]LineChange{}
	for rows.Next() {
		var row LineChange
		var reason, actor sql.NullString
		var atRaw string
		if err := rows.Scan(&row.LineKey, &row.Effect, &reason, &row.Cause, &row.FromQty, &row.ToQty, &row.N, &actor, &atRaw, &row.TripSeq); err != nil {
			return nil, fmt.Errorf("could not read list change: %w", err)
		}
		at, err := parseStoredTime(atRaw)
		if err != nil {
			return nil, fmt.Errorf("could not read list change time: %w", err)
		}
		row.At = at
		if reason.Valid {
			row.Reason = TakenOffReason(reason.String)
		}
		if actor.Valid {
			id := actor.String
			row.ActorUserID = &id
		}
		out[row.LineKey] = row
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("could not read list changes: %w", err)
	}
	if err := resolveLineChangeActors(ctx, s.db, out); err != nil {
		return nil, err
	}
	return out, nil
}

func resolveLineChangeActors(ctx context.Context, db *sql.DB, rows map[string]LineChange) error {
	ids := map[string]struct{}{}
	for _, row := range rows {
		if row.ActorUserID != nil && *row.ActorUserID != "" {
			ids[*row.ActorUserID] = struct{}{}
		}
	}
	if len(ids) == 0 {
		return nil
	}
	args := make([]any, 0, len(ids))
	placeholders := make([]string, 0, len(ids))
	for id := range ids {
		placeholders = append(placeholders, "?")
		args = append(args, id)
	}
	q := `SELECT id, COALESCE(name, '') FROM users WHERE id IN (` + strings.Join(placeholders, ",") + `)`
	names := map[string]string{}
	dbRows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return fmt.Errorf("could not resolve change actors: %w", err)
	}
	defer dbRows.Close()
	for dbRows.Next() {
		var id, name string
		if err := dbRows.Scan(&id, &name); err != nil {
			return fmt.Errorf("could not read change actor: %w", err)
		}
		names[id] = name
	}
	if err := dbRows.Err(); err != nil {
		return fmt.Errorf("could not read change actors: %w", err)
	}
	for key, row := range rows {
		if row.ActorUserID == nil {
			continue
		}
		if name, ok := names[*row.ActorUserID]; ok && name != "" {
			row.ActorDisplayName = &name
			rows[key] = row
		}
	}
	return nil
}

// UpsertLineChange writes or skips a change row in the same transaction as the line write.
// When effect and quantities are unchanged, at is left alone (T33).
func upsertLineChange(ctx context.Context, scope tenant.Scope, tx *sql.Tx, change LineChange) error {
	if err := scope.Require(); err != nil {
		return err
	}
	if err := change.Validate(); err != nil {
		return err
	}
	household := scope.HouseholdID()
	var existing Effect
	var fromQty, toQty sql.NullInt64
	var existingAt string
	err := tx.QueryRowContext(ctx, `
		SELECT effect, from_qty, to_qty, at FROM list_line_changes
		WHERE household_id = ? AND line_key = ?`, household, change.LineKey).
		Scan(&existing, &fromQty, &toQty, &existingAt)
	if err == nil {
		if existing == change.Effect && qtyEqual(fromQty, change.FromQty) && qtyEqual(toQty, change.ToQty) {
			return nil
		}
	} else if err != sql.ErrNoRows {
		return fmt.Errorf("could not load list change: %w", err)
	}
	actor := any(nil)
	if change.ActorUserID != nil {
		actor = *change.ActorUserID
	}
	reason := any(nil)
	if change.Reason != "" {
		reason = string(change.Reason)
	}
	at := change.At.UTC().Format(time.RFC3339Nano)
	if at == "" {
		at = time.Now().UTC().Format(time.RFC3339Nano)
	}
	_, err = tx.ExecContext(ctx, `
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
		household, change.LineKey, change.Effect, reason, change.Cause,
		change.FromQty, change.ToQty, change.N, actor, at, change.TripSeq,
	)
	if err != nil {
		return fmt.Errorf("could not save list change: %w", err)
	}
	return nil
}

func qtyEqual(null sql.NullInt64, ptr *int) bool {
	if ptr == nil {
		return !null.Valid
	}
	return null.Valid && int(null.Int64) == *ptr
}

// ActorFromContext returns the commit user when present.
func ActorFromContext(ctx context.Context) *string {
	if p, ok := auth.PrincipalFrom(ctx); ok && p.UserID != "" {
		id := p.UserID
		return &id
	}
	return nil
}