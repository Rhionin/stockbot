package server

import (
	"context"
	"database/sql"

	"github.com/Rhionin/stockbot/internal/cart"
	"github.com/Rhionin/stockbot/internal/inventory"
	"github.com/Rhionin/stockbot/internal/shopping"
	"github.com/Rhionin/stockbot/internal/store"
	"github.com/Rhionin/stockbot/internal/tenant"
)

func productIDByItemID(items []inventory.Item) map[string]string {
	out := make(map[string]string, len(items))
	for _, item := range items {
		out[item.ID] = item.ProductID
	}
	return out
}

func exportScopeFromUseItemIDs(useItemIDs map[string]string) map[string]struct{} {
	if len(useItemIDs) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(useItemIDs))
	for itemID := range useItemIDs {
		out[itemID] = struct{}{}
	}
	return out
}

func registryRegistered(registry *cart.Registry) store.ProviderRegistered {
	if registry == nil {
		return func(string) bool { return false }
	}
	return func(id string) bool {
		_, ok := registry.Get(cart.ProviderID(id))
		return ok
	}
}

func validateScopedCartSend(provision shoppingProvision, lineCtx *store.LineStoreContext, providerID string, scope map[string]struct{}, products map[string]string, registered store.ProviderRegistered) error {
	if scope == nil || lineCtx == nil {
		return nil
	}
	rowsByItem := map[string]shopping.ShoppingListItem{}
	for _, row := range provision.Rows {
		rowsByItem[row.ItemID] = row
	}
	for itemID := range scope {
		row, ok := rowsByItem[itemID]
		if !ok {
			continue
		}
		storeID := lineCtx.LineStore(row.RunStoreID, products[itemID])
		if !store.CanSendToCartForExport(lineCtx, storeID, providerID, registered) {
			return &HTTPError{Code: 422, Message: "This line is not for your online cart."}
		}
	}
	return nil
}

func lineCanSendToCart(lineCtx *store.LineStoreContext, row shopping.ShoppingListItem, productID, providerID string, scope map[string]struct{}, registered store.ProviderRegistered) bool {
	if scope != nil {
		if _, ok := scope[row.ItemID]; !ok {
			return false
		}
	}
	storeID := ""
	if lineCtx != nil {
		storeID = lineCtx.LineStore(row.RunStoreID, productID)
	}
	if registered != nil {
		return store.CanSendToCartForExport(lineCtx, storeID, providerID, registered)
	}
	return store.CanSendToCart(lineCtx, storeID, providerID)
}

func loadLineStoreContext(ctx context.Context, db *sql.DB, scope tenant.Scope) (*store.LineStoreContext, error) {
	if db == nil {
		return &store.LineStoreContext{}, nil
	}
	return store.LoadLineStoreContext(ctx, db, scope)
}
