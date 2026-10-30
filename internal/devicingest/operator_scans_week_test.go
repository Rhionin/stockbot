package devicingest_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/Rhionin/stockbot/internal/scan"
	"github.com/Rhionin/stockbot/internal/tenant"
	"github.com/google/uuid"
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
		units    int
	}{
		{"orphan-at-cutoff", cutoff, 3},
		{"orphan-before-cutoff", cutoff.Add(-time.Second), 1},
	} {
		if _, err := db.Exec(`
			INSERT INTO device_scan_receipts (household_id, device_id, client_scan_id, scan_entry_id, barcode, direction, unit_count, scanned_at)
			VALUES (?, ?, ?, '', '4000000000106', 'stock_out', ?, ?)`,
			house, p.DeviceID, spec.clientID, spec.units, authStamp(spec.at)); err != nil {
			t.Fatal(err)
		}
	}
	if got := operatorScansThisWeek(t, ctx, svc, house); got != 3 {
		t.Fatalf("orphan receipt scansThisWeek=%d want 3 (SUM unit_count at cutoff only)", got)
	}
}

func TestOperatorScansThisWeekIsolatedAcrossHouseholdsWithDecoys(t *testing.T) {
	for _, locName := range []string{"UTC", "America/Denver"} {
		loc, err := time.LoadLocation(locName)
		if err != nil {
			t.Fatal(err)
		}
		t.Run(locName, func(t *testing.T) {
			ctx, svc, p, primary, _, db := bootIngest(t)
			var userID string
			if err := db.QueryRow(`SELECT id FROM users WHERE email_norm = 'pat@example.test'`).Scan(&userID); err != nil {
				t.Fatal(err)
			}
			now := time.Date(2026, 10, 21, 14, 0, 0, 0, loc)
			svc.SetClock(func() time.Time { return now })
			start := now.Add(-48 * time.Hour)
			type spec struct {
				id    string
				name  string
				key   string
				when  time.Time
				units int
			}
			decoyBefore := spec{id: authNewID(), name: "Decoy Before", key: "abcdef0123456789abcdef0123456789e1", when: start, units: 1}
			target := spec{id: authNewID(), name: "Target Week", key: "abcdef0123456789abcdef0123456789e2", when: start.Add(time.Hour), units: 7}
			decoyAfter := spec{id: authNewID(), name: "Decoy After", key: "abcdef0123456789abcdef0123456789e3", when: start.Add(2 * time.Hour), units: 13}
			for _, h := range []spec{decoyBefore, target, decoyAfter} {
				if err := insertOperatorTestHousehold(t, db, userID, h.id, h.name, h.key, h.when); err != nil {
					t.Fatal(err)
				}
				seedOperatorHouseholdScanUnits(t, ctx, db, p.DeviceID, h.id, h.units, now.Add(-2*time.Hour))
			}
			want := map[string]int{
				primary:      0,
				decoyBefore.id: decoyBefore.units,
				target.id:      target.units,
				decoyAfter.id:  decoyAfter.units,
			}
			list, err := svc.ListAllHouseholdsForOperator(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if list[0].ID != primary {
				t.Fatalf("primary first got %s", list[0].ID)
			}
			got := map[string]int{}
			for _, row := range list {
				if _, ok := want[row.ID]; ok {
					got[row.ID] = row.ScansThisWeek
				}
			}
			for id, n := range want {
				if got[id] != n {
					t.Fatalf("house %s scansThisWeek=%d want %d (full got %#v)", id, got[id], n, got)
				}
			}
		})
	}
}

func authNewID() string {
	return uuid.NewString()
}

func insertOperatorTestHousehold(t *testing.T, db *sql.DB, userID, houseID, name, channelKey string, created time.Time) error {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO households (id, name, restock_mode, created_at, channel_key, is_primary)
		VALUES (?, ?, 'auto', ?, ?, 0)`, houseID, name, authStamp(created), channelKey); err != nil {
		return err
	}
	_, err := db.Exec(`INSERT INTO memberships (household_id, user_id, role, created_at) VALUES (?, ?, 'owner', ?)`,
		houseID, userID, authStamp(created))
	return err
}

func seedOperatorHouseholdScanUnits(t *testing.T, ctx context.Context, db *sql.DB, userID, house string, units int, scannedAt time.Time) {
	t.Helper()
	queue := scan.NewQueue(db)
	scope, err := tenant.New(house)
	if err != nil {
		t.Fatal(err)
	}
	dir := scan.StockOut
	barcode := "4000000" + uuid.NewString()[:8]
	if _, err := queue.CreateScanEntry(ctx, scope, scan.ScanEntry{
		UserID: userID, Barcode: barcode, Direction: &dir, UnitCount: units, ScannedAt: scannedAt,
	}); err != nil {
		t.Fatal(err)
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
