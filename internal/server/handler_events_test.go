package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/events"
	"github.com/Rhionin/stockbot/internal/product"
	"github.com/Rhionin/stockbot/internal/scan"
)

// sseFrame is one decoded "event: <type>\ndata: <json>\n\n" server-sent
// event frame.
type sseFrame struct {
	event string
	data  string
}

// readSSEFrames parses SSE frames from body line-by-line, sending each
// completed frame on frames, until a read fails (connection closed or
// context cancelled) or the body reaches EOF. It closes done when it returns,
// so callers can wait for the goroutine to exit during cleanup.
func readSSEFrames(body io.Reader, frames chan<- sseFrame, done chan<- struct{}) {
	defer close(done)

	scanner := bufio.NewScanner(body)
	var current sseFrame
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "event: "):
			current.event = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			current.data = strings.TrimPrefix(line, "data: ")
		case line == "":
			if current.event != "" {
				frames <- current
				current = sseFrame{}
			}
		}
	}
}

// TestEventsStream_ScanEventOnCreate verifies GET /api/events streams a
// "scan" server-sent event carrying the entry created by a normal
// POST /api/scans call against the same handler/DB the stream is subscribed
// to (Requirements 1.1, 1.2, 1.5, 2.1).
func TestEventsStream_ScanEventOnCreate(t *testing.T) {
	handler, env := setupTestWithDB(t)
	catalog := env.ProductStore

	const barcode = "999888777666"
	if err := catalog.CreateProduct(context.Background(), product.Product{
		ID: "prod-events", Name: "Events Product", Category: "Test", UnitOfMeasure: "unit",
	}); err != nil {
		t.Fatalf("CreateProduct: %v", err)
	}
	if err := catalog.UpsertBarcodeMapping(context.Background(), barcode, "prod-events", "global", ""); err != nil {
		t.Fatalf("UpsertBarcodeMapping: %v", err)
	}

	testServer := httptest.NewServer(handler)
	// Registered before the stream's own cleanup below, so LIFO ordering
	// closes the stream (and lets EventsHandler.Handle return) before
	// Close() blocks waiting for that connection to go idle.
	t.Cleanup(testServer.Close)

	streamCtx, cancelStream := context.WithCancel(context.Background())
	streamReq, err := http.NewRequestWithContext(streamCtx, http.MethodGet, testServer.URL+"/api/events", nil)
	if err != nil {
		t.Fatalf("build GET /api/events request: %v", err)
	}

	client := &http.Client{} // no timeout: the connection is meant to stay open for the stream's lifetime
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

	// Trigger a mutation through the normal POST /api/scans path, on the same
	// handler/DB the stream above is subscribed to.
	createBody := `{"barcode":"` + barcode + `","direction":"stock_in","userId":"events-user"}`
	postRes, err := http.Post(testServer.URL+"/api/scans", "application/json", strings.NewReader(createBody))
	if err != nil {
		t.Fatalf("POST /api/scans: %v", err)
	}
	defer postRes.Body.Close()
	if postRes.StatusCode != http.StatusCreated {
		t.Fatalf("POST /api/scans: want %d, got %d", http.StatusCreated, postRes.StatusCode)
	}

	var created struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(postRes.Body).Decode(&created); err != nil {
		t.Fatalf("decode POST /api/scans response: %v", err)
	}
	if created.ID == "" {
		t.Fatal("created scan entry has empty id")
	}

	processing := waitSSEFrame(t, frames)
	if processing.event != "scan_processing" {
		t.Fatalf("first SSE event type: want %q, got %q", "scan_processing", processing.event)
	}
	var notice struct {
		Barcode string `json:"barcode"`
	}
	if err := json.Unmarshal([]byte(processing.data), &notice); err != nil {
		t.Fatalf("decode processing event: %v (data: %s)", err, processing.data)
	}
	if notice.Barcode != barcode {
		t.Fatalf("processing barcode: want %q, got %q", barcode, notice.Barcode)
	}

	frame := waitSSEFrame(t, frames)
	if frame.event != "scan" {
		t.Fatalf("SSE event type: want %q, got %q", "scan", frame.event)
	}
	var payload struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(frame.data), &payload); err != nil {
		t.Fatalf("decode SSE data: %v (data: %s)", err, frame.data)
	}
	if payload.ID != created.ID {
		t.Fatalf("SSE event id: want %q, got %q", created.ID, payload.ID)
	}
}

