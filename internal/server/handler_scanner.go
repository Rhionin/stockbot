package server

import (
	"context"
	"database/sql"
	"strings"
	"sync"
	"time"

	"github.com/Rhionin/stockbot/internal/events"
	"github.com/Rhionin/stockbot/internal/scan"
	"github.com/Rhionin/stockbot/internal/scanlistener"
	"github.com/Rhionin/stockbot/internal/tenant"
)

// scannerIdleTimeout is how long scan-in stays selected with no product-scan
// traffic before the server returns to scan-out. Only a product scan moves
// the deadline; reading the mode or selecting scan-in again does not.
const scannerIdleTimeout = 5 * time.Minute

// scannerMode is the direction the next product scan is stamped with. HTTP
// mode switches and the headless listener share one value, and a scanner_mode
// event is published whenever it changes, including the idle return to
// scan-out. It starts at stock_in, matching a process that has not yet been
// told otherwise.
type scannerMode struct {
	mu         sync.Mutex
	current    scan.ScanDirection
	idleAnchor time.Time
	now        func() time.Time
	schedule   func(time.Duration, func()) (stop func())
	stopTimer  func()
	publish    func(scan.ScanDirection)
}

// NewScannerMode returns the scan direction shared by HTTP handlers and the
// headless listener. publish runs for every change, including the idle return
// to scan-out, and may be nil.
func NewScannerMode(publish func(scan.ScanDirection)) *scannerMode {
	return newScannerMode(publish)
}

func newScannerMode(publish func(scan.ScanDirection)) *scannerMode {
	m := &scannerMode{
		current: scan.StockIn,
		now:     time.Now,
		publish: publish,
		schedule: func(d time.Duration, f func()) func() {
			timer := time.AfterFunc(d, f)
			return func() { timer.Stop() }
		},
	}
	m.idleAnchor = m.now()
	m.rescheduleLocked()
	return m
}

// useClock replaces the clock and disables the wall-clock timer so a test can
// advance time without waiting. The idle window starts at the new clock's
// current instant.
func (m *scannerMode) useClock(now func() time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stopTimer != nil {
		m.stopTimer()
		m.stopTimer = nil
	}
	m.now = now
	m.idleAnchor = now()
	m.schedule = func(time.Duration, func()) func() { return func() {} }
}

// Set selects d. Selecting the direction that is already active does not move
// the idle deadline: a repeated switch is not scan traffic.
func (m *scannerMode) Set(d scan.ScanDirection) {
	m.mu.Lock()
	if m.current == d {
		m.mu.Unlock()
		return
	}
	m.current = d
	m.idleAnchor = m.now()
	m.rescheduleLocked()
	publish := m.publish
	m.mu.Unlock()
	if publish != nil {
		publish(d)
	}
}

// Get returns the direction the next product scan uses, after applying an
// idle return to scan-out that has come due.
func (m *scannerMode) Get() scan.ScanDirection {
	m.mu.Lock()
	changed := m.applyIdleLocked()
	current := m.current
	publish := m.publish
	m.mu.Unlock()
	if changed && publish != nil {
		publish(current)
	}
	return current
}

// NoteScan records a product barcode. It is the only event that postpones the
// return to scan-out. A scan that arrives after the idle window has already
// elapsed is stamped scan-out and does not start a new scan-in window.
func (m *scannerMode) NoteScan() {
	m.mu.Lock()
	changed := m.applyIdleLocked()
	if !changed && m.current == scan.StockIn {
		m.idleAnchor = m.now()
		m.rescheduleLocked()
	}
	current := m.current
	publish := m.publish
	m.mu.Unlock()
	if changed && publish != nil {
		publish(current)
	}
}

func (m *scannerMode) applyIdleLocked() bool {
	if m.current != scan.StockIn {
		return false
	}
	if m.now().Sub(m.idleAnchor) < scannerIdleTimeout {
		return false
	}
	m.current = scan.StockOut
	m.idleAnchor = m.now()
	if m.stopTimer != nil {
		m.stopTimer()
		m.stopTimer = nil
	}
	return true
}

func (m *scannerMode) rescheduleLocked() {
	if m.stopTimer != nil {
		m.stopTimer()
		m.stopTimer = nil
	}
	if m.current != scan.StockIn || m.schedule == nil {
		return
	}
	remaining := scannerIdleTimeout - m.now().Sub(m.idleAnchor)
	if remaining < 0 {
		remaining = 0
	}
	m.stopTimer = m.schedule(remaining, m.onIdleTimer)
}

var _ scanlistener.ModeControl = (*scannerMode)(nil)

func (m *scannerMode) onIdleTimer() {
	m.mu.Lock()
	changed := m.applyIdleLocked()
	current := m.current
	publish := m.publish
	m.mu.Unlock()
	if changed && publish != nil {
		publish(current)
	}
}

// ScannerConfig carries the reserved control-barcode strings the backend
// classifies against, so the browser can recognize the exact same values.
type ScannerConfig struct {
	StockInBarcode  string
	StockOutBarcode string
}

// ScannerModeHandler implements POST /api/scanner/mode. It switches the shared
// scanner mode. scannerMode publishes the scanner_mode SSE event itself, so a
// browser switch and a headless control barcode reach subscribers the same way.
type ScannerModeHandler struct {
	Mode *scannerMode
	Book *modeBook
}

