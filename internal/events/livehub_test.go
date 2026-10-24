package events_test

import (
	"strings"
	"testing"

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
