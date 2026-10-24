package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/scan"
	"github.com/Rhionin/stockbot/internal/scanlistener"
)

// TestScannerMode_InAppSwitchAndPhysicalControlBarcodeShareState verifies the
// in-app mode write and a headless control barcode update one scannerMode.
// The config read and the next untagged product scan both follow whichever
// path changed it last.
func TestScannerMode_InAppSwitchAndPhysicalControlBarcodeShareState(t *testing.T) {
	mode := newScannerMode(nil)
	mode.useClock(time.Now)
	handler, _ := setupTestWithContributor(t, nil, WithScannerMode(mode))

	postScannerMode(t, handler, "stock_out")
	if got := getCurrentMode(t, handler); got != "stock_out" {
		t.Fatalf("mode after in-app switch = %q, want stock_out", got)
	}
	if got := postUntaggedScan(t, handler, "111122223333", "user-shared-mode"); got != "stock_out" {
		t.Fatalf("scan after in-app switch = %q, want stock_out", got)
	}

	listener := scanlistener.New()
	listener.Source = scanlistener.SourceStdin
	listener.StockInBarcode = "STOCK_IN"
	listener.StockOutBarcode = "STOCK_OUT"
	listener.Stdin = strings.NewReader("STOCK_IN\n")
	listener.Mode = mode
	listener.Run(context.Background())

	if got := getCurrentMode(t, handler); got != "stock_in" {
		t.Fatalf("mode after physical control barcode = %q, want stock_in", got)
	}
	if got := postUntaggedScan(t, handler, "444455556666", "user-shared-mode"); got != "stock_in" {
		t.Fatalf("scan after physical control barcode = %q, want stock_in", got)
	}
}

// TestScannerModeHandler_ValidModes verifies POST /api/scanner/mode accepts
// both scan directions and echoes the selected mode back.
func TestScannerModeHandler_ValidModes(t *testing.T) {
	tests := []handlerTestCase{
		{
			name: "stock_in is accepted and echoed",
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scanner/mode",
				body:           `{"mode":"stock_in"}`,
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.mode", value: "stock_in"},
				},
			},
		},
		{
			name: "stock_out is accepted and echoed",
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scanner/mode",
				body:           `{"mode":"stock_out"}`,
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.mode", value: "stock_out"},
				},
			},
		},
	}

	runHandlerTests(t, tests)
}

// TestScannerModeHandler_InvalidMode verifies POST /api/scanner/mode rejects
// anything that is not a known scan direction with 400 Bad Request.
func TestScannerModeHandler_InvalidMode(t *testing.T) {
	tests := []handlerTestCase{
		{
			name: "unknown mode string is rejected",
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scanner/mode",
				body:           `{"mode":"sideways"}`,
				expectedStatus: http.StatusBadRequest,
			},
		},
		{
			name: "empty mode is rejected",
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scanner/mode",
				body:           `{"mode":""}`,
				expectedStatus: http.StatusBadRequest,
			},
		},
	}

	runHandlerTests(t, tests)
}

// TestScannerConfigHandler_Defaults verifies GET /api/scanner/config reports
// the STOCK_IN/STOCK_OUT defaults when NewHandler is built without
// WithScannerConfig (the setupTestWithDB case).
func TestScannerConfigHandler_Defaults(t *testing.T) {
	tests := []handlerTestCase{
		{
			name: "config returns the default control-barcode strings",
			httpExchange: httpExchange{
				method:         "GET",
				path:           "/api/scanner/config",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.stockInBarcode", value: "STOCK_IN"},
					{path: "$.stockOutBarcode", value: "STOCK_OUT"},
					// Defaults to stock_in before any switch, matching the
					// headless listener's newModeState.
					{path: "$.currentMode", value: "stock_in"},
					{path: "$.connected", value: false},
				},
			},
		},
	}

	runHandlerTests(t, tests)
}