type scannerModeRequest struct {
	Mode     scan.ScanDirection `json:"mode"`
	DeviceID string             `json:"deviceId"`
}

type scannerModeResponse struct {
	Mode scan.ScanDirection `json:"mode"`
}

func (h *ScannerModeHandler) Handle(req Request[scannerModeRequest, struct{}]) (scannerModeResponse, error) {
	switch req.Body.Mode {
	case scan.StockIn, scan.StockOut:
	default:
		return scannerModeResponse{}, BadRequest("mode must be stock_in or stock_out")
	}
	device := req.Body.DeviceID
	if device == "" && req.RawRequest != nil {
		device = req.RawRequest.URL.Query().Get("deviceId")
	}
	mode := h.pick(req.Context, device)
	if mode == nil {
		return scannerModeResponse{}, InternalError(tenant.ErrMissingHousehold)
	}
	mode.Set(req.Body.Mode)
	return scannerModeResponse{Mode: req.Body.Mode}, nil
}

func (h *ScannerModeHandler) Get(req Request[struct{}, struct{}]) (scannerModeResponse, error) {
	device := ""
	if req.RawRequest != nil {
		device = req.RawRequest.URL.Query().Get("deviceId")
	}
	mode := h.pick(req.Context, device)
	if mode == nil {
		return scannerModeResponse{}, InternalError(tenant.ErrMissingHousehold)
	}
	return scannerModeResponse{Mode: mode.Get()}, nil
}

func (h *ScannerModeHandler) pick(ctx context.Context, device string) *scannerMode {
	if h != nil && h.Book != nil {
		household := ""
		if scope, err := requestScope(ctx); err == nil {
			household = scope.HouseholdID()
		}
		if slot := h.Book.For(ctx, household, device); slot != nil {
			return slot
		}
	}
	if h != nil {
		return h.Mode
	}
	return nil
}

// ScannerConfigHandler implements GET /api/scanner/config, exposing the
// reserved control-barcode strings so the browser classifies the exact values
// the backend does, plus the current scanner mode so a browser that connects
// after a mode switch starts on the right direction.
type ScannerConfigHandler struct {
	Config ScannerConfig
	Mode   *scannerMode
	Book   *modeBook
	// Status reports the headless capture path. Nil means no listener is
	// configured, which the response reports as disconnected.
	Status func() scanlistener.Status
}

type scannerConfigResponse struct {
	StockInBarcode  string             `json:"stockInBarcode"`
	StockOutBarcode string             `json:"stockOutBarcode"`
	CurrentMode     scan.ScanDirection `json:"currentMode"`
	Connected       bool               `json:"connected"`
}

func (h *ScannerConfigHandler) Handle(req Request[struct{}, struct{}]) (scannerConfigResponse, error) {
	currentMode := scan.StockIn
	if h.Book != nil {
		household := ""
		if scope, err := requestScope(req.Context); err == nil {
			household = scope.HouseholdID()
		}
		if slot := h.Book.For(req.Context, household, ""); slot != nil {
			currentMode = slot.Get()
		}
	} else if h.Mode != nil {
		currentMode = h.Mode.Get()
	}
	connected := false
	if h.Status != nil {
		connected = h.Status().Connected
	}
	return scannerConfigResponse{
		StockInBarcode:  h.Config.StockInBarcode,
		StockOutBarcode: h.Config.StockOutBarcode,
		CurrentMode:     currentMode,
		Connected:       connected,
	}, nil
}

// modeBook keeps one scanner direction per household and device.
// Household 1's direct scanner is the shared value the headless listener uses.
type modeBook struct {
	shared  *scannerMode
	primary string
	db      *sql.DB
	events  *events.Broadcaster
	mu      sync.Mutex
	slots   map[string]*scannerMode
}

func newModeBook(shared *scannerMode, primary string, db *sql.DB, broadcaster *events.Broadcaster) *modeBook {
	return &modeBook{
		shared:  shared,
		primary: primary,
		db:      db,
		events:  broadcaster,
		slots:   map[string]*scannerMode{},
	}
}

func (b *modeBook) For(ctx context.Context, household, device string) *scannerMode {
	if b == nil || b.shared == nil {
		return nil
	}
	household = strings.TrimSpace(household)
	device = strings.TrimSpace(device)
	if household == "" {
		household = b.primary
	}
	direct := b.directID(ctx, household)
	if device == "" || device == "default" {
		if direct != "" {
			device = direct
		} else {
			device = "default"
		}
	}
	if household == b.primary && (device == "default" || device == direct) {
		return b.shared
	}
	key := household + "\n" + device
	b.mu.Lock()
	defer b.mu.Unlock()
	if slot := b.slots[key]; slot != nil {
		return slot
	}
	house := household
	slot := newScannerMode(func(d scan.ScanDirection) {
		if b.events != nil {
			b.events.PublishScannerModeFor(house, d)
		}
	})
	b.slots[key] = slot
	return slot
}

func (b *modeBook) directID(ctx context.Context, household string) string {
	if b == nil || b.db == nil || household == "" {
		return ""
	}
	var id string
	err := b.db.QueryRowContext(ctx, `
		SELECT id FROM devices
		WHERE household_id = ? AND kind = 'direct' AND revoked_at IS NULL
		ORDER BY created_at LIMIT 1`, household).Scan(&id)
	if err != nil {
		return ""
	}
	return id
}
