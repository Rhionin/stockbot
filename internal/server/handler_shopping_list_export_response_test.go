package server

import (
	"testing"

	"github.com/Rhionin/stockbot/internal/cart"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildProvisionExportViews(t *testing.T) {
	entries := []cart.EntryOutcome{
		{EntryID: "e1", ItemID: "i1", Name: "Milk", Quantity: 2, Outcome: cart.OutcomeConfirmed},
		{EntryID: "e2", ItemID: "i2", Name: "Cheese", Quantity: 1, Outcome: cart.OutcomeFailed, Reason: cart.ReasonNoMatch},
		{EntryID: "e3", ItemID: "i3", Name: "Rice", Quantity: 3, Outcome: cart.OutcomeFailed, Reason: cart.ReasonNoUPC},
		{EntryID: "e4", ItemID: "i4", Name: "Spice", Quantity: 1, Outcome: cart.OutcomeFailed, Reason: cart.ReasonBatchRejected, Detail: "The store rejected this batch."},
		{EntryID: "e5", ItemID: "i5", Name: "Tea", Quantity: 1, Outcome: cart.OutcomeUnknown, Reason: cart.ReasonRequestIncomplete},
	}

	provisionEntries, results, failedItems, unknownItems, counts := buildProvisionExportViews(entries, cart.ConfirmPerRequest)

	assert.Equal(t, 0, counts.AddedCount)
	assert.Equal(t, 1, counts.SentCount)
	assert.Equal(t, 2, counts.SkippedCount)
	assert.Equal(t, 1, counts.FailedCount)
	assert.Equal(t, 1, counts.UnknownCount)
	assert.Equal(t, []string{"Cheese", "Rice", "Spice"}, failedItems)
	assert.Equal(t, []string{"Tea"}, unknownItems)
	assert.Len(t, provisionEntries, 5)
	assert.Len(t, results, 5)

	assert.Equal(t, "sent", results[0].Status)
	assert.Equal(t, "skipped", results[1].Status)
	assert.Equal(t, "no_match", results[1].Reason)
	assert.Equal(t, "no_product_identity", provisionEntries[1].OutcomeReason)
	assert.Equal(t, "failed", results[3].Status)
	assert.Equal(t, "batch_rejected", results[3].Reason)
	assert.Equal(t, "provider_rejected", provisionEntries[3].OutcomeReason)
}

func TestBuildProvisionExportViewsPerLineAdded(t *testing.T) {
	_, results, _, _, counts := buildProvisionExportViews([]cart.EntryOutcome{
		{Name: "Milk", Outcome: cart.OutcomeConfirmed},
	}, cart.ConfirmPerLine)
	assert.Equal(t, 1, counts.AddedCount)
	assert.Equal(t, 0, counts.SentCount)
	assert.Equal(t, "added", results[0].Status)
}

// TestLegacyProvisionOutcomeReasonMatchesMasterEngine pins every post-#484
// engine (outcome, reason) pair to the exact entries[].outcomeReason string
// master emitted via string(entry.Reason). Expected values are literals only.
func TestLegacyProvisionOutcomeReasonMatchesMasterEngine(t *testing.T) {
	tests := []struct {
		situation    string
		outcome      cart.Outcome
		engineReason cart.OutcomeReason
		masterLegacy string
	}{
		{"confirmed", cart.OutcomeConfirmed, cart.OutcomeReason(""), ""},
		{"unresolved no_upc", cart.OutcomeFailed, cart.ReasonNoUPC, "no_product_identity"},
		{"unresolved no_match", cart.OutcomeFailed, cart.ReasonNoMatch, "no_product_identity"},
		{"unresolved no_product_identity", cart.OutcomeFailed, cart.ReasonNoProductIdentity, "no_product_identity"},
		{"indeterminate batch", cart.OutcomeUnknown, cart.ReasonRequestIncomplete, "request_incomplete"},
		{"halt after indeterminate", cart.OutcomeFailed, cart.ReasonRequestIncomplete, "request_incomplete"},
		{"halt after reconnect", cart.OutcomeFailed, cart.ReasonReconnectRequired, "request_incomplete"},
		{"batch rejected", cart.OutcomeFailed, cart.ReasonBatchRejected, "provider_rejected"},
		{"per-line not confirmed", cart.OutcomeFailed, cart.ReasonProviderRejected, "provider_rejected"},
		{"per-line unavailable engine reason", cart.OutcomeFailed, cart.ReasonUnavailable, "provider_rejected"},
		{"rejected with client validation error", cart.OutcomeFailed, cart.ReasonInvalidLine, "request_incomplete"},
		{"handoff no per-item result", cart.OutcomeUnknown, cart.ReasonNoPerItemResult, "no_per_item_result"},
		{"confirm none", cart.OutcomeUnknown, cart.ReasonNoPerItemResult, "no_per_item_result"},
		{"provider unavailable", cart.OutcomeFailed, cart.ReasonProviderUnavailable, "request_incomplete"},
		{"api error line", cart.OutcomeFailed, cart.ReasonAPIError, "request_incomplete"},
		{"unknown empty reason", cart.OutcomeUnknown, cart.OutcomeReason(""), "request_incomplete"},
		{"unknown unrecognized reason", cart.OutcomeUnknown, cart.OutcomeReason("weird"), "request_incomplete"},
		{"failed unrecognized reason", cart.OutcomeFailed, cart.OutcomeReason("weird"), "provider_rejected"},
	}
	for _, tc := range tests {
		t.Run(tc.situation, func(t *testing.T) {
			got := legacyProvisionOutcomeReason(tc.outcome, tc.engineReason)
			assert.Equal(t, tc.masterLegacy, got)
		})
	}
}

func TestLegacyRejectedValidationUsesMasterUnknownOutcome(t *testing.T) {
	provisionEntries, results, failedItems, unknownItems, counts := buildProvisionExportViews([]cart.EntryOutcome{
		{Name: "Bad qty", Outcome: cart.OutcomeFailed, Reason: cart.ReasonInvalidLine},
	}, cart.ConfirmPerRequest)
	require.Len(t, provisionEntries, 1)
	assert.Equal(t, "unknown", provisionEntries[0].Outcome)
	assert.Equal(t, "request_incomplete", provisionEntries[0].OutcomeReason)
	assert.Equal(t, "failed", results[0].Status)
	assert.Equal(t, "invalid_line", results[0].Reason)
	assert.Empty(t, failedItems)
	assert.Equal(t, []string{"Bad qty"}, unknownItems)
	assert.Equal(t, 1, counts.FailedCount)
	assert.Equal(t, 0, counts.UnknownCount)
}

func TestLegacyPerLineUnconfirmedMapsToMasterProviderRejected(t *testing.T) {
	provisionEntries, _, _, _, _ := buildProvisionExportViews([]cart.EntryOutcome{
		{Name: "Tea", Outcome: cart.OutcomeFailed, Reason: cart.ReasonUnavailable},
	}, cart.ConfirmPerLine)
	require.Len(t, provisionEntries, 1)
	assert.Equal(t, "provider_rejected", provisionEntries[0].OutcomeReason)
}

func TestPublicProvisionReasonMapsDeprecatedEngineReasons(t *testing.T) {
	assert.Equal(t, "no_match", publicProvisionReason(cart.OutcomeFailed, cart.ReasonNoProductIdentity))
	assert.Equal(t, "batch_rejected", publicProvisionReason(cart.OutcomeFailed, cart.ReasonProviderRejected))
	assert.Equal(t, "request_incomplete", publicProvisionReason(cart.OutcomeUnknown, ""))
	assert.Equal(t, "api_error", publicProvisionReason(cart.OutcomeFailed, cart.OutcomeReason("weird")))
	assert.Equal(t, "", publicProvisionReason(cart.OutcomeConfirmed, cart.ReasonNoUPC))
	for _, reason := range []cart.OutcomeReason{
		cart.ReasonNoUPC, cart.ReasonUnavailable, cart.ReasonReconnectRequired,
	} {
		got := publicProvisionReason(cart.OutcomeFailed, reason)
		if got != string(reason) {
			t.Fatalf("public reason %q = %q", reason, got)
		}
	}
}

func TestProvisionResultStatusCoversEveryOutcomeReason(t *testing.T) {
	want := map[cart.OutcomeReason]struct {
		outcome cart.Outcome
		status  string
	}{
		cart.ReasonNoUPC:               {cart.OutcomeFailed, "skipped"},
		cart.ReasonNoMatch:             {cart.OutcomeFailed, "skipped"},
		cart.ReasonUnavailable:         {cart.OutcomeFailed, "skipped"},
		cart.ReasonNoProductIdentity:   {cart.OutcomeFailed, "failed"},
		cart.ReasonAPIError:            {cart.OutcomeFailed, "failed"},
		cart.ReasonBatchRejected:       {cart.OutcomeFailed, "failed"},
		cart.ReasonInvalidLine:         {cart.OutcomeFailed, "failed"},
		cart.ReasonProviderRejected:    {cart.OutcomeFailed, "failed"},
		cart.ReasonProviderUnavailable: {cart.OutcomeUnknown, "unknown"},
		cart.ReasonReconnectRequired:   {cart.OutcomeUnknown, "unknown"},
		cart.ReasonNoPerItemResult:     {cart.OutcomeUnknown, "unknown"},
		cart.ReasonRequestIncomplete:   {cart.OutcomeUnknown, "unknown"},
	}
	for reason, exp := range want {
		assert.Equal(t, exp.status, provisionResultStatus(exp.outcome, reason, cart.ConfirmPerRequest), string(reason))
	}
	assert.Equal(t, "unknown", provisionResultStatus(cart.OutcomeUnknown, "", cart.ConfirmPerRequest))
	assert.Equal(t, "sent", provisionResultStatus(cart.OutcomeConfirmed, "", cart.ConfirmPerRequest))
	assert.Equal(t, "added", provisionResultStatus(cart.OutcomeConfirmed, "", cart.ConfirmPerLine))
}
