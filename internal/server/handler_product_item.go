package server

import (
	"github.com/Rhionin/stockbot/internal/inventory"
	"github.com/Rhionin/stockbot/internal/product"
)

type ProductEnsureItemHandler struct {
	Catalog *product.Catalog
	Pantry  *inventory.Pantry
	Events  interface {
		PublishInventoryEvent(householdID string, item inventory.InventoryItem)
	}
}

type productEnsureItemPath struct {
	ID string `json:"id"`
}

type productEnsureItemResponse struct {
	ItemID string `json:"itemId"`
}

func (h *ProductEnsureItemHandler) Handle(req Request[struct{}, productEnsureItemPath]) (productEnsureItemResponse, error) {
	if req.PathParams.ID == "" {
		return productEnsureItemResponse{}, BadRequest("product id is required")
	}
	scope, err := requestScope(req.Context)
	if err != nil {
		return productEnsureItemResponse{}, err
	}
	got, err := h.Catalog.ProductFor(req.Context, scope, req.PathParams.ID)
	if err != nil {
		return productEnsureItemResponse{}, err
	}
	if got == nil {
		return productEnsureItemResponse{}, NotFound("product not found")
	}
	itemID, err := ensureHouseholdItem(req.Context, scope, h.Pantry, h.Events, got.ID)
	if err != nil {
		return productEnsureItemResponse{}, err
	}
	return productEnsureItemResponse{ItemID: itemID}, nil
}
