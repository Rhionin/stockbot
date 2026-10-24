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
		StockOut(ctx context.Context, scope tenant.Scope, itemID string, at time.Time) error
	}
}

type inventoryStockOutPathParams struct {
	ItemID string `json:"itemId"`
}

func (h *InventoryStockOutHandler) Handle(req Request[struct{}, inventoryStockOutPathParams]) (struct{}, error) {
	scope, err := requestScope(req.Context)
	if err != nil {
		return struct{}{}, err
	}
	err = h.Queue.StockOut(req.Context, scope, req.PathParams.ItemID, time.Now().UTC())
	if err != nil {
		if errors.Is(err, scan.ErrItemNotFound) {
			return struct{}{}, NotFound("item not found")
		}
		return struct{}{}, commitClientError(err)
	}
	return struct{}{}, nil
}
