package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

const listReplan032Key = "list_replan_032"

// ListReplan032Coalescer triggers a full list refresh per household (§4.6).
type ListReplan032Coalescer interface {
	MarkDirty(household string)
	StartupCatchup(ctx context.Context)
	Drain(ctx context.Context, household string, timeout time.Duration) bool
}

// RunListReplan032 enqueues a one-time full list refresh for each household
// that has not yet recorded list_replan_032. When coalescer is nil (admin CLI),
// this is a no-op; the HTTP server passes the real coalescer after migrations.
func RunListReplan032(ctx context.Context, db *sql.DB, coalescer ListReplan032Coalescer) error {
	if db == nil || coalescer == nil {
		return nil
	}
	rows, err := db.QueryContext(ctx, `SELECT id FROM households ORDER BY id`)
	if err != nil {
		return fmt.Errorf("list households: %w", err)
	}
	defer rows.Close()
	var pending []string
	var householdIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return err
		}
		householdIDs = append(householdIDs, id)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("list households: %w", err)
	}
	for _, id := range householdIDs {
		var done string
		err := db.QueryRowContext(ctx,
			`SELECT value FROM household_settings WHERE household_id = ? AND key = ?`,
			id, listReplan032Key).Scan(&done)
		if errors.Is(err, sql.ErrNoRows) {
			pending = append(pending, id)
			continue
		}
		if err != nil {
			return fmt.Errorf("read %s for %s: %w", listReplan032Key, id, err)
		}
	}
	if len(pending) == 0 {
		return nil
	}
	for _, id := range pending {
		coalescer.MarkDirty(id)
	}
	coalescer.StartupCatchup(ctx)
	const drainTimeout = 2 * time.Minute
	for _, id := range pending {
		if !coalescer.Drain(ctx, id, drainTimeout) {
			return fmt.Errorf("list replan 032 timed out for household %s", id)
		}
		if _, err := db.ExecContext(ctx, `
			INSERT INTO household_settings (household_id, key, value)
			VALUES (?, ?, '1')
			ON CONFLICT (household_id, key) DO UPDATE SET value = excluded.value`,
			id, listReplan032Key); err != nil {
			return fmt.Errorf("record %s for %s: %w", listReplan032Key, id, err)
		}
	}
	return nil
}