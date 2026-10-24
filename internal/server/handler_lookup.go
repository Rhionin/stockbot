package server

import (
	"context"

	"github.com/Rhionin/stockbot/internal/group"
	"github.com/Rhionin/stockbot/internal/product"
	"github.com/Rhionin/stockbot/internal/tenant"
)

type LookupHandler struct {
	Service interface {
		Lookup(ctx context.Context, scope tenant.Scope, barcode string) (product.LookupResult, error)
	}
	Groups *group.Groups
}

func (h *LookupHandler) Handle(req Request[struct{}, struct{}]) (product.LookupResult, error) {
	barcode := req.RawRequest.URL.Query().Get("barcode")
	if barcode == "" {
		return product.LookupResult{}, BadRequest("barcode query parameter is required")
	}

	scope, err := requestScope(req.Context)
	if err != nil {
		return product.LookupResult{}, err
	}

	result, err := h.Service.Lookup(req.Context, scope, barcode)
	if err != nil {
		return product.LookupResult{}, err
	}
	if result.Product != nil {
		considerGroupProduct(req.Context, scope, h.Groups, result.Product.ID)
	}
	return result, nil
}
