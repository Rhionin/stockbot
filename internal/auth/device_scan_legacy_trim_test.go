package auth

import (
	"testing"
	"time"
)

func TestLoadDeviceScanReplay_PaddedLegacyBarcodeTreatsAsDuplicate(t *testing.T) {
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
	const upc = "4000000000011"
	padded := " " + upc + "\t\r\n"
	at := time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)
	_, err = svc.db.Exec(`
		INSERT INTO scan_entries (id, user_id, household_id, device_id, client_scan_id, barcode, scanned_at, direction, unit_count, status)
		VALUES ('scan-row-pad', ?, ?, ?, 'replay-pad', ?, ?, 'stock_in', 1, 'pending')`,
		principal.DeviceID, house, principal.DeviceID, padded, stamp(at))
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.db.Exec(`
		INSERT INTO device_scan_receipts (
			household_id, device_id, client_scan_id, scan_entry_id, barcode, direction, unit_count, scanned_at,
			device_result_status, device_on_hand, device_queued_count, device_delta, device_backfilled, product_name
		) VALUES (?, ?, 'replay-pad', 'scan-row-pad', ?, 'stock_in', 1, ?, 'queued', 2, 3, 0, 0, 'Canned Beans')`,
		house, principal.DeviceID, padded, stamp(at))
	if err != nil {
		t.Fatal(err)
	}
	out, err := svc.IngestScans(ctx, principal, []ScanInput{{
		ID: "replay-pad", Barcode: upc, Direction: "stock_in", UnitCount: 1, HasBarcode: true,
	}}, 0)
	if err != nil || len(out) != 1 || out[0].Status != "duplicate" {
		t.Fatalf("out %#v err %v, want duplicate", out, err)
	}
}