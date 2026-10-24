package server

import (
	"context"

	"github.com/Rhionin/stockbot/internal/inventory"
	"github.com/Rhionin/stockbot/internal/tenant"
)

// SetReplenishmentModePathParams holds path parameters for the replenishment mode endpoint.
type SetReplenishmentModePathParams struct {
	ItemID string `json:"itemId"`
}

// SetReplenishmentModeHandler handles POST /api/items/{itemId}/replenishment-mode.
// Sets the replenishment mode for an item (target or replenish).
type SetReplenishmentModeHandler struct {
	Pantry interface {
		UpdateReplenishmentMode(ctx context.Context, scope tenant.Scope, itemID string, mode inventory.ReplenishmentMode) error
	}
}

// SetReplenishmentModeBody holds the request body.
type SetReplenishmentModeBody struct {
	Mode string `json:"mode"`
}

// SetReplenishmentModeResponse holds the response.
type SetReplenishmentModeResponse struct {
	Mode inventory.ReplenishmentMode `json:"mode"`
}

// Handle sets the replenishment mode for an item.
func (h *SetReplenishmentModeHandler) Handle(req Request[SetReplenishmentModeBody, SetReplenishmentModePathParams]) (*SetReplenishmentModeResponse, error) {
	itemID := req.PathParams.ItemID
	if itemID == "" {
		return nil, BadRequest("missing itemId path parameter")
	}

	// Validate the mode value
	mode := inventory.ReplenishmentMode(req.Body.Mode)
	if mode != inventory.ReplenishMode && mode != inventory.TargetMode {
		return nil, &HTTPError{Code: 422, Message: "mode must be \"target\" or \"replenish\""}
	}

	scope, err := requestScope(req.Context)
	if err != nil {
		return nil, err
	}

	// Set the replenishment mode
	if err := h.Pantry.UpdateReplenishmentMode(req.Context, scope, itemID, mode); err != nil {
		return nil, InternalError(err)
	}

	return &SetReplenishmentModeResponse{Mode: mode}, nil
}
