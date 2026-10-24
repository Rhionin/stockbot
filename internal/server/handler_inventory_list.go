package server

import (
	"context"
	"time"

	"github.com/Rhionin/stockbot/internal/inventory"
	"github.com/Rhionin/stockbot/internal/tenant"
)

type InventoryListHandler struct {
	Pantry interface {
		GetInventoryList(ctx context.Context, scope tenant.Scope, now time.Time, warningDays int, query string) ([]inventory.InventoryItem, error)
	}
}

func (h *InventoryListHandler) Handle(req Request[struct{}, struct{}]) ([]inventory.InventoryItem, error) {
	scope, err := requestScope(req.Context)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	warningDays := inventory.DefaultWarningDays

	// Extract query parameter from raw request
	query := req.RawRequest.URL.Query().Get("q")

	return h.Pantry.GetInventoryList(req.Context, scope, now, warningDays, query)
}
