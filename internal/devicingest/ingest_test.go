package devicingest_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/Rhionin/stockbot/internal/devicingest"
	"github.com/Rhionin/stockbot/internal/product"
	"github.com/Rhionin/stockbot/internal/scan"
	"github.com/Rhionin/stockbot/internal/tenant"
)

func bootIngest(t *testing.T) (context.Context, *auth.Service, auth.Principal, string, *product.Catalog, *sql.DB) {
	t.Helper()
	db, err := app.OpenSQLite(":memory:")
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

func seedProduct(t *testing.T, ctx context.Context, catalog *product.Catalog, house, id, name, barcode string) {
	t.Helper()
	scope, err := tenant.New(house)
	if err != nil {
		t.Fatal(err)
	}
	if err := catalog.CreateProduct(ctx, product.Product{ID: id, Name: name, Category: "Pantry", UnitOfMeasure: "each"}); err != nil {
		t.Fatal(err)
	}
	if err := catalog.UpsertBarcodeMapping(ctx, barcode, id, "global", ""); err != nil {
		t.Fatal(err)
	}
	_ = scope
}

func ingest(t *testing.T, ctx context.Context, svc *auth.Service, p auth.Principal, id, barcode, direction string) auth.ScanResult {
	t.Helper()
	results, err := svc.IngestScans(ctx, p, []auth.ScanInput{{
		ID: id, Barcode: barcode, Direction: direction, UnitCount: 1, HasBarcode: true,
	}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("results %v", results)
	}
	return results[0]
}

func TestIngestRejectsNon1DBarcodes(t *testing.T) {
	ctx, svc, p, _, _, db := bootIngest(t)
	for _, tc := range []struct {
		id, barcode string
	}{
		{"qr", "https://example.com/x"},
		{"json", `{"upc":"1"}`},
		{"pair", "SBP-ABCDEFGHJKLMNPQR-TUVWXY"},
		{"alpha", "ABC123"},
		{"empty", ""},
		{"long", "123456789012345678901234567890123456789012345678901234567890"},
	} {
		r := ingest(t, ctx, svc, p, tc.id, tc.barcode, "stock_out")
		if r.Status != "rejected" || r.Code != auth.CodeValidation {
			t.Fatalf("%s = %#v", tc.id, r)
		}
	}
	var qr int
	if err := db.QueryRow(`SELECT COUNT(*) FROM scan_entries WHERE status = 'qr'`).Scan(&qr); err != nil || qr != 0 {
		t.Fatalf("qr rows %d", qr)
	}
}

func TestIngestQueueKnownAndUnknown(t *testing.T) {
	ctx, svc, p, house, catalog, db := bootIngest(t)
	seedProduct(t, ctx, catalog, house, "prod-1", "Beans", "4000000000011")
	unk := ingest(t, ctx, svc, p, "u1", "9999999999999", "stock_in")
	if unk.ScanStatus != "flagged" || unk.Known == nil || *unk.Known {
		t.Fatalf("unk %#v", unk)
	}
	known := ingest(t, ctx, svc, p, "k1", "4000000000011", "stock_in")
	if known.ScanStatus != "queued" || known.Known == nil || !*known.Known || known.QueuedCount == nil || *known.QueuedCount != 1 {
		t.Fatalf("known %#v", known)
	}
	second := ingest(t, ctx, svc, p, "k2", "4000000000011", "stock_in")
	if second.QueuedCount == nil || *second.QueuedCount != 2 {
		t.Fatalf("merge %#v", second)
	}
	var cancelled int
	if err := db.QueryRow(`SELECT COUNT(*) FROM scan_entries WHERE status = 'cancelled'`).Scan(&cancelled); err != nil || cancelled != 0 {
		t.Fatalf("cancelled rows %d", cancelled)
	}
	var receipts int
	if err := db.QueryRow(`SELECT COUNT(*) FROM device_scan_receipts WHERE household_id = ?`, house).Scan(&receipts); err != nil || receipts != 3 {
		t.Fatalf("receipts %d", receipts)
	}
}

func TestIngestImmediateStockMoves(t *testing.T) {
	ctx, svc, p, house, catalog, _ := bootIngest(t)
	seedProduct(t, ctx, catalog, house, "prod-2", "Rice", "4000000000022")
	user, _ := svc.Authenticate(ctx, "pat@example.test", "horse battery")
	patch := auth.UpdateDevicePatch{CommitMode: strPtr("immediate")}
	if _, err := svc.UpdateDevice(ctx, user.ID, house, p.DeviceID, patch); err != nil {
		t.Fatal(err)
	}
	in := ingest(t, ctx, svc, p, "in-1", "4000000000022", "stock_in")
	if in.ScanStatus != "committed" || in.Delta == nil || *in.Delta != 1 || in.OnHand == nil || *in.OnHand != 1 {
		t.Fatalf("stock in %#v", in)
	}
	out := ingest(t, ctx, svc, p, "out-1", "4000000000022", "stock_out")
	if out.ScanStatus != "committed" || out.Delta == nil || *out.Delta != -1 || out.OnHand == nil || *out.OnHand != 0 {
		t.Fatalf("stock out %#v", out)
	}
	backfill := ingest(t, ctx, svc, p, "out-2", "4000000000022", "stock_out")
	if backfill.Backfilled == nil || !*backfill.Backfilled || backfill.Delta == nil || *backfill.Delta != -1 {
		t.Fatalf("backfill %#v", backfill)
	}
}

func TestIngestIdempotentReplayAndConflict(t *testing.T) {
	ctx, svc, p, _, catalog, _ := bootIngest(t)
	house := p.HouseholdID
	seedProduct(t, ctx, catalog, house, "prod-3", "Oats", "4000000000033")
	first := ingest(t, ctx, svc, p, "dup-1", "4000000000033", "stock_in")
	dup := ingest(t, ctx, svc, p, "dup-1", "4000000000033", "stock_in")
	if dup.Status != "duplicate" || dup.ScanStatus != first.ScanStatus || dup.QueuedCount == nil || *dup.QueuedCount != *first.QueuedCount {
		t.Fatalf("dup %#v first %#v", dup, first)
	}
	conflict := ingest(t, ctx, svc, p, "dup-1", "4000000000033", "stock_out")
	if conflict.Status != "conflict" {
		t.Fatalf("conflict %#v", conflict)
	}
}

func TestIngestImmediateUnknownFlagged(t *testing.T) {
	ctx, svc, p, house, _, db := bootIngest(t)
	user, _ := svc.Authenticate(ctx, "pat@example.test", "horse battery")
	patch := auth.UpdateDevicePatch{CommitMode: strPtr("immediate")}
	if _, err := svc.UpdateDevice(ctx, user.ID, house, p.DeviceID, patch); err != nil {
		t.Fatal(err)
	}
	flagged := ingest(t, ctx, svc, p, "imm-u1", "8888888888888", "stock_in")
	if flagged.ScanStatus != "flagged" || flagged.Known == nil || *flagged.Known {
		t.Fatalf("flagged %#v", flagged)
	}
	var pending int
	if err := db.QueryRow(`SELECT COUNT(*) FROM scan_entries WHERE household_id = ? AND status = 'flagged'`, house).Scan(&pending); err != nil || pending != 1 {
		t.Fatalf("flagged rows %d", pending)
	}
}

func TestIngestImmediateInvalidDirection(t *testing.T) {
	ctx, svc, p, house, catalog, _ := bootIngest(t)
	seedProduct(t, ctx, catalog, house, "prod-4", "Salt", "4000000000044")
	user, _ := svc.Authenticate(ctx, "pat@example.test", "horse battery")
	patch := auth.UpdateDevicePatch{CommitMode: strPtr("immediate")}
	if _, err := svc.UpdateDevice(ctx, user.ID, house, p.DeviceID, patch); err != nil {
		t.Fatal(err)
	}
	results, err := svc.IngestScans(ctx, p, []auth.ScanInput{{
		ID: "bad-dir", Barcode: "4000000000044", Direction: "sideways", UnitCount: 1, HasBarcode: true,
	}}, 0)
	if err != nil || len(results) != 1 || results[0].Status != "rejected" {
		t.Fatalf("results %#v err %v", results, err)
	}
}

func TestIngestImmediateStockOutMultipleUnits(t *testing.T) {
	ctx, svc, p, house, catalog, _ := bootIngest(t)
	seedProduct(t, ctx, catalog, house, "prod-multi", "Bulk", "4000000000155")
	user, _ := svc.Authenticate(ctx, "pat@example.test", "horse battery")
	if _, err := svc.UpdateDevice(ctx, user.ID, house, p.DeviceID, auth.UpdateDevicePatch{CommitMode: strPtr("immediate")}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.IngestScans(ctx, p, []auth.ScanInput{{
		ID: "in-a", Barcode: "4000000000155", Direction: "stock_in", UnitCount: 2, HasBarcode: true,
	}}, 0); err != nil {
		t.Fatal(err)
	}
	out, err := svc.IngestScans(ctx, p, []auth.ScanInput{{
		ID: "out-a", Barcode: "4000000000155", Direction: "stock_out", UnitCount: 2, HasBarcode: true,
	}}, 0)
	if err != nil || len(out) != 1 || out[0].Delta == nil || *out[0].Delta != -2 || out[0].OnHand == nil || *out[0].OnHand != 0 {
		t.Fatalf("out %#v err %v", out, err)
	}
}

func TestIngestRequiresTxRejectsNonTransaction(t *testing.T) {
	if err := devicingest.IngestRequiresTx(nil); err == nil {
		t.Fatal("expected error")
	}
}

func TestAttachIgnoresNil(t *testing.T) {
	devicingest.Attach(nil, nil, nil)
	svc := auth.NewService(nil, t.TempDir())
	devicingest.Attach(svc, nil, nil)
}

func strPtr(s string) *string { return &s }

type stubFanOut struct {
	lookup func(context.Context, string) product.FanOutResult
}

func (s stubFanOut) Lookup(ctx context.Context, barcode string) product.FanOutResult {
	if s.lookup != nil {
		return s.lookup(ctx, barcode)
	}
	return product.FanOutResult{Outcome: product.FanOutConfirmedMiss}
}
