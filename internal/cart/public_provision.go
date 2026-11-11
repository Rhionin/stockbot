package cart

import "time"

// PublicStatus is the #485 per-line send result status exposed to clients.
type PublicStatus string

const (
	PublicStatusAdded   PublicStatus = "added"
	PublicStatusSent    PublicStatus = "sent"
	PublicStatusSkipped PublicStatus = "skipped"
	PublicStatusFailed  PublicStatus = "failed"
	PublicStatusUnknown PublicStatus = "unknown"
)

// PublicReason is the stored and exported reason vocabulary for send results.
// Empty means none (added/sent). store_sign_in is stored-only until T2 emits it.
type PublicReason string

const (
	PublicReasonNoUPC               PublicReason = "no_upc"
	PublicReasonNoMatch             PublicReason = "no_match"
	PublicReasonUnavailable         PublicReason = "unavailable"
	PublicReasonAPIError            PublicReason = "api_error"
	PublicReasonBatchRejected       PublicReason = "batch_rejected"
	PublicReasonInvalidLine         PublicReason = "invalid_line"
	PublicReasonProviderUnavailable PublicReason = "provider_unavailable"
	PublicReasonReconnectRequired   PublicReason = "reconnect_required"
	PublicReasonNoPerItemResult     PublicReason = "no_per_item_result"
	PublicReasonRequestIncomplete   PublicReason = "request_incomplete"
	PublicReasonStoreSignIn         PublicReason = "store_sign_in"
)

// AllPublicStatuses is the closed set of PublicStatus values (contract parity).
func AllPublicStatuses() []PublicStatus {
	return []PublicStatus{
		PublicStatusAdded,
		PublicStatusSent,
		PublicStatusSkipped,
		PublicStatusFailed,
		PublicStatusUnknown,
	}
}

// AllPublicReasons is the closed set of PublicReason values (contract parity).
func AllPublicReasons() []PublicReason {
	return []PublicReason{
		PublicReasonNoUPC,
		PublicReasonNoMatch,
		PublicReasonUnavailable,
		PublicReasonAPIError,
		PublicReasonBatchRejected,
		PublicReasonInvalidLine,
		PublicReasonProviderUnavailable,
		PublicReasonReconnectRequired,
		PublicReasonNoPerItemResult,
		PublicReasonRequestIncomplete,
		PublicReasonStoreSignIn,
	}
}

// SendResultRetention is how long a cart_line_results row stays visible to readers.
const SendResultRetention = 90 * 24 * time.Hour

// LastSend is the public snapshot of the latest send outcome for one line (#571 W2).
type LastSend struct {
	ProviderID  string       `json:"providerId"`
	Status      PublicStatus `json:"status"`
	Reason      PublicReason `json:"reason,omitempty"`
	Quantity    int          `json:"quantity"`
	AttemptedAt string       `json:"attemptedAt"`
}

// ProvisionResultStatus maps an engine outcome to the #485 public status.
func ProvisionResultStatus(outcome Outcome, reason OutcomeReason, confirm ConfirmationCapability) PublicStatus {
	switch outcome {
	case OutcomeConfirmed:
		if confirm == ConfirmPerLine {
			return PublicStatusAdded
		}
		// Kroger and other per_request providers: bodyless acceptance is not per-line proof.
		return PublicStatusSent
	case OutcomeUnknown:
		return PublicStatusUnknown
	case OutcomeFailed:
		switch reason {
		case ReasonNoUPC, ReasonNoMatch, ReasonUnavailable:
			return PublicStatusSkipped
		default:
			return PublicStatusFailed
		}
	default:
		return PublicStatusFailed
	}
}

// PublicProvisionReason maps an engine outcome to the public reason string.
func PublicProvisionReason(outcome Outcome, reason OutcomeReason) PublicReason {
	if outcome == OutcomeConfirmed {
		return ""
	}
	switch reason {
	case ReasonNoUPC, ReasonNoMatch, ReasonUnavailable,
		ReasonAPIError, ReasonBatchRejected, ReasonInvalidLine,
		ReasonProviderUnavailable, ReasonReconnectRequired,
		ReasonNoPerItemResult, ReasonRequestIncomplete:
		return PublicReason(reason)
	case ReasonNoProductIdentity:
		return PublicReasonNoMatch
	case ReasonProviderRejected:
		return PublicReasonBatchRejected
	default:
		if outcome == OutcomeUnknown {
			return PublicReasonRequestIncomplete
		}
		return PublicReasonAPIError
	}
}
