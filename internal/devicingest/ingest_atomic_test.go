package devicingest_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/Rhionin/stockbot/internal/devicingest"
	"github.com/Rhionin/stockbot/internal/product"
	"github.com/Rhionin/stockbot/internal/scan"
)

func bootIngestFile(t *testing.T) (context.Context, *auth.Service, auth.Principal, string, *product.Catalog, *sql.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pantry.db")
	db, err := app.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := app.RunMigrations(db); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	svc := auth.NewService(db, dir)
	ctx := context.Background()
	if err := svc.Boot(ctx); err != nil {
		t.Fatal(err)
	}
	code, err := auth.ReadSetupCode(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.RedeemSetup(ctx, code, "Pat", "pat@example.test", "horse battery", ""); err != nil {
		t.Fatal(err)
	}
	user, err := svc.Authenticate(ctx, "pat@example.test", "horse battery")
	if err != nil {
		t.Fatal(err)
	}
	var house string
	if err := db.QueryRow(`SELECT id FROM households WHERE is_primary = 1`).Scan(&house); err != nil {
		t.Fatal(err)
	}
	pair, err := svc.StartPairing(ctx, user.ID, house)
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
	catalog := product.NewCatalog(db)
	lookup := &product.LookupService{
		Catalog: catalog,
		Upstream: stubFanOut{func(context.Context, string) product.FanOutResult {
			return product.FanOutResult{Outcome: product.FanOutConfirmedMiss}
		}},
	}
	devicingest.Attach(svc, scan.NewQueue(db), lookup)
	return ctx, svc, principal, house, catalog, db
}

func setImmediate(t *testing.T, ctx context.Context, svc *auth.Service, p auth.Principal) {
	t.Helper()
	user, err := svc.Authenticate(ctx, "pat@example.test", "horse battery")
	if err != nil {
		t.Fatal(err)
	}
	mode := "immediate"
	if _, err := svc.UpdateDevice(ctx, user.ID, p.HouseholdID, p.DeviceID, auth.UpdateDevicePatch{CommitMode: &mode}); err != nil {
		t.Fatal(err)
	}
}

func countStockMoves(t *testing.T, db *sql.DB, house string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM stock_moves WHERE household_id = ?`, house).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func productOnHandDB(t *testing.T, db *sql.DB, house, productID string) int {
	t.Helper()
	var n int
	err := db.QueryRow(`
		SELECT COUNT(*) FROM item_instances ii
		JOIN items i ON i.id = ii.item_id AND i.household_id = ii.household_id
		WHERE ii.household_id = ? AND i.product_id = ? AND ii.removed_at IS NULL`, house, productID).Scan(&n)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestDeviceIngestFaultInjectionRollsBackQueue(t *testing.T) {
	ctx, svc, p, house, catalog, db := bootIngest(t)
	seedProduct(t, ctx, catalog, house, "prod-q", "Beans", "4000000000011")
	devicingest.AfterDeviceStockCommitHook = func() error { return errors.New("injected") }
	t.Cleanup(func() { devicingest.AfterDeviceStockCommitHook = nil })

	_, err := svc.IngestScans(ctx, p, []auth.ScanInput{{
		ID: "fault-q", Barcode: "4000000000011", Direction: "stock_in", UnitCount: 1, HasBarcode: true,
	}}, 0)
	if err == nil {
		t.Fatal("expected error")
	}
	var receipts, entries int
	if err := db.QueryRow(`SELECT COUNT(*) FROM device_scan_receipts WHERE client_scan_id = 'fault-q'`).Scan(&receipts); err != nil || receipts != 0 {
		t.Fatalf("receipts %d", receipts)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM scan_entries WHERE client_scan_id = 'fault-q'`).Scan(&entries); err != nil || entries != 0 {
		t.Fatalf("entries %d", entries)
	}
	devicingest.AfterDeviceStockCommitHook = nil
	r := ingest(t, ctx, svc, p, "fault-q", "4000000000011", "stock_in")
	if r.Status != "created" || r.QueuedCount == nil || *r.QueuedCount != 1 {
		t.Fatalf("retry %#v", r)
	}
	if err := db.QueryRow(`SELECT unit_count FROM scan_entries WHERE client_scan_id = 'fault-q'`).Scan(&entries); err != nil || entries != 1 {
		t.Fatalf("unit_count %d", entries)
	}
}

