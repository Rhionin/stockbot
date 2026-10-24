package server

import (
	"context"
	"errors"

	"github.com/Rhionin/stockbot/internal/shopping"
	"github.com/Rhionin/stockbot/internal/tenant"
)

// ShoppingListItemDeleteHandler handles DELETE /api/shopping-list/items/{id}.
type ShoppingListItemDeleteHandler struct {
	ShoppingList interface {
		RemoveItem(ctx context.Context, scope tenant.Scope, id string) error
	}
}

func (h *ShoppingListItemDeleteHandler) Handle(req Request[struct{}, shoppingListItemPathParams]) (struct{}, error) {
	scope, err := requestScope(req.Context)
	if err != nil {
		return struct{}{}, InternalError(err)
	}
	err = h.ShoppingList.RemoveItem(req.Context, scope, req.PathParams.ID)
	if err != nil {
		if errors.Is(err, shopping.ErrItemNotFound) {
			return struct{}{}, NotFound("shopping list item not found")
		}
		return struct{}{}, InternalError(err)
	}
	return struct{}{}, nil
}
