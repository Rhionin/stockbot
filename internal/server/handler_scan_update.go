package server

import (
	"context"
	"errors"
	"time"

	"github.com/Rhionin/stockbot/internal/scan"
)

type ScanUpdateHandler struct {
	Queue interface {
		UpdateScanEntry(ctx context.Context, id string, direction *scan.ScanDirection, unitCount *int, expiresAt *time.Time, productID *string, status *scan.ScanStatus) error
		GetScanEntry(ctx context.Context, id string) (*scan.ScanEntry, error)
		ResolveFlaggedEntry(ctx context.Context, scanEntryID, productID string) error
	}
}

type updateScanRequest struct {
	Direction *scan.ScanDirection `json:"direction,omitempty"`
	UnitCount *int                `json:"unitCount,omitempty"`
	ExpiresAt *time.Time          `json:"expiresAt,omitempty"`
	ProductID *string             `json:"productId,omitempty"`
	Status    *scan.ScanStatus    `json:"status,omitempty"`
}

type updateScanPathParams struct {
	ID string `json:"id"`
}

func (h *ScanUpdateHandler) Handle(req Request[updateScanRequest, updateScanPathParams]) (*scan.ScanEntry, error) {
	if req.PathParams.ID == "" {
		return nil, BadRequest("scan entry id is required")
	}

	// Validate status if provided
	if req.Body.Status != nil {
		status := *req.Body.Status
		if status != scan.Pending && status != scan.Flagged && status != scan.Committed && status != scan.Cancelled {
			return nil, BadRequest("invalid status value")
		}
	}

	// Validate direction if provided
	if req.Body.Direction != nil {
		dir := *req.Body.Direction
		if dir != scan.StockIn && dir != scan.StockOut {
			return nil, BadRequest("invalid direction value")
		}
	}

	// Check if this is a flagged entry resolution
	// (updating productID and optionally status to pending on a flagged entry)
	if req.Body.ProductID != nil {
		entry, err := h.Queue.GetScanEntry(req.Context, req.PathParams.ID)
		if err != nil {
			return nil, InternalError(err)
		}
		if entry == nil {
			return nil, NotFound("scan entry not found")
		}

		// If the entry is currently flagged and we're setting a productID,
		// use ResolveFlaggedEntry which handles barcode override creation
		if entry.Status == scan.Flagged {
			err := h.Queue.ResolveFlaggedEntry(req.Context, req.PathParams.ID, *req.Body.ProductID)
			if err != nil {
				return nil, InternalError(err)
			}

			// Return the resolved entry
			resolved, err := h.Queue.GetScanEntry(req.Context, req.PathParams.ID)
			if err != nil {
				return nil, InternalError(err)
			}
			return resolved, nil
		}
	}

	// Standard update path for non-resolution updates
	err := h.Queue.UpdateScanEntry(
		req.Context,
		req.PathParams.ID,
		req.Body.Direction,
		req.Body.UnitCount,
		req.Body.ExpiresAt,
		req.Body.ProductID,
		req.Body.Status,
	)
	if err != nil {
		if errors.Is(err, scan.ErrNotOpen) {
			return nil, ConflictDetails(scan.NotOpenMessage, "not_open", nil)
		}
		return nil, InternalError(err)
	}

	// Return the updated entry
	entry, err := h.Queue.GetScanEntry(req.Context, req.PathParams.ID)
	if err != nil {
		return nil, InternalError(err)
	}
	if entry == nil {
		return nil, NotFound("scan entry not found")
	}

	return entry, nil
}
