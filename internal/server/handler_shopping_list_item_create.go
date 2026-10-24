package server

import (
	"context"

	"github.com/Rhionin/stockbot/internal/inventory"
	"github.com/Rhionin/stockbot/internal/shopping"
	"github.com/Rhionin/stockbot/internal/tenant"
)

// ShoppingListItemCreateHandler handles POST /api/shopping-list/items.
type ShoppingListItemCreateHandler struct {
	ShoppingList interface {
		AddManualItem(ctx context.Context, scope tenant.Scope, itemID string, quantity int) (*shopping.ShoppingListItem, error)
	}
	Pantry interface {
		GetItem(ctx context.Context, scope tenant.Scope, itemID string) (*inventory.Item, error)
	}
}

type shoppingListItemCreateRequest struct {
	ItemID   string `json:"itemId"`
	Quantity int    `json:"quantity"`
}

func (h *ShoppingListItemCreateHandler) Handle(req Request[shoppingListItemCreateRequest, struct{}]) (Created, error) {
	if req.Body.ItemID == "" {
		return Created{}, &HTTPError{Code: 422, Message: "itemId is required"}
	}
	if req.Body.Quantity < 1 {
		return Created{}, &HTTPError{Code: 422, Message: "quantity must be 1 or greater"}
	}

	scope, err := requestScope(req.Context)
	if err != nil {
		return Created{}, InternalError(err)
	}
	item, err := h.Pantry.GetItem(req.Context, scope, req.Body.ItemID)
	if err != nil {
		return Created{}, InternalError(err)
	}
	if item == nil {
		return Created{}, NotFound("item not found")
	}

	created, err := h.ShoppingList.AddManualItem(req.Context, scope, req.Body.ItemID, req.Body.Quantity)
	if err != nil {
		return Created{}, InternalError(err)
	}

	resp := shoppingListItemToResponse(created)
	return Created{Value: resp}, nil
}
