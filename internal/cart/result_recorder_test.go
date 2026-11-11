package cart

import (
	"testing"
	"time"

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

func TestFormatSendAttemptedAtRFC3339UTC(t *testing.T) {
	t.Parallel()
	ts := time.Date(2026, 11, 11, 14, 0, 0, 0, time.FixedZone("EST", -5*3600))
	formatted := formatSendAttemptedAt(ts)
	assert.Equal(t, "2026-11-11T19:00:00Z", formatted)
	parsed, err := time.Parse(time.RFC3339, formatted)
	require.NoError(t, err)
	assert.Equal(t, time.UTC, parsed.Location())
}
