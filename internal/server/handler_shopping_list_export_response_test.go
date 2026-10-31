package server

import (
	"testing"

	"github.com/Rhionin/stockbot/internal/cart"
	"github.com/stretchr/testify/assert"
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
	assert.Equal(t, "failed", results[3].Status)
	assert.Equal(t, "batch_rejected", results[3].Reason)
}

func TestBuildProvisionExportViewsPerLineAdded(t *testing.T) {
	_, results, _, _, counts := buildProvisionExportViews([]cart.EntryOutcome{
		{Name: "Milk", Outcome: cart.OutcomeConfirmed},
	}, cart.ConfirmPerLine)
	assert.Equal(t, 1, counts.AddedCount)
	assert.Equal(t, 0, counts.SentCount)
	assert.Equal(t, "added", results[0].Status)
}

func TestExportErrorCodeWholeRequestFailures(t *testing.T) {
	assert.Equal(t, "reconnect_required", exportErrorCode([]cart.EntryOutcome{
		{Reason: cart.ReasonReconnectRequired},
	}))
	assert.Equal(t, "provider_unavailable", exportErrorCode([]cart.EntryOutcome{
		{Reason: cart.ReasonProviderUnavailable},
	}))
	assert.Equal(t, "", exportErrorCode([]cart.EntryOutcome{
		{Reason: cart.ReasonRequestIncomplete, Outcome: cart.OutcomeUnknown},
	}))
}

func TestProvisionReasonUXCategory(t *testing.T) {
	assert.Equal(t, "no_barcode", provisionReasonUXCategory("no_upc"))
	assert.Equal(t, "no_match", provisionReasonUXCategory("no_match"))
	assert.Equal(t, "rejected", provisionReasonUXCategory("batch_rejected"))
	assert.Equal(t, "error", provisionReasonUXCategory("api_error"))
	assert.Equal(t, "", provisionReasonUXCategory("unavailable"))
}

func TestProvisionResultStatusCoversEveryOutcomeReason(t *testing.T) {
	reasons := []cart.OutcomeReason{
		cart.ReasonNoUPC,
		cart.ReasonNoMatch,
		cart.ReasonUnavailable,
		cart.ReasonBatchRejected,
	}
	for _, reason := range reasons {
		status := provisionResultStatus(cart.OutcomeFailed, reason, cart.ConfirmPerRequest)
		if reason == cart.ReasonNoUPC || reason == cart.ReasonNoMatch || reason == cart.ReasonUnavailable {
			assert.Equal(t, "skipped", status, string(reason))
		} else {
			assert.Equal(t, "failed", status, string(reason))
		}
	}
	assert.Equal(t, "unknown", provisionResultStatus(cart.OutcomeUnknown, "", cart.ConfirmPerRequest))
	assert.Equal(t, "sent", provisionResultStatus(cart.OutcomeConfirmed, "", cart.ConfirmPerRequest))
	assert.Equal(t, "added", provisionResultStatus(cart.OutcomeConfirmed, "", cart.ConfirmPerLine))
}
