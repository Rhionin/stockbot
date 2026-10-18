package server

import (
	"errors"
	"log"
	"net/http"

	"github.com/Rhionin/stockbot/internal/inventory"
	"github.com/Rhionin/stockbot/internal/scan"
)

// knownCommitFailure maps a scan-commit sentinel to the sentence and code the
// household sees. ok is false for an unexpected error, including a database
// failure; those must be logged and not copied into the response.
func knownCommitFailure(err error) (message, code string, ok bool) {
	var unstocked *scan.NoItemError
	switch {
	case errors.As(err, &unstocked), errors.Is(err, scan.ErrNoneOnHand):
		return inventory.NoneOnHandMessage, inventory.CodeNoneOnHand, true
	case errors.Is(err, inventory.ErrInstanceNotFound):
		return inventory.InstanceGoneMessage, inventory.CodeInstanceGone, true
	case errors.Is(err, scan.ErrNotPending):
		return inventory.NotPendingMessage, inventory.CodeNotPending, true
	case errors.Is(err, scan.ErrProductUnresolved):
		return inventory.ProductUnresolvedMessage, inventory.CodeProductUnresolved, true
	case errors.Is(err, scan.ErrScanNotFound):
		return inventory.ScanNotFoundMessage, inventory.CodeScanNotFound, true
	case errors.Is(err, scan.ErrDirectionUnset):
		return inventory.DirectionUnsetMessage, inventory.CodeDirectionUnset, true
	default:
		return "", "", false
	}
}

// explainCommitFailure is the sentence and code for one batch entry.
// An unrecognized error is logged and replaced with the internal sentence.
func explainCommitFailure(err error) (message, code string) {
	if message, code, ok := knownCommitFailure(err); ok {
		return message, code
	}
	log.Printf("scan commit failed: %s", err)
	return inventory.InternalMessage, inventory.CodeInternal
}

func commitStatus(err error) int {
	switch {
	case errors.Is(err, scan.ErrScanNotFound):
		return http.StatusNotFound
	case errors.Is(err, scan.ErrNotPending), errors.Is(err, scan.ErrDirectionUnset), errors.Is(err, scan.ErrProductUnresolved):
		return http.StatusBadRequest
	default:
		return http.StatusConflict
	}
}

// commitClientError is the HTTP error for a single scan commit or stock-out.
// Unexpected errors stay on the server log and answer with code internal.
func commitClientError(err error) error {
	if message, code, ok := knownCommitFailure(err); ok {
		return &HTTPError{Code: commitStatus(err), Message: message, ErrorCode: code, Err: err}
	}
	return &HTTPError{
		Code:      http.StatusInternalServerError,
		Message:   inventory.InternalMessage,
		ErrorCode: inventory.CodeInternal,
		Err:       err,
	}
}
