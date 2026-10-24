package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/Rhionin/stockbot/internal/events"
	"github.com/Rhionin/stockbot/internal/scan"
	"github.com/Rhionin/stockbot/internal/telemetry"
)

type flushOnce struct {
	*httptest.ResponseRecorder
	once sync.Once
	then func()
}

func (f *flushOnce) Flush() {
	if f.then != nil {
		f.once.Do(f.then)
	}
}

func (f *flushOnce) Write([]byte) (int, error) {
	return 0, errors.New("client closed the stream")
}

func TestEventsRefuseAnEleventhStreamAndAClosedWriter(t *testing.T) {
	streams := auth.NewStreams()
	var cancels []context.CancelFunc
	t.Cleanup(func() {
		for _, cancel := range cancels {
			cancel()
		}
	})
	for range 10 {
		ctx, cancel := context.WithCancel(context.Background())
		cancels = append(cancels, cancel)
		if _, err := streams.Bind(ctx, "lan:192.0.2.10"); err != nil {
			t.Fatal(err)
		}
	}

	broadcaster := events.NewBroadcaster()
	handler := &EventsHandler{Broadcaster: broadcaster, Streams: streams, Keepalive: time.Hour}
	req := httptest.NewRequest(http.MethodGet, "/api/events", nil)
	req.RemoteAddr = "192.0.2.10:443"
	req = req.WithContext(auth.WithPrincipal(req.Context(), auth.Principal{Kind: "lan"}))
	rec := httptest.NewRecorder()
	handler.Handle(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("eleventh stream = %d %s", rec.Code, rec.Body.String())
	}

	writer := &flushOnce{ResponseRecorder: httptest.NewRecorder(), then: func() {
		broadcaster.PublishScannerModeEvent(scan.StockOut)
	}}
	closed := httptest.NewRequest(http.MethodGet, "/api/events", nil)
	handler = &EventsHandler{
		Broadcaster: broadcaster,
		Telemetry:   telemetry.NewRegistry(),
		Keepalive:   time.Hour,
	}
	handler.Handle(writer, closed)

	delivery := &EventsHandler{Telemetry: telemetry.NewRegistry()}
	delivery.recordDelivery(events.Message{EventType: "scan"})
	delivery.recordDelivery(events.Message{EventType: "scan", PublishedAt: time.Now(), IngestAt: time.Now()})

	failing := &failAfterOK{ResponseRecorder: httptest.NewRecorder(), publish: func() {
		broadcaster.PublishScannerModeEvent(scan.StockIn)
	}}
	req = httptest.NewRequest(http.MethodGet, "/api/events", nil)
	handler = &EventsHandler{Broadcaster: broadcaster, Telemetry: telemetry.NewRegistry(), Keepalive: time.Hour}
	handler.Handle(failing, req)
	if failing.n < 2 {
		t.Fatalf("event write was not attempted (%d writes)", failing.n)
	}
}

// failAfterOK accepts the connect comment and fails the next body write.
type failAfterOK struct {
	*httptest.ResponseRecorder
	n       int
	publish func()
	once    sync.Once
}

func (f *failAfterOK) Write(p []byte) (int, error) {
	f.n++
	if f.n > 1 {
		return 0, errors.New("client closed the stream")
	}
	return f.ResponseRecorder.Write(p)
}

func (f *failAfterOK) Flush() {
	if f.publish != nil {
		f.once.Do(f.publish)
	}
}
