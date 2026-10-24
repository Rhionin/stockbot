package server

import (
	"context"

	"github.com/Rhionin/stockbot/internal/scan"
	"github.com/Rhionin/stockbot/internal/tenant"
)

type ScanHistoryHandler struct {
	Queue interface {
		ListScanEntries(ctx context.Context, scope tenant.Scope, status scan.ScanStatus) ([]scan.ScanEntry, error)
	}
}

func (h *ScanHistoryHandler) Handle(req Request[struct{}, struct{}]) ([]scan.ScanEntry, error) {
	userIDParam := req.RawRequest.URL.Query().Get("userId")

	if userIDParam == "" {
		return nil, BadRequest("userId query parameter is required")
	}

	scope, err := requestScope(req.Context)
	if err != nil {
		return nil, err
	}

	// Return only committed entries for history
	entries, err := h.Queue.ListScanEntries(req.Context, scope, scan.Committed)
	if err != nil {
		return nil, InternalError(err)
	}

	return entries, nil
}
