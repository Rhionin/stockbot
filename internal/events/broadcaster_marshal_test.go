package events

import (
	"testing"
	"time"
)

type marshalObserver struct {
	failed string
}

func (o *marshalObserver) Published(string, int, int, int) {}

func (o *marshalObserver) MarshalFailed(eventType string) {
	o.failed = eventType
}

func TestPublish_MarshalErrorNotifiesObserver(t *testing.T) {
	broadcaster := NewBroadcaster()
	obs := &marshalObserver{}
	broadcaster.SetObserver(obs)

	// A channel cannot be encoded as JSON, so publish fails before any send.
	broadcaster.publish("scan", make(chan int), time.Time{}, "")

	if obs.failed != "scan" {
		t.Fatalf("marshal failure event = %q, want scan", obs.failed)
	}
	if broadcaster.eventSeq != 0 {
		t.Fatalf("eventSeq = %d, want 0 after a failed encode", broadcaster.eventSeq)
	}
}
