package server

import (
	"context"
	"errors"
	"time"

	"github.com/Rhionin/stockbot/internal/inventory"
	"github.com/Rhionin/stockbot/internal/shopping"
	"github.com/Rhionin/stockbot/internal/supply"
	"github.com/Rhionin/stockbot/internal/tenant"
)

// ShoppingListUndoRemoveHandler handles POST /api/shopping-list/items/{itemId}/undo-remove.
// Path itemId is the pantry item id (not a shopping-list row id).
type ShoppingListUndoRemoveHandler struct {
	ShoppingList interface {
		UndoRemove(ctx context.Context, scope tenant.Scope, itemID string, line shopping.PlannedLine) (*shopping.ShoppingListItem, error)
		ActiveRemoveSkip(ctx context.Context, scope tenant.Scope, itemID string) (groupID string, found bool, err error)
		ItemGroups(ctx context.Context, scope tenant.Scope) (map[string]string, error)
	}
	Pantry interface {
		GetItem(ctx context.Context, scope tenant.Scope, itemID string) (*inventory.Item, error)
		ListItems(ctx context.Context, scope tenant.Scope) ([]inventory.Item, error)
	}
	Supply *supply.Service
}

func (h *ShoppingListUndoRemoveHandler) Handle(req Request[struct{}, shoppingDealPath]) (*ShoppingListEntryResponse, error) {
	itemID := req.PathParams.ItemID
	if itemID == "" {
		return nil, &HTTPError{Code: 422, Message: "itemId is required"}
	}
	scope, err := requestScope(req.Context)
	if err != nil {
		return nil, InternalError(err)
	}
	item, err := h.Pantry.GetItem(req.Context, scope, itemID)
	if err != nil {
		return nil, InternalError(err)
	}
	if item == nil {
		return nil, NotFound("item not found")
	}
	skipGroup, hasSkip, err := h.ShoppingList.ActiveRemoveSkip(req.Context, scope, itemID)
	if err != nil {
		return nil, InternalError(err)
	}
	if !hasSkip {
		return nil, NotFound("trip remove skip not found")
	}
	groupOf, err := h.ShoppingList.ItemGroups(req.Context, scope)
	if err != nil {
		return nil, InternalError(err)
	}
	items, err := h.Pantry.ListItems(req.Context, scope)
	if err != nil {
		return nil, InternalError(err)
	}
	planned, err := h.Supply.Plan(req.Context, scope, time.Now())
	if err != nil {
		return nil, InternalError(err)
	}
	line, ok := plannedLineForUndo(planned, items, *item, skipGroup, groupOf[itemID])
	if !ok {
		return nil, NotFound("trip remove skip not found")
	}
	restored, err := h.ShoppingList.UndoRemove(req.Context, scope, itemID, line)
	if err != nil {
		if errors.Is(err, shopping.ErrTripSkipNotFound) {
			return nil, NotFound("trip remove skip not found")
		}
		return nil, InternalError(err)
	}
	resp := shoppingListItemToResponse(restored)
	return &resp, nil
}

func plannedLineForUndo(
	planned []supply.Line,
	items []inventory.Item,
	item inventory.Item,
	skipGroup, memberGroup string,
) (shopping.PlannedLine, bool) {
	groupKey := skipGroup
	if groupKey == "" {
		groupKey = memberGroup
	}
	if groupKey != "" {
		byProduct := make(map[string]inventory.Item, len(items))
		for _, shelf := range items {
			byProduct[shelf.ProductID] = shelf
		}
		for _, line := range planned {
			if line.Source == supply.SourceManual || line.Buy < 1 {
				continue
			}
			if line.GroupID != groupKey {
				continue
			}
			shelf, ok := byProduct[string(line.Product)]
			if !ok {
				continue
			}
			return shopping.PlannedLine{
				ProductID: string(line.Product),
				ItemID:    shelf.ID,
				GroupKey:  line.GroupID,
				Quantity:  int(line.Buy),
				Note:      line.Note,
			}, true
		}
		return shopping.PlannedLine{}, false
	}
	return plannedAutoLine(planned, item)
}

func plannedAutoLine(lines []supply.Line, item inventory.Item) (shopping.PlannedLine, bool) {
	for _, line := range lines {
		if line.Source == supply.SourceManual {
			continue
		}
		if string(line.Product) != item.ProductID {
			continue
		}
		if line.Buy < 1 {
			return shopping.PlannedLine{}, false
		}
		return shopping.PlannedLine{
			ProductID: string(line.Product),
			ItemID:    item.ID,
			GroupKey:  line.GroupID,
			Quantity:  int(line.Buy),
			Note:      line.Note,
		}, true
	}
	return shopping.PlannedLine{}, false
}
