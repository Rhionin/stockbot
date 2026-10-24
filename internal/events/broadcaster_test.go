package events_test

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/events"
	"github.com/Rhionin/stockbot/internal/inventory"
	"github.com/Rhionin/stockbot/internal/scan"
)

// subscriberBuffer mirrors the unexported constant of the same name in
// broadcaster.go so the buffer-overflow test can fill a subscriber's channel
// to exactly its capacity.
const subscriberBuffer = 16

const recvTimeout = time.Second

// recvMessage waits up to recvTimeout for a message on ch and fails the test
// if none arrives.
func recvMessage(t *testing.T, ch <-chan events.Message) events.Message {
	t.Helper()
	select {
	case msg, ok := <-ch:
		if !ok {
			t.Fatal("channel closed before delivering a message")
		}
		return msg
	case <-time.After(recvTimeout):
		t.Fatal("timed out waiting for message")
		return events.Message{}
	}
}

// assertNoMessage fails the test if a message arrives on ch within a short
// window, confirming that no delivery occurred.
func assertNoMessage(t *testing.T, ch <-chan events.Message) {
	t.Helper()
	select {
	case msg, ok := <-ch:
		if ok {
			t.Fatalf("unexpected message delivered: %+v", msg)
		}
	case <-time.After(50 * time.Millisecond):
	}
}

func TestSubscribe_PublishScanEvent_DeliversOneMessage(t *testing.T) {
	broadcaster := events.NewBroadcaster()
	ch, unsubscribe := broadcaster.Subscribe()
	defer unsubscribe()

	entry := scan.ScanEntry{ID: "scan-1", Barcode: "000000000001"}
	broadcaster.PublishScanEvent("", entry)

	msg := recvMessage(t, ch)
	if msg.EventType != "scan" {
		t.Errorf("EventType: got %q, want %q", msg.EventType, "scan")
	}
	var got scan.ScanEntry
	if err := json.Unmarshal(msg.Data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !reflect.DeepEqual(got, entry) {
		t.Errorf("Data: got %+v, want %+v", got, entry)
	}
	assertNoMessage(t, ch)
}

func TestSubscribe_PublishInventoryEvent_DeliversOneMessage(t *testing.T) {
	broadcaster := events.NewBroadcaster()
	ch, unsubscribe := broadcaster.Subscribe()
	defer unsubscribe()

	item := inventory.InventoryItem{Item: inventory.Item{ID: "item-1"}, InstanceCount: 2}
	broadcaster.PublishInventoryEvent("", item)

	msg := recvMessage(t, ch)
	if msg.EventType != "inventory" {
		t.Errorf("EventType: got %q, want %q", msg.EventType, "inventory")
	}
	var got inventory.InventoryItem
	if err := json.Unmarshal(msg.Data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !reflect.DeepEqual(got, item) {
		t.Errorf("Data: got %+v, want %+v", got, item)
	}
	assertNoMessage(t, ch)
}

func TestPublish_DeliversToTwoIndependentSubscribers(t *testing.T) {
	broadcaster := events.NewBroadcaster()
	chA, unsubA := broadcaster.Subscribe()
	defer unsubA()
	chB, unsubB := broadcaster.Subscribe()
	defer unsubB()

	entry := scan.ScanEntry{ID: "scan-2"}
	broadcaster.PublishScanEvent("", entry)

	for _, ch := range []<-chan events.Message{chA, chB} {
		msg := recvMessage(t, ch)
		var got scan.ScanEntry
		if err := json.Unmarshal(msg.Data, &got); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		if !reflect.DeepEqual(got, entry) {
			t.Errorf("Data: got %+v, want %+v", got, entry)
		}
	}
}

func TestUnsubscribe_StopsDeliveryToThatSubscriberOnly(t *testing.T) {
	broadcaster := events.NewBroadcaster()
	chA, unsubA := broadcaster.Subscribe()
	chB, unsubB := broadcaster.Subscribe()
	defer unsubB()

	unsubA()

	entry := scan.ScanEntry{ID: "scan-3"}
	broadcaster.PublishScanEvent("", entry)

	assertNoMessage(t, chA)
	recvMessage(t, chB)
}

func TestPublishBeforeSubscribe_NotObservedByLaterSubscriber(t *testing.T) {
	broadcaster := events.NewBroadcaster()

	broadcaster.PublishScanEvent("", scan.ScanEntry{ID: "scan-4"})

	ch, unsubscribe := broadcaster.Subscribe()
	defer unsubscribe()

	assertNoMessage(t, ch)
}

func TestPublish_DropsSubscriberWithFullBuffer(t *testing.T) {
	broadcaster := events.NewBroadcaster()
	slow, unsubSlow := broadcaster.Subscribe()
	defer unsubSlow()
	draining, unsubDraining := broadcaster.Subscribe()
	defer unsubDraining()

	// Fill slow's buffer to capacity without draining it, while draining
	// keeps reading so it never fills.
	for i := 0; i < subscriberBuffer; i++ {
		broadcaster.PublishScanEvent("", scan.ScanEntry{ID: fmt.Sprintf("scan-%d", i)})
		recvMessage(t, draining)
	}

	// slow's buffer is now full, so this publish overflows it: the
	// Broadcaster drops slow (closing its channel) but still delivers to draining.
	overflow := scan.ScanEntry{ID: "scan-overflow"}
	broadcaster.PublishScanEvent("", overflow)

	// slow's channel is closed, but its buffer still holds the
	// subscriberBuffer messages queued before the overflow; drain those
	// before observing the closed channel so the overflow message itself
	// was never enqueued for slow.
	for i := 0; i < subscriberBuffer; i++ {
		recvMessage(t, slow)
	}
	select {
	case msg, ok := <-slow:
		if ok {
			t.Errorf("slow: expected channel to be closed, got message %+v", msg)
		}
	case <-time.After(recvTimeout):
		t.Fatal("slow: timed out waiting for channel to close")
	}

	msg := recvMessage(t, draining)
	var got scan.ScanEntry
	if err := json.Unmarshal(msg.Data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !reflect.DeepEqual(got, overflow) {
		t.Errorf("draining: got %+v, want %+v", got, overflow)
	}
}
