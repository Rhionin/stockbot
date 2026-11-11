package cart

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func insertSendResultRow(t *testing.T, db *sql.DB, household, itemID, attemptedAt string) {
	t.Helper()
	_, err := db.Exec(`
		INSERT INTO cart_line_results (household_id, provider_id, item_id, status, quantity, attempted_at)
		VALUES (?, 'fake-none', ?, 'sent', 1, ?)
		ON CONFLICT(household_id, provider_id, item_id) DO UPDATE SET attempted_at = excluded.attempted_at`,
		household, itemID, attemptedAt)
	require.NoError(t, err)
}

func TestSendResultRetentionBoundary(t *testing.T) {
	env := newProvisionEnv(t)
	now := time.Date(2026, 11, 11, 14, 0, 0, 0, time.UTC)
	rec := NewResultRecorder(env.db)
	rec.SetClock(func() time.Time { return now })

	exactly := now.Add(-SendResultRetention).Format(time.RFC3339)
	expired := now.Add(-SendResultRetention - time.Second).Format(time.RFC3339)
	inside := now.Add(-89 * 24 * time.Hour).Format(time.RFC3339)

	h := env.scope.HouseholdID()
	insertSendResultRow(t, env.db, h, "item-exact", exactly)
	insertSendResultRow(t, env.db, h, "item-expired", expired)
	insertSendResultRow(t, env.db, h, "item-inside", inside)

	cutoff := now.Add(-SendResultRetention)
	got, err := rec.LatestResults(context.Background(), env.scope, "fake-none", cutoff)
	require.NoError(t, err)
	assert.Contains(t, got, "item-exact")
	assert.NotContains(t, got, "item-expired")
	assert.Contains(t, got, "item-inside")

	result, err := rec.SweepExpiredSendResults(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, result.Deleted)
	assert.Equal(t, 1, result.Batches)

	var gone string
	err = env.db.QueryRow(`SELECT item_id FROM cart_line_results WHERE household_id = ? AND item_id = ?`, h, "item-expired").Scan(&gone)
	assert.ErrorIs(t, err, sql.ErrNoRows)

	var kept int
	require.NoError(t, env.db.QueryRow(`SELECT COUNT(*) FROM cart_line_results WHERE household_id = ?`, h).Scan(&kept))
	assert.Equal(t, 2, kept)
}

func TestSendResultRetentionPruneOnWrite(t *testing.T) {
	env := newProvisionEnv(t)
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	rec := NewResultRecorder(env.db)
	rec.SetClock(func() time.Time { return now })

	cedar := cedarScope(t, env.db)
	expired := now.Add(-SendResultRetention - time.Hour).Format(time.RFC3339)
	insertSendResultRow(t, env.db, env.scope.HouseholdID(), "mine-old", expired)
	insertSendResultRow(t, env.db, cedar.HouseholdID(), "other-old", expired)

	require.NoError(t, rec.Record(context.Background(), env.scope, "fake-none", ConfirmPerRequest, []EntryOutcome{
		{ItemID: "mine-new", EntryID: "e1", Quantity: 1, Outcome: OutcomeConfirmed},
	}))

	var mineCount, otherCount int
	require.NoError(t, env.db.QueryRow(`SELECT COUNT(*) FROM cart_line_results WHERE household_id = ?`, env.scope.HouseholdID()).Scan(&mineCount))
	require.NoError(t, env.db.QueryRow(`SELECT COUNT(*) FROM cart_line_results WHERE household_id = ?`, cedar.HouseholdID()).Scan(&otherCount))
	assert.Equal(t, 1, mineCount)
	assert.Equal(t, 1, otherCount)
}

func TestSendResultRetentionSweepBatching(t *testing.T) {
	env := newProvisionEnv(t)
	now := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	rec := NewResultRecorder(env.db)
	rec.SetClock(func() time.Time { return now })

	expired := now.Add(-SendResultRetention - time.Minute).Format(time.RFC3339)
	h := env.scope.HouseholdID()
	for i := 0; i < 1201; i++ {
		insertSendResultRow(t, env.db, h, fmt.Sprintf("item-%04d", i), expired)
	}
	insertSendResultRow(t, env.db, h, "item-fresh", now.Add(-time.Hour).Format(time.RFC3339))

	result, err := rec.SweepExpiredSendResults(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1201, result.Deleted)
	assert.Equal(t, 3, result.Batches)
	assert.Equal(t, sendResultRetentionBatch, result.Largest)

	var left int
	require.NoError(t, env.db.QueryRow(`SELECT COUNT(*) FROM cart_line_results WHERE household_id = ?`, h).Scan(&left))
	assert.Equal(t, 1, left)
}

