package server

import (
	"context"
	"database/sql"
	"time"

	"github.com/Rhionin/stockbot/internal/cart"
	"github.com/Rhionin/stockbot/internal/cart/appcred"
	"github.com/Rhionin/stockbot/internal/shopping"
	"github.com/Rhionin/stockbot/internal/supply"
	"github.com/Rhionin/stockbot/internal/tenant"
)

// ShoppingListFillHandler handles POST /api/shopping-list/fill.
// One call snapshots the current supply plan into the staged cart.
// Lines the owner already changed stay. The response is that staged cart.
type ShoppingListFillHandler struct {
	ShoppingList shoppingPlanWriter
	Shopping     *shopping.Store
	Pantry       pantryLister
	Supply       *supply.Service
	Coalescer    *shopping.ListCoalescer
	Ledger       *cart.Ledger
	Registry     *cart.Registry
	Adjustments  interface {
		GetAdjustment(ctx context.Context, scope tenant.Scope, entryID, providerID string) (*shopping.Adjustment, error)
	}
	Credentials *appcred.Vault
	DB          *sql.DB
}

func (h *ShoppingListFillHandler) Handle(req Request[struct{}, struct{}]) ([]ShoppingListEntryResponse, error) {
	scope, err := requestScope(req.Context)
	if err != nil {
		return nil, InternalError(err)
	}

	now := time.Now()
	provision, err := loadShoppingSnapshot(req.Context, scope, h.Pantry, h.ShoppingList)
	if err != nil {
		return nil, InternalError(err)
	}
	if h.Supply != nil {
		planned, err := h.Supply.Plan(req.Context, scope, now)
		if err != nil {
			return nil, InternalError(err)
		}
		groupOf := map[string]string{}
		if h.ShoppingList != nil {
			if lookup, ok := h.ShoppingList.(interface {
				ItemGroups(ctx context.Context, scope tenant.Scope) (map[string]string, error)
			}); ok {
				groupOf, _ = lookup.ItemGroups(req.Context, scope)
			}
		}
		store := h.Shopping
		if store == nil {
			if s, ok := h.ShoppingList.(*shopping.Store); ok {
				store = s
			}
		}
		if store != nil {
			tripSeq := 0
			_ = h.DB.QueryRowContext(req.Context, `SELECT trip_seq FROM households WHERE id = ?`, scope.HouseholdID()).Scan(&tripSeq)
			actor := shopping.ActorFromContext(req.Context)
			if err := store.SavePlannedLinesWithChanges(req.Context, scope, plannedLines(planned, provision.Items), shelfMembers(provision.Items, groupOf), shopping.CauseListUpdated, actor, tripSeq, now); err != nil {
				return nil, InternalError(err)
			}
		} else if err := h.ShoppingList.SavePlannedLines(req.Context, scope, plannedLines(planned, provision.Items), shelfMembers(provision.Items, groupOf)); err != nil {
			return nil, InternalError(err)
		}
		rows, err := h.ShoppingList.ListUnpurchased(req.Context, scope)
		if err != nil {
			return nil, InternalError(err)
		}
		provision.Rows = rows
	} else {
		provision, err = stageShoppingPlan(req.Context, scope, h.Pantry, h.ShoppingList, h.Supply, now)
		if err != nil {
			return nil, InternalError(err)
		}
	}
	lineCtx, err := loadLineStoreContext(req.Context, h.DB, scope)
	if err != nil {
		return nil, InternalError(err)
	}
	return presentShoppingList(req, scope, provision, h.Ledger, h.Registry, h.Adjustments, lineCtx, h.Credentials)
}
