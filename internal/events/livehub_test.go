package events_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/events"
	"github.com/Rhionin/stockbot/internal/inventory"
	"github.com/Rhionin/stockbot/internal/scan"
)

func TestLiveHubKeepsHouseholdsApart(t *testing.T) {
	b := events.NewBroadcaster()
	b.SetCursor(func(householdID, scope string) string {
		if householdID == "house-north" && scope == "stock" {
			return "v1.opaque-stock"
		}
		return "v1.opaque-other"
	})
	north, cancelNorth := b.SubscribeHousehold("house-north", "user-north", "account")
	defer cancelNorth()
	south, cancelSouth := b.SubscribeHousehold("house-south", "user-south", "account")
	defer cancelSouth()
	legacy, cancelLegacy := b.Subscribe()
	defer cancelLegacy()

	b.PublishHint("house-north", "stock")
	b.PublishScanEvent("house-north", scan.ScanEntry{ID: "scan-north", Barcode: "000111222"})

	hint := recvMessage(t, north)
	if hint.EventType != "hint" || hint.HouseholdID != "house-north" {
		t.Fatalf("hint = %+v", hint)
	}
	if strings.Contains(string(hint.Data), "household") || strings.Contains(string(hint.Data), "Widget") || strings.Contains(string(hint.Data), "000111222") {
		t.Fatalf("hint leaked %s", hint.Data)
	}
	if !strings.Contains(string(hint.Data), `"type":"stock"`) || !strings.Contains(string(hint.Data), `"cursor":"v1.opaque-stock"`) {
		t.Fatalf("hint body %s", hint.Data)
	}
	scanMsg := recvMessage(t, north)
	if scanMsg.EventType != "scan" || scanMsg.HouseholdID != "house-north" {
		t.Fatalf("scan = %+v", scanMsg)
	}
	scanHint := recvMessage(t, north)
	if scanHint.EventType != "hint" {
		t.Fatalf("scan hint = %+v", scanHint)
	}
	assertNoMessage(t, south)
	assertNoMessage(t, legacy)

	b.PublishInventoryEvent("house-south", inventory.InventoryItem{})
	southMsg := recvMessage(t, south)
	if southMsg.EventType != "inventory" || southMsg.HouseholdID != "house-south" {
		t.Fatalf("south = %+v", southMsg)
	}
	assertNoMessage(t, north)
}

func TestLiveHubKickClosesOnlyTheMatchingStream(t *testing.T) {
	b := events.NewBroadcaster()
	north, _ := b.SubscribeHousehold("house-north", "user-north", "account")
	otherHouse, _ := b.SubscribeHousehold("house-south", "user-north", "account")
	password, _ := b.SubscribeHousehold("house-north", "", "household_password")
	kept, cancelKept := b.SubscribeHousehold("house-north", "user-kept", "account")
	defer cancelKept()

	b.Hub().Kick("house-north", "user-north")
	if _, ok := <-north; ok {
		t.Fatal("removed member stream stayed open")
	}
	b.PublishHint("house-south", "list")
	if msg := recvMessage(t, otherHouse); msg.HouseholdID != "house-south" {
		t.Fatalf("other household = %+v", msg)
	}
	b.Hub().Kick("house-north", "")
	if _, ok := <-password; ok {
		t.Fatal("household-password stream stayed open")
	}
	b.PublishHint("house-north", "devices")
	if msg := recvMessage(t, kept); msg.EventType != "hint" {
		t.Fatalf("kept member = %+v", msg)
	}
	b.PublishHint("house-north", "list")
	if msg := recvMessage(t, kept); msg.EventType != "hint" {
		t.Fatalf("account stream closed with the password: %+v", msg)
	}

	b.Hub().Kick("", "user-kept")
	if _, ok := <-kept; ok {
		t.Fatal("revoked account stream stayed open")
	}
}

func TestRekeyHintUsesTheDevicesCursorAndDropsOversizedHints(t *testing.T) {
	b := events.NewBroadcaster()
	b.SetCursor(func(_, scope string) string {
		if scope == "devices" {
			return "v1.devices"
		}
		return "v1.other"
	})
	sub, cancel := b.SubscribeHousehold("house-north", "user-north", "account")
	defer cancel()
	b.PublishHint("house-north", "rekey")
	msg := recvMessage(t, sub)
	if !strings.Contains(string(msg.Data), `"type":"rekey"`) || !strings.Contains(string(msg.Data), `"cursor":"v1.devices"`) {
		t.Fatalf("rekey %s", msg.Data)
	}
	b.SetCursor(func(_, _ string) string {
		return strings.Repeat("x", 1100)
	})
	b.PublishHint("house-north", "scans")
	assertNoMessage(t, sub)
}