// TestScannerConfigHandler_ReflectsCurrentMode verifies GET /api/scanner/config
// reports the mode set by a prior POST /api/scanner/mode against the same
// handler, so a browser that connects after a switch starts on the current
// direction. This is the reader of scannerMode that closes the write-only gap.
func TestScannerConfigHandler_ReflectsCurrentMode(t *testing.T) {
	handler, _ := setupTestWithDB(t)

	// Default before any switch is stock_in.
	if got := getCurrentMode(t, handler); got != "stock_in" {
		t.Fatalf("initial currentMode = %q, want %q", got, "stock_in")
	}

	modeReq := httptest.NewRequest(http.MethodPost, "/api/scanner/mode", strings.NewReader(`{"mode":"stock_out"}`))
	modeReq.Header.Set("Content-Type", "application/json")
	modeRes := httptest.NewRecorder()
	handler.ServeHTTP(modeRes, modeReq)
	if modeRes.Code != http.StatusOK {
		t.Fatalf("POST /api/scanner/mode status = %d, want %d", modeRes.Code, http.StatusOK)
	}

	if got := getCurrentMode(t, handler); got != "stock_out" {
		t.Fatalf("currentMode after switch = %q, want %q", got, "stock_out")
	}
}

func getCurrentMode(t *testing.T, handler http.Handler) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/scanner/config", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/scanner/config status = %d, want %d", w.Code, http.StatusOK)
	}
	var body struct {
		CurrentMode string `json:"currentMode"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode config response: %v (%s)", err, w.Body.String())
	}
	return body.CurrentMode
}

// TestScannerConfigHandler_PartialConfigKeepsProvidedValue verifies that
// WithScannerConfig falls back per field: setting only one control barcode
// keeps that value and defaults only the unset one, instead of reverting both.
func TestScannerConfigHandler_PartialConfigKeepsProvidedValue(t *testing.T) {
	handler, _ := NewHandler(nil, nil, nil, nil, WithScannerConfig(ScannerConfig{
		StockInBarcode: "IN-ONLY",
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/scanner/config", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/scanner/config status = %d, want %d", w.Code, http.StatusOK)
	}

	var body struct {
		StockInBarcode  string `json:"stockInBarcode"`
		StockOutBarcode string `json:"stockOutBarcode"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode config response: %v (%s)", err, w.Body.String())
	}
	if body.StockInBarcode != "IN-ONLY" {
		t.Errorf("stockInBarcode = %q, want %q (provided value must be kept)", body.StockInBarcode, "IN-ONLY")
	}
	if body.StockOutBarcode != "STOCK_OUT" {
		t.Errorf("stockOutBarcode = %q, want default %q (unset field only)", body.StockOutBarcode, "STOCK_OUT")
	}
}

