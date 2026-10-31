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
	assert.Equal(t, "no_match", provisionEntries[1].OutcomeReason)
	assert.Equal(t, "failed", results[3].Status)
	assert.Equal(t, "batch_rejected", results[3].Reason)
	assert.Equal(t, "batch_rejected", provisionEntries[3].OutcomeReason)
}

func TestBuildProvisionExportViewsPerLineAdded(t *testing.T) {
	_, results, _, _, counts := buildProvisionExportViews([]cart.EntryOutcome{
		{Name: "Milk", Outcome: cart.OutcomeConfirmed},
	}, cart.ConfirmPerLine)
	assert.Equal(t, 1, counts.AddedCount)
	assert.Equal(t, 0, counts.SentCount)
	assert.Equal(t, "added", results[0].Status)
}

func TestLegacyProvisionOutcomeReasonPreservesDeprecatedValues(t *testing.T) {
	assert.Equal(t, "no_product_identity", legacyProvisionOutcomeReason(cart.OutcomeFailed, cart.ReasonNoProductIdentity))
	assert.Equal(t, "provider_rejected", legacyProvisionOutcomeReason(cart.OutcomeFailed, cart.ReasonProviderRejected))
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