// An idle stream writes an SSE comment so the proxy in front of the Pi does
// not drop it. The comment is not an event, and a real event published
// afterward still arrives intact.
func TestEventsStream_KeepaliveCommentThenRealEvent(t *testing.T) {
	broadcaster := events.NewBroadcaster()
	handler := &EventsHandler{Broadcaster: broadcaster, Keepalive: 15 * time.Millisecond}
	testServer := httptest.NewServer(http.HandlerFunc(handler.Handle))
	t.Cleanup(testServer.Close)

	streamCtx, cancelStream := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(streamCtx, http.MethodGet, testServer.URL, nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET stream: %v", err)
	}
	if res.StatusCode != http.StatusOK {
		res.Body.Close()
		t.Fatalf("status: want %d, got %d", http.StatusOK, res.StatusCode)
	}
	if got := res.Header.Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("content type: want text/event-stream, got %q", got)
	}

	lines := make(chan string, 32)
	done := make(chan struct{})
	go func() {
		defer close(done)
		scanner := bufio.NewScanner(res.Body)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
	}()
	t.Cleanup(func() {
		cancelStream()
		res.Body.Close()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("stream reader did not exit")
		}
	})

	sawComment := false
	deadline := time.After(2 * time.Second)
	for !sawComment {
		select {
		case line := <-lines:
			if strings.HasPrefix(line, "event:") || strings.HasPrefix(line, "data:") {
				t.Fatalf("keepalive looked like an event: %q", line)
			}
			sawComment = line == ": ping"
		case <-deadline:
			t.Fatal("timed out waiting for a keepalive comment")
		}
	}

	broadcaster.PublishScannerModeEvent(scan.StockIn)
	var eventLine, dataLine string
	deadline = time.After(2 * time.Second)
	for eventLine == "" || dataLine == "" {
		select {
		case line := <-lines:
			switch {
			case line == ": ping" || line == "" || strings.HasPrefix(line, "id:"):
			case strings.HasPrefix(line, "event: "):
				eventLine = line
			case strings.HasPrefix(line, "data: "):
				dataLine = line
			default:
				t.Fatalf("unexpected stream line %q", line)
			}
		case <-deadline:
			t.Fatalf("timed out waiting for the event (event %q data %q)", eventLine, dataLine)
		}
	}
	if eventLine != "event: scanner_mode" {
		t.Fatalf("event line: got %q", eventLine)
	}
	if dataLine != `data: "stock_in"` {
		t.Fatalf("data line: got %q", dataLine)
	}
}

func TestEventsStreamingUnsupported(t *testing.T) {
	var logs bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(previous) })

	handler := &EventsHandler{}
	rec := &plainResponse{}
	req := httptest.NewRequest(http.MethodGet, "/api/events", nil)
	handler.Handle(rec, req)

	if rec.code != http.StatusInternalServerError {
		t.Fatalf("status = %d, body = %s", rec.code, rec.body.String())
	}
	if rec.body.String() != "Something went wrong. Try again.\n" {
		t.Fatalf("body = %q", rec.body.String())
	}
	if strings.Contains(rec.body.String(), "streaming") {
		t.Fatalf("body leaked the reason: %s", rec.body.String())
	}
	if !strings.Contains(logs.String(), "events: streaming unsupported") {
		t.Fatalf("log = %q", logs.String())
	}
}

type plainResponse struct {
	header http.Header
	code   int
	body   bytes.Buffer
}

func (p *plainResponse) Header() http.Header {
	if p.header == nil {
		p.header = make(http.Header)
	}
	return p.header
}

func (p *plainResponse) Write(b []byte) (int, error) {
	if p.code == 0 {
		p.code = http.StatusOK
	}
	return p.body.Write(b)
}

func (p *plainResponse) WriteHeader(status int) { p.code = status }

func waitSSEFrame(t *testing.T, frames <-chan sseFrame) sseFrame {
	t.Helper()
	select {
	case frame := <-frames:
		return frame
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for an SSE frame")
	}
	return sseFrame{}
}