// TestScannerConfigHandler_ConfiguredStrings verifies WithScannerConfig
// threads custom control-barcode strings through to GET /api/scanner/config.
func TestScannerConfigHandler_ConfiguredStrings(t *testing.T) {
	handler, _ := NewHandler(nil, nil, nil, nil, WithScannerConfig(ScannerConfig{
		StockInBarcode:  "IN-42",
		StockOutBarcode: "OUT-42",
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/scanner/config", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/scanner/config status = %d, want %d", w.Code, http.StatusOK)
	}

	var body struct {
		StockInBarcode  string `json:"stockInBarcode"`
		StockOutBarcode string `json:"stockOutBarcode"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode config response: %v (%s)", err, w.Body.String())
	}
	if body.StockInBarcode != "IN-42" {
		t.Errorf("stockInBarcode = %q, want %q", body.StockInBarcode, "IN-42")
	}
	if body.StockOutBarcode != "OUT-42" {
		t.Errorf("stockOutBarcode = %q, want %q", body.StockOutBarcode, "OUT-42")
	}
}

func TestScannerConfigHandler_ReportsDeviceConnection(t *testing.T) {
	handler, _ := NewHandler(nil, nil, nil, nil, WithScannerStatus(func() scanlistener.Status {
		return scanlistener.Status{Connected: true, Source: scanlistener.SourceDevice}
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/scanner/config", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/scanner/config status = %d, want %d", w.Code, http.StatusOK)
	}

	var body struct {
		Connected bool `json:"connected"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode config response: %v", err)
	}
	if !body.Connected {
		t.Fatal("connected = false, want true")
	}
}

// TestScannerModeHandler_BroadcastsSSEEvent verifies that POSTing to
// /api/scanner/mode publishes a scanner_mode event through the same
// Broadcaster GET /api/events subscribes to, so a browser-initiated mode
// switch reaches every connected client exactly like a headless control scan.
// It reuses the SSE frame reader from handler_events_test.go.
func TestScannerModeHandler_BroadcastsSSEEvent(t *testing.T) {
	handler, _ := setupTestWithDB(t)

	testServer := httptest.NewServer(handler)
	t.Cleanup(testServer.Close)

	streamCtx, cancelStream := context.WithCancel(context.Background())
	streamReq, err := http.NewRequestWithContext(streamCtx, http.MethodGet, testServer.URL+"/api/events", nil)
	if err != nil {
		t.Fatalf("build GET /api/events request: %v", err)
	}

	client := &http.Client{} // no timeout: the stream stays open for its lifetime
	streamRes, err := client.Do(streamReq)
	if err != nil {
		t.Fatalf("GET /api/events: %v", err)
	}
	if streamRes.StatusCode != http.StatusOK {
		streamRes.Body.Close()
		t.Fatalf("GET /api/events: want %d, got %d", http.StatusOK, streamRes.StatusCode)
	}

	frames := make(chan sseFrame, 16)
	done := make(chan struct{})
	go readSSEFrames(streamRes.Body, frames, done)

	t.Cleanup(func() {
		cancelStream()
		streamRes.Body.Close()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("readSSEFrames goroutine did not exit after cancelling the stream")
		}
	})

	modeRes, err := http.Post(testServer.URL+"/api/scanner/mode", "application/json", strings.NewReader(`{"mode":"stock_out"}`))
	if err != nil {
		t.Fatalf("POST /api/scanner/mode: %v", err)
	}
	defer modeRes.Body.Close()
	if modeRes.StatusCode != http.StatusOK {
		t.Fatalf("POST /api/scanner/mode: want %d, got %d", http.StatusOK, modeRes.StatusCode)
	}

	select {
	case frame := <-frames:
		if frame.event != "scanner_mode" {
			t.Fatalf("SSE event type: want %q, got %q", "scanner_mode", frame.event)
		}
		// PublishScannerModeEvent marshals the raw ScanDirection string, so the
		// data field is a JSON string literal, e.g. "stock_out".
		var mode string
		if err := json.Unmarshal([]byte(frame.data), &mode); err != nil {
			t.Fatalf("decode SSE data: %v (data: %s)", err, frame.data)
		}
		if mode != "stock_out" {
			t.Fatalf("scanner_mode event payload: want %q, got %q", "stock_out", mode)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for a scanner_mode event on the /api/events stream")
	}
}

// clockedScanner starts a handler whose scan direction uses a clock the test
// can move. The idle window begins at the returned instant.
func clockedScanner(t *testing.T) (http.Handler, func(time.Duration)) {
	t.Helper()
	start := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	now := start
	mode := newScannerMode(nil)
	mode.useClock(func() time.Time { return now })
	handler, _ := setupTestWithContributor(t, nil, WithScannerMode(mode))
	return handler, func(d time.Duration) { now = now.Add(d) }
}

func postScannerMode(t *testing.T, handler http.Handler, mode string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/scanner/mode", strings.NewReader(`{"mode":"`+mode+`"}`))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("POST /api/scanner/mode %s: status %d body %s", mode, res.Code, res.Body.String())
	}
}

