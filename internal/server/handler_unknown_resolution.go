package server

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/Rhionin/stockbot/internal/cart"
	"github.com/Rhionin/stockbot/internal/shopping"
	"github.com/Rhionin/stockbot/internal/tenant"
)

// UnknownResolutionPathParams holds path parameters for the unknown resolution endpoint.
type UnknownResolutionPathParams struct {
	ID string `json:"id"`
}

// UnknownResolutionHandler handles POST /api/shopping-list/items/{id}/unknown-resolution.
// Handles the owner's declaration of whether an item reached the provider or not.
type UnknownResolutionHandler struct {
	ShoppingList interface {
		GetItemByID(ctx context.Context, scope tenant.Scope, entryID string) (*shopping.ShoppingListItem, error)
	}
	Ledger      *cart.Ledger
	SendResults *cart.ResultRecorder
}

// UnknownResolutionBody holds the request body.
type UnknownResolutionBody struct {
	ReachedProvider bool   `json:"reachedProvider"`
	Provider        string `json:"provider"`
}

// UnknownResolutionResponse holds the response.
type UnknownResolutionResponse struct {
	EntryID string `json:"entryId"`
}

// Handle handles unknown-outcome resolution.
func (h *UnknownResolutionHandler) Handle(req Request[UnknownResolutionBody, UnknownResolutionPathParams]) (*UnknownResolutionResponse, error) {
	scope, err := requestScope(req.Context)
	if err != nil {
		return nil, err
	}
	entryID := req.PathParams.ID
	if entryID == "" {
		return nil, BadRequest("missing id path parameter")
	}

	entry, err := h.ShoppingList.GetItemByID(req.Context, scope, entryID)
	if err != nil {
		return nil, InternalError(err)
	}
	if entry == nil {
		return nil, NotFound("entry not found")
	}

	providerID := cart.ProviderID(req.Body.Provider)
	if !req.Body.ReachedProvider {
		if req.Body.Provider != "" && h.SendResults != nil {
			if err := h.SendResults.ResolveUnknownLine(req.Context, scope, providerID, entry.ItemID, entryID, false); err != nil {
				return nil, InternalError(err)
			}
		}
		return &UnknownResolutionResponse{EntryID: entryID}, nil
	}
	if req.Body.Provider == "" {
		return nil, BadRequest("missing provider")
	}
	qty := entry.Quantity
	if qty < 1 {
		qty = 1
	}
	if h.Ledger == nil {
		return nil, InternalError(fmt.Errorf("ledger is not available"))
	}
	recorder := h.SendResults
	err = h.Ledger.AdvanceWithTx(req.Context, scope, providerID, entry.ItemID, qty, func(tx *sql.Tx) error {
		if recorder == nil {
			return nil
		}
		_, err := recorder.ResolveUnknownLineTx(req.Context, scope, tx, providerID, entry.ItemID, entryID, true)
		return err
	})
	if err != nil {
		return nil, InternalError(err)
	}
	return &UnknownResolutionResponse{EntryID: entryID}, nil
}
