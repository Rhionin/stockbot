// Package events provides an in-memory publish/subscribe broadcaster that
// delivers Scan_Events and Inventory_Events to every browser connected to
// GET /api/events. It has no persistence: a subscriber only ever receives
// events published after it subscribes (Requirement 1.2), and there is no
// buffering or replay for a client that reconnects after a gap
// (Requirement 6.2 is satisfied by this absence, not by extra bookkeeping).
package events

import (
	"context"
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/Rhionin/stockbot/internal/inventory"
	"github.com/Rhionin/stockbot/internal/scan"
)

// subscriberBuffer bounds how many unread messages a single connection may
// accumulate before it is treated as unable to keep up and dropped, so one
// slow reader can never block delivery to the others (Requirement 1.4).
const subscriberBuffer = 16

// Message is one server-sent event: EventType becomes the SSE "event:" field
// ("scan" or "inventory") and Data is the JSON-encoded ScanEntry or
// InventoryItem that becomes the "data:" field.
//
// Seq is a process-wide id written as the SSE "id:" field so a browser can
// notice a gap after a dropped connection. PublishedAt and IngestAt are for
// delivery latency; IngestAt is the scan timestamp when the payload has one.
type Message struct {
	EventType   string
	Data        []byte
	Seq         int64
	PublishedAt time.Time
	IngestAt    time.Time
	// HouseholdID routes the frame. It is not part of the JSON payload.
	HouseholdID string
}

// Observer receives publish outcomes. Implementations must be safe for
// concurrent use. publish does not hold its lock while calling the observer.
type Observer interface {
	Published(eventType string, subscribers, enqueued, dropped int)
	MarshalFailed(eventType string)
}

// subscriber is one open stream. legacy receives only events that name no
// household, which is how the original single-tenant tests publish.
// all receives every household, for the scan-alert fanout.
type subscriber struct {
	ch          chan Message
	householdID string
	userID      string
	kind        string
	sessionID   string
	all         bool
	legacy      bool
}

// Broadcaster fans out published events. A household stream receives only
// that household. The zero value is not usable; construct with NewBroadcaster.
type Broadcaster struct {
	mu          sync.Mutex
	nextID      int64
	eventSeq    int64
	subscribers map[int64]*subscriber
	observer    Observer
	cursor      func(householdID, scope string) string
}

func NewBroadcaster() *Broadcaster {
	return &Broadcaster{subscribers: make(map[int64]*subscriber)}
}

// SetObserver installs the publish observer. It is meant to be called once,
// before events are published.
func (b *Broadcaster) SetObserver(observer Observer) {
	b.mu.Lock()
	b.observer = observer
	b.mu.Unlock()
}

// SetCursor installs the hint cursor. Nil leaves the cursor the caller set.
func (b *Broadcaster) SetCursor(cursor func(householdID, scope string) string) {
	if b == nil {
		return
	}
	b.mu.Lock()
	b.cursor = cursor
	b.mu.Unlock()
}

// SubscriberCount reports how many connections will receive the next event.
func (b *Broadcaster) SubscriberCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.subscribers)
}

// Subscribe registers a new connection and returns the channel it should
// read published messages from, plus an unsubscribe function the caller
// MUST call exactly once when the connection ends (Requirement 1.3). The
// returned channel receives only messages published after Subscribe returns.
func (b *Broadcaster) Subscribe() (<-chan Message, func()) {
	return b.add(&subscriber{legacy: true})
}

// SubscribeHousehold returns events for one household.
func (b *Broadcaster) SubscribeHousehold(householdID, userID, kind string) (<-chan Message, func()) {
	return b.subscribe(householdID, userID, kind, "")
}

// SubscribeSession returns one household's events for a single session.
// DropSession closes that session without touching the account's other sessions.
func (b *Broadcaster) SubscribeSession(householdID, userID, kind, sessionID string) (<-chan Message, func()) {
	return b.subscribe(householdID, userID, kind, sessionID)
}

func (b *Broadcaster) subscribe(householdID, userID, kind, sessionID string) (<-chan Message, func()) {
	return b.add(&subscriber{householdID: householdID, userID: userID, kind: kind, sessionID: sessionID})
}

