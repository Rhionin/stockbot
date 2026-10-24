package server

import (
	"context"
	"errors"

	"github.com/Rhionin/stockbot/internal/shopping"
	"github.com/Rhionin/stockbot/internal/tenant"
)

// ShoppingListItemUpdateHandler handles PATCH /api/shopping-list/items/{id}.
// The only supported update is marking an item as purchased.
type ShoppingListItemUpdateHandler struct {
	ShoppingList interface {
		MarkPurchased(ctx context.Context, scope tenant.Scope, id string) error
		GetItemByID(ctx context.Context, scope tenant.Scope, id string) (*shopping.ShoppingListItem, error)
	}
}

type shoppingListItemUpdateRequest struct {
	Purchased bool `json:"purchased"`
}

func (h *ShoppingListItemUpdateHandler) Handle(req Request[shoppingListItemUpdateRequest, shoppingListItemPathParams]) (*ShoppingListEntryResponse, error) {
	id := req.PathParams.ID
	scope, err := requestScope(req.Context)
	if err != nil {
		return nil, InternalError(err)
	}

	if req.Body.Purchased {
		err := h.ShoppingList.MarkPurchased(req.Context, scope, id)
		if err != nil {
			if errors.Is(err, shopping.ErrItemNotFound) {
				return nil, NotFound("shopping list item not found")
			}
			return nil, InternalError(err)
		}
	}

	item, err := h.ShoppingList.GetItemByID(req.Context, scope, id)
	if err != nil {
		return nil, InternalError(err)
	}
	if item == nil {
		return nil, NotFound("shopping list item not found")
	}

	resp := shoppingListItemToResponse(item)
	return &resp, nil
}
