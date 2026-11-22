package server

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/Rhionin/stockbot/internal/attention"
	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/Rhionin/stockbot/internal/cart"
	"github.com/Rhionin/stockbot/internal/cart/connection"
	"github.com/Rhionin/stockbot/internal/shopping"
	"github.com/Rhionin/stockbot/internal/store"
	"github.com/Rhionin/stockbot/internal/supply"
	"github.com/Rhionin/stockbot/internal/tenant"
)

func providerAuthCapability(registry *cart.Registry, providerID string) cart.AuthCapability {
	if registry == nil || providerID == "" {
		return cart.AuthNone
	}
	p, ok := registry.Get(cart.ProviderID(providerID))
	if !ok {
		return cart.AuthNone
	}
	return p.Capabilities().Auth
}

func krogerSendingDisabled(providerID string, env ProviderEnv) bool {
	return providerID == "kroger" && env.Disabled
}

func storeDisabledResponse(providerID string, env ProviderEnv) *StoreDisabledResponse {
	if krogerSendingDisabled(providerID, env) {
		return &StoreDisabledResponse{ProviderID: "kroger"}
	}
	return nil
}

func connectAllowedForViewer(ctx context.Context, authSvc *auth.Service, householdID string) (bool, error) {
	if authSvc == nil {
		return true, nil
	}
	return auth.HouseholdOwner(ctx, authSvc, householdID)
}

func loadConnFactForAttention(
	ctx context.Context,
	db *sql.DB,
	scope tenant.Scope,
	providerID string,
	auth cart.AuthCapability,
	suppressed bool,
) (attention.Reason, error) {
	if suppressed || providerID == "" || auth == cart.AuthNone {
		return "", nil
	}
	if err := attention.BeginConnFactLoad(); err != nil {
		return "", err
	}
	if err := scope.Require(); err != nil {
		return "", err
	}
	conn, err := connection.NewDirectory(db).Read(ctx, scope, providerID)
	if err != nil {
		return "", fmt.Errorf("could not load provider connection: %w", err)
	}
	block := cart.ConnectionBlock(auth, conn)
	if block == "" {
		return "", attention.EndConnFactLoad()
	}
	return attention.Reason(block), attention.EndConnFactLoad()
}

func buildAttentionSendable(
	planViews map[string]supply.LinePlanView,
	sendByLine map[string][]attention.SendResultRow,
	unpurchasedStaged map[string]int,
	rows []shopping.ShoppingListItem,
	products map[string]string,
	lineCtx *store.LineStoreContext,
	providerID string,
	gate store.ProviderGate,
) map[string]bool {
	keys := map[string]struct{}{}
	for k := range planViews {
		keys[k] = struct{}{}
	}
	for k := range unpurchasedStaged {
		keys[k] = struct{}{}
	}
	for k := range sendByLine {
		keys[k] = struct{}{}
	}
	rowByKey := map[string]shopping.ShoppingListItem{}
	for _, row := range rows {
		if row.PurchasedAt != nil {
			continue
		}
		key := shopping.ItemLineKey(row.ItemID)
		if row.GroupID != "" {
			key = shopping.GroupLineKey(row.GroupID)
		}
		rowByKey[key] = row
	}
	out := make(map[string]bool, len(keys))
	for key := range keys {
		if row, ok := rowByKey[key]; ok {
			out[key] = lineCanSendToCart(lineCtx, row, products[row.ItemID], providerID, nil, gate)
			continue
		}
		if _, ok := planViews[key]; ok {
			out[key] = store.CanSendToCart(lineCtx, "", providerID, gate)
			continue
		}
		out[key] = false
	}
	return out
}
