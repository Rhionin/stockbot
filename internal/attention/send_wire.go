package attention

import "github.com/Rhionin/stockbot/internal/cart"

// WireFromStored maps one cart_line_results row to a wire reason and detail.
// Returns ok false when the row must not flag a line (skipped catch-all, sent, etc.).
func WireFromStored(status, storedReason string) (reason Reason, detail string, ok bool) {
	switch cart.PublicStatus(status) {
	case cart.PublicStatusFailed:
		switch cart.PublicReason(storedReason) {
		case cart.PublicReasonBatchRejected:
			return ReasonSendFailed, string(cart.PublicReasonBatchRejected), true
		case cart.PublicReasonInvalidLine:
			return ReasonSendFailed, string(cart.PublicReasonInvalidLine), true
		case cart.PublicReasonAPIError:
			return ReasonSendFailed, string(cart.PublicReasonAPIError), true
		case cart.PublicReasonRequestIncomplete:
			return ReasonSendFailed, string(cart.PublicReasonRequestIncomplete), true
		case cart.PublicReasonStoreSignIn:
			return ReasonSendFailed, string(cart.PublicReasonStoreSignIn), true
		case cart.PublicReasonReconnectRequired:
			return ReasonSendFailed, string(cart.PublicReasonReconnectRequired), true
		case cart.PublicReasonNoPerItemResult:
			return ReasonSendFailed, string(cart.PublicReasonNoPerItemResult), true
		case cart.PublicReasonProviderUnavailable:
			return ReasonSendFailed, string(cart.PublicReasonProviderUnavailable), true
		case cart.PublicReasonNoUPC, cart.PublicReasonNoMatch,
			cart.PublicReasonUnavailable:
			// wrong status for listed codes — catch-all by status
		}
		return ReasonSendFailed, "", true
	case cart.PublicStatusSkipped:
		switch cart.PublicReason(storedReason) {
		case cart.PublicReasonUnavailable:
			return ReasonSendFailed, string(cart.PublicReasonUnavailable), true
		case cart.PublicReasonNoUPC:
			return ReasonNoBarcode, string(cart.PublicReasonNoUPC), true
		case cart.PublicReasonNoMatch:
			return ReasonNoStoreMatch, string(cart.PublicReasonNoMatch), true
		default:
			return "", "", false
		}
	case cart.PublicStatusUnknown:
		switch cart.PublicReason(storedReason) {
		case cart.PublicReasonRequestIncomplete:
			return ReasonSendUnsure, string(cart.PublicReasonRequestIncomplete), true
		case cart.PublicReasonNoPerItemResult:
			return ReasonSendUnsure, string(cart.PublicReasonNoPerItemResult), true
		case cart.PublicReasonProviderUnavailable:
			return ReasonSendUnsure, string(cart.PublicReasonProviderUnavailable), true
		default:
			return ReasonSendUnsure, "", true
		}
	default:
		return "", "", false
	}
}