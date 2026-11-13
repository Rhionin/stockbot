package cart

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/Rhionin/stockbot/internal/cart/connection"
	"github.com/Rhionin/stockbot/internal/supply"
	"github.com/Rhionin/stockbot/internal/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const cedarHouseholdID = "0190bbbb-cccc-7000-8000-0000000000e5"

func cedarScope(t *testing.T, db *sql.DB) tenant.Scope {
	t.Helper()
	_, err := db.Exec(`
		INSERT INTO households (id, name, created_at, channel_key, is_primary)
		VALUES (?, 'Household Cedar', '2026-02-02T00:00:00Z', 'abcdef0123456789abcdef0123456789', 0)`,
		cedarHouseholdID)
	require.NoError(t, err)
	scope, err := tenant.New(cedarHouseholdID)
	require.NoError(t, err)
	return scope
}

func readCartLineResult(t *testing.T, db *sql.DB, household, provider, itemID string) (status string, reason sql.NullString, qty int, attemptedAt string, ok bool) {
	t.Helper()
	err := db.QueryRow(
		`SELECT status, reason, quantity, attempted_at FROM cart_line_results
		 WHERE household_id = ? AND provider_id = ? AND item_id = ?`,
		household, provider, itemID).Scan(&status, &reason, &qty, &attemptedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return "", sql.NullString{}, 0, "", false
	}
	require.NoError(t, err)
	return status, reason, qty, attemptedAt, true
}

func assertAttemptedAtRFC3339UTC(t *testing.T, attemptedAt string) {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, attemptedAt)
	require.NoError(t, err, "attemptedAt %q", attemptedAt)
	assert.Equal(t, time.UTC, parsed.Location())
}

func latestSendForItem(t *testing.T, rec *ResultRecorder, scope tenant.Scope, provider ProviderID, itemID string) LastSend {
	t.Helper()
	cutoff := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	got, err := rec.LatestResults(context.Background(), scope, provider, cutoff)
	require.NoError(t, err)
	row, ok := got[itemID]
	require.True(t, ok, "item %q", itemID)
	return row.LastSend
}