func TestLiveHubInterfaceCopiesFramesAndIgnoresANilHub(t *testing.T) {
	var missing events.LiveHub = (*events.Broadcaster)(nil).Hub()
	frames, cancel := missing.Subscribe(events.Principal{})
	cancel()
	if _, ok := <-frames; ok {
		t.Fatal("nil hub left the stream open")
	}
	missing.Publish(context.Background(), "house-north", events.LiveHint{Type: "stock"}, nil)
	missing.Kick("house-north", "user-north")

	var quiet *events.Broadcaster
	quiet.PublishHint("house-north", "stock")
	quiet.Publish(context.Background(), "house-north", events.LiveHint{Type: "stock"}, nil)
	quiet.Kick("house-north", "user-north")

	b := events.NewBroadcaster()
	b.PublishHint("", "stock")
	b.PublishHint("house-north", "")
	hub := b.Hub()
	hub.Publish(context.Background(), "", events.LiveHint{Type: "stock"}, nil)
	hub.Publish(context.Background(), "house-north", events.LiveHint{}, nil)
	hub.Kick("", "")

	frames, cancel = hub.Subscribe(events.Principal{HouseholdID: "house-north", UserID: "user-north", Kind: "account"})
	defer cancel()
	hub.Publish(context.Background(), "house-north", events.LiveHint{Type: "mystery"}, "not-a-legacy")
	hub.Publish(context.Background(), "house-north", events.LiveHint{Type: "list", Cursor: "v1.ready"}, events.Legacy{})
	hub.Publish(context.Background(), "house-north", events.LiveHint{Type: "list", Cursor: "v1.ready"}, events.Legacy{Event: "scan", Payload: "plain"})

	hint := recvEvent(t, frames)
	if hint.EventType != "hint" || !strings.Contains(string(hint.Data), `"type":"mystery"`) || strings.Contains(string(hint.Data), `"cursor"`) {
		t.Fatalf("unknown hint %s", hint.Data)
	}
	skipped := recvEvent(t, frames)
	if skipped.EventType != "hint" {
		t.Fatalf("empty legacy still published %s", skipped.EventType)
	}
	list := recvEvent(t, frames)
	if list.EventType != "hint" || !strings.Contains(string(list.Data), `"cursor":"v1.ready"`) {
		t.Fatalf("list hint %s", list.Data)
	}
	legacy := recvEvent(t, frames)
	if legacy.EventType != "scan" || string(legacy.Data) != `"plain"` {
		t.Fatalf("legacy = %+v %s", legacy.EventType, legacy.Data)
	}

	for range 24 {
		hub.Publish(context.Background(), "house-north", events.LiveHint{Type: "stock", Cursor: "v1.c"}, nil)
		time.Sleep(time.Millisecond)
	}
	n := drainEvents(t, frames)
	if n == 0 {
		t.Fatal("dropped every frame")
	}
	hub.Publish(context.Background(), "house-north", events.LiveHint{Type: "devices", Cursor: "v1.after"}, nil)
	last := recvEvent(t, frames)
	if last.EventType != "hint" || !strings.Contains(string(last.Data), `"type":"devices"`) {
		t.Fatalf("stream closed after a full buffer: %+v %s", last.EventType, last.Data)
	}
}

func recvEvent(t *testing.T, frames <-chan events.Event) events.Event {
	t.Helper()
	select {
	case ev, ok := <-frames:
		if !ok {
			t.Fatal("stream closed")
		}
		return ev
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for an event")
	}
	return events.Event{}
}

func drainEvents(t *testing.T, frames <-chan events.Event) int {
	t.Helper()
	n := 0
	deadline := time.After(time.Second)
	for {
		select {
		case _, ok := <-frames:
			if !ok {
				t.Fatal("stream closed while draining")
			}
			n++
		case <-time.After(30 * time.Millisecond):
			return n
		case <-deadline:
			return n
		}
	}
}