func TestDeviceIngestFaultInjectionRollsBackImmediateStockIn(t *testing.T) {
	ctx, svc, p, house, catalog, db := bootIngest(t)
	seedProduct(t, ctx, catalog, house, "prod-in", "Rice", "4000000000022")
	setImmediate(t, ctx, svc, p)
	devicingest.AfterDeviceStockCommitHook = func() error { return errors.New("injected") }
	t.Cleanup(func() { devicingest.AfterDeviceStockCommitHook = nil })

	if _, err := svc.IngestScans(ctx, p, []auth.ScanInput{{
		ID: "fault-in", Barcode: "4000000000022", Direction: "stock_in", UnitCount: 1, HasBarcode: true,
	}}, 0); err == nil {
		t.Fatal("expected error")
	}
	if countStockMoves(t, db, house) != 0 || productOnHandDB(t, db, house, "prod-in") != 0 {
		t.Fatalf("stock leaked")
	}
	devicingest.AfterDeviceStockCommitHook = nil
	r := ingest(t, ctx, svc, p, "fault-in", "4000000000022", "stock_in")
	if r.Status != "created" || countStockMoves(t, db, house) != 1 || productOnHandDB(t, db, house, "prod-in") != 1 {
		t.Fatalf("retry moves=%d onHand=%d result %#v", countStockMoves(t, db, house), productOnHandDB(t, db, house, "prod-in"), r)
	}
}

func TestDeviceIngestFaultInjectionRollsBackImmediateBackfill(t *testing.T) {
	ctx, svc, p, house, catalog, db := bootIngest(t)
	seedProduct(t, ctx, catalog, house, "prod-bf", "Oats", "4000000000033")
	setImmediate(t, ctx, svc, p)
	ingest(t, ctx, svc, p, "setup-in", "4000000000033", "stock_in")
	ingest(t, ctx, svc, p, "setup-out", "4000000000033", "stock_out")

	devicingest.AfterDeviceStockCommitHook = func() error { return errors.New("injected") }
	t.Cleanup(func() { devicingest.AfterDeviceStockCommitHook = nil })

	if _, err := svc.IngestScans(ctx, p, []auth.ScanInput{{
		ID: "fault-bf", Barcode: "4000000000033", Direction: "stock_out", UnitCount: 1, HasBarcode: true,
	}}, 0); err == nil {
		t.Fatal("expected error")
	}
	if countStockMoves(t, db, house) != 2 {
		t.Fatalf("moves %d want 2 from setup", countStockMoves(t, db, house))
	}
	devicingest.AfterDeviceStockCommitHook = nil
	r := ingest(t, ctx, svc, p, "fault-bf", "4000000000033", "stock_out")
	if r.Status != "created" || r.Backfilled == nil || !*r.Backfilled {
		t.Fatalf("backfill %#v", r)
	}
	if countStockMoves(t, db, house) != 4 {
		t.Fatalf("backfill pair moves %d want 4 total", countStockMoves(t, db, house))
	}
}

