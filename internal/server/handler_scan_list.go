package server

import (
	"context"
	"database/sql"
	"strings"

	"github.com/Rhionin/stockbot/internal/group"
	"github.com/Rhionin/stockbot/internal/scan"
	"github.com/Rhionin/stockbot/internal/tenant"
)

type ScanListHandler struct {
	Queue interface {
		ListScanEntries(ctx context.Context, scope tenant.Scope, status scan.ScanStatus) ([]scan.ScanEntry, error)
	}
	Groups *group.Groups
	DB     *sql.DB
}

func (h *ScanListHandler) Handle(req Request[struct{}, struct{}]) (changeView, error) {
	// Extract status from query params. userId is ignored: the queue is
	// household-scoped, and Stage-1 clients omit the query parameter.
	statusParam := req.RawRequest.URL.Query().Get("status")
	since := strings.TrimSpace(req.RawRequest.URL.Query().Get("since"))
	before := strings.TrimSpace(req.RawRequest.URL.Query().Get("before"))

	status := scan.ScanStatus(statusParam)
	if statusParam != "" && status != scan.Pending && status != scan.Flagged && status != scan.Committed && status != scan.Cancelled && status != scan.QR {
		return changeView{}, BadRequest("invalid status value")
	}
	if since != "" && (status == scan.Committed || status == scan.Cancelled || before != "") {
		return changeView{}, BadRequest("since does not apply to that history page")
	}

	scope, err := requestScope(req.Context)
	if err != nil {
		return changeView{}, err
	}
	listStatus := status
	if since != "" && status == "" {
		listStatus = ""
	}
	entries, err := h.Queue.ListScanEntries(req.Context, scope, listStatus)
	if err != nil {
		return changeView{}, InternalError(err)
	}
	if since != "" && status == "" {
		queue := entries[:0]
		for _, entry := range entries {
			if entry.Status == scan.Pending || entry.Status == scan.Flagged || entry.Status == scan.QR {
				queue = append(queue, entry)
			}
		}
		entries = queue
	}
	if err := h.attachGroupHints(req.Context, scope, entries); err != nil {
		return changeView{}, InternalError(err)
	}
	if entries == nil {
		entries = []scan.ScanEntry{}
	}
	view, err := viewChanges(req.Context, h.DB, scope.HouseholdID(), "scans", since, "scans", entries)
	if err != nil {
		return changeView{}, InternalError(err)
	}
	return view, nil
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
