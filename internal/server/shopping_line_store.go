package server

import (
	"context"
	"database/sql"
	"strings"

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

func exportScopeFromItemIDs(itemIDs []string) map[string]struct{} {
	if len(itemIDs) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(itemIDs))
	for _, itemID := range itemIDs {
		itemID = strings.TrimSpace(itemID)
		if itemID == "" {
			continue
		}
		out[itemID] = struct{}{}
	}
	if len(out) == 0 {
		return nil
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

type providerCredentialVault interface {
	HasApp(ctx context.Context, scope tenant.Scope, providerID string) (bool, error)
}

func householdProviderConfigured(ctx context.Context, scope tenant.Scope, providerID string, vault providerCredentialVault) (bool, error) {
	if providerID == "" || vault == nil {
		return false, nil
	}
	return vault.HasApp(ctx, scope, providerID)
}

func providerGate(ctx context.Context, scope tenant.Scope, registry *cart.Registry, vault providerCredentialVault) store.ProviderGate {
	reg := registryRegistered(registry)
	configured := func(id string) bool {
		ok, err := householdProviderConfigured(ctx, scope, id, vault)
		return err == nil && ok
	}
	sole := func() (string, bool) {
		if registry == nil {
			return "", false
		}
		var found string
		count := 0
		for _, id := range registry.List() {
			sid := string(id)
			if !reg(sid) || !configured(sid) {
				continue
			}
			found = sid
			count++
		}
		if count == 1 {
			return found, true
		}
		return "", false
	}
	return store.ProviderGate{Registered: reg, Configured: configured, SoleConfigured: sole}
}

func soleHouseholdConfiguredProvider(ctx context.Context, scope tenant.Scope, registry *cart.Registry, vault providerCredentialVault) (string, bool) {
	gate := providerGate(ctx, scope, registry, vault)
	if gate.SoleConfigured == nil {
		return "", false
	}
	return gate.SoleConfigured()
}

func validateExportItemIDs(provision shoppingProvision, scope map[string]struct{}) error {
	if scope == nil {
		return nil
	}
	known := make(map[string]struct{}, len(provision.Rows))
	for _, row := range provision.Rows {
		known[row.ItemID] = struct{}{}
	}
	for itemID := range scope {
		if _, ok := known[itemID]; !ok {
			return &HTTPError{Code: 422, Message: "That item isn't on your shopping list."}
		}
	}
	return nil
}

func validateUseItemIDsSwap(provision shoppingProvision, useItemIDs map[string]string) error {
	if len(useItemIDs) == 0 {
		return nil
	}
	for _, entry := range provision.Rows {
		if useID := useItemIDs[entry.ItemID]; useID != "" && useID != entry.ItemID {
			return &HTTPError{Code: 422, Message: "Choose a product on this line."}
		}
	}
	return nil
}

func validateScopedExportCart(provision shoppingProvision, lineCtx *store.LineStoreContext, providerID string, scope map[string]struct{}, products map[string]string, gate store.ProviderGate) error {
	if scope == nil {
		return nil
	}
	rowsByItem := map[string]shopping.ShoppingListItem{}
	for _, row := range provision.Rows {
		rowsByItem[row.ItemID] = row
	}
	for itemID := range scope {
		row := rowsByItem[itemID]
		storeID := ""
		if lineCtx != nil {
			storeID = lineCtx.LineStore(row.RunStoreID, products[itemID])
		}
		if !store.CanSendToCart(lineCtx, storeID, providerID, gate) {
			return &HTTPError{Code: 422, Message: "This line is not for your online cart."}
		}
	}
	return nil
}

func lineCanSendToCart(lineCtx *store.LineStoreContext, row shopping.ShoppingListItem, productID, providerID string, scope map[string]struct{}, gate store.ProviderGate) bool {
	if scope != nil {
		if _, ok := scope[row.ItemID]; !ok {
			return false
		}
	}
	storeID := ""
	if lineCtx != nil {
		storeID = lineCtx.LineStore(row.RunStoreID, productID)
	}
	return store.CanSendToCart(lineCtx, storeID, providerID, gate)
}

func loadLineStoreContext(ctx context.Context, db *sql.DB, scope tenant.Scope) (*store.LineStoreContext, error) {
	if db == nil {
		return &store.LineStoreContext{}, nil
	}
	return store.LoadLineStoreContext(ctx, db, scope)
}
