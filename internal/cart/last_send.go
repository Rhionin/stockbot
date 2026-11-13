package cart

import (
	"database/sql"
	"time"
)

// lastSendFromStored is the only constructor for cart.LastSend (reads and API consumers via LatestResults).
func lastSendFromStored(providerID string, status PublicStatus, storedReason sql.NullString, quantity int, attemptedAt string) LastSend {
	ls := LastSend{
		ProviderID:  providerID,
		Status:      status,
		Quantity:    quantity,
		AttemptedAt: attemptedAt,
	}
	if status == PublicStatusAdded || status == PublicStatusSent {
		return ls
	}
	if storedReason.Valid && storedReason.String != "" {
		ls.Reason = PublicReason(storedReason.String)
	}
	return ls
}

// storedLineReason is the reason written to cart_line_results (nil for added/sent).
func storedLineReason(status PublicStatus, raw PublicReason) PublicReason {
	if status == PublicStatusAdded || status == PublicStatusSent {
		return ""
	}
	if raw == PublicReasonProviderUnavailable {
		// provider_unavailable is whole-request only; per-line rows use api_error.
		return PublicReasonAPIError
	}
	return raw
}

// formatSendAttemptedAt formats one send instant for storage (RFC3339 UTC).
func formatSendAttemptedAt(at time.Time) string {
	return at.UTC().Format(time.RFC3339)
}
