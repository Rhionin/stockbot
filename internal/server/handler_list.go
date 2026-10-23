package server

import (
	"context"

	"github.com/Rhionin/stockbot/internal/product"
)

type ListHandler struct {
	Catalog interface {
		ListProducts(ctx context.Context, userID string) ([]product.Product, error)
	}
}

func (h *ListHandler) Handle(req Request[struct{}, struct{}]) ([]product.Product, error) {
	userID := "user-1"
	if req.RawRequest != nil {
		if id := req.RawRequest.URL.Query().Get("userId"); id != "" {
			userID = id
		}
	}
	return h.Catalog.ListProducts(req.Context, userID)
}
