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
}

type provisionSummaryCounts struct {
	AddedCount    int `json:"addedCount"`
	SkippedCount  int `json:"skippedCount"`
	FailedCount   int `json:"failedCount"`
	UnknownCount  int `json:"unknownCount"`
}

func provisionStatus(outcome cart.Outcome, reason cart.OutcomeReason) string {
	switch outcome {
	case cart.OutcomeConfirmed:
		return "added"
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
		cart.ReasonAPIError, cart.ReasonNoPerItemResult:
		return string(reason)
	case cart.ReasonProviderRejected, cart.ReasonRequestIncomplete:
		return string(cart.ReasonAPIError)
	case cart.ReasonNoProductIdentity:
		return string(cart.ReasonNoMatch)
	default:
		if outcome == cart.OutcomeUnknown {
			return string(cart.ReasonNoPerItemResult)
		}
		return string(cart.ReasonAPIError)
	}
}

func buildProvisionExportViews(entries []cart.EntryOutcome) (
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
		status := provisionStatus(entry.Outcome, entry.Reason)
		reason := publicProvisionReason(entry.Outcome, entry.Reason)

		provisionEntries = append(provisionEntries, ProvisionEntry{
			EntryID:       entry.EntryID,
			ItemID:        entry.ItemID,
			Name:          entry.Name,
			Quantity:      entry.Quantity,
			Outcome:       string(entry.Outcome),
			OutcomeReason: reason,
		})
		results = append(results, provisionResultEntry{
			EntryID:  entry.EntryID,
			ItemID:   entry.ItemID,
			Name:     entry.Name,
			Quantity: entry.Quantity,
			Status:   status,
			Reason:   reason,
		})

		switch status {
		case "added":
			counts.AddedCount++
		case "skipped":
			counts.SkippedCount++
		case "failed":
			counts.FailedCount++
			failedItems = append(failedItems, entry.Name)
		case "unknown":
			counts.UnknownCount++
			unknownItems = append(unknownItems, entry.Name)
		}
	}

	return provisionEntries, results, failedItems, unknownItems, counts
}
