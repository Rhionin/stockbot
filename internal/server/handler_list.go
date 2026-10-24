package server

import (
	"context"

	"github.com/Rhionin/stockbot/internal/product"
	"github.com/Rhionin/stockbot/internal/tenant"
)

type ListHandler struct {
	Catalog interface {
		ListProducts(ctx context.Context, scope tenant.Scope) ([]product.Product, error)
	}
}

func (h *ListHandler) Handle(req Request[struct{}, struct{}]) ([]product.Product, error) {
	scope, err := requestScope(req.Context)
	if err != nil {
		return nil, err
	}
	return h.Catalog.ListProducts(req.Context, scope)
}
