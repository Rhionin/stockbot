package server

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/Rhionin/stockbot/internal/clientmsg"
	"github.com/Rhionin/stockbot/internal/events"
	"github.com/Rhionin/stockbot/internal/telemetry"
)

// eventKeepalive is how often an open /api/events stream writes an SSE
// comment. The proxy in front of the Pi closes an idle HTTP response after
// about 100 seconds, and a half-open stream looks alive to the browser until
// the next write. A comment is not an event, so the page and the Grocy
// watcher ignore it.
const eventKeepalive = 25 * time.Second

type EventsHandler struct {
	Broadcaster *events.Broadcaster
	Telemetry   *telemetry.Registry
	// Shutdown, when set, ends this stream during process shutdown so the
	// listener does not wait out an open tab.
	Shutdown *StreamShutdown
	// Streams cancels a member's event stream when their access ends.
	Streams *auth.Streams
	// Keepalive is how often to write an SSE comment. Zero uses eventKeepalive.
	Keepalive time.Duration
}

func (h *EventsHandler) keepaliveEvery() time.Duration {
	if h.Keepalive > 0 {
		return h.Keepalive
	}
	return eventKeepalive
}

// Handle implements GET /api/events (Requirement 1.1). It blocks for the
// lifetime of the connection, writing one
// "id: <seq>\nevent: <type>\ndata: <json>\n\n" frame per published message
// until the client disconnects (Requirement 1.3) or a write to it fails
// (Requirement 1.4).
func (h *EventsHandler) Handle(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		log.Printf("events: streaming unsupported")
		writeError(w, http.StatusInternalServerError, codeInternal, clientmsg.Generic)
		return
	}

	ctx := r.Context()
	if h.Shutdown != nil {
		ctx = h.Shutdown.Context(ctx)
	}
	principal, _ := auth.PrincipalFrom(r.Context())
	householdID := principal.HouseholdID
	if scope, err := requestScope(r.Context()); err == nil && householdID == "" {
		householdID = scope.HouseholdID()
	}
	if h.Streams != nil {
		if key := eventStreamKey(r); key != "" {
			bound, err := h.Streams.BindLive(ctx, key, householdID, principal.UserID, principal.Kind)
			if err != nil {
				secs := 1
				var ae *auth.Error
				if errors.As(err, &ae) && ae.Retry > 0 {
					secs = int(ae.Retry.Seconds())
					if secs < 1 {
						secs = 1
					}
				}
				w.Header().Set("Retry-After", strconv.Itoa(secs))
				writeJSON(w, http.StatusTooManyRequests, errorPayload{Error: auth.SentenceRate, Code: codeRateLimited})
				return
			}
			ctx = bound
		}
	}

	// Subscribe before flushing the response headers. A client that observes
	// the 200 OK is then guaranteed to already be subscribed, so an event
	// published immediately after the client's request returns cannot slip
	// through the gap between headers being sent and the subscription being
	// registered. The stream is this household only.
	messages, unsubscribe := h.Broadcaster.SubscribeHousehold(householdID, principal.UserID, principal.Kind)
	defer unsubscribe()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	// The tunnel can hold a response that has flushed headers and no body.
	// The comment is not an event. It is the first body byte.
	if _, err := w.Write([]byte(": ok\n\n")); err != nil {
		return
	}
	flusher.Flush()

	keepalive := time.NewTicker(h.keepaliveEvery())
	defer keepalive.Stop()

	for {
		select {
		case <-keepalive.C:
			if _, err := io.WriteString(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case msg, ok := <-messages:
			if !ok {
				return // buffer overflow: Broadcaster already dropped us
			}
			// The id lets the browser notice a gap. Payloads are unchanged;
			// EventSource exposes the id separately as lastEventId.
			if _, err := fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", msg.Seq, msg.EventType, msg.Data); err != nil {
				if h.Telemetry != nil {
					h.Telemetry.ObserveWriteError(msg.EventType)
				}
				return // write failed: stop, defer unsubscribes us
			}
			// Count the write before Flush so a client that reads the frame
			// cannot observe the snapshot before the delivery is recorded.
			if h.Telemetry != nil {
				h.recordDelivery(msg)
			}
			flusher.Flush()
		case <-ctx.Done():
			return // client disconnected, or the process is stopping
		}
	}
}

func (h *EventsHandler) recordDelivery(msg events.Message) {
	fanout := time.Since(msg.PublishedAt)
	if msg.PublishedAt.IsZero() {
		fanout = 0
	}
	hasIngest := !msg.IngestAt.IsZero()
	var ingest time.Duration
	if hasIngest {
		ingest = time.Since(msg.IngestAt)
	}
	h.Telemetry.ObserveDelivery(msg.EventType, fanout, ingest, hasIngest)
}
