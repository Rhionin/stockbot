package cart

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCollapseSendResultOutcomesSeverity(t *testing.T) {
	t.Parallel()
	confirm := ConfirmPerRequest
	outcomes := []EntryOutcome{
		{ItemID: "i-1", EntryID: "e-1", Quantity: 1, Outcome: OutcomeConfirmed},
		{ItemID: "i-1", EntryID: "e-2", Quantity: 2, Outcome: OutcomeFailed, Reason: ReasonBatchRejected},
	}
	rows := collapseSendResultOutcomes(outcomes, confirm)
	requireLen := func() {
		if len(rows) != 1 {
			t.Fatalf("rows = %d, want 1", len(rows))
		}
	}
	requireLen()
	assert.Equal(t, PublicStatusFailed, rows[0].status)
	assert.Equal(t, PublicReasonBatchRejected, rows[0].reason)
	assert.Equal(t, 2, rows[0].quantity)

	outcomes = []EntryOutcome{
		{ItemID: "i-1", Quantity: 1, Outcome: OutcomeConfirmed},
		{ItemID: "i-1", Quantity: 1, Outcome: OutcomeUnknown, Reason: ReasonRequestIncomplete},
	}
	rows = collapseSendResultOutcomes(outcomes, confirm)
	requireLen()
	assert.Equal(t, PublicStatusUnknown, rows[0].status)
}

func TestRewriteBatchOutcomesUnknown(t *testing.T) {
	t.Parallel()
	outcomes := []EntryOutcome{
		{EntryID: "e-1", ItemID: "i-1", Outcome: OutcomeConfirmed, Quantity: 2},
		{EntryID: "e-2", ItemID: "i-2", Outcome: OutcomeConfirmed, Quantity: 1},
	}
	req := ProvisionRequest{Lines: []ProvisionLine{{
		Accounts: []ResolvedItem{{EntryID: "e-2", ItemID: "i-2", Quantity: 1}},
	}}}
	outcomes = rewriteBatchOutcomesUnknown(outcomes, req)
	assert.Equal(t, OutcomeConfirmed, outcomes[0].Outcome)
	assert.Equal(t, OutcomeUnknown, outcomes[1].Outcome)
	assert.Equal(t, ReasonRequestIncomplete, outcomes[1].Reason)
}

func TestStoredLineReasonMapsProviderUnavailableToAPIError(t *testing.T) {
	t.Parallel()
	got := storedLineReason(PublicStatusFailed, PublicReasonProviderUnavailable)
	assert.Equal(t, PublicReasonAPIError, got)
	assert.Empty(t, storedLineReason(PublicStatusSent, PublicReasonAPIError))
}

func TestShouldRecordSendResultsSkipsReconnect409WithSkippedOutcomes(t *testing.T) {
	t.Parallel()
	err := &ErrReconnectRequired{Provider: "fake-oauth"}
	outcomes := []EntryOutcome{{ItemID: "i-1", Outcome: OutcomeFailed, Reason: ReasonNoMatch}}
	assert.False(t, shouldRecordSendResults(err, ProvisionReport{}, outcomes))
}

func TestShouldRecordSendResultsSkipsProvisionConflict(t *testing.T) {
	t.Parallel()
	assert.False(t, shouldRecordSendResults(&ProvisionConflict{Reason: "busy"}, ProvisionReport{}, []EntryOutcome{
		{ItemID: "i-1", Outcome: OutcomeConfirmed},
	}))
}

func TestLastSendFromStoredOmitsReasonForSent(t *testing.T) {
	t.Parallel()
	got := lastSendFromStored("kroger", PublicStatusSent, sql.NullString{Valid: true, String: "api_error"}, 2, "2026-01-01T00:00:00Z")
	assert.Empty(t, got.Reason)
}

func TestRecordNoopsOnNilReceiver(t *testing.T) {
	t.Parallel()
	var rec *ResultRecorder
	assert.NoError(t, rec.Record(context.Background(), tenant.Scope{}, "kroger", ConfirmPerRequest, []EntryOutcome{
		{ItemID: "i-1", Outcome: OutcomeConfirmed},
	}))
}

func TestFormatSendAttemptedAtRFC3339UTC(t *testing.T) {
	t.Parallel()
	ts := time.Date(2026, 11, 11, 14, 0, 0, 0, time.FixedZone("EST", -5*3600))
	formatted := formatSendAttemptedAt(ts)
	assert.Equal(t, "2026-11-11T19:00:00Z", formatted)
	parsed, err := time.Parse(time.RFC3339, formatted)
	require.NoError(t, err)
	assert.Equal(t, time.UTC, parsed.Location())
}
