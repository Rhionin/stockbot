package server

import (
	"context"
	"errors"
	"time"

	"github.com/Rhionin/stockbot/internal/inventory"
	"github.com/Rhionin/stockbot/internal/scan"
)

type InventoryStockOutHandler struct {
	Queue interface {
		StockOut(ctx context.Context, itemID string, at time.Time) error
	}
}

type inventoryStockOutPathParams struct {
	ItemID string `json:"itemId"`
}

func (h *InventoryStockOutHandler) Handle(req Request[struct{}, inventoryStockOutPathParams]) (struct{}, error) {
	err := h.Queue.StockOut(req.Context, req.PathParams.ItemID, time.Now().UTC())
	if err != nil {
		if errors.Is(err, scan.ErrNoneOnHand) {
			return struct{}{}, ConflictDetails(inventory.NoneOnHandMessage, inventory.CodeNoneOnHand, nil)
		}
		if errors.Is(err, scan.ErrItemNotFound) {
			return struct{}{}, NotFound("item not found")
		}
		return struct{}{}, InternalError(err)
	}
	return struct{}{}, nil
}
