package server

import (
	"github.com/Rhionin/stockbot/internal/supply"
)

type InventoryWipeHandler struct {
	Supply *supply.Service
}

type inventoryWipeRequest struct {
	Confirmation string `json:"confirmation"`
}

func (h *InventoryWipeHandler) Handle(req Request[inventoryWipeRequest, struct{}]) (struct{}, error) {
	err := h.Supply.Wipe(req.Context, req.Body.Confirmation)
	if err == nil {
		return struct{}{}, nil
	}
	if req.Body.Confirmation != supply.WipePhrase {
		return struct{}{}, ConfirmationMismatch(err.Error())
	}
	return struct{}{}, InternalError(err)
}
