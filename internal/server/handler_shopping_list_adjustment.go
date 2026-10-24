package server

import (
	"context"
	"fmt"
	"net/http"

	"github.com/Rhionin/stockbot/internal/tenant"
)

// ShoppingListAdjustmentPathParams holds path parameters for the adjustment endpoint.
type ShoppingListAdjustmentPathParams struct {
	ID         string `json:"id"`
	ProviderID string `json:"providerId"`
}

// ShoppingListAdjustmentHandler handles PUT/DELETE /api/shopping-list/items/{id}/adjustment.
type ShoppingListAdjustmentHandler struct {
	ShoppingList interface {
		SetAdjustment(ctx context.Context, scope tenant.Scope, entryID, providerID string, quantity int) error
		RemoveAdjustment(ctx context.Context, scope tenant.Scope, entryID, providerID string) error
	}
}

// ShoppingListAdjustmentBody holds the request body.
type ShoppingListAdjustmentBody struct {
	Adjustment *int   `json:"adjustment,omitempty"`
	Provider   string `json:"provider,omitempty"`
}

// ShoppingListAdjustmentResponse holds the response.
type ShoppingListAdjustmentResponse struct {
	Adjustment int `json:"adjustment"`
}

// Handle sets or clears an adjustment for a shopping list entry.
func (h *ShoppingListAdjustmentHandler) Handle(req Request[ShoppingListAdjustmentBody, ShoppingListAdjustmentPathParams]) (*ShoppingListAdjustmentResponse, error) {
	entryID := req.PathParams.ID
	if entryID == "" {
		return nil, BadRequest("missing id path parameter")
	}

	providerID := req.PathParams.ProviderID
	if providerID == "" && req.RawRequest != nil {
		providerID = req.RawRequest.URL.Query().Get("provider")
	}
	if providerID == "" {
		providerID = req.Body.Provider
	}
	if providerID == "" {
		return nil, BadRequest("missing provider")
	}

	scope, err := requestScope(req.Context)
	if err != nil {
		return nil, InternalError(err)
	}

	// If adjustment is nil, clear it; otherwise set it
	if req.Body.Adjustment == nil {
		// Clear adjustment
		if err := h.ShoppingList.RemoveAdjustment(req.Context, scope, entryID, providerID); err != nil {
			return nil, InternalError(err)
		}
		return &ShoppingListAdjustmentResponse{Adjustment: 0}, nil
	}

	adjustment := *req.Body.Adjustment

	// Validate the adjustment value (0-999)
	if adjustment < 0 || adjustment > 999 {
		return nil, &HTTPError{Code: http.StatusUnprocessableEntity, Message: fmt.Sprintf("adjustment must be between 0 and 999, got %d", adjustment)}
	}

	// Set the adjustment
	if err := h.ShoppingList.SetAdjustment(req.Context, scope, entryID, providerID, adjustment); err != nil {
		return nil, InternalError(err)
	}

	return &ShoppingListAdjustmentResponse{Adjustment: adjustment}, nil
}
