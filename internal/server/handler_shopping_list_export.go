package server

import (
	"errors"
	"fmt"

	"github.com/Rhionin/stockbot/internal/cart"
	"github.com/Rhionin/stockbot/internal/cart/connection"
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
}

type shoppingListExportResponse struct {
	Provider     string                 `json:"provider,omitempty"`
	Exported     int                    `json:"exported"`
	AddedCount   int                    `json:"addedCount"`
	SkippedCount int                    `json:"skippedCount"`
	FailedCount  int                    `json:"failedCount"`
	UnknownCount int                    `json:"unknownCount"`
	FailedItems  []string               `json:"failedItems,omitempty"`
	UnknownItems []string               `json:"unknownItems,omitempty"`
	Entries      []ProvisionEntry       `json:"entries,omitempty"`
	Results      []provisionResultEntry `json:"results"`
	Handoff      *HandoffResponse       `json:"handoff,omitempty"`
	Items        []exportedItemResponse `json:"items,omitempty"`
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
	planned, err := planExportLines(provision, req.Body.UseItemIDs)
	if err != nil {
		return nil, err
	}

	providerID := cart.ProviderID(req.Body.Provider)
	if providerID == "" && h.Registry != nil {
		if id, ok := h.Registry.SoleConfigured(); ok {
			providerID = id
		}
	}

	// No configured provider: the historical no-op report. Naming an unknown
	// or unconfigured provider is still an error, so a client can tell those
	// apart from "nothing was sent". The planned lines stay on the response
	// so the cart can be checked before a retailer is connected.
	if h.Registry == nil || !h.Registry.AnyCredentialsConfigured() {
		if req.Body.Provider != "" {
			if h.Registry == nil {
				return nil, BadRequest(fmt.Sprintf("provider %q is not registered", req.Body.Provider))
			}
			provider, ok := h.Registry.Get(providerID)
			if !ok {
				return nil, BadRequest(fmt.Sprintf("provider %q is not registered", providerID))
			}
			if !h.Registry.CredentialsConfigured(providerID) {
				return nil, Conflict(fmt.Sprintf("%s is not configured", provider.DisplayName()))
			}
		}
		return &shoppingListExportResponse{Provider: string(providerID), Exported: 0, Results: []provisionResultEntry{}, Items: planned}, nil
	}

	provider, ok := h.Registry.Get(providerID)
	if !ok || providerID == "" {
		named := string(providerID)
		if named == "" {
			named = req.Body.Provider
		}
		return nil, BadRequest(fmt.Sprintf("provider %q is not registered", named))
	}
	if !h.Registry.CredentialsConfigured(providerID) {
		return nil, Conflict(fmt.Sprintf("%s is not configured", provider.DisplayName()))
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
			return nil, Conflict(fmt.Sprintf("%s is not connected", provider.DisplayName()))
		}
	}

	report, err := h.Provisioner.Provision(req.Context, scope, providerID)
	if err != nil {
		var conflict *cart.ProvisionConflict
		if errors.As(err, &conflict) {
			return nil, Conflict(conflict.Reason)
		}
		var authFail *cart.AuthExchangeFailed
		if errors.As(err, &authFail) {
			return nil, ProviderUnavailable(provider.DisplayName())
		}
		return nil, InternalError(err)
	}

	entries, results, failedItems, unknownItems, counts := buildProvisionExportViews(report.Entries)

	var handoff *HandoffResponse
	if report.Handoff != nil && report.Handoff.URL != "" {
		handoff = &HandoffResponse{
			URL:     report.Handoff.URL,
			Expires: "30 minutes from now",
		}
	}

	return &shoppingListExportResponse{
		Provider:     string(providerID),
		Exported:     report.Confirmed,
		AddedCount:   counts.AddedCount,
		SkippedCount: counts.SkippedCount,
		FailedCount:  counts.FailedCount,
		UnknownCount: counts.UnknownCount,
		FailedItems:  failedItems,
		UnknownItems: unknownItems,
		Entries:      entries,
		Results:      results,
		Handoff:      handoff,
		Items:        planned,
	}, nil
}

// planExportLines copies the staged lines. Naming a different product for a
// line is refused; the group swap already changes the staged row itself.
func planExportLines(provision shoppingProvision, useItemIDs map[string]string) ([]exportedItemResponse, error) {
	planned := make([]exportedItemResponse, 0, len(provision.Rows))
	for _, entry := range provision.Rows {
		if useID := useItemIDs[entry.ItemID]; useID != "" && useID != entry.ItemID {
			return nil, &HTTPError{Code: 422, Message: "Choose a product on this line."}
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
