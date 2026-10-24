package server

import (
	"context"
	"errors"

	"github.com/Rhionin/stockbot/internal/product"
	"github.com/Rhionin/stockbot/internal/tenant"
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
	if catalog, ok := h.Catalog.(interface {
		ProductFor(ctx context.Context, scope tenant.Scope, id string) (*product.Product, error)
	}); ok {
		if scope, scopeErr := requestScope(req.Context); scopeErr == nil {
			overlaid, overlayErr := catalog.ProductFor(req.Context, scope, req.PathParams.ID)
			if overlayErr != nil {
				return refreshProductResponse{}, InternalError(overlayErr)
			}
			if overlaid != nil {
				prod = overlaid
			}
		}
	}

	return refreshProductResponse{Product: *prod, Outcome: outcome}, nil
}
