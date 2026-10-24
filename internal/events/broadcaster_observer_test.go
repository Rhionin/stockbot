package events_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/events"
	"github.com/Rhionin/stockbot/internal/scan"
)

type recordingObserver struct {
	published   int
	subscribers int
	enqueued    int
	dropped     int
}

func (o *recordingObserver) Published(eventType string, subscribers, enqueued, dropped int) {
	o.published++
	o.subscribers = subscribers
	o.enqueued += enqueued
	o.dropped += dropped
}

func (o *recordingObserver) MarshalFailed(string) {}

func TestPublish_AssignsSeqAndIngestTime(t *testing.T) {
	broadcaster := events.NewBroadcaster()
	obs := &recordingObserver{}
	broadcaster.SetObserver(obs)

	ch, unsubscribe := broadcaster.Subscribe()
	defer unsubscribe()

	scannedAt := time.Now().Add(-time.Second)
	broadcaster.PublishScanEvent("", scan.ScanEntry{ID: "scan-seq", ScannedAt: scannedAt})
	msg := recvMessage(t, ch)
	if msg.Seq != 1 {
		t.Fatalf("seq = %d, want 1", msg.Seq)
	}
	if msg.PublishedAt.IsZero() {
		t.Fatal("PublishedAt was not set")
	}
	if !msg.IngestAt.Equal(scannedAt) {
		t.Fatalf("IngestAt = %v, want %v", msg.IngestAt, scannedAt)
	}
	if obs.published != 1 || obs.subscribers != 1 || obs.enqueued != 1 || obs.dropped != 0 {
		t.Fatalf("observer = %+v", obs)
	}
	if broadcaster.SubscriberCount() != 1 {
		t.Fatalf("subscribers = %d, want 1", broadcaster.SubscriberCount())
	}
}

func TestPublish_NoSubscriberNotifiesObserver(t *testing.T) {
	broadcaster := events.NewBroadcaster()
	obs := &recordingObserver{}
	broadcaster.SetObserver(obs)

	broadcaster.PublishScanEvent("", scan.ScanEntry{ID: "nobody"})
	if obs.published != 1 || obs.subscribers != 0 || obs.enqueued != 0 {
		t.Fatalf("observer = %+v", obs)
	}
	if broadcaster.SubscriberCount() != 0 {
		t.Fatalf("subscribers = %d, want 0", broadcaster.SubscriberCount())
	}
}

func TestPublish_ObserverSeesDroppedSubscriber(t *testing.T) {
	broadcaster := events.NewBroadcaster()
	obs := &recordingObserver{}
	broadcaster.SetObserver(obs)

	slow, unsubscribe := broadcaster.Subscribe()
	defer unsubscribe()

	for i := 0; i < subscriberBuffer; i++ {
		broadcaster.PublishScanEvent("", scan.ScanEntry{ID: fmt.Sprintf("scan-%d", i)})
	}
	broadcaster.PublishScanEvent("", scan.ScanEntry{ID: "overflow"})

	if obs.dropped != 1 {
		t.Fatalf("dropped = %d, want 1", obs.dropped)
	}
	if broadcaster.SubscriberCount() != 0 {
		t.Fatalf("subscribers = %d after drop, want 0", broadcaster.SubscriberCount())
	}
	for i := 0; i < subscriberBuffer; i++ {
		recvMessage(t, slow)
	}
}