// postUntaggedScan creates a product scan that does not name a direction, so
// the server stamps whatever mode the next scan is supposed to use.
func postUntaggedScan(t *testing.T, handler http.Handler, barcode, userID string) string {
	t.Helper()
	body := `{"barcode":"` + barcode + `","userId":"` + userID + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/scans", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusCreated {
		t.Fatalf("POST /api/scans: status %d body %s", res.Code, res.Body.String())
	}
	var created struct {
		Direction string `json:"direction"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode scan: %v (%s)", err, res.Body.String())
	}
	return created.Direction
}

// TestScannerMode_SwitchIsWhatTheNextScanUses verifies a mode write shows up
// on the config read and is the direction stamped on the next product scan.
func TestScannerMode_SwitchIsWhatTheNextScanUses(t *testing.T) {
	handler, _ := clockedScanner(t)

	if got := getCurrentMode(t, handler); got != "stock_in" {
		t.Fatalf("initial mode = %q, want stock_in", got)
	}

	postScannerMode(t, handler, "stock_out")
	if got := getCurrentMode(t, handler); got != "stock_out" {
		t.Fatalf("mode after switch = %q, want stock_out", got)
	}
	if got := postUntaggedScan(t, handler, "111122223333", "user-mode-switch"); got != "stock_out" {
		t.Fatalf("next scan direction = %q, want stock_out", got)
	}

	postScannerMode(t, handler, "stock_in")
	if got := postUntaggedScan(t, handler, "444455556666", "user-mode-switch"); got != "stock_in" {
		t.Fatalf("next scan direction = %q, want stock_in", got)
	}
}

// TestScannerMode_IdleRevertsToStockOut proves the server, not a browser
// timer, returns to scan-out after five minutes in scan-in with no product
// scan, and that the scan after that return is stamped scan-out.
func TestScannerMode_IdleRevertsToStockOut(t *testing.T) {
	handler, advance := clockedScanner(t)

	advance(scannerIdleTimeout - time.Second)
	if got := getCurrentMode(t, handler); got != "stock_in" {
		t.Fatalf("mode before idle elapsed = %q, want stock_in", got)
	}

	advance(time.Second)
	// The scan is the first thing to observe the deadline. It must be stamped
	// scan-out, and the mode read after it must agree.
	if got := postUntaggedScan(t, handler, "777788889999", "user-idle"); got != "stock_out" {
		t.Fatalf("scan after idle revert direction = %q, want stock_out", got)
	}
	if got := getCurrentMode(t, handler); got != "stock_out" {
		t.Fatalf("mode after the scan = %q, want stock_out", got)
	}
}

// TestScannerMode_ProductScanResetsIdleWindow verifies a product scan postpones
// the return to scan-out, measured from that scan rather than from when
// scan-in was selected.
func TestScannerMode_ProductScanResetsIdleWindow(t *testing.T) {
	handler, advance := clockedScanner(t)

	advance(4 * time.Minute)
	if got := postUntaggedScan(t, handler, "121212121212", "user-idle-reset"); got != "stock_in" {
		t.Fatalf("scan inside the window direction = %q, want stock_in", got)
	}

	advance(4 * time.Minute)
	if got := getCurrentMode(t, handler); got != "stock_in" {
		t.Fatalf("mode 4m after a scan = %q, want stock_in", got)
	}

	advance(time.Minute)
	if got := getCurrentMode(t, handler); got != "stock_out" {
		t.Fatalf("mode 5m after the last scan = %q, want stock_out", got)
	}
}

// TestScannerMode_NonScanActivityDoesNotResetIdle verifies that reading the
// mode and selecting scan-in again are not scan traffic: five minutes from
// the original entry still returns to scan-out.
func TestScannerMode_NonScanActivityDoesNotResetIdle(t *testing.T) {
	handler, advance := clockedScanner(t)

	advance(4 * time.Minute)
	if got := getCurrentMode(t, handler); got != "stock_in" {
		t.Fatalf("mode while reading config = %q, want stock_in", got)
	}
	postScannerMode(t, handler, "stock_in")

	advance(time.Minute)
	if got := getCurrentMode(t, handler); got != "stock_out" {
		t.Fatalf("mode after config read and repeated switch = %q, want stock_out", got)
	}
	if got := postUntaggedScan(t, handler, "131313131313", "user-idle-noscan"); got != "stock_out" {
		t.Fatalf("next scan direction = %q, want stock_out", got)
	}
}

func TestScannerIdleTimerReturnsToStockOut(t *testing.T) {
	var published []scan.ScanDirection
	mode := NewScannerMode(func(d scan.ScanDirection) {
		published = append(published, d)
	})
	t.Cleanup(func() {
		mode.mu.Lock()
		defer mode.mu.Unlock()
		if mode.stopTimer != nil {
			mode.stopTimer()
		}
	})

	mode.onIdleTimer()
	if len(published) != 0 {
		t.Fatalf("idle timer fired before the window elapsed: %v", published)
	}
	mode.mu.Lock()
	mode.idleAnchor = time.Now().Add(-scannerIdleTimeout - time.Second)
	mode.mu.Unlock()
	mode.onIdleTimer()
	if len(published) != 1 || published[0] != scan.StockOut {
		t.Fatalf("published %v", published)
	}
	if mode.Get() != scan.StockOut {
		t.Fatalf("mode = %s", mode.Get())
	}
}
