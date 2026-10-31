package server

import (
	"github.com/Rhionin/stockbot/internal/cart"
)

type provisionResultEntry struct {
	EntryID  string `json:"entryId"`
	ItemID   string `json:"itemId"`
	Name     string `json:"name"`
	Quantity int    `json:"quantity"`
	Status   string `json:"status"`
	Reason   string `json:"reason,omitempty"`
	Detail   string `json:"detail,omitempty"`
}

type provisionSummaryCounts struct {
	AddedCount   int `json:"addedCount"`
	SentCount    int `json:"sentCount"`
	SkippedCount int `json:"skippedCount"`
	FailedCount  int `json:"failedCount"`
	UnknownCount int `json:"unknownCount"`
}

func provisionResultStatus(outcome cart.Outcome, reason cart.OutcomeReason, confirm cart.ConfirmationCapability) string {
	switch outcome {
	case cart.OutcomeConfirmed:
		if confirm == cart.ConfirmPerLine {
			return "added"
		}
		// Kroger and other per_request providers: bodyless acceptance is not per-line proof.
		return "sent"
	case cart.OutcomeUnknown:
		return "unknown"
	case cart.OutcomeFailed:
		switch reason {
		case cart.ReasonNoUPC, cart.ReasonNoMatch, cart.ReasonUnavailable:
			return "skipped"
		default:
			return "failed"
		}
	default:
		return "failed"
	}
}

func publicProvisionReason(outcome cart.Outcome, reason cart.OutcomeReason) string {
	if outcome == cart.OutcomeConfirmed {
		return ""
	}
	switch reason {
	case cart.ReasonNoUPC, cart.ReasonNoMatch, cart.ReasonUnavailable,
		cart.ReasonAPIError, cart.ReasonBatchRejected, cart.ReasonInvalidLine,
		cart.ReasonProviderUnavailable, cart.ReasonReconnectRequired,
		cart.ReasonNoPerItemResult, cart.ReasonRequestIncomplete:
		return string(reason)
	case cart.ReasonNoProductIdentity:
		return string(cart.ReasonNoMatch)
	case cart.ReasonProviderRejected:
		return string(cart.ReasonBatchRejected)
	default:
		if outcome == cart.OutcomeUnknown {
			return string(cart.ReasonRequestIncomplete)
		}
		return string(cart.ReasonAPIError)
	}
}

// legacyProvisionOutcomeReason maps post-#484 engine reasons to the exact
// entries[].outcomeReason strings master emitted (string(entry.Reason) there).
func legacyProvisionOutcomeReason(outcome cart.Outcome, reason cart.OutcomeReason) string {
	if outcome == cart.OutcomeConfirmed {
		return ""
	}
	switch reason {
	case cart.ReasonNoUPC, cart.ReasonNoMatch, cart.ReasonNoProductIdentity:
		return "no_product_identity"
	case cart.ReasonBatchRejected, cart.ReasonProviderRejected, cart.ReasonInvalidLine,
		cart.ReasonUnavailable:
		return "provider_rejected"
	case cart.ReasonNoPerItemResult:
		return "no_per_item_result"
	case cart.ReasonRequestIncomplete, cart.ReasonReconnectRequired,
		cart.ReasonProviderUnavailable, cart.ReasonAPIError:
		return "request_incomplete"
	default:
		if outcome == cart.OutcomeUnknown {
			return "request_incomplete"
		}
		return "provider_rejected"
	}
}

func buildProvisionExportViews(entries []cart.EntryOutcome, confirm cart.ConfirmationCapability) (
	[]ProvisionEntry,
	[]provisionResultEntry,
	[]string,
	[]string,
	provisionSummaryCounts,
) {
	provisionEntries := make([]ProvisionEntry, 0, len(entries))
	results := make([]provisionResultEntry, 0, len(entries))
	failedItems := make([]string, 0)
	unknownItems := make([]string, 0)
	var counts provisionSummaryCounts

	for _, entry := range entries {
		status := provisionResultStatus(entry.Outcome, entry.Reason, confirm)
		reason := publicProvisionReason(entry.Outcome, entry.Reason)
		detail := entry.Detail
		legacyReason := legacyProvisionOutcomeReason(entry.Outcome, entry.Reason)

		provisionEntries = append(provisionEntries, ProvisionEntry{
			EntryID:       entry.EntryID,
			ItemID:        entry.ItemID,
			Name:          entry.Name,
			Quantity:      entry.Quantity,
			Outcome:       string(entry.Outcome),
			OutcomeReason: legacyReason,
			Detail:        detail,
		})
		results = append(results, provisionResultEntry{
			EntryID:  entry.EntryID,
			ItemID:   entry.ItemID,
			Name:     entry.Name,
			Quantity: entry.Quantity,
			Status:   status,
			Reason:   reason,
			Detail:   detail,
		})

		switch entry.Outcome {
		case cart.OutcomeFailed:
			failedItems = append(failedItems, entry.Name)
		case cart.OutcomeUnknown:
			unknownItems = append(unknownItems, entry.Name)
		}

		switch status {
		case "added":
			counts.AddedCount++
		case "sent":
			counts.SentCount++
		case "skipped":
			counts.SkippedCount++
		case "failed":
			counts.FailedCount++
		case "unknown":
			counts.UnknownCount++
		}
	}

	return provisionEntries, results, failedItems, unknownItems, counts
}
