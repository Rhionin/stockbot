package server

import (
	"context"
	"errors"

	"github.com/Rhionin/stockbot/internal/product"
)

// RefreshHandler handles POST /api/products/{id}/refresh. It revalidates a
// cached product against Open Food Facts and responds with the row as it
// stands after the revalidation completes.
type RefreshHandler struct {
	Refresher interface {
		Refresh(ctx context.Context, productID string) (product.RefreshOutcome, error)
	}
	Catalog interface {
		GetProductByID(ctx context.Context, id string) (*product.Product, error)
	}
}

type refreshProductPathParams struct {
	ID string `json:"id"`
}

type refreshProductResponse struct {
	Product product.Product        `json:"product"`
	Outcome product.RefreshOutcome `json:"outcome"`
}

func (h *RefreshHandler) Handle(req Request[struct{}, refreshProductPathParams]) (refreshProductResponse, error) {
	if req.PathParams.ID == "" {
		return refreshProductResponse{}, BadRequest("product id is required")
	}

	outcome, err := h.Refresher.Refresh(req.Context, req.PathParams.ID)
	if err != nil {
		if errors.Is(err, product.ErrRefreshTargetMissing) {
			return refreshProductResponse{}, NotFound("product not found")
		}
		return refreshProductResponse{}, ProviderUnavailable(product.ExternalSourceOpenFoodFacts.DisplayName())
	}

	prod, err := h.Catalog.GetProductByID(req.Context, req.PathParams.ID)
	if err != nil {
		return refreshProductResponse{}, InternalError(err)
	}
	if prod == nil {
		return refreshProductResponse{}, NotFound("product not found")
	}

	return refreshProductResponse{Product: *prod, Outcome: outcome}, nil
}
