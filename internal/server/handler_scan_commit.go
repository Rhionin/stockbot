package server

import (
	"context"
	"errors"
	"strings"

	"github.com/Rhionin/stockbot/internal/scan"
)

type ScanCommitHandler struct {
	Queue interface {
		GetScanEntry(ctx context.Context, id string) (*scan.ScanEntry, error)
		CommitStockIn(ctx context.Context, scanEntry *scan.ScanEntry) error
		CommitStockOut(ctx context.Context, scanEntry *scan.ScanEntry, instanceID *string) error
		CommitStockOutLink(ctx context.Context, scanEntry *scan.ScanEntry, instanceID *string, linkProductID string) error
	}
}

type commitScanRequest struct {
	InstanceID    *string `json:"instanceId,omitempty"`
	LinkProductID *string `json:"linkProductId,omitempty"`
}

type commitScanPathParams struct {
	ID string `json:"id"`
}

func (h *ScanCommitHandler) Handle(req Request[commitScanRequest, commitScanPathParams]) (*scan.ScanEntry, error) {
	if req.PathParams.ID == "" {
		return nil, BadRequest("scan entry id is required")
	}

	// Get the scan entry
	entry, err := h.Queue.GetScanEntry(req.Context, req.PathParams.ID)
	if err != nil {
		return nil, commitClientError(err)
	}
	if entry == nil {
		return nil, commitClientError(scan.ErrScanNotFound)
	}

	// Validate the entry can be committed
	if entry.Status != scan.Pending {
		return nil, commitClientError(scan.ErrNotPending)
	}
	if entry.Direction == nil {
		return nil, commitClientError(scan.ErrDirectionUnset)
	}
	if entry.ProductID == nil || *entry.ProductID == "" {
		return nil, commitClientError(scan.ErrProductUnresolved)
	}

	linkProductID := ""
	if req.Body.LinkProductID != nil {
		linkProductID = strings.TrimSpace(*req.Body.LinkProductID)
	}

	// Commit based on direction. A link is an explicit choice: the server
	// never attaches a barcode on its own when the household has no item.
	if *entry.Direction == scan.StockIn {
		if linkProductID != "" {
			return nil, BadRequest("linking a barcode is only available when scanning out")
		}
		if err := h.Queue.CommitStockIn(req.Context, entry); err != nil {
			return nil, commitClientError(err)
		}
	} else if linkProductID != "" {
		if err := h.Queue.CommitStockOutLink(req.Context, entry, req.Body.InstanceID, linkProductID); err != nil {
			return nil, stockOutError(err)
		}
	} else if err := h.Queue.CommitStockOut(req.Context, entry, req.Body.InstanceID); err != nil {
		return nil, stockOutError(err)
	}

	// Return the committed entry
	committed, err := h.Queue.GetScanEntry(req.Context, req.PathParams.ID)
	if err != nil {
		return nil, commitClientError(err)
	}

	return committed, nil
}

func stockOutError(err error) error {
	var missing *scan.NoItemError
	if errors.As(err, &missing) {
		var suggestions any
		if len(missing.Suggestions) > 0 {
			suggestions = missing.Suggestions
		}
		return ConflictSuggestions("Not in your pantry", "no_item", suggestions)
	}
	if errors.Is(err, scan.ErrLinkNotStocked) {
		return BadRequest("That product is not in your pantry")
	}
	return commitClientError(err)
}
