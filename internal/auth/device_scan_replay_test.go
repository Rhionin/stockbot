package auth

import (
	"testing"
	"time"
)

func TestLoadDeviceScanReplayReturnsStoredLCD(t *testing.T) {
	ctx, svc, userID, house := bootDeviceAPI(t)
	pair, err := svc.StartPairing(ctx, userID, house)
	if err != nil {
		t.Fatal(err)
	}
	paired, err := svc.CompletePairing(ctx, pair.Code, "Scanner", "esp32_scanner", "1.0", "boot-1")
	if err != nil {
		t.Fatal(err)
	}
	principal, err := svc.AuthenticateDevice(ctx, paired.Token)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)
	_, err = svc.db.Exec(`
		INSERT INTO scan_entries (id, user_id, household_id, device_id, client_scan_id, barcode, scanned_at, direction, unit_count, status)
		VALUES ('scan-row-1', ?, ?, ?, 'replay-1', '4000000000011', ?, 'stock_in', 1, 'pending')`,
		principal.DeviceID, house, principal.DeviceID, stamp(at))
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.db.Exec(`
		INSERT INTO device_scan_receipts (
			household_id, device_id, client_scan_id, scan_entry_id, barcode, direction, unit_count, scanned_at,
			device_result_status, device_on_hand, device_queued_count, device_delta, device_backfilled, product_name
		) VALUES (?, ?, 'replay-1', 'scan-row-1', '4000000000011', 'stock_in', 1, ?, 'queued', 2, 3, 0, 0, 'Canned Beans')`,
		house, principal.DeviceID, stamp(at))
	if err != nil {
		t.Fatal(err)
	}
	out, err := svc.IngestScans(ctx, principal, []ScanInput{{
		ID: "replay-1", Barcode: "4000000000011", Direction: "stock_in", UnitCount: 1, HasBarcode: true,
	}}, 0)
	if err != nil || len(out) != 1 || out[0].Status != "duplicate" || out[0].ScanStatus != "queued" ||
		out[0].ProductName == nil || *out[0].ProductName != "Canned Beans" || out[0].QueuedCount == nil || *out[0].QueuedCount != 3 {
		t.Fatalf("out %#v err %v", out, err)
	}
}

func TestUpdateDeviceRejectsInvalidCommitMode(t *testing.T) {
	ctx, svc, userID, house := bootDeviceAPI(t)
	_, deviceID := pairESP32(t, ctx, svc, userID, house)
	mode := "instant"
	if _, err := svc.UpdateDevice(ctx, userID, house, deviceID, UpdateDevicePatch{CommitMode: &mode}); err == nil {
		t.Fatal("invalid commit mode accepted")
	}
	dir := "sideways"
	if _, err := svc.UpdateDevice(ctx, userID, house, deviceID, UpdateDevicePatch{DefaultDirection: &dir}); err == nil {
		t.Fatal("invalid direction accepted")
	}
}
