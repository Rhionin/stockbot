package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/Rhionin/stockbot/internal/cart"
	"github.com/Rhionin/stockbot/internal/cart/appcred"
	"github.com/Rhionin/stockbot/internal/cart/connection"
	"github.com/Rhionin/stockbot/internal/shopping"
	"github.com/Rhionin/stockbot/internal/store"
	"github.com/Rhionin/stockbot/internal/tenant"
)

// ShoppingListExportHandler handles POST /api/shopping-list/export.
// It sends the staged cart. It does not recompute the supply plan, so a
// quantity the owner changed is the quantity that is sent. A request that
// names a different product for a line is refused. With no configured
// provider the call still returns the staged lines and confirms nothing.
type ShoppingListExportHandler struct {
	ShoppingList shoppingListReader
	Pantry       pantryLister
	Provisioner  cart.Provisioner
	Ledger       *cart.Ledger
	Registry     *cart.Registry
	Connections  *connection.Directory
	Credentials  *appcred.Vault
	Env          ProviderEnv
	DB           *sql.DB
}

func (h *ShoppingListExportHandler) householdCredentialsConfigured(ctx context.Context, scope tenant.Scope, providerID cart.ProviderID) (bool, error) {
	if providerID == "" {
		return false, nil
	}
	if h.Credentials != nil {
		has, err := h.Credentials.HasApp(ctx, scope, string(providerID))
		if err != nil {
			return false, err
		}
		if has {
			return true, nil
		}
	}
	return false, nil
}

