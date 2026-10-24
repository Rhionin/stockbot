package server

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/Rhionin/stockbot/internal/inventory"
	"github.com/Rhionin/stockbot/internal/tenant"
)

type InventoryListHandler struct {
	Pantry interface {
		GetInventoryList(ctx context.Context, scope tenant.Scope, now time.Time, warningDays int, query string) ([]inventory.InventoryItem, error)
	}
	DB *sql.DB
}

func (h *InventoryListHandler) Handle(req Request[struct{}, struct{}]) (changeView, error) {
	scope, err := requestScope(req.Context)
	if err != nil {
		return changeView{}, err
	}

	now := time.Now()
	warningDays := inventory.DefaultWarningDays

	query := req.RawRequest.URL.Query().Get("q")
	since := strings.TrimSpace(req.RawRequest.URL.Query().Get("since"))
	items, err := h.Pantry.GetInventoryList(req.Context, scope, now, warningDays, query)
	if err != nil {
		return changeView{}, InternalError(err)
	}
	if items == nil {
		items = []inventory.InventoryItem{}
	}
	view, err := viewChanges(req.Context, h.DB, scope.HouseholdID(), "stock", since, "items", items)
	if err != nil {
		return changeView{}, InternalError(err)
	}
	return view, nil
}
