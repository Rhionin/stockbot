package auth

import (
	"context"
	"testing"
)

func TestIngestScansUsesProcessorWithoutImportCycle(t *testing.T) {
	ctx, svc, userID, house := bootDeviceAPI(t)
	svc.SetDeviceScanProcessor(func(ctx context.Context, q Queryer, s *Service, p Principal, item PreparedScanItem, group int) (ScanResult, CreatedScan, error) {
		_, err := q.ExecContext(ctx, `
			INSERT INTO scan_entries (id, user_id, household_id, device_id, client_scan_id, barcode, scanned_at, direction, unit_count, status)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 'pending')`,
			"scan-proc-1", p.DeviceID, p.HouseholdID, p.DeviceID, item.ClientID(), item.Barcode(), stamp(item.At()), item.Direction(), item.Unit())
		if err != nil {
			return ScanResult{}, CreatedScan{}, AsError(err)
		}
		if err := s.RecordDeviceScanReceipt(ctx, q, p, item, "scan-proc-1", DeviceLCD{
			ScanStatus: "queued", Known: true, Barcode: item.Barcode(), Direction: item.Direction(),
			QueuedCount: 1, OnHand: 0,
		}); err != nil {
			return ScanResult{}, CreatedScan{}, err
		}
		return ScanResult{ID: item.ClientID(), Status: "created", ScanStatus: "queued"}, CreatedScan{ScanID: "scan-proc-1", Barcode: item.Barcode()}, nil
	})
	pair, err := svc.StartPairing(ctx, userID, house)
	if err != nil {
		t.Fatal(err)
	}
	paired, err := svc.CompletePairing(ctx, pair.Code, "Counter", "esp32_scanner", "1.0", "boot-1")
	if err != nil {
		t.Fatal(err)
	}
	principal, err := svc.AuthenticateDevice(ctx, paired.Token)
	if err != nil {
		t.Fatal(err)
	}
	results, err := svc.IngestScans(ctx, principal, []ScanInput{{
		ID: "proc-1", Barcode: "1234567890123", Direction: "stock_in", UnitCount: 1, HasBarcode: true,
	}}, 0)
	if err != nil || len(results) != 1 || results[0].Status != "created" || results[0].ScanStatus != "queued" {
		t.Fatalf("results %#v err %v", results, err)
	}
	dup, err := svc.IngestScans(ctx, principal, []ScanInput{{
		ID: "proc-1", Barcode: "1234567890123", Direction: "stock_in", UnitCount: 1, HasBarcode: true,
	}}, 0)
	if err != nil || len(dup) != 1 || dup[0].Status != "duplicate" {
		t.Fatalf("dup %#v err %v", dup, err)
	}
}

func TestUpdateDeviceRequiresAField(t *testing.T) {
	ctx, svc, userID, house := bootDeviceAPI(t)
	_, deviceID := pairESP32(t, ctx, svc, userID, house)
	if _, err := svc.UpdateDevice(ctx, userID, house, deviceID, UpdateDevicePatch{}); err == nil {
		t.Fatal("empty patch succeeded")
	}
}

func TestHubConfigMissingDevice(t *testing.T) {
	ctx, svc, _, house := bootDeviceAPI(t)
	if _, err := svc.HubConfigForDevice(ctx, house, "missing-device", "", ""); err == nil {
		t.Fatal("expected not found")
	}
}