func (h *ShoppingListExportHandler) anyExportCredentials(ctx context.Context, scope tenant.Scope) (bool, error) {
	if h.Registry == nil {
		return false, nil
	}
	for _, id := range h.Registry.List() {
		ok, err := h.householdCredentialsConfigured(ctx, scope, id)
		if err != nil {
			return false, err
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}

func (h *ShoppingListExportHandler) registryConfiguredCount() int {
	if h.Registry == nil {
		return 0
	}
	n := 0
	for _, id := range h.Registry.List() {
		if h.Registry.CredentialsConfigured(id) {
			n++
		}
	}
	return n
}

type shoppingListExportRequest struct {
	Provider   string            `json:"provider"`
	UseItemIDs map[string]string `json:"useItemIds"`
}

type exportedItemResponse struct {
	ItemID   string `json:"itemId"`
	Name     string `json:"name"`
	Quantity int    `json:"quantity"`
}

type ProvisionEntry struct {
	EntryID       string `json:"entryId"`
	ItemID        string `json:"itemId"`
	Name          string `json:"name"`
	Quantity      int    `json:"quantity"`
	Outcome       string `json:"outcome"`
	OutcomeReason string `json:"outcomeReason,omitempty"`
	Detail        string `json:"detail,omitempty"`
}

type shoppingListExportResponse struct {
	Provider          string                 `json:"provider,omitempty"`
	Exported          int                    `json:"exported"`
	AddedCount        int                    `json:"addedCount"`
	SentCount         int                    `json:"sentCount"`
	SkippedCount      int                    `json:"skippedCount"`
	FailedCount       int                    `json:"failedCount"`
	UnknownCount      int                    `json:"unknownCount"`
	ReconnectRequired bool                   `json:"reconnectRequired,omitempty"`
	FailedItems       []string               `json:"failedItems,omitempty"`
	UnknownItems      []string               `json:"unknownItems,omitempty"`
	Entries           []ProvisionEntry       `json:"entries,omitempty"`
	Results           []provisionResultEntry `json:"results"`
	Handoff           *HandoffResponse       `json:"handoff,omitempty"`
	Items             []exportedItemResponse `json:"items,omitempty"`
}

type HandoffResponse struct {
	URL     string `json:"url,omitempty"`
	Expires string `json:"expires,omitempty"`
}

func (h *ShoppingListExportHandler) Handle(req Request[shoppingListExportRequest, struct{}]) (*shoppingListExportResponse, error) {
	scope, err := requestScope(req.Context)
	if err != nil {
		return nil, InternalError(err)
	}

	provision, err := loadShoppingSnapshot(req.Context, scope, h.Pantry, h.ShoppingList)
	if err != nil {
		return nil, InternalError(err)
	}
	lineCtx, err := loadLineStoreContext(req.Context, h.DB, scope)
	if err != nil {
		return nil, InternalError(err)
	}
	products := productIDByItemID(provision.Items)
	scoped := exportScopeFromUseItemIDs(req.Body.UseItemIDs)

	providerID := cart.ProviderID(req.Body.Provider)
	if providerID == "" && h.Registry != nil {
		if id, ok := h.Registry.SoleConfigured(); ok {
			providerID = id
		}
	}
	if providerID == "kroger" && h.Env.Disabled {
		return nil, StoreDisabled()
	}

	planned, err := planExportLines(provision, req.Body.UseItemIDs, lineCtx, string(providerID), products, scoped, registryRegistered(h.Registry))
	if err != nil {
		return nil, err
	}

	// No configured provider: the historical no-op report. Naming an unknown
	// or unconfigured provider is still an error, so a client can tell those
	// apart from "nothing was sent". The planned lines stay on the response
	// so the cart can be checked before a retailer is connected.
	if h.Registry == nil {
		if req.Body.Provider != "" {
			return nil, BadRequest(fmt.Sprintf("provider %q is not registered", req.Body.Provider))
		}
		return &shoppingListExportResponse{Provider: string(providerID), Exported: 0, Results: []provisionResultEntry{}, Items: planned}, nil
	}
	anyConfigured, err := h.anyExportCredentials(req.Context, scope)
	if err != nil {
		return nil, InternalError(err)
	}
	if !anyConfigured {
		if req.Body.Provider != "" {
			provider, ok := h.Registry.Get(providerID)
			if !ok {
				return nil, BadRequest(fmt.Sprintf("provider %q is not registered", providerID))
			}
			hasApp, err := h.householdCredentialsConfigured(req.Context, scope, providerID)
			if err != nil {
				return nil, InternalError(err)
			}
			if !hasApp {
				return nil, Conflict(fmt.Sprintf("%s is not configured", provider.DisplayName()))
			}
		}
		return &shoppingListExportResponse{Provider: string(providerID), Exported: 0, Results: []provisionResultEntry{}, Items: planned}, nil
	}
	if providerID == "" && req.Body.Provider == "" {
		if h.Registry != nil && h.registryConfiguredCount() > 1 {
			return nil, BadRequest(`provider "" is not registered`)
		}
		return &shoppingListExportResponse{Exported: 0, Results: []provisionResultEntry{}, Items: planned}, nil
	}

	provider, ok := h.Registry.Get(providerID)
	if !ok || providerID == "" {
		named := string(providerID)
		if named == "" {
			named = req.Body.Provider
		}
		return nil, BadRequest(fmt.Sprintf("provider %q is not registered", named))
	}
	configured, err := h.householdCredentialsConfigured(req.Context, scope, providerID)
	if err != nil {
		return nil, InternalError(err)
	}
	if !configured {
		return nil, Conflict(fmt.Sprintf("%s is not configured", provider.DisplayName()))
	}
	registered := registryRegistered(h.Registry)
	if err := validateScopedCartSend(provision, lineCtx, string(providerID), scoped, products, registered); err != nil {
		return nil, err
	}
	caps := provider.Capabilities()
	if caps.Auth != cart.AuthNone {
		if h.Connections == nil {
			return nil, Conflict(fmt.Sprintf("%s is not connected", provider.DisplayName()))
		}
		conn, err := h.Connections.Read(req.Context, scope, string(providerID))
		if err != nil {
			return nil, InternalError(err)
		}
		if conn == nil || conn.State != connection.StateConnected {
			return nil, ReconnectRequired(provider.DisplayName())
		}
	}

	targetProvider := string(providerID)
	exportCtx := cart.WithProvisionFilter(req.Context, &cart.ProvisionFilter{
		ScopedItemIDs: scoped,
		CanSendToCart: func(itemID, productID string, runStoreID *string) bool {
			row := shopping.ShoppingListItem{ItemID: itemID, RunStoreID: runStoreID}
			return lineCanSendToCart(lineCtx, row, productID, targetProvider, nil, registered)
		},
	})
	report, err := h.Provisioner.Provision(exportCtx, scope, providerID)
	if err != nil {
		var conflict *cart.ProvisionConflict
		if errors.As(err, &conflict) {
			return nil, Conflict(conflict.Reason)
		}
		var reconnect *cart.ErrReconnectRequired
		if errors.As(err, &reconnect) {
			return nil, ReconnectRequired(provider.DisplayName())
		}
		var unavailable *cart.ErrProviderUnavailable
		if errors.As(err, &unavailable) {
			return nil, ProviderUnavailable(provider.DisplayName())
		}
		return nil, InternalError(err)
	}

	entries, results, failedItems, unknownItems, counts := buildProvisionExportViews(report.Entries, caps.Confirmation)

	var handoff *HandoffResponse
	if report.Handoff != nil && report.Handoff.URL != "" {
		handoff = &HandoffResponse{
			URL:     report.Handoff.URL,
			Expires: "30 minutes from now",
		}
	}

	return &shoppingListExportResponse{
		Provider:          string(providerID),
		Exported:          report.Confirmed,
		AddedCount:        counts.AddedCount,
		SentCount:         counts.SentCount,
		SkippedCount:      counts.SkippedCount,
		FailedCount:       counts.FailedCount,
		UnknownCount:      counts.UnknownCount,
		ReconnectRequired: report.ReconnectRequired,
		FailedItems:       failedItems,
		UnknownItems:      unknownItems,
		Entries:           entries,
		Results:           results,
		Handoff:           handoff,
		Items:             planned,
	}, nil
}

// planExportLines copies the staged lines. Naming a different product for a
// line is refused; the group swap already changes the staged row itself.
func planExportLines(provision shoppingProvision, useItemIDs map[string]string, lineCtx *store.LineStoreContext, providerID string, products map[string]string, scoped map[string]struct{}, registered store.ProviderRegistered) ([]exportedItemResponse, error) {
	planned := make([]exportedItemResponse, 0, len(provision.Rows))
	for _, entry := range provision.Rows {
		if useID := useItemIDs[entry.ItemID]; useID != "" && useID != entry.ItemID {
			return nil, &HTTPError{Code: 422, Message: "Choose a product on this line."}
		}
		if scoped != nil {
			if _, ok := scoped[entry.ItemID]; !ok {
				continue
			}
		}
		if providerID != "" {
			if !lineCanSendToCart(lineCtx, entry, products[entry.ItemID], providerID, nil, registered) {
				continue
			}
		}
		planned = append(planned, exportedItemResponse{
			ItemID:   entry.ItemID,
			Name:     itemName(provision.Needs, entry.ItemID),
			Quantity: entry.Quantity,
		})
	}
	if len(planned) == 0 {
		return nil, nil
	}
	return planned, nil
}
