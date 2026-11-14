package cart

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"io"
	"log"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mutexLogBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (m *mutexLogBuffer) Write(p []byte) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.buf.Write(p)
}

func (m *mutexLogBuffer) String() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.buf.String()
}

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

func TestSweepExpiredSendResultsCancelledContext(t *testing.T) {
	env := newProvisionEnv(t)
	now := time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)
	rec := NewResultRecorder(env.db)
	rec.SetClock(func() time.Time { return now })
	expired := now.Add(-SendResultRetention - time.Minute).Format(time.RFC3339)
	insertSendResultRow(t, env.db, env.scope.HouseholdID(), "cancel-me", expired)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err := rec.SweepExpiredSendResults(ctx)
	assert.ErrorIs(t, err, context.Canceled)
	assert.Zero(t, result.Deleted)

	var still int
	require.NoError(t, env.db.QueryRow(`SELECT COUNT(*) FROM cart_line_results WHERE household_id = ? AND item_id = ?`,
		env.scope.HouseholdID(), "cancel-me").Scan(&still))
	assert.Equal(t, 1, still)
}

func TestRunSendResultRetentionTicksDeleteExpiredRows(t *testing.T) {
	env := newProvisionEnv(t)
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	rec := NewResultRecorder(env.db)
	rec.SetClock(func() time.Time { return now })

	expired := now.Add(-SendResultRetention - time.Hour).Format(time.RFC3339)
	fresh := now.Add(-time.Hour).Format(time.RFC3339)
	h := env.scope.HouseholdID()
	insertSendResultRow(t, env.db, h, "startup-expired", expired)
	insertSendResultRow(t, env.db, h, "stays-fresh", fresh)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		rec.RunSendResultRetention(ctx, 15*time.Millisecond)
		close(done)
	}()

	require.Eventually(t, func() bool {
		var n int
		if err := env.db.QueryRow(`SELECT COUNT(*) FROM cart_line_results WHERE household_id = ? AND item_id = ?`,
			h, "startup-expired").Scan(&n); err != nil {
			return false
		}
		return n == 0
	}, time.Second, 10*time.Millisecond, "startup sweep should delete startup-expired before tick test continues")

	insertSendResultRow(t, env.db, h, "late-expired", expired)

	require.Eventually(t, func() bool {
		var late, kept int
		if err := env.db.QueryRow(`SELECT COUNT(*) FROM cart_line_results WHERE household_id = ? AND item_id = ?`,
			h, "late-expired").Scan(&late); err != nil {
			return false
		}
		if err := env.db.QueryRow(`SELECT COUNT(*) FROM cart_line_results WHERE household_id = ? AND item_id = ?`,
			h, "stays-fresh").Scan(&kept); err != nil {
			return false
		}
		return late == 0 && kept == 1
	}, time.Second, 10*time.Millisecond)

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("retention loop did not stop after tick sweep")
	}
}

func TestRunSendResultRetentionLogsSweepErrorWithoutPanic(t *testing.T) {
	env := newProvisionEnv(t)
	now := time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC)
	rec := NewResultRecorder(env.db)
	rec.SetClock(func() time.Time { return now })

	_, err := env.db.Exec(`CREATE TRIGGER block_send_result_delete BEFORE DELETE ON cart_line_results BEGIN SELECT RAISE(ABORT, 'blocked'); END`)
	require.NoError(t, err)
	insertSendResultRow(t, env.db, env.scope.HouseholdID(), "stuck-loop", now.Add(-SendResultRetention-time.Hour).Format(time.RFC3339))

	_, sweepErr := rec.SweepExpiredSendResults(context.Background())
	require.Error(t, sweepErr)
	wantLog := fmt.Sprintf("send-result retention: %s", sweepErr.Error())

	var logs mutexLogBuffer
	previous := log.Writer()
	log.SetOutput(io.Writer(&logs))
	t.Cleanup(func() { log.SetOutput(previous) })

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		rec.RunSendResultRetention(ctx, time.Hour)
		close(done)
	}()

	require.Eventually(t, func() bool {
		return strings.Contains(logs.String(), wantLog)
	}, 2*time.Second, 5*time.Millisecond, "startup sweep should log the blocked delete error before cancellation")

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("retention loop did not exit")
	}

	assert.Contains(t, logs.String(), wantLog)

	var still int
	require.NoError(t, env.db.QueryRow(`SELECT COUNT(*) FROM cart_line_results WHERE household_id = ? AND item_id = ?`,
		env.scope.HouseholdID(), "stuck-loop").Scan(&still))
	assert.Equal(t, 1, still)
}

func TestRunSendResultRetentionZeroEveryDoesNotPanic(t *testing.T) {
	env := newProvisionEnv(t)
	rec := NewResultRecorder(env.db)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan struct{})
	go func() {
		rec.RunSendResultRetention(ctx, 0)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("zero interval retention loop did not exit")
	}
}