func TestDeviceIngestConcurrentDuplicate(t *testing.T) {
	ctx, svc, p, house, catalog, db := bootIngestFile(t)
	seedProduct(t, ctx, catalog, house, "prod-c", "Salt", "4000000000044")
	setImmediate(t, ctx, svc, p)

	const workers = 16
	var wg sync.WaitGroup
	wg.Add(workers)
	errs := make([]error, workers)
	statuses := make([]string, workers)
	for i := 0; i < workers; i++ {
		go func(idx int) {
			defer wg.Done()
			out, err := svc.IngestScans(ctx, p, []auth.ScanInput{{
				ID: "same-id", Barcode: "4000000000044", Direction: "stock_in", UnitCount: 1, HasBarcode: true,
			}}, 0)
			errs[idx] = err
			if err == nil && len(out) == 1 {
				statuses[idx] = out[0].Status
			}
		}(i)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatalf("5xx or error: %v", err)
		}
	}
	var created, duplicate int
	for _, st := range statuses {
		switch st {
		case "created":
			created++
		case "duplicate":
			duplicate++
		default:
			t.Fatalf("status %q", st)
		}
	}
	if created != 1 || duplicate != workers-1 {
		t.Fatalf("created %d duplicate %d", created, duplicate)
	}
	if countStockMoves(t, db, house) != 1 || productOnHandDB(t, db, house, "prod-c") != 1 {
		t.Fatalf("stock moves=%d onHand=%d", countStockMoves(t, db, house), productOnHandDB(t, db, house, "prod-c"))
	}
}

func TestDeviceIngestConcurrentDuplicateQueueMerge(t *testing.T) {
	ctx, svc, p, house, catalog, db := bootIngestFile(t)
	seedProduct(t, ctx, catalog, house, "prod-qc", "Pepper", "4000000000055")

	const workers = 12
	var wg sync.WaitGroup
	wg.Add(workers)
	errs := make([]error, workers)
	statuses := make([]string, workers)
	for i := 0; i < workers; i++ {
		go func(idx int) {
			defer wg.Done()
			out, err := svc.IngestScans(ctx, p, []auth.ScanInput{{
				ID: "queue-dup", Barcode: "4000000000055", Direction: "stock_in", UnitCount: 1, HasBarcode: true,
			}}, 0)
			errs[idx] = err
			if err == nil && len(out) == 1 {
				statuses[idx] = out[0].Status
			}
		}(i)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	var created int
	for _, st := range statuses {
		if st == "created" {
			created++
		} else if st != "duplicate" {
			t.Fatalf("status %q", st)
		}
	}
	if created != 1 {
		t.Fatalf("created %d", created)
	}
	var unitCount int
	if err := db.QueryRow(`SELECT unit_count FROM scan_entries WHERE client_scan_id = 'queue-dup'`).Scan(&unitCount); err != nil || unitCount != 1 {
		t.Fatalf("unit_count %d", unitCount)
	}
	if countStockMoves(t, db, house) != 0 {
		t.Fatalf("queue mode wrote stock moves")
	}
}

func TestDeviceIngestConcurrentConflict(t *testing.T) {
	ctx, svc, p, house, catalog, _ := bootIngestFile(t)
	seedProduct(t, ctx, catalog, house, "prod-x", "Sugar", "4000000000066")

	var wg sync.WaitGroup
	var created, conflict int
	for _, dir := range []string{"stock_in", "stock_out"} {
		dir := dir
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, err := svc.IngestScans(ctx, p, []auth.ScanInput{{
				ID: "conflict-id", Barcode: "4000000000066", Direction: dir, UnitCount: 1, HasBarcode: true,
			}}, 0)
			if err != nil {
				t.Errorf("err %v", err)
				return
			}
			if len(out) != 1 {
				t.Errorf("results %v", out)
				return
			}
			switch out[0].Status {
			case "created":
				created++
			case "conflict":
				conflict++
			default:
				t.Errorf("status %q", out[0].Status)
			}
		}()
	}
	wg.Wait()
	if created != 1 || conflict != 1 {
		t.Fatalf("created %d conflict %d", created, conflict)
	}
}

func TestMain(m *testing.M) {
	code := m.Run()
	devicingest.AfterDeviceStockCommitHook = nil
	os.Exit(code)
}
