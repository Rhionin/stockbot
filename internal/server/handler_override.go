package server

import (
	"context"
)

type OverrideCreateHandler struct {
	Catalog interface {
		UpsertBarcodeMapping(ctx context.Context, barcode, productID, source, userID string) error
	}
}

type createOverrideRequest struct {
	Barcode   string `json:"barcode"`
	ProductID string `json:"productId"`
}

type createOverrideResponse struct {
	Barcode   string `json:"barcode"`
	ProductID string `json:"productId"`
	Source    string `json:"source"`
}

func (h *OverrideCreateHandler) Handle(req Request[createOverrideRequest, struct{}]) (Created, error) {
	if req.Body.Barcode == "" {
		return Created{}, BadRequest("barcode is required")
	}
	if req.Body.ProductID == "" {
		return Created{}, BadRequest("productId is required")
	}

	scope, err := requestScope(req.Context)
	if err != nil {
		return Created{}, err
	}

	if err := h.Catalog.UpsertBarcodeMapping(req.Context, req.Body.Barcode, req.Body.ProductID, "user_override", scope.HouseholdID()); err != nil {
		return Created{}, err
	}

	resp := createOverrideResponse{
		Barcode:   req.Body.Barcode,
		ProductID: req.Body.ProductID,
		Source:    "user_override",
	}

	return Created{Value: resp}, nil
}
