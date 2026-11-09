package server

import (
	"context"
	"database/sql"
	"time"

	"github.com/Rhionin/stockbot/internal/cart"
	"github.com/Rhionin/stockbot/internal/shopping"
	"github.com/Rhionin/stockbot/internal/supply"
	"github.com/Rhionin/stockbot/internal/tenant"
)

// ShoppingListFillHandler handles POST /api/shopping-list/fill.
// One call snapshots the current supply plan into the staged cart.
// Lines the owner already changed stay. The response is that staged cart.
type ShoppingListFillHandler struct {
	ShoppingList shoppingPlanWriter
	Pantry       pantryLister
	Supply       *supply.Service
	Ledger       *cart.Ledger
	Registry     *cart.Registry
	Adjustments  interface {
		GetAdjustment(ctx context.Context, scope tenant.Scope, entryID, providerID string) (*shopping.Adjustment, error)
	}
	DB *sql.DB
}

func (h *ShoppingListFillHandler) Handle(req Request[struct{}, struct{}]) ([]ShoppingListEntryResponse, error) {
	scope, err := requestScope(req.Context)
	if err != nil {
		return nil, InternalError(err)
	}

	provision, err := stageShoppingPlan(req.Context, scope, h.Pantry, h.ShoppingList, h.Supply, time.Now())
	if err != nil {
		return nil, InternalError(err)
	}
	lineCtx, err := loadLineStoreContext(req.Context, h.DB, scope)
	if err != nil {
		return nil, InternalError(err)
	}
	return presentShoppingList(req, scope, provision, h.Ledger, h.Registry, h.Adjustments, lineCtx)
}
