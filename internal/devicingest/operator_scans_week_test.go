package devicingest_test

import (
	"context"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/Rhionin/stockbot/internal/scan"
	"github.com/Rhionin/stockbot/internal/tenant"
)

func operatorScansThisWeek(t *testing.T, ctx context.Context, svc *auth.Service, house string) int {
	t.Helper()
	list, err := svc.ListAllHouseholdsForOperator(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range list {
		if row.ID == house {
			return row.ScansThisWeek
		}
	}
	t.Fatal("household missing from operator list")
	return 0
}

func ingestAt(t *testing.T, ctx context.Context, svc *auth.Service, p auth.Principal, id, barcode, direction string, at time.Time) auth.ScanResult {
	t.Helper()
	results, err := svc.IngestScans(ctx, p, []auth.ScanInput{{
		ID: id, Barcode: barcode, Direction: direction, UnitCount: 1, HasBarcode: true,
		HasScannedAt: true, ScannedAt: at,
	}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("results %v", results)
	}
	return results[0]
}

func TestOperatorScansThisWeekWebEntryAtCutoffPlus30mCountsUnits(t *testing.T) {
	for _, locName := range []string{"UTC", "America/Denver"} {
		loc, err := time.LoadLocation(locName)
		if err != nil {
			t.Fatal(err)
		}
		t.Run(locName, func(t *testing.T) {
			ctx, svc, p, house, _, db := bootIngest(t)
			queue := scan.NewQueue(db)
			now := time.Date(2026, 10, 15, 15, 0, 0, 0, loc)
			svc.SetClock(func() time.Time { return now })
			scope, err := tenant.New(house)
			if err != nil {
				t.Fatal(err)
			}
			cutoff := now.Add(-7 * 24 * time.Hour)
			scannedAt := cutoff.Add(30 * time.Minute)
			dir := scan.StockOut
			if _, err := queue.CreateScanEntry(ctx, scope, scan.ScanEntry{
				UserID: p.DeviceID, Barcode: "4000000000099", Direction: &dir, UnitCount: 2, ScannedAt: scannedAt,
			}); err != nil {
				t.Fatal(err)
			}
			if got := operatorScansThisWeek(t, ctx, svc, house); got != 2 {
				t.Fatalf("scansThisWeek=%d want 2 (cutoff+30m web units)", got)
			}
		})
	}
}

func TestOperatorScansThisWeekMergedWebAndDeviceCountsFour(t *testing.T) {
	for _, locName := range []string{"UTC", "America/Denver"} {
		loc, err := time.LoadLocation(locName)
		if err != nil {
			t.Fatal(err)
		}
		t.Run(locName, func(t *testing.T) {
			ctx, svc, p, house, _, db := bootIngest(t)
			queue := scan.NewQueue(db)
			now := time.Date(2026, 10, 16, 10, 0, 0, 0, loc)
			svc.SetClock(func() time.Time { return now })
			scope, err := tenant.New(house)
			if err != nil {
				t.Fatal(err)
			}
			at := now.Add(-2 * time.Hour)
			dir := scan.StockOut
			barcode := "4000000000100"
			if _, err := queue.CreateScanEntry(ctx, scope, scan.ScanEntry{
				UserID: p.DeviceID, Barcode: barcode, Direction: &dir, UnitCount: 3, ScannedAt: at,
			}); err != nil {
				t.Fatal(err)
			}
			r := ingestAt(t, ctx, svc, p, "dev-merge-1", barcode, "stock_out", at.Add(time.Minute))
			if r.QueuedCount == nil || *r.QueuedCount != 4 {
				t.Fatalf("merge queued count %#v want 4", r.QueuedCount)
			}
			if got := operatorScansThisWeek(t, ctx, svc, house); got != 4 {
				t.Fatalf("scansThisWeek=%d want 4", got)
			}
		})
	}
}

func TestOperatorScansThisWeekUnmergedDeviceScanCountsOne(t *testing.T) {
	for _, locName := range []string{"UTC", "America/Denver"} {
		loc, err := time.LoadLocation(locName)
		if err != nil {
			t.Fatal(err)
		}
		t.Run(locName, func(t *testing.T) {
			ctx, svc, p, house, _, _ := bootIngest(t)
			now := time.Date(2026, 10, 17, 8, 0, 0, 0, loc)
			svc.SetClock(func() time.Time { return now })
			ingestAt(t, ctx, svc, p, "solo-dev-1", "4000000000101", "stock_out", now.Add(-time.Hour))
			if got := operatorScansThisWeek(t, ctx, svc, house); got != 1 {
				t.Fatalf("scansThisWeek=%d want 1", got)
			}
		})
	}
}

func TestOperatorScansThisWeekOrphanReceiptCutoffBoundary(t *testing.T) {
	ctx, svc, p, house, _, db := bootIngest(t)
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	svc.SetClock(func() time.Time { return now })
	cutoff := now.Add(-7 * 24 * time.Hour)
	for _, spec := range []struct {
		clientID string
		at       time.Time
	}{
		{"orphan-at-cutoff", cutoff},
		{"orphan-before-cutoff", cutoff.Add(-time.Second)},
	} {
		if _, err := db.Exec(`
			INSERT INTO device_scan_receipts (household_id, device_id, client_scan_id, scan_entry_id, barcode, direction, unit_count, scanned_at)
			VALUES (?, ?, ?, '', '4000000000106', 'stock_out', 1, ?)`,
			house, p.DeviceID, spec.clientID, authStamp(spec.at)); err != nil {
			t.Fatal(err)
		}
	}
	if got := operatorScansThisWeek(t, ctx, svc, house); got != 1 {
		t.Fatalf("orphan receipt scansThisWeek=%d want 1 (only at cutoff)", got)
	}
}

func authStamp(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}

func TestOperatorScansThisWeekCutoffInclusiveExclusive(t *testing.T) {
	for _, locName := range []string{"UTC", "America/Denver"} {
		loc, err := time.LoadLocation(locName)
		if err != nil {
			t.Fatal(err)
		}
		t.Run(locName, func(t *testing.T) {
			ctx, svc, p, house, _, db := bootIngest(t)
			queue := scan.NewQueue(db)
			now := time.Date(2026, 10, 18, 12, 0, 0, 0, loc)
			svc.SetClock(func() time.Time { return now })
			scope, err := tenant.New(house)
			if err != nil {
				t.Fatal(err)
			}
			cutoff := now.Add(-7 * 24 * time.Hour)
			dir := scan.StockOut
			if _, err := queue.CreateScanEntry(ctx, scope, scan.ScanEntry{
				UserID: p.DeviceID, Barcode: "4000000000102", Direction: &dir, UnitCount: 1, ScannedAt: cutoff,
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := queue.CreateScanEntry(ctx, scope, scan.ScanEntry{
				UserID: p.DeviceID, Barcode: "4000000000103", Direction: &dir, UnitCount: 1, ScannedAt: cutoff.Add(-time.Second),
			}); err != nil {
				t.Fatal(err)
			}
			ingestAt(t, ctx, svc, p, "dev-at-cutoff", "4000000000104", "stock_out", cutoff)
			ingestAt(t, ctx, svc, p, "dev-before-cutoff", "4000000000105", "stock_out", cutoff.Add(-time.Second))
			if got := operatorScansThisWeek(t, ctx, svc, house); got != 2 {
				t.Fatalf("scansThisWeek=%d want 2 (web+device at cutoff only)", got)
			}
		})
	}
}
