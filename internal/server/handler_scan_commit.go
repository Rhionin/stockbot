package server

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/Rhionin/stockbot/internal/inventory"
	"github.com/Rhionin/stockbot/internal/scan"
)

type ScanCommitHandler struct {
	Queue interface {
		GetScanEntry(ctx context.Context, id string) (*scan.ScanEntry, error)
		CommitStockIn(ctx context.Context, scanEntry *scan.ScanEntry) error
		CommitStockOut(ctx context.Context, scanEntry *scan.ScanEntry, instanceID *string) error
		CommitStockOutLink(ctx context.Context, scanEntry *scan.ScanEntry, instanceID *string, linkProductID string) error
		CommitBackfill(ctx context.Context, scanEntry *scan.ScanEntry) (string, int, error)
		CountOnHand(ctx context.Context, userID, productID string) (int, error)
	}
}

type commitScanRequest struct {
	InstanceID    *string `json:"instanceId,omitempty"`
	LinkProductID *string `json:"linkProductId,omitempty"`
	Backfill      bool    `json:"backfill,omitempty"`
}

type backfillResponse struct {
	Removed    int    `json:"removed"`
	Backfilled int    `json:"backfilled"`
	ItemID     string `json:"itemId"`
}

type commitScanPathParams struct {
	ID string `json:"id"`
}

func (h *ScanCommitHandler) Handle(req Request[commitScanRequest, commitScanPathParams]) (any, error) {
	if req.PathParams.ID == "" {
		return nil, BadRequest("scan entry id is required")
	}

	entry, err := h.Queue.GetScanEntry(req.Context, req.PathParams.ID)
	if err != nil {
		return nil, commitClientError(err)
	}
	if entry == nil {
		return nil, commitClientError(scan.ErrScanNotFound)
	}

	if entry.Status != scan.Pending {
		return nil, commitClientError(scan.ErrNotPending)
	}
	if entry.Direction == nil {
		return nil, commitClientError(scan.ErrDirectionUnset)
	}
	if req.Body.Backfill {
		return h.commitBackfill(req.Context, entry)
	}
	if entry.ProductID == nil || *entry.ProductID == "" {
		return nil, commitClientError(scan.ErrProductUnresolved)
	}

	linkProductID := ""
	if req.Body.LinkProductID != nil {
		linkProductID = strings.TrimSpace(*req.Body.LinkProductID)
	}

	// A link is an explicit choice: the server never attaches a barcode on
	// its own when the household has no item.
	if *entry.Direction == scan.StockIn {
		if linkProductID != "" {
			return nil, BadRequest("linking a barcode is only available when scanning out")
		}
		if err := h.Queue.CommitStockIn(req.Context, entry); err != nil {
			return nil, commitClientError(err)
		}
	} else if linkProductID != "" {
		if err := h.Queue.CommitStockOutLink(req.Context, entry, req.Body.InstanceID, linkProductID); err != nil {
			var empty *scan.EmptyShelfLink
			if errors.As(err, &empty) {
				return nil, ShelfCount(http.StatusConflict, inventory.NoneOnHandMessage, inventory.CodeNoneOnHand, empty.ItemID, 0)
			}
			return nil, stockOutError(err)
		}
	} else if err := h.Queue.CommitStockOut(req.Context, entry, req.Body.InstanceID); err != nil {
		return nil, stockOutError(err)
	}

	committed, err := h.Queue.GetScanEntry(req.Context, req.PathParams.ID)
	if err != nil {
		return nil, commitClientError(err)
	}
	if linkProductID != "" && committed != nil {
		onHand, err := h.Queue.CountOnHand(req.Context, entry.UserID, linkProductID)
		if err != nil {
			return nil, commitClientError(err)
		}
		committed.OnHand = &onHand
	}

	return committed, nil
}

func (h *ScanCommitHandler) commitBackfill(ctx context.Context, entry *scan.ScanEntry) (any, error) {
	if entry.ProductID == nil || *entry.ProductID == "" {
		return nil, commitClientError(scan.ErrProductUnresolved)
	}
	itemID, units, err := h.Queue.CommitBackfill(ctx, entry)
	if err != nil {
		var stocked *scan.UnitsOnHandError
		if errors.As(err, &stocked) {
			return nil, ShelfCount(http.StatusBadRequest, inventory.UnitsOnHandMessage, inventory.CodeUnitsOnHand, stocked.ItemID, stocked.OnHand)
		}
		return nil, commitClientError(err)
	}
	return backfillResponse{Removed: units, Backfilled: units, ItemID: itemID}, nil
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
