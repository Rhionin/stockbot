// Package events provides an in-memory publish/subscribe broadcaster that
// delivers Scan_Events and Inventory_Events to every browser connected to
// GET /api/events. It has no persistence: a subscriber only ever receives
// events published after it subscribes (Requirement 1.2), and there is no
// buffering or replay for a client that reconnects after a gap
// (Requirement 6.2 is satisfied by this absence, not by extra bookkeeping).
package events

import (
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
}

// Observer receives publish outcomes. Implementations must be safe for
// concurrent use. publish does not hold its lock while calling the observer.
type Observer interface {
	Published(eventType string, subscribers, enqueued, dropped int)
	MarshalFailed(eventType string)
}

// Broadcaster fans out published events to every currently-subscribed
// connection. The zero value is not usable; construct with NewBroadcaster.
type Broadcaster struct {
	mu          sync.Mutex
	nextID      int64
	eventSeq    int64
	subscribers map[int64]chan Message
	observer    Observer
}

func NewBroadcaster() *Broadcaster {
	return &Broadcaster{subscribers: make(map[int64]chan Message)}
}

// SetObserver installs the publish observer. It is meant to be called once,
// before events are published.
func (b *Broadcaster) SetObserver(observer Observer) {
	b.mu.Lock()
	b.observer = observer
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
	b.mu.Lock()
	defer b.mu.Unlock()

	id := b.nextID
	b.nextID++
	ch := make(chan Message, subscriberBuffer)
	b.subscribers[id] = ch

	unsubscribe := func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		delete(b.subscribers, id)
	}
	return ch, unsubscribe
}

// PublishScanEvent delivers entry to every currently-subscribed connection.
// entry.ScannedAt is kept as the ingest timestamp so delivery latency can be
// measured from the scan itself, not only from the moment of publish.
func (b *Broadcaster) PublishScanEvent(entry scan.ScanEntry) {
	b.publish("scan", entry, entry.ScannedAt)
}

// PublishInventoryEvent delivers item to every currently-subscribed connection.
func (b *Broadcaster) PublishInventoryEvent(item inventory.InventoryItem) {
	b.publish("inventory", item, time.Time{})
}

// PublishScannerModeEvent delivers mode to every currently-subscribed connection.
func (b *Broadcaster) PublishScannerModeEvent(mode scan.ScanDirection) {
	b.publish("scanner_mode", mode, time.Time{})
}

// PublishScanProcessingEvent delivers a just-accepted barcode before product
// lookup finishes, so a subscriber can show that the scan was received.
func (b *Broadcaster) PublishScanProcessingEvent(notice scan.ProcessingNotice) {
	b.publish("scan_processing", notice, notice.ScannedAt)
}

// PublishScanProcessingFailedEvent withdraws a processing notice when lookup
// does not finish.
func (b *Broadcaster) PublishScanProcessingFailedEvent(failure scan.ProcessingFailure) {
	b.publish("scan_processing_failed", failure, time.Time{})
}

// Hint is a household refetch signal. It carries no names and no barcodes.
type Hint struct {
	Type        string `json:"type"`
	HouseholdID string `json:"householdId"`
}

// PublishHint tells subscribers to refetch one household collection.
func (b *Broadcaster) PublishHint(householdID, kind string) {
	if b == nil || householdID == "" || kind == "" {
		return
	}
	b.publish("hint", Hint{Type: kind, HouseholdID: householdID}, time.Time{})
}

func (b *Broadcaster) publish(eventType string, payload any, ingestAt time.Time) {
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
	msg := Message{
		EventType:   eventType,
		Data:        data,
		Seq:         b.eventSeq,
		PublishedAt: time.Now(),
		IngestAt:    ingestAt,
	}
	subscribers := len(b.subscribers)
	enqueued := 0
	dropped := 0
	for id, ch := range b.subscribers {
		select {
		case ch <- msg:
			enqueued++
		default:
			// Subscriber's buffer is full: it cannot keep up. Drop it here
			// rather than block every other subscriber's delivery
			// (Requirement 1.4's "continue delivering ... to all other
			// connected clients"). EventsHandler treats a dropped/closed
			// subscriber the same way it treats a write error: the browser's
			// native EventSource reconnect (Requirement 6.1) recovers it.
			// There is no replay, so the dropped browser misses this event.
			close(ch)
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
