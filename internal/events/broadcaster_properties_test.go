package events_test

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"pgregory.net/rapid"

	"github.com/Rhionin/stockbot/internal/events"
	"github.com/Rhionin/stockbot/internal/scan"
)

// recvMessageRT waits up to recvTimeout for a message on ch and fails the
// property check if none arrives. It mirrors recvMessage from
// broadcaster_test.go but takes a *rapid.T so it can be called from inside
// rapid.Check.
func recvMessageRT(rt *rapid.T, ch <-chan events.Message) events.Message {
	select {
	case msg, ok := <-ch:
		if !ok {
			rt.Fatal("channel closed before delivering a message")
		}
		return msg
	case <-time.After(recvTimeout):
		rt.Fatal("timed out waiting for message")
	}
	return events.Message{}
}

// assertNoMessageRT fails the property check if a message arrives on ch
// within a short window, confirming that no delivery occurred.
func assertNoMessageRT(rt *rapid.T, ch <-chan events.Message) {
	select {
	case msg, ok := <-ch:
		if ok {
			rt.Fatalf("unexpected message delivered: %+v", msg)
		}
	case <-time.After(50 * time.Millisecond):
	}
}

// drainUntilClosedRT reads and discards messages from ch until it observes
// the channel close, failing the property check if that never happens
// within recvTimeout.
func drainUntilClosedRT(rt *rapid.T, ch <-chan events.Message) {
	deadline := time.After(recvTimeout)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return
			}
		case <-deadline:
			rt.Fatal("timed out waiting for channel to close")
		}
	}
}

// scanEntryID unmarshals msg.Data as a scan.ScanEntry and returns its ID.
func scanEntryID(rt *rapid.T, msg events.Message) string {
	var entry scan.ScanEntry
	if err := json.Unmarshal(msg.Data, &entry); err != nil {
		rt.Fatalf("Unmarshal: %v", err)
	}
	return entry.ID
}

// Feature: realtime-scan-updates, Property 1: Subscribers only receive events published after they subscribe
// **Validates: Requirements 1.2**
//
// For any sequence of PublishScanEvent/PublishInventoryEvent calls split into
// a "before" group and an "after" group by when a new Subscribe() call
// occurs between them, the channel returned by that Subscribe() call SHALL
// receive exactly the messages in the "after" group, in publish order, and
// SHALL receive none of the messages in the "before" group.
func TestProperty1_SubscribersOnlyReceiveEventsPublishedAfterSubscribing(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		broadcaster := events.NewBroadcaster()

		beforeCount := rapid.IntRange(0, 20).Draw(rt, "beforeCount")
		for i := 0; i < beforeCount; i++ {
			broadcaster.PublishScanEvent("", scan.ScanEntry{ID: fmt.Sprintf("before-%d", i)})
		}

		ch, unsubscribe := broadcaster.Subscribe()
		defer unsubscribe()

		// Capped at subscriberBuffer so this property isn't also exercising
		// the overflow behavior Property 3 covers.
		afterCount := rapid.IntRange(0, subscriberBuffer).Draw(rt, "afterCount")
		afterIDs := make([]string, afterCount)
		for i := 0; i < afterCount; i++ {
			id := fmt.Sprintf("after-%d", i)
			afterIDs[i] = id
			broadcaster.PublishScanEvent("", scan.ScanEntry{ID: id})
		}

		for i, wantID := range afterIDs {
			msg := recvMessageRT(rt, ch)
			if gotID := scanEntryID(rt, msg); gotID != wantID {
				rt.Fatalf("message %d: want ID %q, got %q", i, wantID, gotID)
			}
		}
		assertNoMessageRT(rt, ch)
	})
}

