package devicingest_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/Rhionin/stockbot/internal/changes"
	"github.com/Rhionin/stockbot/internal/devicingest"
	"github.com/Rhionin/stockbot/internal/events"
	"github.com/Rhionin/stockbot/internal/inventory"
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
	spy := &ingestPublishSpy{}
	svc.SetDeviceIngestEventPublisher(spy)
	devicingest.SetAfterDeviceStockCommitHook(func() error { return errors.New("injected") })
	t.Cleanup(func() { devicingest.SetAfterDeviceStockCommitHook(nil) })

	_, err := svc.IngestScans(ctx, p, []auth.ScanInput{{
		ID: "fault-q", Barcode: "4000000000011", Direction: "stock_in", UnitCount: 1, HasBarcode: true,
	}}, 0)
	if err == nil {
		t.Fatal("expected error")
	}
	if spy.len() != 0 {
		t.Fatalf("publish calls %d want 0 on rollback", spy.len())
	}
	var receipts, entries int
	if err := db.QueryRow(`SELECT COUNT(*) FROM device_scan_receipts WHERE client_scan_id = 'fault-q'`).Scan(&receipts); err != nil || receipts != 0 {
		t.Fatalf("receipts %d", receipts)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM scan_entries WHERE client_scan_id = 'fault-q'`).Scan(&entries); err != nil || entries != 0 {
		t.Fatalf("entries %d", entries)
	}
	devicingest.SetAfterDeviceStockCommitHook(nil)
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
	devicingest.SetAfterDeviceStockCommitHook(func() error { return errors.New("injected") })
	t.Cleanup(func() { devicingest.SetAfterDeviceStockCommitHook(nil) })

	if _, err := svc.IngestScans(ctx, p, []auth.ScanInput{{
		ID: "fault-in", Barcode: "4000000000022", Direction: "stock_in", UnitCount: 1, HasBarcode: true,
	}}, 0); err == nil {
		t.Fatal("expected error")
	}
	if countStockMoves(t, db, house) != 0 || productOnHandDB(t, db, house, "prod-in") != 0 {
		t.Fatalf("stock leaked")
	}
	devicingest.SetAfterDeviceStockCommitHook(nil)
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

	devicingest.SetAfterDeviceStockCommitHook(func() error { return errors.New("injected") })
	t.Cleanup(func() { devicingest.SetAfterDeviceStockCommitHook(nil) })

	if _, err := svc.IngestScans(ctx, p, []auth.ScanInput{{
		ID: "fault-bf", Barcode: "4000000000033", Direction: "stock_out", UnitCount: 1, HasBarcode: true,
	}}, 0); err == nil {
		t.Fatal("expected error")
	}
	if countStockMoves(t, db, house) != 2 {
		t.Fatalf("moves %d want 2 from setup", countStockMoves(t, db, house))
	}
	var receipts int
	if err := db.QueryRow(`SELECT COUNT(*) FROM device_scan_receipts WHERE client_scan_id = 'fault-bf'`).Scan(&receipts); err != nil || receipts != 0 {
		t.Fatalf("receipts %d want 0 after rollback", receipts)
	}
	if productOnHandDB(t, db, house, "prod-bf") != 0 {
		t.Fatalf("on-hand leaked after rollback")
	}
	devicingest.SetAfterDeviceStockCommitHook(nil)
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
	var ready sync.WaitGroup
	ready.Add(workers)
	start := make(chan struct{})
	wg.Add(workers)
	errs := make([]error, workers)
	statuses := make([]string, workers)
	for i := 0; i < workers; i++ {
		go func(idx int) {
			defer wg.Done()
			ready.Done()
			<-start
			out, err := svc.IngestScans(ctx, p, []auth.ScanInput{{
				ID: "same-id", Barcode: "4000000000044", Direction: "stock_in", UnitCount: 1, HasBarcode: true,
			}}, 0)
			errs[idx] = err
			if err == nil && len(out) == 1 {
				statuses[idx] = out[0].Status
			}
		}(i)
	}
	ready.Wait()
	close(start)
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
	var ready sync.WaitGroup
	ready.Add(workers)
	start := make(chan struct{})
	wg.Add(workers)
	errs := make([]error, workers)
	statuses := make([]string, workers)
	for i := 0; i < workers; i++ {
		go func(idx int) {
			defer wg.Done()
			ready.Done()
			<-start
			out, err := svc.IngestScans(ctx, p, []auth.ScanInput{{
				ID: "queue-dup", Barcode: "4000000000055", Direction: "stock_in", UnitCount: 1, HasBarcode: true,
			}}, 0)
			errs[idx] = err
			if err == nil && len(out) == 1 {
				statuses[idx] = out[0].Status
			}
		}(i)
	}
	ready.Wait()
	close(start)
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

type hintSpy struct {
	kinds *[]string
}

func (h hintSpy) PublishHint(_, kind string) {
	*h.kinds = append(*h.kinds, kind)
}

func TestDeviceIngestProcessorBatchSideEffects(t *testing.T) {
	ctx, svc, p, house, catalog, db := bootIngest(t)
	seedProduct(t, ctx, catalog, house, "prod-side", "Oil", "4000000000099")
	var hints []string
	svc.SetHints(hintSpy{&hints})
	var lookedUp bool
	svc.SetBarcodeLookup(func(_ context.Context, _, _, _ string) { lookedUp = true })
	if _, err := svc.IngestScans(ctx, p, []auth.ScanInput{{
		ID: "side-1", Barcode: "4000000000099", Direction: "stock_in", UnitCount: 1, HasBarcode: true,
	}}, 3); err != nil {
		t.Fatal(err)
	}
	if !lookedUp {
		t.Fatal("barcode lookup not queued")
	}
	if len(hints) == 0 {
		t.Fatalf("hints %v", hints)
	}
	var dropped int
	if err := db.QueryRow(`SELECT dropped_total FROM devices WHERE id = ?`, p.DeviceID).Scan(&dropped); err != nil || dropped != 3 {
		t.Fatalf("dropped %d err %v", dropped, err)
	}
}

func TestDeviceIngestDuplicateFlaggedScanWithoutStoredProductName(t *testing.T) {
	ctx, svc, p, house, _, db := bootIngest(t)
	first := ingest(t, ctx, svc, p, "flag-dup", "8888888888888", "stock_in")
	if first.ScanStatus != "flagged" {
		t.Fatalf("first %#v", first)
	}
	dup := ingest(t, ctx, svc, p, "flag-dup", "8888888888888", "stock_in")
	if dup.Status != "duplicate" || dup.ScanStatus != "flagged" {
		t.Fatalf("dup %#v", dup)
	}
	var receipts int
	if err := db.QueryRow(`SELECT COUNT(*) FROM device_scan_receipts WHERE household_id = ? AND client_scan_id = 'flag-dup'`, house).Scan(&receipts); err != nil || receipts != 1 {
		t.Fatalf("receipts %d", receipts)
	}
}

func TestDeviceIngestReceiptMissingStatusIsConflict(t *testing.T) {
	ctx, svc, p, house, catalog, db := bootIngest(t)
	seedProduct(t, ctx, catalog, house, "prod-conf", "Pasta", "4000000000111")
	ingest(t, ctx, svc, p, "claim-1", "4000000000111", "stock_in")
	if _, err := db.Exec(`UPDATE device_scan_receipts SET device_result_status = '' WHERE household_id = ? AND client_scan_id = 'claim-1'`, house); err != nil {
		t.Fatal(err)
	}
	out, err := svc.IngestScans(ctx, p, []auth.ScanInput{{
		ID: "claim-1", Barcode: "4000000000111", Direction: "stock_in", UnitCount: 1, HasBarcode: true,
	}}, 0)
	if err != nil || len(out) != 1 || out[0].Status != "conflict" {
		t.Fatalf("out %#v err %v", out, err)
	}
}

func TestDeviceIngestProcessorDeviceCameOnline(t *testing.T) {
	ctx, svc, p, house, catalog, db := bootIngest(t)
	seedProduct(t, ctx, catalog, house, "prod-online", "Honey", "4000000000122")
	var hints []string
	svc.SetHints(hintSpy{&hints})
	if _, err := db.Exec(`UPDATE devices SET last_seen_at = '2000-01-01T00:00:00Z' WHERE id = ?`, p.DeviceID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.IngestScans(ctx, p, []auth.ScanInput{{
		ID: "online-1", Barcode: "4000000000122", Direction: "stock_in", UnitCount: 1, HasBarcode: true,
	}}, 0); err != nil {
		t.Fatal(err)
	}
	var sawDevices bool
	for _, kind := range hints {
		if kind == "devices" {
			sawDevices = true
		}
	}
	if !sawDevices {
		t.Fatalf("hints %v", hints)
	}
}

type errPublisher struct{ err error }

func (e errPublisher) PublishAfterIngest(context.Context, string, string, bool) error {
	return e.err
}

func TestDeviceIngestPublishErrorReturnsSuccessAndLogs(t *testing.T) {
	var logBuf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelError})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	ctx, svc, p, house, catalog, db := bootIngest(t)
	seedProduct(t, ctx, catalog, house, "prod-pub-err", "Jam", "4000000000133")
	setImmediate(t, ctx, svc, p)
	svc.SetDeviceIngestEventPublisher(errPublisher{err: errors.New("publish failed")})

	out, err := svc.IngestScans(ctx, p, []auth.ScanInput{{
		ID: "pub-err", Barcode: "4000000000133", Direction: "stock_in", UnitCount: 1, HasBarcode: true,
	}}, 0)
	if err != nil || len(out) != 1 || out[0].Status != "created" {
		t.Fatalf("out %#v err %v", out, err)
	}
	if !strings.Contains(logBuf.String(), "publish failed") {
		t.Fatalf("log %q", logBuf.String())
	}
	if countStockMoves(t, db, house) != 1 {
		t.Fatalf("stock moves %d", countStockMoves(t, db, house))
	}
	var receipts int
	if err := db.QueryRow(`SELECT COUNT(*) FROM device_scan_receipts WHERE client_scan_id = 'pub-err'`).Scan(&receipts); err != nil || receipts != 1 {
		t.Fatalf("receipts %d", receipts)
	}
	dup := ingest(t, ctx, svc, p, "pub-err", "4000000000133", "stock_in")
	if dup.Status != "duplicate" || countStockMoves(t, db, house) != 1 {
		t.Fatalf("dup %#v moves %d", dup, countStockMoves(t, db, house))
	}
}

func TestDeviceIngestReplayDifferentBarcodeIsConflict(t *testing.T) {
	ctx, svc, p, house, catalog, _ := bootIngest(t)
	seedProduct(t, ctx, catalog, house, "prod-bc", "Tea", "4000000000166")
	seedProduct(t, ctx, catalog, house, "prod-bc2", "Coffee", "4000000000177")
	first := ingest(t, ctx, svc, p, "same-client", "4000000000166", "stock_in")
	if first.Status != "created" {
		t.Fatalf("first %#v", first)
	}
	conflict, err := svc.IngestScans(ctx, p, []auth.ScanInput{{
		ID: "same-client", Barcode: "4000000000177", Direction: "stock_in", UnitCount: 1, HasBarcode: true,
	}}, 0)
	if err != nil || len(conflict) != 1 || conflict[0].Status != "conflict" {
		t.Fatalf("conflict %#v err %v", conflict, err)
	}
}

func TestDeviceIngestImmediateStockOutPublishStockMoved(t *testing.T) {
	ctx, svc, p, house, catalog, _, spy := bootIngestWithPublishSpy(t)
	seedProduct(t, ctx, catalog, house, "prod-out-move", "Oil", "4000000000188")
	setImmediate(t, ctx, svc, p)
	ingest(t, ctx, svc, p, "in-for-out", "4000000000188", "stock_in")
	out := ingest(t, ctx, svc, p, "out-move", "4000000000188", "stock_out")
	if out.Status != "created" || spy.len() < 2 {
		t.Fatalf("out %#v publish calls %d", out, spy.len())
	}
	if !spy.lastStockMoved() {
		t.Fatal("stock_out publish must pass stockMoved=true")
	}
}

func TestDeviceIngestImmediateBackfillPublishStockMovedTrue(t *testing.T) {
	ctx, svc, p, house, catalog, _, spy := bootIngestWithPublishSpy(t)
	seedProduct(t, ctx, catalog, house, "prod-bf-flag", "Pasta", "4000000000211")
	setImmediate(t, ctx, svc, p)
	ingest(t, ctx, svc, p, "bf-setup-in", "4000000000211", "stock_in")
	ingest(t, ctx, svc, p, "bf-setup-out", "4000000000211", "stock_out")
	if _, err := svc.IngestScans(ctx, p, []auth.ScanInput{{
		ID: "bf-flag", Barcode: "4000000000211", Direction: "stock_out", UnitCount: 1, HasBarcode: true,
	}}, 0); err != nil {
		t.Fatal(err)
	}
	if !spy.lastStockMoved() {
		t.Fatal("backfill commit must publish with stockMoved=true")
	}
}

func TestDeviceIngestImmediateBackfillPublishesLiveInventory(t *testing.T) {
	ctx, svc, p, house, catalog, _, msgs := bootIngestWithBroadcaster(t)
	seedProduct(t, ctx, catalog, house, "prod-bf-live", "Rice", "4000000000199")
	setImmediate(t, ctx, svc, p)
	ingest(t, ctx, svc, p, "bf-in", "4000000000199", "stock_in")
	ingest(t, ctx, svc, p, "bf-out", "4000000000199", "stock_out")
	if _, err := svc.IngestScans(ctx, p, []auth.ScanInput{{
		ID: "bf-live", Barcode: "4000000000199", Direction: "stock_out", UnitCount: 1, HasBarcode: true,
	}}, 0); err != nil {
		t.Fatal(err)
	}
	var invSeen bool
	deadline := time.After(2 * time.Second)
	for !invSeen {
		select {
		case msg := <-msgs:
			if msg.EventType == "inventory" {
				invSeen = true
			}
		case <-deadline:
			t.Fatal("expected inventory live event after backfill commit")
		}
	}
}

func TestDeviceIngestImmediateStockInBumpsScopeStockAndLiveInventory(t *testing.T) {
	ctx, svc, p, house, catalog, db, msgs := bootIngestWithBroadcaster(t)
	seedProduct(t, ctx, catalog, house, "prod-scope-stock", "Flour", "4000000000200")
	setImmediate(t, ctx, svc, p)
	before, err := changes.Version(ctx, db, house, changes.ScopeStock)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.IngestScans(ctx, p, []auth.ScanInput{{
		ID: "scope-stock", Barcode: "4000000000200", Direction: "stock_in", UnitCount: 1, HasBarcode: true,
	}}, 0); err != nil {
		t.Fatal(err)
	}
	after, err := changes.Version(ctx, db, house, changes.ScopeStock)
	if err != nil || after <= before {
		t.Fatalf("ScopeStock version before %d after %d err %v", before, after, err)
	}
	var invSeen bool
	deadline := time.After(2 * time.Second)
	for !invSeen {
		select {
		case msg := <-msgs:
			if msg.EventType == "inventory" {
				invSeen = true
			}
		case <-deadline:
			t.Fatal("expected inventory live event after immediate stock-in")
		}
	}
}

func bootIngestWithBroadcaster(t *testing.T) (context.Context, *auth.Service, auth.Principal, string, *product.Catalog, *sql.DB, <-chan events.Message) {
	t.Helper()
	ctx, svc, p, house, catalog, db := bootIngestFile(t)
	queue := scan.NewQueue(db)
	queue.Pantry = inventory.NewPantry(db)
	b := events.NewBroadcaster()
	queue.Broadcaster = b
	msgs, unsub := b.SubscribeHousehold(house, "", "")
	t.Cleanup(unsub)
	devicingest.Attach(svc, queue, &product.LookupService{
		Catalog: catalog,
		Upstream: stubFanOut{func(context.Context, string) product.FanOutResult {
			return product.FanOutResult{Outcome: product.FanOutConfirmedMiss}
		}},
	})
	return ctx, svc, p, house, catalog, db, msgs
}

func TestDeviceIngestImmediatePublishesLiveEvents(t *testing.T) {
	ctx, svc, p, house, catalog, _, msgs := bootIngestWithBroadcaster(t)
	seedProduct(t, ctx, catalog, house, "prod-live-ev", "Syrup", "4000000000144")
	setImmediate(t, ctx, svc, p)
	if _, err := svc.IngestScans(ctx, p, []auth.ScanInput{{
		ID: "live-ev", Barcode: "4000000000144", Direction: "stock_in", UnitCount: 1, HasBarcode: true,
	}}, 0); err != nil {
		t.Fatal(err)
	}
	var scanEvent, invEvent bool
	deadline := time.After(2 * time.Second)
	for !scanEvent || !invEvent {
		select {
		case msg := <-msgs:
			switch msg.EventType {
			case "scan":
				scanEvent = true
			case "inventory":
				invEvent = true
			}
		case <-deadline:
			t.Fatalf("scan=%v inventory=%v", scanEvent, invEvent)
		}
	}
}

type ingestPublishSpy struct {
	mu    sync.Mutex
	calls []ingestPublishCall
}

type ingestPublishCall struct {
	householdID, scanEntryID string
	stockMoved               bool
}

func (s *ingestPublishSpy) PublishAfterIngest(_ context.Context, householdID, scanEntryID string, stockMoved bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, ingestPublishCall{householdID: householdID, scanEntryID: scanEntryID, stockMoved: stockMoved})
	return nil
}

func (s *ingestPublishSpy) len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.calls)
}

func (s *ingestPublishSpy) lastStockMoved() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.calls) == 0 {
		return false
	}
	return s.calls[len(s.calls)-1].stockMoved
}

func bootIngestWithPublishSpy(t *testing.T) (context.Context, *auth.Service, auth.Principal, string, *product.Catalog, *sql.DB, *ingestPublishSpy) {
	t.Helper()
	ctx, svc, p, house, catalog, db := bootIngest(t)
	spy := &ingestPublishSpy{}
	svc.SetDeviceIngestEventPublisher(spy)
	return ctx, svc, p, house, catalog, db, spy
}

func TestDeviceIngestRollbackDoesNotPublishAfterIngest(t *testing.T) {
	ctx, svc, p, house, catalog, _, spy := bootIngestWithPublishSpy(t)
	seedProduct(t, ctx, catalog, house, "prod-pub-rollback", "Corn", "4000000000077")
	setImmediate(t, ctx, svc, p)
	devicingest.SetAfterDeviceStockCommitHook(func() error { return errors.New("injected") })
	t.Cleanup(func() { devicingest.SetAfterDeviceStockCommitHook(nil) })

	if _, err := svc.IngestScans(ctx, p, []auth.ScanInput{{
		ID: "pub-rollback", Barcode: "4000000000077", Direction: "stock_in", UnitCount: 1, HasBarcode: true,
	}}, 0); err == nil {
		t.Fatal("expected error")
	}
	if spy.len() != 0 {
		t.Fatalf("publish calls %d want 0", spy.len())
	}
}

func TestDeviceIngestCommitPublishesAfterIngestOnce(t *testing.T) {
	ctx, svc, p, house, catalog, _, spy := bootIngestWithPublishSpy(t)
	seedProduct(t, ctx, catalog, house, "prod-pub-once", "Flour", "4000000000088")
	setImmediate(t, ctx, svc, p)

	first := ingest(t, ctx, svc, p, "pub-once", "4000000000088", "stock_in")
	if spy.len() != 1 {
		t.Fatalf("publish calls %d want 1", spy.len())
	}
	dup := ingest(t, ctx, svc, p, "pub-once", "4000000000088", "stock_in")
	if dup.Status != "duplicate" || spy.len() != 1 {
		t.Fatalf("duplicate %#v publish calls %d", dup, spy.len())
	}
	_ = first
}

func TestDeviceIngestProductColumnsMatchMasterBehavior(t *testing.T) {
	ctx, svc, p, house, catalog, db := bootIngest(t)
	seedProduct(t, ctx, catalog, house, "prod-master", "Beans", "4000000000011")

	known := ingest(t, ctx, svc, p, "prod-known", "4000000000011", "stock_in")
	if known.ProductName == nil || *known.ProductName != "Beans" {
		t.Fatalf("lcd name %#v", known)
	}
	var entryProductID sql.NullString
	var receiptName sql.NullString
	if err := db.QueryRow(`
		SELECT se.product_id, r.product_name
		FROM scan_entries se
		JOIN device_scan_receipts r ON r.scan_entry_id = se.id AND r.household_id = se.household_id
		WHERE se.household_id = ? AND se.client_scan_id = 'prod-known'`, house).Scan(&entryProductID, &receiptName); err != nil {
		t.Fatal(err)
	}
	if !entryProductID.Valid || entryProductID.String != "prod-master" || !receiptName.Valid || receiptName.String != "Beans" {
		t.Fatalf("known product_id=%v name=%v", entryProductID, receiptName)
	}

	unk := ingest(t, ctx, svc, p, "prod-unk", "9999999999999", "stock_in")
	if unk.ProductName != nil {
		t.Fatalf("unknown lcd name %#v", unk)
	}
	if err := db.QueryRow(`
		SELECT se.product_id, r.product_name
		FROM scan_entries se
		JOIN device_scan_receipts r ON r.scan_entry_id = se.id AND r.household_id = se.household_id
		WHERE se.household_id = ? AND se.client_scan_id = 'prod-unk'`, house).Scan(&entryProductID, &receiptName); err != nil {
		t.Fatal(err)
	}
	if entryProductID.Valid || receiptName.Valid {
		t.Fatalf("unknown product_id=%v name=%v", entryProductID, receiptName)
	}
}

func TestMain(m *testing.M) {
	code := m.Run()
	devicingest.SetAfterDeviceStockCommitHook(nil)
	os.Exit(code)
}
