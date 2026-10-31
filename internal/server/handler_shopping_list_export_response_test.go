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
		{EntryID: "e4", ItemID: "i4", Name: "Spice", Quantity: 1, Outcome: cart.OutcomeFailed, Reason: cart.ReasonAPIError},
		{EntryID: "e5", ItemID: "i5", Name: "Tea", Quantity: 1, Outcome: cart.OutcomeUnknown, Reason: cart.ReasonNoPerItemResult},
	}

	provisionEntries, results, failedItems, unknownItems, counts := buildProvisionExportViews(entries)

	assert.Equal(t, 1, counts.AddedCount)
	assert.Equal(t, 2, counts.SkippedCount)
	assert.Equal(t, 1, counts.FailedCount)
	assert.Equal(t, 1, counts.UnknownCount)
	assert.Equal(t, []string{"Spice"}, failedItems)
	assert.Equal(t, []string{"Tea"}, unknownItems)
	assert.Len(t, provisionEntries, 5)
	assert.Len(t, results, 5)

	assert.Equal(t, "skipped", results[1].Status)
	assert.Equal(t, "no_match", results[1].Reason)
	assert.Equal(t, "failed", results[3].Status)
	assert.Equal(t, "api_error", results[3].Reason)
}

func TestBuildProvisionExportViewsLegacyReasonMapsToAPIError(t *testing.T) {
	_, results, failedItems, _, counts := buildProvisionExportViews([]cart.EntryOutcome{
		{Name: "X", Outcome: cart.OutcomeFailed, Reason: cart.ReasonProviderRejected},
	})
	assert.Equal(t, 1, counts.FailedCount)
	assert.Equal(t, []string{"X"}, failedItems)
	assert.Equal(t, "api_error", results[0].Reason)
}

func TestProvisionStatusCoversEveryOutcomeReason(t *testing.T) {
	reasons := []cart.OutcomeReason{
		cart.ReasonNoUPC,
		cart.ReasonNoMatch,
		cart.ReasonUnavailable,
		cart.ReasonAPIError,
		cart.ReasonNoPerItemResult,
		cart.ReasonProviderRejected,
		cart.ReasonRequestIncomplete,
	}
	for _, reason := range reasons {
		status := provisionStatus(cart.OutcomeFailed, reason)
		if reason == cart.ReasonNoUPC || reason == cart.ReasonNoMatch || reason == cart.ReasonUnavailable {
			assert.Equal(t, "skipped", status, string(reason))
		} else {
			assert.Equal(t, "failed", status, string(reason))
		}
	}
	assert.Equal(t, "unknown", provisionStatus(cart.OutcomeUnknown, ""))
	assert.Equal(t, "added", provisionStatus(cart.OutcomeConfirmed, ""))
}