func TestSendResultRetentionSweepMultipleHouseholds(t *testing.T) {
	env := newProvisionEnv(t)
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	rec := NewResultRecorder(env.db)
	rec.SetClock(func() time.Time { return now })

	cedar := cedarScope(t, env.db)
	expired := now.Add(-SendResultRetention - time.Second).Format(time.RFC3339)
	fresh := now.Add(-24 * time.Hour).Format(time.RFC3339)
	insertSendResultRow(t, env.db, env.scope.HouseholdID(), "a-old", expired)
	insertSendResultRow(t, env.db, env.scope.HouseholdID(), "a-new", fresh)
	insertSendResultRow(t, env.db, cedar.HouseholdID(), "b-old", expired)
	insertSendResultRow(t, env.db, cedar.HouseholdID(), "b-new", fresh)

	result, err := rec.SweepExpiredSendResults(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 2, result.Deleted)

	assert.Equal(t, 1, countCartLineResults(t, env.db, env.scope.HouseholdID()))
	assert.Equal(t, 1, countCartLineResults(t, env.db, cedar.HouseholdID()))
}

func TestSendResultRetentionSweepEmptyTable(t *testing.T) {
	env := newProvisionEnv(t)
	rec := NewResultRecorder(env.db)
	rec.SetClock(func() time.Time { return time.Now().UTC() })

	result, err := rec.SweepExpiredSendResults(context.Background())
	require.NoError(t, err)
	assert.Zero(t, result.Deleted)
	assert.Zero(t, result.Batches)
}

func TestSendResultRetentionSweepIdempotent(t *testing.T) {
	env := newProvisionEnv(t)
	now := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	rec := NewResultRecorder(env.db)
	rec.SetClock(func() time.Time { return now })

	expired := now.Add(-SendResultRetention - time.Second).Format(time.RFC3339)
	insertSendResultRow(t, env.db, env.scope.HouseholdID(), "gone", expired)

	ctx := context.Background()
	first, err := rec.SweepExpiredSendResults(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, first.Deleted)

	second, err := rec.SweepExpiredSendResults(ctx)
	require.NoError(t, err)
	assert.Zero(t, second.Deleted)
	assert.Zero(t, second.Batches)
}

func TestSendResultRetentionSweepDoesNotTouchOtherTables(t *testing.T) {
	env := newProvisionEnv(t)
	now := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	rec := NewResultRecorder(env.db)
	rec.SetClock(func() time.Time { return now })

	expired := now.Add(-SendResultRetention - time.Second).Format(time.RFC3339)
	insertSendResultRow(t, env.db, env.scope.HouseholdID(), "sweep-me", expired)

	var itemsBefore int
	require.NoError(t, env.db.QueryRow(`SELECT COUNT(*) FROM items WHERE household_id = ?`, env.scope.HouseholdID()).Scan(&itemsBefore))

	_, err := rec.SweepExpiredSendResults(context.Background())
	require.NoError(t, err)

	var itemsAfter int
	require.NoError(t, env.db.QueryRow(`SELECT COUNT(*) FROM items WHERE household_id = ?`, env.scope.HouseholdID()).Scan(&itemsAfter))
	assert.Equal(t, itemsBefore, itemsAfter)
}

func TestSendResultRetentionSweepError(t *testing.T) {
	env := newProvisionEnv(t)
	now := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	rec := NewResultRecorder(env.db)
	rec.SetClock(func() time.Time { return now })

	_, err := env.db.Exec(`CREATE TRIGGER block_send_result_delete BEFORE DELETE ON cart_line_results BEGIN SELECT RAISE(ABORT, 'blocked'); END`)
	require.NoError(t, err)
	insertSendResultRow(t, env.db, env.scope.HouseholdID(), "stuck", now.Add(-SendResultRetention-time.Hour).Format(time.RFC3339))

	_, err = rec.SweepExpiredSendResults(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "blocked")
}

func TestRunSendResultRetentionStopsWhenCancelled(t *testing.T) {
	env := newProvisionEnv(t)
	rec := NewResultRecorder(env.db)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		rec.RunSendResultRetention(ctx, time.Hour)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("retention loop did not stop")
	}
}

func TestRunSendResultRetentionNilRecorder(t *testing.T) {
	var rec *ResultRecorder
	done := make(chan struct{})
	go func() {
		rec.RunSendResultRetention(context.Background(), time.Hour)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("nil recorder did not return")
	}
}

func TestSweepExpiredSendResultsNilRecorder(t *testing.T) {
	var rec *ResultRecorder
	result, err := rec.SweepExpiredSendResults(context.Background())
	require.NoError(t, err)
	assert.Zero(t, result.Deleted)
}
