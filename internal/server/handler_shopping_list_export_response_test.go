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

func TestLegacyProvisionOutcomeReasonMapsNewEngineReasons(t *testing.T) {
	cases := map[cart.OutcomeReason]string{
		cart.ReasonNoUPC:               "no_product_identity",
		cart.ReasonNoMatch:             "no_product_identity",
		cart.ReasonNoProductIdentity:   "no_product_identity",
		cart.ReasonBatchRejected:       "provider_rejected",
		cart.ReasonProviderRejected:    "provider_rejected",
		cart.ReasonReconnectRequired:   "reconnect_required",
		cart.ReasonRequestIncomplete:   "request_incomplete",
		cart.ReasonProviderUnavailable: "provider_unavailable",
		cart.ReasonAPIError:            "api_error",
	}
	for reason, want := range cases {
		assert.Equal(t, want, legacyProvisionOutcomeReason(cart.OutcomeFailed, reason), string(reason))
	}
	assert.Equal(t, "", legacyProvisionOutcomeReason(cart.OutcomeConfirmed, cart.ReasonNoUPC))
	assert.Equal(t, "request_incomplete", legacyProvisionOutcomeReason(cart.OutcomeUnknown, ""))
	assert.Equal(t, "api_error", legacyProvisionOutcomeReason(cart.OutcomeFailed, cart.OutcomeReason("weird")))
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