// Feature: realtime-scan-updates, Property 2: Unsubscribing removes exactly that subscriber without affecting the rest
// **Validates: Requirements 1.3**
//
// For any number of concurrently subscribed connections and any subset of
// them whose unsubscribe function is called before a subsequent publish,
// that publish SHALL deliver the message to every subscriber not in the
// unsubscribed subset and SHALL deliver it to none of the subscribers in the
// unsubscribed subset.
func TestProperty2_UnsubscribingRemovesExactlyThatSubscriber(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		broadcaster := events.NewBroadcaster()

		numSubscribers := rapid.IntRange(1, 20).Draw(rt, "numSubscribers")
		type subscriber struct {
			ch           <-chan events.Message
			unsubscribe  func()
			unsubscribed bool
		}
		subscribers := make([]subscriber, numSubscribers)
		for i := 0; i < numSubscribers; i++ {
			ch, unsub := broadcaster.Subscribe()
			subscribers[i] = subscriber{ch: ch, unsubscribe: unsub}
		}

		for i := range subscribers {
			if rapid.Bool().Draw(rt, fmt.Sprintf("unsubscribe-%d", i)) {
				subscribers[i].unsubscribed = true
				subscribers[i].unsubscribe()
			}
		}
		defer func() {
			for _, s := range subscribers {
				if !s.unsubscribed {
					s.unsubscribe()
				}
			}
		}()

		entry := scan.ScanEntry{ID: "prop2-entry"}
		broadcaster.PublishScanEvent("", entry)

		for i, s := range subscribers {
			if s.unsubscribed {
				assertNoMessageRT(rt, s.ch)
				continue
			}
			msg := recvMessageRT(rt, s.ch)
			if gotID := scanEntryID(rt, msg); gotID != entry.ID {
				rt.Fatalf("subscriber %d: want ID %q, got %q", i, entry.ID, gotID)
			}
		}
	})
}

// Feature: realtime-scan-updates, Property 3: A subscriber that cannot keep up is dropped without blocking delivery to others
// **Validates: Requirements 1.4**
//
// For any set of subscribed connections where an arbitrary non-empty subset
// never drain their channel until it fills, publishing enough events to
// overflow those channels SHALL cause every subscriber outside that subset
// to receive every published event, regardless of how many events were
// needed to overflow the non-draining subset's buffers.
func TestProperty3_SlowSubscriberDroppedWithoutBlockingOthers(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		broadcaster := events.NewBroadcaster()

		numSubscribers := rapid.IntRange(2, 8).Draw(rt, "numSubscribers")
		type subscriber struct {
			ch          <-chan events.Message
			unsubscribe func()
			nonDraining bool
		}
		subscribers := make([]subscriber, numSubscribers)
		anyNonDraining := false
		for i := 0; i < numSubscribers; i++ {
			ch, unsub := broadcaster.Subscribe()
			nonDraining := rapid.Bool().Draw(rt, fmt.Sprintf("nonDraining-%d", i))
			anyNonDraining = anyNonDraining || nonDraining
			subscribers[i] = subscriber{ch: ch, unsubscribe: unsub, nonDraining: nonDraining}
		}
		// The subset that never drains must be non-empty; force the last
		// subscriber into it if the draws happened to produce an empty subset.
		if !anyNonDraining {
			subscribers[numSubscribers-1].nonDraining = true
		}
		defer func() {
			for _, s := range subscribers {
				s.unsubscribe()
			}
		}()

		// More than subscriberBuffer publishes guarantees every non-draining
		// subscriber's channel overflows and gets dropped.
		publishCount := rapid.IntRange(subscriberBuffer+1, subscriberBuffer+10).Draw(rt, "publishCount")
		for i := 0; i < publishCount; i++ {
			id := fmt.Sprintf("prop3-%d", i)
			broadcaster.PublishScanEvent("", scan.ScanEntry{ID: id})

			for j, s := range subscribers {
				if s.nonDraining {
					continue
				}
				msg := recvMessageRT(rt, s.ch)
				if gotID := scanEntryID(rt, msg); gotID != id {
					rt.Fatalf("draining subscriber %d: publish %d: want ID %q, got %q", j, i, id, gotID)
				}
			}
		}

		// Every non-draining subscriber's channel must eventually close: its
		// buffer overflowed and the Broadcaster dropped it.
		for j, s := range subscribers {
			if !s.nonDraining {
				continue
			}
			drainUntilClosedRT(rt, s.ch)
			_ = j
		}
	})
}
