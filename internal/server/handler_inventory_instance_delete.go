package server

import (
	"context"
	"errors"
	"time"

	"github.com/Rhionin/stockbot/internal/inventory"
	"github.com/Rhionin/stockbot/internal/tenant"
)

type InventoryInstanceDeleteHandler struct {
	Queue interface {
		StockOutInstance(ctx context.Context, scope tenant.Scope, instanceID string, at time.Time) error
	}
}

type inventoryInstanceDeletePathParams struct {
	InstanceID string `json:"instanceId"`
}

func (h *InventoryInstanceDeleteHandler) Handle(req Request[struct{}, inventoryInstanceDeletePathParams]) (struct{}, error) {
	scope, err := requestScope(req.Context)
	if err != nil {
		return struct{}{}, err
	}
	err = h.Queue.StockOutInstance(req.Context, scope, req.PathParams.InstanceID, time.Now().UTC())
	if err != nil {
		if errors.Is(err, inventory.ErrInstanceNotFound) {
			return struct{}{}, NotFound("item instance not found")
		}
		return struct{}{}, InternalError(err)
	}
	return struct{}{}, nil
}
