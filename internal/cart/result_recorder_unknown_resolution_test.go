package cart

import (
	"context"
	"database/sql"
	"testing"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveUnknownLine(t *testing.T) {
	t.Parallel()
	db, err := app.OpenSQLite(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	require.NoError(t, app.RunMigrations(db))
	scope, err := auth.PrimaryScope(context.Background(), db)
	require.NoError(t, err)
	rec := NewResultRecorder(db)
	household := scope.HouseholdID()
	provider := "fake-none"
	itemID := "item-tea"
	entryID := "sli-tea"
	attemptedAt := "2026-06-01T12:00:00Z"

	insertUnknown := func(entry string) {
		t.Helper()
		_, err := db.Exec(`
			INSERT INTO cart_line_results (household_id, provider_id, item_id, entry_id, status, reason, quantity, attempted_at)
			VALUES (?, ?, ?, ?, 'unknown', 'request_incomplete', 2, ?)`,
			household, provider, itemID, entry, attemptedAt)
		require.NoError(t, err)
	}

	t.Run("reached_true_sent_keeps_attempted_at", func(t *testing.T) {
		insertUnknown(entryID)
		require.NoError(t, rec.ResolveUnknownLine(context.Background(), scope, ProviderID(provider), itemID, entryID, true))
		status, reason, qty, at, ok := readCartLineResult(t, db, household, provider, itemID)
		require.True(t, ok)
		assert.Equal(t, "sent", status)
		assert.False(t, reason.Valid)
		assert.Equal(t, 2, qty)
		assert.Equal(t, attemptedAt, at)
	})

	t.Run("reached_false_failed", func(t *testing.T) {
		_, err := db.Exec(`DELETE FROM cart_line_results WHERE household_id = ? AND item_id = ?`, household, itemID)
		require.NoError(t, err)
		insertUnknown(entryID)
		require.NoError(t, rec.ResolveUnknownLine(context.Background(), scope, ProviderID(provider), itemID, entryID, false))
		status, reason, _, at, ok := readCartLineResult(t, db, household, provider, itemID)
		require.True(t, ok)
		assert.Equal(t, "failed", status)
		assert.True(t, reason.Valid)
		assert.Equal(t, "request_incomplete", reason.String)
		assert.Equal(t, attemptedAt, at)
	})

	t.Run("no_row_is_ok", func(t *testing.T) {
		require.NoError(t, rec.ResolveUnknownLine(context.Background(), scope, ProviderID(provider), "item-missing", entryID, true))
	})

	t.Run("non_unknown_unchanged", func(t *testing.T) {
		_, err := db.Exec(`
			INSERT INTO cart_line_results (household_id, provider_id, item_id, entry_id, status, reason, quantity, attempted_at)
			VALUES (?, ?, 'item-sent', ?, 'sent', NULL, 1, ?)
			ON CONFLICT(household_id, provider_id, item_id) DO UPDATE SET
				status = excluded.status, reason = excluded.reason, attempted_at = excluded.attempted_at`,
			household, provider, entryID, attemptedAt)
		require.NoError(t, err)
		require.NoError(t, rec.ResolveUnknownLine(context.Background(), scope, ProviderID(provider), "item-sent", entryID, true))
		status, _, _, _, ok := readCartLineResult(t, db, household, provider, "item-sent")
		require.True(t, ok)
		assert.Equal(t, "sent", status)
	})

	t.Run("sent_row_not_downgraded_when_not_reached", func(t *testing.T) {
		_, err := db.Exec(`
			INSERT INTO cart_line_results (household_id, provider_id, item_id, entry_id, status, reason, quantity, attempted_at)
			VALUES (?, ?, 'item-keep-sent', ?, 'sent', NULL, 2, ?)
			ON CONFLICT(household_id, provider_id, item_id) DO UPDATE SET
				status = excluded.status, reason = excluded.reason, attempted_at = excluded.attempted_at`,
			household, provider, entryID, attemptedAt)
		require.NoError(t, err)
		require.NoError(t, rec.ResolveUnknownLine(context.Background(), scope, ProviderID(provider), "item-keep-sent", entryID, false))
		status, reason, _, _, ok := readCartLineResult(t, db, household, provider, "item-keep-sent")
		require.True(t, ok)
		assert.Equal(t, "sent", status)
		assert.False(t, reason.Valid)
	})

	t.Run("failed_row_unchanged_when_reached", func(t *testing.T) {
		_, err := db.Exec(`
			INSERT INTO cart_line_results (household_id, provider_id, item_id, entry_id, status, reason, quantity, attempted_at)
			VALUES (?, ?, 'item-keep-failed', ?, 'failed', 'batch_rejected', 1, ?)
			ON CONFLICT(household_id, provider_id, item_id) DO UPDATE SET
				status = excluded.status, reason = excluded.reason, attempted_at = excluded.attempted_at`,
			household, provider, entryID, attemptedAt)
		require.NoError(t, err)
		require.NoError(t, rec.ResolveUnknownLine(context.Background(), scope, ProviderID(provider), "item-keep-failed", entryID, true))
		status, reason, _, _, ok := readCartLineResult(t, db, household, provider, "item-keep-failed")
		require.True(t, ok)
		assert.Equal(t, "failed", status)
		require.True(t, reason.Valid)
		assert.Equal(t, "batch_rejected", reason.String)
	})

	t.Run("newer_attempt_entry_mismatch_no_clobber", func(t *testing.T) {
		_, err := db.Exec(`DELETE FROM cart_line_results WHERE household_id = ? AND item_id = ?`, household, itemID)
		require.NoError(t, err)
		newerAt := "2026-06-02T12:00:00Z"
		_, err = db.Exec(`
			INSERT INTO cart_line_results (household_id, provider_id, item_id, entry_id, status, reason, quantity, attempted_at)
			VALUES (?, ?, ?, 'sli-new', 'unknown', 'request_incomplete', 1, ?)`,
			household, provider, itemID, newerAt)
		require.NoError(t, err)
		require.NoError(t, rec.ResolveUnknownLine(context.Background(), scope, ProviderID(provider), itemID, entryID, true))
		status, _, _, at, ok := readCartLineResult(t, db, household, provider, itemID)
		require.True(t, ok)
		assert.Equal(t, "unknown", status)
		assert.Equal(t, newerAt, at)
	})

	t.Run("other_household_untouched", func(t *testing.T) {
		other := cedarScope(t, db)
		_, err := db.Exec(`
			INSERT INTO cart_line_results (household_id, provider_id, item_id, status, reason, quantity, attempted_at)
			VALUES (?, ?, 'item-cedar', 'unknown', 'request_incomplete', 1, ?)`,
			other.HouseholdID(), provider, attemptedAt)
		require.NoError(t, err)
		require.NoError(t, rec.ResolveUnknownLine(context.Background(), scope, ProviderID(provider), "item-cedar", entryID, true))
		status, _, _, _, ok := readCartLineResult(t, db, other.HouseholdID(), provider, "item-cedar")
		require.True(t, ok)
		assert.Equal(t, "unknown", status)
	})

	t.Run("ledger_tx_rolls_back_when_resolve_fails", func(t *testing.T) {
		_, err := db.Exec(`DELETE FROM cart_line_results WHERE household_id = ?`, household)
		require.NoError(t, err)
		_, err = db.Exec(`CREATE TRIGGER block_resolve_update BEFORE UPDATE ON cart_line_results
			WHEN NEW.status = 'sent' BEGIN SELECT RAISE(ABORT, 'blocked'); END`)
		require.NoError(t, err)
		t.Cleanup(func() {
			_, _ = db.Exec(`DROP TRIGGER IF EXISTS block_resolve_update`)
		})
		insertUnknown(entryID)
		ledger := NewLedger(db)
		err = ledger.AdvanceWithTx(context.Background(), scope, ProviderID(provider), itemID, 1, func(tx *sql.Tx) error {
			_, err := rec.ResolveUnknownLineTx(context.Background(), scope, tx, ProviderID(provider), itemID, entryID, true)
			return err
		})
		require.Error(t, err)
		status, _, _, _, ok := readCartLineResult(t, db, household, provider, itemID)
		require.True(t, ok)
		assert.Equal(t, "unknown", status)
		var requested int
		err = db.QueryRow(
			`SELECT requested_quantity FROM fulfillment_ledger WHERE household_id = ? AND provider_id = ? AND item_id = ?`,
			household, provider, itemID).Scan(&requested)
		assert.ErrorIs(t, err, sql.ErrNoRows)
	})
}
