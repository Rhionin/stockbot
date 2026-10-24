package server

import (
	"context"

	"github.com/Rhionin/stockbot/internal/group"
	"github.com/Rhionin/stockbot/internal/scan"
	"github.com/Rhionin/stockbot/internal/tenant"
)

type ScanListHandler struct {
	Queue interface {
		ListScanEntries(ctx context.Context, scope tenant.Scope, status scan.ScanStatus) ([]scan.ScanEntry, error)
	}
	Groups *group.Groups
}

func (h *ScanListHandler) Handle(req Request[struct{}, struct{}]) ([]scan.ScanEntry, error) {
	// Extract status from query params. userId is ignored: the queue is
	// household-scoped, and Stage-1 clients omit the query parameter.
	statusParam := req.RawRequest.URL.Query().Get("status")

	status := scan.ScanStatus(statusParam)
	if statusParam != "" && status != scan.Pending && status != scan.Flagged && status != scan.Committed && status != scan.Cancelled && status != scan.QR {
		return nil, BadRequest("invalid status value")
	}

	scope, err := requestScope(req.Context)
	if err != nil {
		return nil, err
	}
	entries, err := h.Queue.ListScanEntries(req.Context, scope, status)
	if err != nil {
		return nil, InternalError(err)
	}
	if err := h.attachGroupHints(req.Context, scope, entries); err != nil {
		return nil, InternalError(err)
	}

	return entries, nil
}

func (h *ScanListHandler) attachGroupHints(ctx context.Context, scope tenant.Scope, entries []scan.ScanEntry) error {
	if h.Groups == nil || len(entries) == 0 {
		return nil
	}
	names := map[string]string{}
	for _, entry := range entries {
		if entry.ProductID == nil || entry.Product == nil || entry.Product.Name == "" {
			continue
		}
		names[*entry.ProductID] = entry.Product.Name
	}
	hints, err := h.Groups.Hints(ctx, scope, names)
	if err != nil {
		return err
	}
	for i := range entries {
		if entries[i].ProductID == nil {
			continue
		}
		hint, ok := hints[*entries[i].ProductID]
		if !ok || hint.Name == "" {
			continue
		}
		entries[i].GroupHint = &scan.GroupHint{GroupID: hint.GroupID, Name: hint.Name}
	}
	return nil
}
