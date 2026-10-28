package auth

import (
	"context"
	"testing"
	"time"
)

func TestProductOnHandCountsActiveInstances(t *testing.T) {
	ctx, svc, _, house := bootDeviceAPI(t)
	const productID = "prod-on-hand-1"
	if _, err := svc.db.Exec(`INSERT INTO products (id, name, category, unit_of_measure) VALUES (?, 'Milk', 'Dairy', 'gal')`, productID); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 6, 3, 8, 0, 0, 0, time.UTC)
	if _, err := svc.db.Exec(`INSERT INTO items (id, user_id, household_id, product_id) VALUES ('item-1', 'user-1', ?, ?)`, house, productID); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"u1", "u2"} {
		if _, err := svc.db.Exec(`INSERT INTO item_instances (id, item_id, household_id, stock_in_at, created_at) VALUES (?, 'item-1', ?, ?, ?)`, id, house, stamp(at), stamp(at)); err != nil {
			t.Fatal(err)
		}
	}
	n, err := svc.ProductOnHand(ctx, svc.db, house, productID)
	if err != nil || n != 2 {
		t.Fatalf("on hand %d %v", n, err)
	}
}

func TestDisplayProductNameFromMatch(t *testing.T) {
	name := DisplayProductName(DeviceProductMatch{Found: true, Name: "Bread"})
	if name == nil || *name != "Bread" {
		t.Fatalf("name %v", name)
	}
}

func TestRecordDeviceScanReceiptSetsApprox(t *testing.T) {
	ctx, svc, userID, house := bootDeviceAPI(t)
	principal, _ := pairESP32(t, ctx, svc, userID, house)
	at := time.Date(2026, 6, 3, 9, 0, 0, 0, time.UTC)
	_, err := svc.db.Exec(`
		INSERT INTO scan_entries (id, user_id, household_id, device_id, client_scan_id, barcode, scanned_at, direction, unit_count, status)
		VALUES ('scan-approx', ?, ?, ?, 'approx-1', '1234567890123', ?, 'stock_in', 1, 'pending')`,
		principal.DeviceID, house, principal.DeviceID, stamp(at))
	if err != nil {
		t.Fatal(err)
	}
	item := preparedItem(preparedScan{
		item: ScanInput{ID: "approx-1", Barcode: "1234567890123", Direction: "stock_in", UnitCount: 1},
		at: at, unit: 1, barcode: "1234567890123", approx: true,
	})
	if err := svc.RecordDeviceScanReceipt(ctx, svc.db, principal, item, "scan-approx", DeviceLCD{ScanStatus: "queued", QueuedCount: 1}); err != nil {
		t.Fatal(err)
	}
	var approx int
	if err := svc.db.QueryRow(`SELECT time_approx FROM scan_entries WHERE id = 'scan-approx'`).Scan(&approx); err != nil || approx != 1 {
		t.Fatalf("approx %d %v", approx, err)
	}
}

func TestIngestScansRecordsDroppedTotal(t *testing.T) {
	ctx, svc, userID, house := bootDeviceAPI(t)
	hints := &hintLog{}
	svc.SetHints(hints)
	svc.SetDeviceScanProcessor(func(ctx context.Context, q Queryer, s *Service, p Principal, item PreparedScanItem, group int) (ScanResult, CreatedScan, error) {
		return ScanResult{ID: item.ClientID(), Status: "created"}, CreatedScan{}, nil
	})
	pair, _ := svc.StartPairing(ctx, userID, house)
	paired, _ := svc.CompletePairing(ctx, pair.Code, "Hub", "esp32_scanner", "1", "b")
	principal, _ := svc.AuthenticateDevice(ctx, paired.Token)
	if _, err := svc.IngestScans(ctx, principal, []ScanInput{{ID: "d1", Barcode: "1234567890123", HasBarcode: true}}, 4); err != nil {
		t.Fatal(err)
	}
	var dropped int
	if err := svc.db.QueryRow(`SELECT dropped_total FROM devices WHERE id = ?`, paired.DeviceID).Scan(&dropped); err != nil || dropped != 4 {
		t.Fatalf("dropped %d %v", dropped, err)
	}
}
