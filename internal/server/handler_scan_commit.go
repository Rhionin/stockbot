package server

import (
	"context"
	"errors"
	"strings"

	"github.com/Rhionin/stockbot/internal/inventory"
	"github.com/Rhionin/stockbot/internal/scan"
)

type ScanCommitHandler struct {
	Queue interface {
		GetScanEntry(ctx context.Context, id string) (*scan.ScanEntry, error)
		CommitStockIn(ctx context.Context, scanEntry *scan.ScanEntry) error
		CommitStockOut(ctx context.Context, scanEntry *scan.ScanEntry, instanceID *string) (int, error)
		CommitStockOutLink(ctx context.Context, scanEntry *scan.ScanEntry, instanceID *string, linkProductID string) (int, error)
	}
}

// commitScanResponse is the committed scan. Every successful stock-out 200
// also carries removedCount (units that left the shelf) and requestedCount
// (the scan's unit count). Stock-in omits both.
type commitScanResponse struct {
	scan.ScanEntry
	RemovedCount   *int `json:"removedCount,omitempty"`
	RequestedCount *int `json:"requestedCount,omitempty"`
}

type commitScanRequest struct {
	InstanceID    *string `json:"instanceId,omitempty"`
	LinkProductID *string `json:"linkProductId,omitempty"`
}

type commitScanPathParams struct {
	ID string `json:"id"`
}

func (h *ScanCommitHandler) Handle(req Request[commitScanRequest, commitScanPathParams]) (*commitScanResponse, error) {
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

	// An open scan is pending or flagged. qr, cancelled, and committed are not.
	if !scan.OpenForCommit(entry.Status) {
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
	var removed int
	stockOut := *entry.Direction != scan.StockIn
	if *entry.Direction == scan.StockIn {
		if linkProductID != "" {
			return nil, BadRequest("linking a barcode is only available when scanning out")
		}
		if err := h.Queue.CommitStockIn(req.Context, entry); err != nil {
			return nil, commitClientError(err)
		}
	} else if linkProductID != "" {
		var err error
		removed, err = h.Queue.CommitStockOutLink(req.Context, entry, req.Body.InstanceID, linkProductID)
		if err != nil {
			return nil, stockOutError(err)
		}
	} else {
		var err error
		removed, err = h.Queue.CommitStockOut(req.Context, entry, req.Body.InstanceID)
		if err != nil {
			return nil, stockOutError(err)
		}
	}

	committed, err := h.Queue.GetScanEntry(req.Context, req.PathParams.ID)
	if err != nil {
		return nil, commitClientError(err)
	}
	if committed == nil {
		return nil, nil
	}

	resp := &commitScanResponse{ScanEntry: *committed}
	if stockOut {
		requested := entry.UnitCount
		resp.RemovedCount = &removed
		resp.RequestedCount = &requested
	}
	return resp, nil
}

func stockOutError(err error) error {
	var missing *scan.NoItemError
	if errors.As(err, &missing) {
		var suggestions any
		if len(missing.Suggestions) > 0 {
			suggestions = missing.Suggestions
		}
		return ConflictSuggestions(inventory.WhichItemMessage, inventory.CodeNoItem, suggestions)
	}
	return commitClientError(err)
}