func countCartLineResults(t *testing.T, db *sql.DB, household string) int {
	t.Helper()
	var n int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM cart_line_results WHERE household_id = ?`, household).Scan(&n))
	return n
}

func TestSendResultsRecorded(t *testing.T) {
	fixed := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

	t.Run("ok_sent", func(t *testing.T) {
		env := newProvisionEnv(t)
		rec := NewResultRecorder(env.db)
		rec.SetClock(func() time.Time { return fixed })
		env.engine.SetResultRecorder(rec)

		caps := derivedNoneCaps(ConfirmPerRequest)
		provider := newFakeProvider("fake-none", caps, &fakeScript{
			dispositions:  []ResultDisposition{DispositionAccepted},
			identityByBar: map[string]ProductIdentity{"0001": "SKU-1"},
		})
		require.NoError(t, env.registry.Register(provider))
		itemID, _ := env.seedShortfall(t, "prod-1", "Milk", "0001", 2)

		report, err := env.engine.Provision(context.Background(), env.scope, "fake-none")
		require.NoError(t, err)
		require.Len(t, report.Entries, 1)

		status, reason, qty, at, ok := readCartLineResult(t, env.db, env.scope.HouseholdID(), "fake-none", itemID)
		require.True(t, ok)
		assert.Equal(t, "sent", status)
		assert.False(t, reason.Valid)
		assert.Equal(t, 2, qty)
		assert.Equal(t, fixed.Format(time.RFC3339), at)
		assertAttemptedAtRFC3339UTC(t, at)

		ls := latestSendForItem(t, rec, env.scope, "fake-none", itemID)
		assert.Equal(t, PublicStatusSent, ls.Status)
		assert.Empty(t, ls.Reason)
		assert.Equal(t, fixed.Format(time.RFC3339), ls.AttemptedAt)
		assertAttemptedAtRFC3339UTC(t, ls.AttemptedAt)

		ledger, err := env.ledger.ListForProvider(context.Background(), env.scope, "fake-none")
		require.NoError(t, err)
		assert.Equal(t, 2, ledger[itemID].Requested)
	})

	t.Run("skipped_no_match", func(t *testing.T) {
		env := newProvisionEnv(t)
		env.engine.SetResultRecorder(NewResultRecorder(env.db))
		caps := derivedNoneCaps(ConfirmPerRequest)
		provider := newFakeProvider("fake-none", caps, &fakeScript{identityByBar: map[string]ProductIdentity{}})
		require.NoError(t, env.registry.Register(provider))
		itemID, _ := env.seedShortfall(t, "prod-1", "Milk", "0001", 2)

		_, err := env.engine.Provision(context.Background(), env.scope, "fake-none")
		require.NoError(t, err)

		status, reason, _, _, ok := readCartLineResult(t, env.db, env.scope.HouseholdID(), "fake-none", itemID)
		require.True(t, ok)
		assert.Equal(t, "skipped", status)
		assert.Equal(t, "no_match", reason.String)

		ls := latestSendForItem(t, NewResultRecorder(env.db), env.scope, "fake-none", itemID)
		assert.Equal(t, PublicReasonNoMatch, ls.Reason)
	})

	t.Run("batch_rejected", func(t *testing.T) {
		env := newProvisionEnv(t)
		env.engine.SetResultRecorder(NewResultRecorder(env.db))
		caps := derivedNoneCaps(ConfirmPerRequest)
		provider := newFakeProvider("fake-none", caps, &fakeScript{
			dispositions:  []ResultDisposition{DispositionRejected},
			identityByBar: map[string]ProductIdentity{"0001": "SKU-1"},
		})
		require.NoError(t, env.registry.Register(provider))
		itemID, _ := env.seedShortfall(t, "prod-1", "Milk", "0001", 2)

		_, err := env.engine.Provision(context.Background(), env.scope, "fake-none")
		require.NoError(t, err)

		status, reason, _, _, ok := readCartLineResult(t, env.db, env.scope.HouseholdID(), "fake-none", itemID)
		require.True(t, ok)
		assert.Equal(t, "failed", status)
		assert.Equal(t, "batch_rejected", reason.String)

		ledger, err := env.ledger.ListForProvider(context.Background(), env.scope, "fake-none")
		require.NoError(t, err)
		assert.Equal(t, 0, ledger[itemID].Requested)
	})

	t.Run("invalid_line", func(t *testing.T) {
		env := newProvisionEnv(t)
		env.engine.SetResultRecorder(NewResultRecorder(env.db))
		caps := derivedNoneCaps(ConfirmPerRequest)
		provider := newFakeProvider("fake-none", caps, &fakeScript{
			dispositions:  []ResultDisposition{DispositionRejected},
			addErrors:     []error{errors.New("invalid line")},
			identityByBar: map[string]ProductIdentity{"0001": "SKU-1"},
		})
		require.NoError(t, env.registry.Register(provider))
		itemID, _ := env.seedShortfall(t, "prod-1", "Milk", "0001", 1)

		_, err := env.engine.Provision(context.Background(), env.scope, "fake-none")
		require.NoError(t, err)

		status, reason, _, _, ok := readCartLineResult(t, env.db, env.scope.HouseholdID(), "fake-none", itemID)
		require.True(t, ok)
		assert.Equal(t, "failed", status)
		assert.Equal(t, "invalid_line", reason.String)
	})

	t.Run("token_store_sign_in", func(t *testing.T) {
		env := newProvisionEnv(t)
		env.engine.SetResultRecorder(NewResultRecorder(env.db))
		caps := derivedNoneCaps(ConfirmPerRequest)
		caps.Auth = AuthOAuth2
		script := &fakeScript{
			refreshErr:    &OAuthTokenError{StatusCode: 503, OAuthError: "server_error"},
			identityByBar: map[string]ProductIdentity{"0001": "SKU-1"},
		}
		provider := newFakeProvider("fake-oauth", caps, script)
		require.NoError(t, env.registry.Register(provider))
		itemID, _ := env.seedShortfall(t, "prod-1", "Milk", "0001", 2)
		expired := time.Now().UTC().Add(-time.Hour)
		require.NoError(t, env.directory.Write(context.Background(), env.scope, &connection.Connection{
			Provider:     "fake-oauth",
			State:        connection.StateConnected,
			AccessToken:  "stale",
			RefreshToken: "refresh-1",
			ExpiresAt:    &expired,
		}))

		_, err := env.engine.Provision(context.Background(), env.scope, "fake-oauth")
		var storeAuth *ErrStoreAuth
		require.ErrorAs(t, err, &storeAuth)
		assert.Equal(t, 0, script.addCalls)

		status, reason, _, _, ok := readCartLineResult(t, env.db, env.scope.HouseholdID(), "fake-oauth", itemID)
		require.True(t, ok)
		assert.Equal(t, "failed", status)
		assert.Equal(t, "store_sign_in", reason.String)

		ledger, err := env.ledger.ListForProvider(context.Background(), env.scope, "fake-oauth")
		require.NoError(t, err)
		assert.Equal(t, 0, ledger[itemID].Requested)
	})

	t.Run("409_token_reconnect_writes_nothing", func(t *testing.T) {
		env := newProvisionEnv(t)
		env.engine.SetResultRecorder(NewResultRecorder(env.db))
		caps := derivedNoneCaps(ConfirmPerRequest)
		caps.Auth = AuthOAuth2
		script := &fakeScript{
			refreshErr:    &OAuthTokenError{StatusCode: 400, OAuthError: "invalid_grant"},
			identityByBar: map[string]ProductIdentity{"0002": "SKU-2"},
		}
		provider := newFakeProvider("fake-oauth", caps, script)
		require.NoError(t, env.registry.Register(provider))
		unresolvedID, resolvedID := seedSkippedAndResolvablePair(t, env)
		expired := time.Now().UTC().Add(-time.Hour)
		require.NoError(t, env.directory.Write(context.Background(), env.scope, &connection.Connection{
			Provider:     "fake-oauth",
			State:        connection.StateConnected,
			AccessToken:  "stale",
			RefreshToken: "refresh-1",
			ExpiresAt:    &expired,
		}))

		_, err := env.engine.Provision(context.Background(), env.scope, "fake-oauth")
		var reconnect *ErrReconnectRequired
		require.ErrorAs(t, err, &reconnect)
		assert.Equal(t, 0, script.addCalls)
		assert.Equal(t, 0, countCartLineResults(t, env.db, env.scope.HouseholdID()))
		_, _, _, _, ok := readCartLineResult(t, env.db, env.scope.HouseholdID(), "fake-oauth", unresolvedID)
		assert.False(t, ok)
		_, _, _, _, ok = readCartLineResult(t, env.db, env.scope.HouseholdID(), "fake-oauth", resolvedID)
		assert.False(t, ok)
	})

	t.Run("409_first_batch_reconnect_writes_nothing", func(t *testing.T) {
		env := newProvisionEnv(t)
		env.engine.SetResultRecorder(NewResultRecorder(env.db))
		caps := derivedNoneCaps(ConfirmPerRequest)
		caps.Auth = AuthOAuth2
		script := &fakeScript{
			refreshErr:    errors.New("refresh unavailable"),
			addStatuses:   []int{http.StatusUnauthorized, http.StatusUnauthorized},
			addErrors:     []error{errors.New("401 unauthorized"), errors.New("401 unauthorized")},
			identityByBar: map[string]ProductIdentity{"0002": "SKU-2"},
		}
		provider := newFakeProvider("fake-oauth", caps, script)
		require.NoError(t, env.registry.Register(provider))
		unresolvedID, resolvedID := seedSkippedAndResolvablePair(t, env)
		future := time.Now().UTC().Add(2 * time.Hour)
		require.NoError(t, env.directory.Write(context.Background(), env.scope, &connection.Connection{
			Provider:     "fake-oauth",
			State:        connection.StateConnected,
			AccessToken:  "valid",
			RefreshToken: "refresh-1",
			ExpiresAt:    &future,
		}))

		_, err := env.engine.Provision(context.Background(), env.scope, "fake-oauth")
		var reconnect *ErrReconnectRequired
		require.ErrorAs(t, err, &reconnect)
		assert.Equal(t, 1, script.addCalls)
		assert.Equal(t, 0, countCartLineResults(t, env.db, env.scope.HouseholdID()))
		_, _, _, _, ok := readCartLineResult(t, env.db, env.scope.HouseholdID(), "fake-oauth", unresolvedID)
		assert.False(t, ok)
		_, _, _, _, ok = readCartLineResult(t, env.db, env.scope.HouseholdID(), "fake-oauth", resolvedID)
		assert.False(t, ok)
	})

	t.Run("409_not_connected_writes_nothing", func(t *testing.T) {
		env := newProvisionEnv(t)
		env.engine.SetResultRecorder(NewResultRecorder(env.db))
		caps := derivedNoneCaps(ConfirmPerRequest)
		caps.Auth = AuthOAuth2
		provider := newFakeProvider("fake-oauth", caps, &fakeScript{})
		require.NoError(t, env.registry.Register(provider))
		itemID, _ := env.seedShortfall(t, "prod-1", "Milk", "0001", 1)

		_, err := env.engine.Provision(context.Background(), env.scope, "fake-oauth")
		require.Error(t, err)
		_, _, _, _, ok := readCartLineResult(t, env.db, env.scope.HouseholdID(), "fake-oauth", itemID)
		assert.False(t, ok)
	})

	t.Run("ledger_write_failure_unknown", func(t *testing.T) {
		env := newProvisionEnv(t)
		env.engine.SetResultRecorder(NewResultRecorder(env.db))
		_, err := env.db.Exec(`CREATE TRIGGER block_ledger_insert BEFORE INSERT ON fulfillment_ledger BEGIN SELECT RAISE(ABORT, 'blocked'); END`)
		require.NoError(t, err)

		caps := derivedNoneCaps(ConfirmPerRequest)
		provider := newFakeProvider("fake-none", caps, &fakeScript{
			dispositions:  []ResultDisposition{DispositionAccepted},
			identityByBar: map[string]ProductIdentity{"0001": "SKU-1"},
		})
		require.NoError(t, env.registry.Register(provider))
		itemID, _ := env.seedShortfall(t, "prod-1", "Milk", "0001", 2)

		report, err := env.engine.Provision(context.Background(), env.scope, "fake-none")
		require.Error(t, err)
		require.Contains(t, err.Error(), "ledger")

		status, reason, _, _, ok := readCartLineResult(t, env.db, env.scope.HouseholdID(), "fake-none", itemID)
		require.True(t, ok)
		assert.Equal(t, "unknown", status)
		assert.Equal(t, "request_incomplete", reason.String)
		assert.Equal(t, 0, report.Confirmed)

		ledger, err := env.ledger.ListForProvider(context.Background(), env.scope, "fake-none")
		require.NoError(t, err)
		assert.Equal(t, 0, ledger[itemID].Requested)
	})

	t.Run("duplicate_collapses_worst", func(t *testing.T) {
		env := newProvisionEnv(t)
		rec := NewResultRecorder(env.db)
		env.engine.SetResultRecorder(rec)
		itemID := "item-dup"
		require.NoError(t, rec.Record(context.Background(), env.scope, "fake-none", ConfirmPerRequest, []EntryOutcome{
			{ItemID: itemID, EntryID: "e1", Quantity: 1, Outcome: OutcomeConfirmed},
			{ItemID: itemID, EntryID: "e2", Quantity: 2, Outcome: OutcomeFailed, Reason: ReasonBatchRejected},
		}))
		status, reason, _, _, ok := readCartLineResult(t, env.db, env.scope.HouseholdID(), "fake-none", itemID)
		require.True(t, ok)
		assert.Equal(t, "failed", status)
		assert.Equal(t, "batch_rejected", reason.String)
	})
}

type errOnlyRecorder struct{ err error }

func (e errOnlyRecorder) Record(context.Context, tenant.Scope, ProviderID, ConfirmationCapability, []EntryOutcome) error {
	return e.err
}

type panicRecorder struct{}

func (panicRecorder) Record(context.Context, tenant.Scope, ProviderID, ConfirmationCapability, []EntryOutcome) error {
	panic("recorder panic")
}

func TestSendResultsIdempotency(t *testing.T) {
	env := newProvisionEnv(t)
	rec := NewResultRecorder(env.db)
	fixed := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	rec.SetClock(func() time.Time { return fixed })
	env.engine.SetResultRecorder(rec)

	caps := derivedNoneCaps(ConfirmPerRequest)
	provider := newFakeProvider("fake-none", caps, &fakeScript{
		dispositions:  []ResultDisposition{DispositionAccepted, DispositionAccepted},
		identityByBar: map[string]ProductIdentity{"0001": "SKU-1"},
	})
	require.NoError(t, env.registry.Register(provider))
	itemID, _ := env.seedShortfall(t, "prod-1", "Milk", "0001", 2)

	ctx := context.Background()
	_, err := env.engine.Provision(ctx, env.scope, "fake-none")
	require.NoError(t, err)

	outcomes := []EntryOutcome{{
		ItemID: itemID, EntryID: "e-retry", Quantity: 2, Outcome: OutcomeConfirmed,
	}}
	later := fixed.Add(2 * time.Hour)
	rec.SetClock(func() time.Time { return later })
	require.NoError(t, rec.Record(ctx, env.scope, "fake-none", ConfirmPerRequest, outcomes))

	assert.Equal(t, 1, countCartLineResults(t, env.db, env.scope.HouseholdID()))
	_, _, _, at, ok := readCartLineResult(t, env.db, env.scope.HouseholdID(), "fake-none", itemID)
	require.True(t, ok)
	assert.Equal(t, later.Format(time.RFC3339), at)
	assertAttemptedAtRFC3339UTC(t, at)

	ls := latestSendForItem(t, rec, env.scope, "fake-none", itemID)
	assert.Equal(t, PublicStatusSent, ls.Status)
	assert.Empty(t, ls.Reason, "sent rows omit reason")
	assert.Equal(t, later.Format(time.RFC3339), ls.AttemptedAt)
	assertAttemptedAtRFC3339UTC(t, ls.AttemptedAt)
}

func TestSendResultsScopedItemIdsLeavesOtherRows(t *testing.T) {
	env := newProvisionEnv(t)
	fixed := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	rec := NewResultRecorder(env.db)
	rec.SetClock(func() time.Time { return fixed })
	env.engine.SetResultRecorder(rec)

	caps := derivedNoneCaps(ConfirmPerRequest)
	script := &fakeScript{
		dispositions:  []ResultDisposition{DispositionAccepted},
		identityByBar: map[string]ProductIdentity{"0001": "SKU-1", "0002": "SKU-2"},
	}
	provider := newFakeProvider("fake-none", caps, script)
	require.NoError(t, env.registry.Register(provider))
	itemA, _ := env.seedShortfall(t, "prod-a", "Milk", "0001", 1)
	itemB, _ := env.seedShortfall(t, "prod-b", "Bread", "0002", 1)

	ctx := context.Background()
	older := fixed.Add(-24 * time.Hour)
	require.NoError(t, rec.Record(ctx, env.scope, "fake-none", ConfirmPerRequest, []EntryOutcome{
		{ItemID: itemB, EntryID: "e-b", Quantity: 1, Outcome: OutcomeFailed, Reason: ReasonBatchRejected},
	}))
	// Backdate B so the row is not pruned when A sends later.
	_, err := env.db.Exec(`UPDATE cart_line_results SET attempted_at = ? WHERE household_id = ? AND item_id = ?`,
		older.Format(time.RFC3339), env.scope.HouseholdID(), itemB)
	require.NoError(t, err)
	beforeB := snapshotCartLineRow(t, env.db, env.scope.HouseholdID(), "fake-none", itemB)

	ctx = WithProvisionFilter(ctx, &ProvisionFilter{ScopedItemIDs: map[string]struct{}{itemA: {}}})
	rec.SetClock(func() time.Time { return fixed })
	_, err = env.engine.Provision(ctx, env.scope, "fake-none")
	require.NoError(t, err)

	afterB := snapshotCartLineRow(t, env.db, env.scope.HouseholdID(), "fake-none", itemB)
	assert.Equal(t, beforeB, afterB)
}

func snapshotCartLineRow(t *testing.T, db *sql.DB, household, provider, itemID string) string {
	t.Helper()
	var row string
	require.NoError(t, db.QueryRow(
		`SELECT status || '|' || COALESCE(reason,'') || '|' || quantity || '|' || attempted_at
		 FROM cart_line_results WHERE household_id = ? AND provider_id = ? AND item_id = ?`,
		household, provider, itemID).Scan(&row))
	return row
}

func TestSendResultsHouseholdScoping(t *testing.T) {
	env := newProvisionEnv(t)
	rec := NewResultRecorder(env.db)
	env.engine.SetResultRecorder(rec)
	cedar := cedarScope(t, env.db)

	old := env.scope.HouseholdID()
	_, err := env.db.Exec(`INSERT INTO cart_line_results (household_id, provider_id, item_id, status, attempted_at)
		VALUES (?, 'fake-none', 'other-item', 'sent', '2026-01-01T00:00:00Z')`, old)
	require.NoError(t, err)

	cutoff := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	got, err := rec.LatestResults(context.Background(), cedar, "fake-none", cutoff)
	require.NoError(t, err)
	assert.Empty(t, got)

	pruneAt := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	rec.SetClock(func() time.Time { return pruneAt })
	require.NoError(t, rec.Record(context.Background(), env.scope, "fake-none", ConfirmPerRequest, []EntryOutcome{
		{ItemID: "i-a", EntryID: "e-a", Quantity: 1, Outcome: OutcomeConfirmed},
	}))

	var otherCount int
	require.NoError(t, env.db.QueryRow(`SELECT COUNT(*) FROM cart_line_results WHERE household_id = ?`, old).Scan(&otherCount))
	assert.Equal(t, 1, otherCount)
}

func TestSendResultsWipeAndPurgeClearsRows(t *testing.T) {
	env := newProvisionEnv(t)
	env.engine.SetResultRecorder(NewResultRecorder(env.db))
	caps := derivedNoneCaps(ConfirmPerRequest)
	provider := newFakeProvider("fake-none", caps, &fakeScript{
		dispositions:  []ResultDisposition{DispositionAccepted},
		identityByBar: map[string]ProductIdentity{"0001": "SKU-1"},
	})
	require.NoError(t, env.registry.Register(provider))
	itemID, _ := env.seedShortfall(t, "prod-1", "Milk", "0001", 1)
	_, err := env.engine.Provision(context.Background(), env.scope, "fake-none")
	require.NoError(t, err)
	_, _, _, _, ok := readCartLineResult(t, env.db, env.scope.HouseholdID(), "fake-none", itemID)
	require.True(t, ok)

	svc := supply.Open(env.db)
	require.NoError(t, svc.Wipe(context.Background(), env.scope, supply.WipePhrase))
	assert.Equal(t, 0, countCartLineResults(t, env.db, env.scope.HouseholdID()))

	_, err = env.db.Exec(`INSERT INTO cart_line_results (household_id, provider_id, item_id, status, attempted_at)
		VALUES (?, 'fake-none', 'purge-x', 'sent', '2026-01-01T00:00:00Z')`, env.scope.HouseholdID())
	require.NoError(t, err)
	_, err = env.db.Exec(`UPDATE households SET purge_after = '2000-01-01T00:00:00Z' WHERE id = ?`, env.scope.HouseholdID())
	require.NoError(t, err)
	authSvc := auth.NewService(env.db, t.TempDir())
	_, err = authSvc.PurgeDue(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 0, countCartLineResults(t, env.db, env.scope.HouseholdID()))
}

func TestSendResultsFailureIsolation(t *testing.T) {
	t.Run("recorder_error", func(t *testing.T) {
		env := newProvisionEnv(t)
		caps := derivedNoneCaps(ConfirmPerRequest)
		provider := newFakeProvider("fake-none", caps, &fakeScript{
			dispositions:  []ResultDisposition{DispositionAccepted},
			identityByBar: map[string]ProductIdentity{"0001": "SKU-1"},
		})
		require.NoError(t, env.registry.Register(provider))
		itemID, _ := env.seedShortfall(t, "prod-1", "Milk", "0001", 2)
		ctx := context.Background()

		env.engine.SetResultRecorder(errOnlyRecorder{err: errors.New("write failed")})
		report, err := env.engine.Provision(ctx, env.scope, "fake-none")
		require.NoError(t, err)
		assert.Equal(t, 1, report.Confirmed)
		ledger, err := env.ledger.ListForProvider(ctx, env.scope, "fake-none")
		require.NoError(t, err)
		assert.Equal(t, 2, ledger[itemID].Requested)
		_, _, _, _, ok := readCartLineResult(t, env.db, env.scope.HouseholdID(), "fake-none", itemID)
		assert.False(t, ok)
	})

	t.Run("recorder_panic", func(t *testing.T) {
		env := newProvisionEnv(t)
		caps := derivedNoneCaps(ConfirmPerRequest)
		provider := newFakeProvider("fake-none", caps, &fakeScript{
			dispositions:  []ResultDisposition{DispositionAccepted},
			identityByBar: map[string]ProductIdentity{"0001": "SKU-1"},
		})
		require.NoError(t, env.registry.Register(provider))
		env.seedShortfall(t, "prod-1", "Milk", "0001", 2)
		env.engine.SetResultRecorder(panicRecorder{})
		report, err := env.engine.Provision(context.Background(), env.scope, "fake-none")
		require.NoError(t, err)
		assert.Equal(t, 1, report.Confirmed)
	})
}

// seedSkippedAndResolvablePair stages one no-match line and one line that resolves.
func seedSkippedAndResolvablePair(t *testing.T, env *provisionEnv) (unresolvedItemID, resolvedItemID string) {
	t.Helper()
	unresolvedItemID, _ = env.seedShortfall(t, "prod-skip", "Milk", "0001", 1)
	resolvedItemID, _ = env.seedShortfall(t, "prod-send", "Bread", "0002", 1)
	return unresolvedItemID, resolvedItemID
}

func TestProvisionCancelledCtxStillRecordsSendResults(t *testing.T) {
	env := newProvisionEnv(t)
	env.engine.SetResultRecorder(NewResultRecorder(env.db))
	block := make(chan struct{})
	started := make(chan struct{})
	script := &fakeScript{
		dispositions:  []ResultDisposition{DispositionAccepted},
		addBlock:      block,
		addStarted:    started,
		identityByBar: map[string]ProductIdentity{"0001": "SKU-1"},
	}
	caps := derivedNoneCaps(ConfirmPerRequest)
	provider := newFakeProvider("fake-none", caps, script)
	require.NoError(t, env.registry.Register(provider))
	itemID, _ := env.seedShortfall(t, "prod-1", "Milk", "0001", 2)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := env.engine.Provision(ctx, env.scope, "fake-none")
		done <- err
	}()
	<-started
	close(block)
	for script.addCalls < 1 {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(5 * time.Millisecond)
	cancel()
	require.NoError(t, <-done)

	_, _, _, at, ok := readCartLineResult(t, env.db, env.scope.HouseholdID(), "fake-none", itemID)
	require.True(t, ok, "finishProvisionRecording must persist after client disconnect")
	assertAttemptedAtRFC3339UTC(t, at)
}

func TestFinishProvisionRecordingCancelledParentCtxStillPersists(t *testing.T) {
	env := newProvisionEnv(t)
	env.engine.SetResultRecorder(NewResultRecorder(env.db))
	itemID := "item-finish"
	outcomes := []EntryOutcome{{
		ItemID: itemID, EntryID: "e1", Quantity: 2, Outcome: OutcomeConfirmed,
	}}
	report := ProvisionReport{
		Provider:  "fake-none",
		Confirmed: 1,
		Entries:   outcomes,
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	env.engine.finishProvisionRecording(ctx, env.scope, "fake-none", ConfirmPerRequest, outcomes, report, nil)
	_, _, _, _, ok := readCartLineResult(t, env.db, env.scope.HouseholdID(), "fake-none", itemID)
	require.True(t, ok)
}

func TestSendResultsIndeterminateThenHaltFailed(t *testing.T) {
	env := newProvisionEnv(t)
	env.engine.SetResultRecorder(NewResultRecorder(env.db))
	caps := derivedNoneCaps(ConfirmPerRequest)
	provider := newFakeProvider("fake-none", caps, &fakeScript{
		dispositions:  []ResultDisposition{DispositionIndeterminate, DispositionAccepted},
		identityByBar: map[string]ProductIdentity{"0001": "SKU-1", "0002": "SKU-2"},
	})
	require.NoError(t, env.registry.Register(provider, WithBatchSize(1)))
	itemA, _ := env.seedShortfall(t, "prod-a", "A", "0001", 1)
	itemB, _ := env.seedShortfall(t, "prod-b", "B", "0002", 1)

	_, err := env.engine.Provision(context.Background(), env.scope, "fake-none")
	require.NoError(t, err)

	byItem := make(map[string]string)
	for _, id := range []string{itemA, itemB} {
		status, _, _, _, ok := readCartLineResult(t, env.db, env.scope.HouseholdID(), "fake-none", id)
		require.True(t, ok)
		byItem[id] = status
	}
	var unknowns, faileds int
	for _, s := range byItem {
		switch s {
		case "unknown":
			unknowns++
		case "failed":
			faileds++
		}
	}
	assert.Equal(t, 1, unknowns)
	assert.Equal(t, 1, faileds)
}

func TestSendResults5xxHaltsRemaining(t *testing.T) {
	env := newProvisionEnv(t)
	env.engine.SetResultRecorder(NewResultRecorder(env.db))
	caps := derivedNoneCaps(ConfirmPerRequest)
	provider := newFakeProvider("fake-none", caps, &fakeScript{
		addStatuses:   []int{http.StatusBadGateway, http.StatusOK},
		addErrors:     []error{errors.New("502"), nil},
		identityByBar: map[string]ProductIdentity{"0001": "SKU-1", "0002": "SKU-2"},
	})
	require.NoError(t, env.registry.Register(provider, WithBatchSize(1)))
	itemA, _ := env.seedShortfall(t, "prod-a", "A", "0001", 1)
	itemB, _ := env.seedShortfall(t, "prod-b", "B", "0002", 1)

	_, err := env.engine.Provision(context.Background(), env.scope, "fake-none")
	require.NoError(t, err)

	byItem := make(map[string]string)
	for _, id := range []string{itemA, itemB} {
		status, _, _, _, ok := readCartLineResult(t, env.db, env.scope.HouseholdID(), "fake-none", id)
		require.True(t, ok)
		byItem[id] = status
	}
	var unknowns, faileds int
	for _, s := range byItem {
		switch s {
		case "unknown":
			unknowns++
		case "failed":
			faileds++
		}
	}
	assert.Equal(t, 1, unknowns)
	assert.Equal(t, 1, faileds)
}
