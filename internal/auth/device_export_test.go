package auth

import (
	"testing"
	"time"
)

func TestPreparedScanItemAndLCDHelpers(t *testing.T) {
	at := time.Date(2026, 6, 4, 10, 0, 0, 0, time.UTC)
	item := preparedItem(preparedScan{
		item:    ScanInput{ID: "c1", Barcode: "1234567890123", Direction: "stock_in", BootID: "boot-x"},
		at:      at,
		unit:    2,
		barcode: "1234567890123",
		approx:  true,
		match:   DeviceProductMatch{Found: true, Name: "Tea"},
	})
	if item.ClientID() != "c1" || item.Barcode() != "1234567890123" || item.Direction() != "stock_in" ||
		item.Unit() != 2 || !item.At().Equal(at) || !item.Approx() || item.BootID() != "boot-x" || !item.Match().Found {
		t.Fatalf("item accessors %#v", item)
	}
	name := DisplayProductName(DeviceProductMatch{Found: false})
	if name != nil {
		t.Fatalf("name %v", name)
	}
	var result ScanResult
	ApplyDeviceLCD(DeviceLCD{
		ScanStatus: "committed", Known: true, Barcode: "1234567890123", Direction: "stock_in",
		QueuedCount: 0, OnHand: 3, Delta: 1, Backfilled: true,
	}, &result)
	if result.ScanStatus != "committed" || result.OnHand == nil || *result.OnHand != 3 {
		t.Fatalf("result %#v", result)
	}
	if Validation() == nil {
		t.Fatal("validation sentinel nil")
	}
}

func TestDeviceSettingsLoadsCommitMode(t *testing.T) {
	ctx, svc, userID, house := bootDeviceAPI(t)
	_, deviceID := pairESP32(t, ctx, svc, userID, house)
	snap, err := svc.DeviceSettings(ctx, svc.db, house, deviceID)
	if err != nil || snap.CommitMode != "queue" {
		t.Fatalf("snap %#v err %v", snap, err)
	}
}
