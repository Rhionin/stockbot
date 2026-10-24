package server

import (
	"context"
	"strings"

	"github.com/Rhionin/stockbot/internal/inventory"
	"github.com/Rhionin/stockbot/internal/shopping"
	"github.com/Rhionin/stockbot/internal/tenant"
)

type shoppingDealRequest struct {
	ItemID            string `json:"itemId"`
	PriceCents        *int   `json:"priceCents"`
	RegularPriceCents *int   `json:"regularPriceCents"`
	Label             string `json:"label"`
}

type shoppingDealResponse struct {
	ItemID            string `json:"itemId"`
	PriceCents        *int   `json:"priceCents"`
	RegularPriceCents *int   `json:"regularPriceCents"`
	Label             string `json:"label"`
	Source            string `json:"source"`
}

// ShoppingDealPutHandler handles PUT /api/shopping-list/deals.
type ShoppingDealPutHandler struct {
	ShoppingList interface {
		SaveDeal(ctx context.Context, scope tenant.Scope, deal shopping.Deal) error
	}
	Pantry interface {
		GetItem(ctx context.Context, scope tenant.Scope, itemID string) (*inventory.Item, error)
	}
}

func (h *ShoppingDealPutHandler) Handle(req Request[shoppingDealRequest, struct{}]) (*shoppingDealResponse, error) {
	if req.Body.ItemID == "" {
		return nil, &HTTPError{Code: 422, Message: "Choose a brand to mark on sale"}
	}
	label := strings.TrimSpace(req.Body.Label)
	if req.Body.PriceCents == nil && label == "" {
		return nil, &HTTPError{Code: 422, Message: "Add a sale price or a short note"}
	}
	if (req.Body.PriceCents != nil && *req.Body.PriceCents < 0) || (req.Body.RegularPriceCents != nil && *req.Body.RegularPriceCents < 0) {
		return nil, &HTTPError{Code: 422, Message: "Sale price can't be negative"}
	}
	scope, err := requestScope(req.Context)
	if err != nil {
		return nil, InternalError(err)
	}
	item, err := h.Pantry.GetItem(req.Context, scope, req.Body.ItemID)
	if err != nil {
		return nil, InternalError(err)
	}
	if item == nil {
		return nil, NotFound("item not found")
	}
	deal := shopping.Deal{
		ItemID:            item.ID,
		PriceCents:        req.Body.PriceCents,
		RegularPriceCents: req.Body.RegularPriceCents,
		Label:             label,
		Source:            shopping.DealSourceRecorded,
	}
	if err := h.ShoppingList.SaveDeal(req.Context, scope, deal); err != nil {
		return nil, InternalError(err)
	}
	return &shoppingDealResponse{
		ItemID:            deal.ItemID,
		PriceCents:        deal.PriceCents,
		RegularPriceCents: deal.RegularPriceCents,
		Label:             deal.Label,
		Source:            deal.Source,
	}, nil
}

type shoppingDealPath struct {
	ItemID string `json:"itemId"`
}

// ShoppingDealDeleteHandler handles DELETE /api/shopping-list/deals/{itemId}.
type ShoppingDealDeleteHandler struct {
	ShoppingList interface {
		DeleteDeal(ctx context.Context, scope tenant.Scope, itemID string) error
	}
}

func (h *ShoppingDealDeleteHandler) Handle(req Request[struct{}, shoppingDealPath]) (struct{}, error) {
	if req.PathParams.ItemID == "" {
		return struct{}{}, &HTTPError{Code: 422, Message: "Choose a brand to clear"}
	}
	scope, err := requestScope(req.Context)
	if err != nil {
		return struct{}{}, InternalError(err)
	}
	if err := h.ShoppingList.DeleteDeal(req.Context, scope, req.PathParams.ItemID); err != nil {
		return struct{}{}, InternalError(err)
	}
	return struct{}{}, nil
}
