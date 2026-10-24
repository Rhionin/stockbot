package auth

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"time"
	"unicode"
)

const purgeBatch = 1000

// PurgeResult is what one purge pass deleted. Largest is the most rows one
// statement removed. It is never above purgeBatch.
type PurgeResult struct {
	Households   int
	Batches      int
	Largest      int
	Passwords    int
	SecureDelete bool
}

// PurgeDue hard-deletes households whose restore window has ended, and clears
// a retired household password once its seven days are over.
// The connection runs with PRAGMA secure_delete=ON so freed pages are overwritten.
func (s *Service) PurgeDue(ctx context.Context) (PurgeResult, error) {
	var result PurgeResult
	if s == nil || s.db == nil {
		return result, nil
	}
	var previous int
	if err := s.db.QueryRowContext(ctx, `PRAGMA secure_delete`).Scan(&previous); err != nil {
		return result, AsError(err)
	}
	if _, err := s.db.ExecContext(ctx, `PRAGMA secure_delete = ON`); err != nil {
		return result, AsError(err)
	}
	defer func() {
		restore := "OFF"
		if previous == 1 {
			restore = "ON"
		}
		_, _ = s.db.ExecContext(context.Background(), `PRAGMA secure_delete = `+restore)
	}()
	var flag int
	if err := s.db.QueryRowContext(ctx, `PRAGMA secure_delete`).Scan(&flag); err != nil {
		return result, AsError(err)
	}
	result.SecureDelete = flag == 1
	if flag != 1 {
		return result, fmt.Errorf("secure_delete is not on")
	}
	now := stamp(s.clock())
	n, err := s.purgeRetiredPasswords(ctx, now)
	if err != nil {
		return result, err
	}
	result.Passwords = n
	rows, err := s.db.QueryContext(ctx, `
		SELECT id FROM households
		WHERE purge_after IS NOT NULL AND purge_after <= ?
		ORDER BY id`, now)
	if err != nil {
		return result, AsError(err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return result, AsError(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return result, AsError(err)
	}
	rows.Close()
	tables, err := tablesWithHouseholdID(ctx, s.db)
	if err != nil {
		return result, err
	}
	for _, id := range ids {
		batches, largest, err := s.purgeHousehold(ctx, id, tables, now)
		if err != nil {
			return result, err
		}
		result.Households++
		result.Batches += batches
		if largest > result.Largest {
			result.Largest = largest
		}
	}
	if result.Households == 0 && result.Passwords == 0 {
		return result, nil
	}
	// Checkpoint, then rewrite the file. secure_delete zeros a page when it
	// is freed, but a freelist trunk keeps the rest of that page. Those
	// leftover bytes are household data. VACUUM writes a new database image
	// that does not include them, and the old image is truncated away.
	if _, err := s.db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		return result, AsError(err)
	}
	if _, err := s.db.ExecContext(ctx, `VACUUM`); err != nil {
		return result, AsError(err)
	}
	return result, nil
}

// RunPurge runs PurgeDue once, then again on every tick until ctx is cancelled.
// Production uses a 24 hour ticker. The log records the failure, not household data.
func (s *Service) RunPurge(ctx context.Context, every time.Duration) {
	if s == nil {
		return
	}
	if every <= 0 {
		every = 24 * time.Hour
	}
	run := func() {
		if ctx.Err() != nil {
			return
		}
		if _, err := s.PurgeDue(ctx); err != nil && ctx.Err() == nil {
			log.Printf("purge: %s", err.Error())
		}
	}
	run()
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}

func (s *Service) purgeRetiredPasswords(ctx context.Context, now string) (int, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT household_id FROM household_password
		WHERE state = 'off' AND restore_until IS NOT NULL AND restore_until <= ? AND household_id != ''
		ORDER BY household_id`, now)
	if err != nil {
		return 0, AsError(err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, AsError(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, AsError(err)
	}
	rows.Close()
	n := 0
	for _, id := range ids {
		res, err := s.db.ExecContext(ctx, `
			UPDATE household_password
			SET pw_hash = NULL, session_key = NULL, restore_until = NULL
			WHERE household_id = ? AND state = 'off' AND restore_until IS NOT NULL AND restore_until <= ?`,
			id, now)
		if err != nil {
			return n, AsError(err)
		}
		affected, _ := res.RowsAffected()
		if affected == 0 {
			continue
		}
		if err := writeAudit(ctx, s.db, id, "system", "purge", "household_password.purged", "household", id, now); err != nil {
			return n, AsError(err)
		}
		n++
	}
	return n, nil
}

func (s *Service) purgeHousehold(ctx context.Context, householdID string, tables []string, now string) (batches, largest int, err error) {
	for _, table := range tables {
		b, large, err := deleteHouseholdTable(ctx, s.db, table, householdID)
		if err != nil {
			return batches, largest, err
		}
		batches += b
		if large > largest {
			largest = large
		}
	}
	if _, err := s.db.ExecContext(ctx, `
		UPDATE sessions SET active_household_id = '' WHERE active_household_id = ?`, householdID); err != nil {
		return batches, largest, AsError(err)
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM households WHERE id = ?`, householdID); err != nil {
		return batches, largest, AsError(err)
	}
	// The audit row is written after the household's own audit rows are gone.
	// It carries the id and nothing else.
	if err := writeAudit(ctx, s.db, householdID, "system", "purge", "household.purged", "household", householdID, now); err != nil {
		return batches, largest, AsError(err)
	}
	return batches, largest, nil
}

func deleteHouseholdTable(ctx context.Context, db *sql.DB, table, householdID string) (batches, largest int, err error) {
	if !safeIdent(table) {
		return 0, 0, fmt.Errorf("refusing to purge table %q", table)
	}
	query := fmt.Sprintf(
		`DELETE FROM %s WHERE rowid IN (SELECT rowid FROM %s WHERE household_id = ? LIMIT %d)`,
		table, table, purgeBatch,
	)
	for {
		res, err := db.ExecContext(ctx, query, householdID)
		if err != nil {
			return batches, largest, AsError(err)
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return batches, largest, nil
		}
		batches++
		if int(n) > largest {
			largest = int(n)
		}
		if n < purgeBatch {
			return batches, largest, nil
		}
	}
}

func tablesWithHouseholdID(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		return nil, AsError(err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, AsError(err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		return nil, AsError(err)
	}
	var out []string
	for _, name := range names {
		if !safeIdent(name) {
			continue
		}
		info, err := db.QueryContext(ctx, fmt.Sprintf(`PRAGMA table_info(%s)`, name))
		if err != nil {
			return nil, AsError(err)
		}
		has := false
		for info.Next() {
			var cid int
			var col, ctype string
			var notnull, pk int
			var dflt sql.NullString
			if err := info.Scan(&cid, &col, &ctype, &notnull, &dflt, &pk); err != nil {
				info.Close()
				return nil, AsError(err)
			}
			if col == "household_id" {
				has = true
			}
		}
		err = info.Err()
		info.Close()
		if err != nil {
			return nil, AsError(err)
		}
		if has {
			out = append(out, name)
		}
	}
	return out, nil
}

func safeIdent(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if r != '_' && !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}
