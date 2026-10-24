package server

import (
	"context"
	"errors"

	"github.com/Rhionin/stockbot/internal/shopping"
	"github.com/Rhionin/stockbot/internal/tenant"
)

// ShoppingListSwapHandler handles POST /api/shopping-list/items/{id}/swap.
// The chosen product stays on the line until the shopper changes it again.
type ShoppingListSwapHandler struct {
	ShoppingList interface {
		SwapAutoLine(ctx context.Context, scope tenant.Scope, lineID, itemID string) error
		GetItemByID(ctx context.Context, scope tenant.Scope, id string) (*shopping.ShoppingListItem, error)
	}
}

type shoppingSwapRequest struct {
	ItemID string `json:"itemId"`
}

func (h *ShoppingListSwapHandler) Handle(req Request[shoppingSwapRequest, shoppingListItemPathParams]) (*ShoppingListEntryResponse, error) {
	if req.Body.ItemID == "" {
		return nil, &HTTPError{Code: 422, Message: "Choose a product in this group."}
	}
	scope, err := requestScope(req.Context)
	if err != nil {
		return nil, InternalError(err)
	}
	if err := h.ShoppingList.SwapAutoLine(req.Context, scope, req.PathParams.ID, req.Body.ItemID); err != nil {
		switch {
		case errors.Is(err, shopping.ErrItemNotFound):
			return nil, NotFound("shopping list item not found")
		case errors.Is(err, shopping.ErrNotGroupLine), errors.Is(err, shopping.ErrNotGroupMember):
			return nil, &HTTPError{Code: 422, Message: err.Error()}
		default:
			return nil, InternalError(err)
		}
	}
	item, err := h.ShoppingList.GetItemByID(req.Context, scope, req.PathParams.ID)
	if err != nil {
		return nil, InternalError(err)
	}
	if item == nil {
		return nil, NotFound("shopping list item not found")
	}
	resp := shoppingListItemToResponse(item)
	return &resp, nil
}
