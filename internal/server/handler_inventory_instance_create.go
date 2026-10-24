package server

import (
	"context"
	"errors"
	"time"

	"github.com/Rhionin/stockbot/internal/inventory"
	"github.com/Rhionin/stockbot/internal/scan"
	"github.com/Rhionin/stockbot/internal/tenant"
)

type InventoryInstanceCreateHandler struct {
	Queue interface {
		StockIn(ctx context.Context, scope tenant.Scope, itemID string, at time.Time, expiresAt *time.Time) (*inventory.ItemInstance, error)
	}
}

type inventoryInstanceCreatePathParams struct {
	ItemID string `json:"itemId"`
}

type inventoryInstanceCreateRequest struct {
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
}

func (h *InventoryInstanceCreateHandler) Handle(req Request[inventoryInstanceCreateRequest, inventoryInstanceCreatePathParams]) (Created, error) {
	scope, err := requestScope(req.Context)
	if err != nil {
		return Created{}, err
	}
	created, err := h.Queue.StockIn(req.Context, scope, req.PathParams.ItemID, time.Now().UTC(), req.Body.ExpiresAt)
	if err != nil {
		if errors.Is(err, scan.ErrItemNotFound) {
			return Created{}, NotFound("item not found")
		}
		return Created{}, InternalError(err)
	}

	return Created{Value: created}, nil
}
