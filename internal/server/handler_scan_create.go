package server

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/Rhionin/stockbot/internal/product"
	"github.com/Rhionin/stockbot/internal/scan"
	"github.com/Rhionin/stockbot/internal/tenant"
)

// directionLabel renders a scan direction for logs, showing "unset" for a nil
// direction so operators can tell an unset scan apart from a stock-in one.
func (h *ScanCreateHandler) scanMode(ctx context.Context, scope tenant.Scope) *scannerMode {
	if h != nil && h.Modes != nil {
		if slot := h.Modes.For(ctx, scope.HouseholdID(), ""); slot != nil {
			return slot
		}
	}
	if h != nil {
		return h.Mode
	}
	return nil
}

func directionLabel(direction *scan.ScanDirection) string {
	if direction == nil {
		return "unset"
	}
	return string(*direction)
}

type ScanCreateHandler struct {
	Queue interface {
		CreateScanEntry(ctx context.Context, scope tenant.Scope, entry scan.ScanEntry) (*scan.ScanEntry, error)
	}
	LookupService interface {
		Lookup(ctx context.Context, scope tenant.Scope, barcode string) (product.LookupResult, error)
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
	Mode  *scannerMode
	Modes *modeBook
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
	if scan.LooksLikePairingCode(req.Body.Barcode) {
		return Created{}, BadRequest(scan.PairingScanMessage)
	}
	scope, err := requestScope(req.Context)
	if err != nil {
		return Created{}, err
	}
	// Stage-1 clients drop userId once a session exists. A supplied id still
	// wins so older callers keep their own actor.
	req.Body.UserID = scanActorID(req.Context, scope, req.Body.UserID)

	// A product barcode with no direction takes the server's current mode, which
	// is already scan-out when scan-in has been idle for 5 minutes. Naming a
	// direction keeps that explicit choice. Either way the barcode is scan
	// traffic, so it is the event that can postpone the idle return.
	if mode := h.scanMode(req.Context, scope); mode != nil {
		if req.Body.Direction == nil {
			current := mode.Get()
			req.Body.Direction = &current
		}
		mode.NoteScan()
	}

	log.Printf("scan received: barcode=%q direction=%s user=%s", req.Body.Barcode, directionLabel(req.Body.Direction), req.Body.UserID)

	scannedAt := time.Now().UTC()
	if scan.IsQRPayload(req.Body.Barcode) {
		entry := scan.NewQREntry(req.Body.UserID, req.Body.Barcode, req.Body.Direction, scannedAt)
		created, err := h.Queue.CreateScanEntry(req.Context, scope, entry)
		if err != nil {
			return Created{}, InternalError(err)
		}
		log.Printf("scan queued: id=%s barcode=%q direction=%s status=%s unitCount=%d", created.ID, created.Barcode, directionLabel(created.Direction), created.Status, created.UnitCount)
		return Created{Value: created}, nil
	}

	notice := scan.AnnounceProcessing(h.Events, scope.HouseholdID(), req.Body.UserID, req.Body.Barcode, req.Body.Direction, scannedAt)

	lookupResult, err := h.LookupService.Lookup(req.Context, scope, req.Body.Barcode)
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

	created, err := h.Queue.CreateScanEntry(req.Context, scope, entry)
	if err != nil {
		return Created{}, InternalError(err)
	}

	log.Printf("scan queued: id=%s barcode=%q direction=%s status=%s unitCount=%d", created.ID, created.Barcode, directionLabel(created.Direction), created.Status, created.UnitCount)

	return Created{Value: created}, nil
}

// scanActorID names who scanned. A body userId is kept. Otherwise the
// principal's account id, then its kind (for example device),
// then the household itself.
func scanActorID(ctx context.Context, scope tenant.Scope, supplied string) string {
	if id := strings.TrimSpace(supplied); id != "" {
		return id
	}
	if p, ok := auth.PrincipalFrom(ctx); ok {
		if p.UserID != "" {
			return p.UserID
		}
		if p.Kind != "" {
			return string(p.Kind)
		}
	}
	return scope.HouseholdID()
}