// SubscribeAll returns every household's events. The scan-alert fanout uses it.
func (b *Broadcaster) SubscribeAll() (<-chan Message, func()) {
	return b.add(&subscriber{all: true})
}

func (b *Broadcaster) add(sub *subscriber) (<-chan Message, func()) {
	b.mu.Lock()
	defer b.mu.Unlock()

	id := b.nextID
	b.nextID++
	sub.ch = make(chan Message, subscriberBuffer)
	b.subscribers[id] = sub

	unsubscribe := func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		delete(b.subscribers, id)
	}
	return sub.ch, unsubscribe
}

// PublishScanEvent delivers entry to every currently-subscribed connection.
// entry.ScannedAt is kept as the ingest timestamp so delivery latency can be
// measured from the scan itself, not only from the moment of publish.
func (b *Broadcaster) PublishScanEvent(householdID string, entry scan.ScanEntry) {
	b.publish("scan", entry, entry.ScannedAt, householdID)
	if householdID != "" {
		b.PublishHint(householdID, "scans")
	}
}

// PublishInventoryEvent delivers item to every currently-subscribed connection.
func (b *Broadcaster) PublishInventoryEvent(householdID string, item inventory.InventoryItem) {
	b.publish("inventory", item, time.Time{}, householdID)
	if householdID != "" {
		b.PublishHint(householdID, "stock")
	}
}

// PublishScannerModeEvent delivers mode to every currently-subscribed connection.
func (b *Broadcaster) PublishScannerModeEvent(mode scan.ScanDirection) {
	b.publish("scanner_mode", mode, time.Time{}, "")
}

// PublishScannerModeFor delivers mode to one household.
func (b *Broadcaster) PublishScannerModeFor(householdID string, mode scan.ScanDirection) {
	b.publish("scanner_mode", mode, time.Time{}, householdID)
}

// PublishScanProcessingEvent delivers a just-accepted barcode before product
// lookup finishes, so a subscriber can show that the scan was received.
func (b *Broadcaster) PublishScanProcessingEvent(notice scan.ProcessingNotice) {
	b.publish("scan_processing", notice, notice.ScannedAt, notice.HouseholdID)
}

// PublishScanProcessingFailedEvent withdraws a processing notice when lookup
// does not finish.
func (b *Broadcaster) PublishScanProcessingFailedEvent(failure scan.ProcessingFailure) {
	b.publish("scan_processing_failed", failure, time.Time{}, failure.HouseholdID)
}

// PublishHint tells one household to refetch. The cursor comes from SetCursor.
func (b *Broadcaster) PublishHint(householdID, kind string) {
	if b == nil || householdID == "" || kind == "" {
		return
	}
	b.Publish(context.Background(), householdID, LiveHint{Type: kind}, nil)
}

// Hub exposes this broadcaster as a LiveHub.
func (b *Broadcaster) Hub() LiveHub {
	return liveHub{b}
}

type liveHub struct{ b *Broadcaster }

func (h liveHub) Publish(ctx context.Context, householdID string, hint LiveHint, legacy any) {
	if h.b != nil {
		h.b.Publish(ctx, householdID, hint, legacy)
	}
}

func (h liveHub) Subscribe(p Principal) (<-chan Event, func()) {
	if h.b == nil {
		ch := make(chan Event)
		close(ch)
		return ch, func() {}
	}
	return h.b.SubscribePrincipal(p)
}

func (h liveHub) Kick(householdID, userID string) {
	if h.b != nil {
		h.b.Kick(householdID, userID)
	}
}

// DropSession closes streams for one session hash.
func (h liveHub) DropSession(sessionID string) {
	if h.b != nil {
		h.b.DropSession(sessionID)
	}
}

