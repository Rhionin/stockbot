package server

import (
	"context"
	"errors"
	"time"

	"github.com/Rhionin/stockbot/internal/scan"
	"github.com/Rhionin/stockbot/internal/tenant"
)

type InventoryStockOutHandler struct {
	Queue interface {
		StockOut(ctx context.Context, scope tenant.Scope, itemID string, at time.Time) (string, error)
		BackfillItem(ctx context.Context, scope tenant.Scope, itemID string, at time.Time) (scan.BackfillResult, error)
	}
}

type inventoryStockOutPathParams struct {
	ItemID string `json:"itemId"`
}

type inventoryStockOutRequest struct {
	Backfill bool `json:"backfill,omitempty"`
}

type inventoryStockOutResponse struct {
	StockOutMoveID string        `json:"stockOutMoveId,omitempty"`
	Backfill       *backfillMoves `json:"backfill,omitempty"`
}

func (h *InventoryStockOutHandler) Handle(req Request[inventoryStockOutRequest, inventoryStockOutPathParams]) (inventoryStockOutResponse, error) {
	scope, err := requestScope(req.Context)
	if err != nil {
		return inventoryStockOutResponse{}, err
	}
	at := time.Now().UTC()
	if req.Body.Backfill {
		result, err := h.Queue.BackfillItem(req.Context, scope, req.PathParams.ItemID, at)
		if err != nil {
			if errors.Is(err, scan.ErrItemNotFound) {
				return inventoryStockOutResponse{}, NotFound("item not found")
			}
			return inventoryStockOutResponse{}, commitClientError(err)
		}
		return inventoryStockOutResponse{
			Backfill: &backfillMoves{
				StockInMoveID:  result.StockInMoveID,
				StockOutMoveID: result.StockOutMoveID,
			},
		}, nil
	}
	moveID, err := h.Queue.StockOut(req.Context, scope, req.PathParams.ItemID, at)
	if err != nil {
		if errors.Is(err, scan.ErrItemNotFound) {
			return inventoryStockOutResponse{}, NotFound("item not found")
		}
		return inventoryStockOutResponse{}, commitClientError(err)
	}
	return inventoryStockOutResponse{StockOutMoveID: moveID}, nil
}
