package devicingest_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/auth"
)

func setQueueMode(t *testing.T, ctx context.Context, svc *auth.Service, p auth.Principal) {
	t.Helper()
	user, err := svc.Authenticate(ctx, "pat@example.test", "horse battery")
	if err != nil {
		t.Fatal(err)
	}
	mode := "queue"
	if _, err := svc.UpdateDevice(ctx, user.ID, p.HouseholdID, p.DeviceID, auth.UpdateDevicePatch{CommitMode: &mode}); err != nil {
		t.Fatal(err)
	}
}

func queuedUnitCount(t *testing.T, db *sql.DB, house, clientScanID string) (status string, units int) {
	t.Helper()
	if err := db.QueryRow(`
		SELECT status, unit_count FROM scan_entries
		WHERE household_id = ? AND client_scan_id = ?`, house, clientScanID).Scan(&status, &units); err != nil {
		t.Fatal(err)
	}
	return status, units
}

func countEntriesForBarcode(t *testing.T, db *sql.DB, house, deviceID, barcode, direction string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM scan_entries
		WHERE household_id = ? AND user_id = ? AND barcode = ? AND direction = ?`,
		house, deviceID, barcode, direction).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func netStockMoveDeltaForScan(t *testing.T, db *sql.DB, house, scanEntryID string) int {
	t.Helper()
	rows, err := db.Query(`
		SELECT direction, quantity FROM stock_moves
		WHERE household_id = ? AND scan_entry_id = ?`, house, scanEntryID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	net := 0
	for rows.Next() {
		var dir string
		var qty int
		if err := rows.Scan(&dir, &qty); err != nil {
			t.Fatal(err)
		}
		switch dir {
		case "in":
			net += qty
		case "out":
			net -= qty
		}
	}
	return net
}

func receiptLCD(t *testing.T, db *sql.DB, house, clientScanID string) (delta, onHand int) {
	t.Helper()
	var deltaNull, onHandNull sql.NullInt64
	if err := db.QueryRow(`
		SELECT device_delta, device_on_hand FROM device_scan_receipts
		WHERE household_id = ? AND client_scan_id = ?`, house, clientScanID).Scan(&deltaNull, &onHandNull); err != nil {
		t.Fatal(err)
	}
	if !deltaNull.Valid || !onHandNull.Valid {
		t.Fatalf("receipt lcd null delta=%v onHand=%v", deltaNull, onHandNull)
	}
	return int(deltaNull.Int64), int(onHandNull.Int64)
}

func TestImmediateKnownScanDoesNotMergeQueuedCardStockIn(t *testing.T) {
	ctx, svc, p, house, catalog, db := bootIngest(t)
	const barcode = "4000000000101"
	seedProduct(t, ctx, catalog, house, "prod-iso-in", "Iso In", barcode)

	for i, id := range []string{"q1", "q2", "q3"} {
		r := ingest(t, ctx, svc, p, id, barcode, "stock_in")
		if i == 0 {
			if r.QueuedCount == nil || *r.QueuedCount != 1 {
				t.Fatalf("first queue %#v", r)
			}
		}
	}
	st, units := queuedUnitCount(t, db, house, "q1")
	if st != "pending" || units != 3 {
		t.Fatalf("queued card status=%q units=%d", st, units)
	}
	onBefore := productOnHandDB(t, db, house, "prod-iso-in")

	setImmediate(t, ctx, svc, p)
	imm := ingest(t, ctx, svc, p, "imm-in", barcode, "stock_in")
	if imm.ScanStatus != "committed" || imm.Delta == nil || *imm.Delta != 1 || imm.OnHand == nil || *imm.OnHand != onBefore+1 {
		t.Fatalf("immediate %#v onBefore=%d", imm, onBefore)
	}
	st, units = queuedUnitCount(t, db, house, "q1")
	if st != "pending" || units != 3 {
		t.Fatalf("queued card after immediate status=%q units=%d", st, units)
	}
	var immStatus string
	var immUnits int
	if err := db.QueryRow(`
		SELECT status, unit_count FROM scan_entries WHERE client_scan_id = 'imm-in'`).Scan(&immStatus, &immUnits); err != nil {
		t.Fatal(err)
	}
	if immStatus != "committed" || immUnits != 1 {
		t.Fatalf("immediate entry status=%q units=%d", immStatus, immUnits)
	}
	if countEntriesForBarcode(t, db, house, p.DeviceID, barcode, "stock_in") != 2 {
		t.Fatal("expected separate committed row plus queued card")
	}
}

func TestImmediateKnownScanDoesNotMergeQueuedCardStockOut(t *testing.T) {
	ctx, svc, p, house, catalog, db := bootIngest(t)
	const barcode = "4000000000102"
	seedProduct(t, ctx, catalog, house, "prod-iso-out", "Iso Out", barcode)
	setImmediate(t, ctx, svc, p)
	ingest(t, ctx, svc, p, "seed-in", barcode, "stock_in")
	setQueueMode(t, ctx, svc, p)

	for _, id := range []string{"q-out-1", "q-out-2", "q-out-3"} {
		ingest(t, ctx, svc, p, id, barcode, "stock_out")
	}
	st, units := queuedUnitCount(t, db, house, "q-out-1")
	if st != "pending" || units != 3 {
		t.Fatalf("queued out card status=%q units=%d", st, units)
	}
	onBefore := productOnHandDB(t, db, house, "prod-iso-out")

	setImmediate(t, ctx, svc, p)
	imm := ingest(t, ctx, svc, p, "imm-out", barcode, "stock_out")
	if imm.ScanStatus != "committed" || imm.Delta == nil || *imm.Delta != -1 || imm.OnHand == nil || *imm.OnHand != onBefore-1 {
		t.Fatalf("immediate out %#v onBefore=%d", imm, onBefore)
	}
	st, units = queuedUnitCount(t, db, house, "q-out-1")
	if st != "pending" || units != 3 {
		t.Fatalf("queued card after immediate status=%q units=%d", st, units)
	}
}

func TestImmediateKnownScanDoesNotMergeQueuedCardStockOutBackfill(t *testing.T) {
	ctx, svc, p, house, catalog, db := bootIngest(t)
	const barcode = "4000000000103"
	seedProduct(t, ctx, catalog, house, "prod-iso-bf", "Iso BF", barcode)
	setImmediate(t, ctx, svc, p)
	ingest(t, ctx, svc, p, "seed-in", barcode, "stock_in")
	ingest(t, ctx, svc, p, "seed-out", barcode, "stock_out")
	setQueueMode(t, ctx, svc, p)
	for _, id := range []string{"q-bf-1", "q-bf-2", "q-bf-3"} {
		ingest(t, ctx, svc, p, id, barcode, "stock_out")
	}
	setImmediate(t, ctx, svc, p)
	onBefore := productOnHandDB(t, db, house, "prod-iso-bf")
	imm := ingest(t, ctx, svc, p, "imm-bf", barcode, "stock_out")
	if imm.Backfilled == nil || !*imm.Backfilled || imm.Delta == nil || *imm.Delta != -1 {
		t.Fatalf("backfill immediate %#v", imm)
	}
	if imm.OnHand == nil || *imm.OnHand != 0 {
		t.Fatalf("onHand %#v", imm.OnHand)
	}
	st, units := queuedUnitCount(t, db, house, "q-bf-1")
	if st != "pending" || units != 3 {
		t.Fatalf("queued card status=%q units=%d", st, units)
	}
	if onBefore != 0 {
		t.Fatalf("onBefore=%d want 0 for backfill setup", onBefore)
	}
}

func TestImmediateKnownScanLeavesFlaggedCardIntact(t *testing.T) {
	ctx, svc, p, house, catalog, db := bootIngest(t)
	const unknown = "8888888888881"
	const known = "4000000000104"
	seedProduct(t, ctx, catalog, house, "prod-flag", "Flag", known)

	setImmediate(t, ctx, svc, p)
	flagged := ingest(t, ctx, svc, p, "flag-1", unknown, "stock_in")
	if flagged.ScanStatus != "flagged" {
		t.Fatalf("flagged %#v", flagged)
	}
	stBefore, unitsBefore := queuedUnitCount(t, db, house, "flag-1")

	knownScan := ingest(t, ctx, svc, p, "imm-known", known, "stock_in")
	if knownScan.ScanStatus != "committed" || knownScan.Delta == nil || *knownScan.Delta != 1 {
		t.Fatalf("known immediate %#v", knownScan)
	}
	stAfter, unitsAfter := queuedUnitCount(t, db, house, "flag-1")
	if stBefore != stAfter || unitsBefore != unitsAfter {
		t.Fatalf("flagged card changed status %q->%q units %d->%d", stBefore, stAfter, unitsBefore, unitsAfter)
	}
	var immUnits int
	if err := db.QueryRow(`SELECT unit_count FROM scan_entries WHERE client_scan_id = 'imm-known'`).Scan(&immUnits); err != nil || immUnits != 1 {
		t.Fatalf("immediate units %d", immUnits)
	}
	_ = house
}

func TestImmediateReceiptAndLCDMatchStockMoves(t *testing.T) {
	ctx, svc, p, house, catalog, db := bootIngest(t)
	const barcode = "4000000000105"
	seedProduct(t, ctx, catalog, house, "prod-lcd", "LCD", barcode)
	setImmediate(t, ctx, svc, p)

	r := ingest(t, ctx, svc, p, "lcd-in", barcode, "stock_in")
	var scanEntryID string
	if err := db.QueryRow(`SELECT scan_entry_id FROM device_scan_receipts WHERE client_scan_id = 'lcd-in'`).Scan(&scanEntryID); err != nil {
		t.Fatal(err)
	}
	net := netStockMoveDeltaForScan(t, db, house, scanEntryID)
	if r.Delta == nil || *r.Delta != net {
		t.Fatalf("delta %v net moves %d", r.Delta, net)
	}
	recDelta, recOnHand := receiptLCD(t, db, house, "lcd-in")
	if r.OnHand == nil || *r.OnHand != recOnHand || *r.Delta != recDelta {
		t.Fatalf("lcd %#v receipt delta=%d onHand=%d", r, recDelta, recOnHand)
	}

	out := ingest(t, ctx, svc, p, "lcd-out", barcode, "stock_out")
	if err := db.QueryRow(`SELECT scan_entry_id FROM device_scan_receipts WHERE client_scan_id = 'lcd-out'`).Scan(&scanEntryID); err != nil {
		t.Fatal(err)
	}
	net = netStockMoveDeltaForScan(t, db, house, scanEntryID)
	if out.Delta == nil || *out.Delta != net {
		t.Fatalf("out delta %v net moves %d", out.Delta, net)
	}
	recDelta, recOnHand = receiptLCD(t, db, house, "lcd-out")
	if out.OnHand == nil || *out.OnHand != recOnHand || *out.Delta != recDelta {
		t.Fatalf("out lcd %#v receipt delta=%d onHand=%d", out, recDelta, recOnHand)
	}
}

func TestQueueModeMergeRegression(t *testing.T) {
	ctx, svc, p, house, catalog, db := bootIngest(t)
	const barcode = "4000000000106"
	seedProduct(t, ctx, catalog, house, "prod-merge-reg", "Merge", barcode)
	first := ingest(t, ctx, svc, p, "merge-1", barcode, "stock_in")
	second := ingest(t, ctx, svc, p, "merge-2", barcode, "stock_in")
	if first.QueuedCount == nil || *first.QueuedCount != 1 || second.QueuedCount == nil || *second.QueuedCount != 2 {
		t.Fatalf("first %#v second %#v", first, second)
	}
	var rows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM scan_entries WHERE barcode = ?`, barcode).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("rows %d", rows)
	}
}

