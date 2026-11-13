package cart

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPublicProvisionReasonMapsDeprecatedEngineReasons(t *testing.T) {
	assert.Equal(t, PublicReasonNoMatch, PublicProvisionReason(OutcomeFailed, ReasonNoProductIdentity))
	assert.Equal(t, PublicReasonBatchRejected, PublicProvisionReason(OutcomeFailed, ReasonProviderRejected))
	assert.Equal(t, PublicReasonRequestIncomplete, PublicProvisionReason(OutcomeUnknown, ""))
	assert.Equal(t, PublicReasonAPIError, PublicProvisionReason(OutcomeFailed, OutcomeReason("weird")))
	assert.Equal(t, PublicReason(""), PublicProvisionReason(OutcomeConfirmed, ReasonNoUPC))
	for _, reason := range []OutcomeReason{
		ReasonNoUPC, ReasonUnavailable, ReasonReconnectRequired,
	} {
		got := PublicProvisionReason(OutcomeFailed, reason)
		if got != PublicReason(reason) {
			t.Fatalf("public reason %q = %q", reason, got)
		}
	}
}

func TestProvisionResultStatusCoversEveryOutcomeReason(t *testing.T) {
	want := map[OutcomeReason]struct {
		outcome Outcome
		status  PublicStatus
	}{
		ReasonNoUPC:               {OutcomeFailed, PublicStatusSkipped},
		ReasonNoMatch:             {OutcomeFailed, PublicStatusSkipped},
		ReasonUnavailable:         {OutcomeFailed, PublicStatusSkipped},
		ReasonNoProductIdentity:   {OutcomeFailed, PublicStatusFailed},
		ReasonAPIError:            {OutcomeFailed, PublicStatusFailed},
		ReasonBatchRejected:       {OutcomeFailed, PublicStatusFailed},
		ReasonInvalidLine:         {OutcomeFailed, PublicStatusFailed},
		ReasonProviderRejected:    {OutcomeFailed, PublicStatusFailed},
		ReasonProviderUnavailable: {OutcomeUnknown, PublicStatusUnknown},
		ReasonReconnectRequired:   {OutcomeUnknown, PublicStatusUnknown},
		ReasonNoPerItemResult:     {OutcomeUnknown, PublicStatusUnknown},
		ReasonRequestIncomplete:   {OutcomeUnknown, PublicStatusUnknown},
	}
	for reason, exp := range want {
		assert.Equal(t, exp.status, ProvisionResultStatus(exp.outcome, reason, ConfirmPerRequest), string(reason))
	}
	assert.Equal(t, PublicStatusUnknown, ProvisionResultStatus(OutcomeUnknown, "", ConfirmPerRequest))
	assert.Equal(t, PublicStatusSent, ProvisionResultStatus(OutcomeConfirmed, "", ConfirmPerRequest))
	assert.Equal(t, PublicStatusAdded, ProvisionResultStatus(OutcomeConfirmed, "", ConfirmPerLine))
}

func TestProvisionResultStatusDefaultOutcomeIsFailed(t *testing.T) {
	assert.Equal(t, PublicStatusFailed, ProvisionResultStatus(Outcome("weird"), "", ConfirmPerRequest))
}
