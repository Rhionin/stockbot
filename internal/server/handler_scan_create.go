package server

import (
	"context"
	"log"
	"time"

	"github.com/Rhionin/stockbot/internal/product"
	"github.com/Rhionin/stockbot/internal/scan"
)

// directionLabel renders a scan direction for logs, showing "unset" for a nil
// direction so operators can tell an unset scan apart from a stock-in one.
func directionLabel(direction *scan.ScanDirection) string {
	if direction == nil {
		return "unset"
	}
	return string(*direction)
}

type ScanCreateHandler struct {
	Queue interface {
		CreateScanEntry(ctx context.Context, entry scan.ScanEntry) (*scan.ScanEntry, error)
	}
	LookupService interface {
		Lookup(ctx context.Context, barcode, userID string) (product.LookupResult, error)
	}
	// Events announces the barcode before lookup returns. Nil skips the
	// announcement; the created scan entry is still published by the queue.
	Events interface {
		PublishScanProcessingEvent(notice scan.ProcessingNotice)
		PublishScanProcessingFailedEvent(failure scan.ProcessingFailure)
	}
	// Mode stamps a product barcode that did not name its own direction, and
	// counts the barcode as scan traffic for the idle return to scan-out.
	// Nil leaves an omitted direction unset and does not track idle time.
	Mode *scannerMode
}

type createScanRequest struct {
	Barcode   string              `json:"barcode"`
	Direction *scan.ScanDirection `json:"direction,omitempty"`
	UnitCount int                 `json:"unitCount,omitempty"`
	ExpiresAt *time.Time          `json:"expiresAt,omitempty"`
	UserID    string              `json:"userId"`
}

func (h *ScanCreateHandler) Handle(req Request[createScanRequest, struct{}]) (Created, error) {
	if req.Body.Barcode == "" {
		return Created{}, BadRequest("barcode is required")
	}
	if req.Body.UserID == "" {
		return Created{}, BadRequest("userId is required")
	}

	// A product barcode with no direction takes the server's current mode, which
	// is already scan-out when scan-in has been idle for 5 minutes. Naming a
	// direction keeps that explicit choice. Either way the barcode is scan
	// traffic, so it is the event that can postpone the idle return.
	if h.Mode != nil {
		if req.Body.Direction == nil {
			current := h.Mode.Get()
			req.Body.Direction = &current
		}
		h.Mode.NoteScan()
	}

	log.Printf("scan received: barcode=%q direction=%s user=%s", req.Body.Barcode, directionLabel(req.Body.Direction), req.Body.UserID)

	scannedAt := time.Now().UTC()
	if scan.IsQRPayload(req.Body.Barcode) {
		entry := scan.NewQREntry(req.Body.UserID, req.Body.Barcode, req.Body.Direction, scannedAt)
		created, err := h.Queue.CreateScanEntry(req.Context, entry)
		if err != nil {
			return Created{}, InternalError(err)
		}
		log.Printf("scan queued: id=%s barcode=%q direction=%s status=%s unitCount=%d", created.ID, created.Barcode, directionLabel(created.Direction), created.Status, created.UnitCount)
		return Created{Value: created}, nil
	}

	notice := scan.AnnounceProcessing(h.Events, req.Body.UserID, req.Body.Barcode, req.Body.Direction, scannedAt)

	lookupResult, err := h.LookupService.Lookup(req.Context, req.Body.Barcode, req.Body.UserID)
	if err != nil {
		scan.AnnounceProcessingFailed(h.Events, notice)
		return Created{}, InternalError(err)
	}

	entry := scan.NewEntryFromLookup(req.Body.UserID, req.Body.Barcode, lookupResult, req.Body.Direction, scannedAt)
	if req.Body.ExpiresAt != nil {
		entry.ExpiresAt = req.Body.ExpiresAt
	}
	if req.Body.UnitCount != 0 {
		entry.UnitCount = req.Body.UnitCount // HTTP callers may override the default of 1
	}

	created, err := h.Queue.CreateScanEntry(req.Context, entry)
	if err != nil {
		return Created{}, InternalError(err)
	}

	log.Printf("scan queued: id=%s barcode=%q direction=%s status=%s unitCount=%d", created.ID, created.Barcode, directionLabel(created.Direction), created.Status, created.UnitCount)

	return Created{Value: created}, nil
}