// Publish delivers a hint, and an optional legacy payload, to one household.
func (b *Broadcaster) Publish(ctx context.Context, householdID string, hint LiveHint, legacy any) {
	if b == nil || householdID == "" || hint.Type == "" {
		return
	}
	if hint.Type == "rekey" {
		return
	}
	if hint.Type == "devices" {
		hint.Cursor = ""
	} else if hint.Cursor == "" {
		hint.Cursor = b.hintCursor(householdID, hint.Type)
	}
	b.publish("hint", hint, time.Time{}, householdID)
	if legacy == nil {
		return
	}
	if ev, ok := legacy.(Legacy); ok && ev.Event != "" {
		b.publish(ev.Event, ev.Payload, time.Time{}, householdID)
	}
}

func (b *Broadcaster) hintCursor(householdID, kind string) string {
	b.mu.Lock()
	cursor := b.cursor
	b.mu.Unlock()
	if cursor == nil {
		return ""
	}
	switch kind {
	case "scans", "stock", "list":
		return cursor(householdID, kind)
	default:
		return ""
	}
}

// SubscribePrincipal registers a household stream and copies frames to Event.
func (b *Broadcaster) SubscribePrincipal(p Principal) (<-chan Event, func()) {
	msgs, cancel := b.SubscribeHousehold(p.HouseholdID, p.UserID, p.Kind)
	out := make(chan Event, subscriberBuffer)
	go func() {
		defer close(out)
		for msg := range msgs {
			ev := Event{EventType: msg.EventType, Data: append([]byte(nil), msg.Data...), HouseholdID: msg.HouseholdID}
			select {
			case out <- ev:
			default:
			}
		}
	}()
	return out, cancel
}

// Kick closes streams for one member of a household. An empty household closes
// that user's streams in every household. An empty user closes household-password
// streams in the household.
// DropSession closes streams opened for one session. Other sessions stay up.
func (b *Broadcaster) DropSession(sessionID string) {
	if b == nil || sessionID == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for id, sub := range b.subscribers {
		if sub.sessionID != sessionID {
			continue
		}
		close(sub.ch)
		delete(b.subscribers, id)
	}
}

func (b *Broadcaster) Kick(householdID, userID string) {
	if b == nil || (householdID == "" && userID == "") {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for id, sub := range b.subscribers {
		if !kickMatch(sub, householdID, userID) {
			continue
		}
		close(sub.ch)
		delete(b.subscribers, id)
	}
}

func kickMatch(sub *subscriber, householdID, userID string) bool {
	if sub == nil {
		return false
	}
	if userID != "" && householdID != "" {
		return sub.userID == userID && sub.householdID == householdID
	}
	if userID != "" {
		return sub.userID == userID
	}
	return sub.householdID == householdID && sub.userID == ""
}

func (b *Broadcaster) publish(eventType string, payload any, ingestAt time.Time, householdID string) {
	data, err := json.Marshal(payload)
	if err != nil {
		log.Printf("events: marshal %s event: %v", eventType, err)
		b.mu.Lock()
		obs := b.observer
		b.mu.Unlock()
		if obs != nil {
			obs.MarshalFailed(eventType)
		}
		return
	}

	b.mu.Lock()
	b.eventSeq++
	if eventType == "hint" && len(data) >= 1024 {
		b.mu.Unlock()
		return
	}
	msg := Message{
		EventType:   eventType,
		Data:        data,
		Seq:         b.eventSeq,
		PublishedAt: time.Now(),
		IngestAt:    ingestAt,
		HouseholdID: householdID,
	}
	subscribers := len(b.subscribers)
	enqueued := 0
	dropped := 0
	for id, sub := range b.subscribers {
		if !delivers(sub, householdID) {
			continue
		}
		select {
		case sub.ch <- msg:
			enqueued++
		default:
			// Subscriber's buffer is full: it cannot keep up. Drop it here
			// rather than block every other subscriber's delivery.
			close(sub.ch)
			delete(b.subscribers, id)
			dropped++
		}
	}
	obs := b.observer
	b.mu.Unlock()
	if obs != nil {
		obs.Published(eventType, subscribers, enqueued, dropped)
	}
}

func delivers(sub *subscriber, householdID string) bool {
	if sub == nil {
		return false
	}
	if sub.all {
		return true
	}
	if sub.legacy {
		return householdID == ""
	}
	return sub.householdID != "" && sub.householdID == householdID
}
