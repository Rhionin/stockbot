package server

import (
	"context"
	"database/sql"

	"github.com/Rhionin/stockbot/internal/cart"
	"github.com/Rhionin/stockbot/internal/cart/appcred"
	"github.com/Rhionin/stockbot/internal/shopping"
	"github.com/Rhionin/stockbot/internal/supply"
	"github.com/Rhionin/stockbot/internal/tenant"
)

// ShoppingListSummaryHandler handles GET /api/shopping-list/summary.
type ShoppingListSummaryHandler struct {
	ShoppingList shoppingListReader
	Pantry       pantryLister
	Ledger       *cart.Ledger
	Registry     *cart.Registry
	Adjustments  interface {
		GetAdjustment(ctx context.Context, scope tenant.Scope, entryID, providerID string) (*shopping.Adjustment, error)
	}
	Credentials *appcred.Vault
	DB          *sql.DB
	ReadDB      *sql.DB
	Supply      *supply.Service
	Coalescer   *shopping.ListCoalescer
	Shopping    *shopping.Store
}

func (h *ShoppingListSummaryHandler) Handle(req Request[struct{}, struct{}]) (ShoppingListSummaryW3Response, error) {
	scope, err := requestScope(req.Context)
	if err != nil {
		return ShoppingListSummaryW3Response{}, InternalError(err)
	}
	asm := listAssembler{
		Coalescer: h.Coalescer, Supply: h.Supply, SupplyRead: supply.Open(h.ReadDB),
		Shopping: h.Shopping, Pantry: h.Pantry, Ledger: h.Ledger, Registry: h.Registry,
		Adjustments: h.Adjustments, Credentials: h.Credentials, DB: h.DB, ReadDB: h.ReadDB,
	}
	if h.Shopping == nil {
		if s, ok := h.ShoppingList.(*shopping.Store); ok {
			asm.Shopping = s
		}
	}
	built, err := asm.assembleShoppingListW3(req, scope, true)
	if err != nil {
		return ShoppingListSummaryW3Response{}, err
	}
	short := countW3ShortNow(built.Items)
	attention := 0
	for _, g := range built.AttentionGroups {
		attention += g.Count
	}
	return ShoppingListSummaryW3Response{
		ShortNow: short, NeedsAttention: attention, NotOnList: countW3NotOnList(built.OffList),
		Cursor: built.Cursor, ListStale: built.ListStale, AttentionGroups: built.AttentionGroups,
		LastTripEndedAt: built.LastTripEndedAt, LastTripEndedBy: built.LastTripEndedBy, StoreDisabled: built.StoreDisabled,
	}, nil
}