func TestDeviceIngestQueueStockOutPendingNoInventoryPublish(t *testing.T) {
	ctx, svc, p, house, catalog, _, spy := bootIngestWithPublishSpy(t)
	const barcode = "4000000000107"
	seedProduct(t, ctx, catalog, house, "prod-q-out", "Q Out", barcode)
	setQueueMode(t, ctx, svc, p)
	ingest(t, ctx, svc, p, "q-out-pending", barcode, "stock_out")
	if spy.lastStockMoved() {
		t.Fatal("queue stock_out with zero units removed must pass stockMoved=false")
	}
	_ = house
}

func TestDeviceIngestImmediateStockOutRemovedUnitsPublishesInventory(t *testing.T) {
	ctx, svc, p, house, catalog, _, msgs := bootIngestWithBroadcaster(t)
	const barcode = "4000000000108"
	seedProduct(t, ctx, catalog, house, "prod-out-pub", "Out Pub", barcode)
	setImmediate(t, ctx, svc, p)
	ingest(t, ctx, svc, p, "pub-in", barcode, "stock_in")
	ingest(t, ctx, svc, p, "pub-out", barcode, "stock_out")
	var invSeen bool
	deadline := time.After(2 * time.Second)
	for !invSeen {
		select {
		case msg := <-msgs:
			if msg.EventType == "inventory" {
				invSeen = true
			}
		case <-deadline:
			t.Fatal("expected inventory live event after stock_out removed units")
		}
	}
	_ = house
}
