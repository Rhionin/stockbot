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

	// Validate status if provided. Committed is not a patch: the commit
	// endpoint is the only way a scan becomes committed and stock moves.
	if req.Body.Status != nil {
		status := *req.Body.Status
		if status != scan.Pending && status != scan.Flagged && status != scan.Cancelled {
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

	// A product id on a flagged scan resolves it. The status read and the
	// write share one transaction, so a remove that commits first is
	// not_open and does not write the override.
	if req.Body.ProductID != nil {
		err := h.Queue.ResolveFlaggedEntry(req.Context, req.PathParams.ID, *req.Body.ProductID)
		if err == nil {
			resolved, err := h.Queue.GetScanEntry(req.Context, req.PathParams.ID)
			if err != nil {
				return nil, InternalError(err)
			}
			if resolved == nil {
				return nil, NotFound(scan.ErrNotFound.Error())
			}
			return resolved, nil
		}
		if errors.Is(err, scan.ErrNotOpen) {
			return nil, ConflictDetails(scan.ErrNotOpen.Error(), "not_open", nil)
		}
		if errors.Is(err, scan.ErrNotFound) {
			return nil, NotFound(scan.ErrNotFound.Error())
		}
		if errors.Is(err, scan.ErrProductNotResolved) {
			return nil, BadRequest(scan.ErrProductNotResolved.Error())
		}
		if !errors.Is(err, scan.ErrNotFlagged) {
			return nil, InternalError(err)
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
			return nil, ConflictDetails(scan.ErrNotOpen.Error(), "not_open", nil)
		}
		if errors.Is(err, scan.ErrNotFound) {
			return nil, NotFound(scan.ErrNotFound.Error())
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
