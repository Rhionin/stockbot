package scan_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/changes"
	"github.com/Rhionin/stockbot/internal/events"
	"github.com/Rhionin/stockbot/internal/inventory"
	"github.com/Rhionin/stockbot/internal/product"
	"github.com/Rhionin/stockbot/internal/scan"
	"github.com/Rhionin/stockbot/internal/tenant"
)

func seedQueueItemWithStock(t *testing.T, ctx context.Context, db *sql.DB, scope tenant.Scope, catalog *product.Catalog, productID, itemID, barcode string, onHand int) {
	t.Helper()
	if err := catalog.CreateProduct(ctx, product.Product{ID: productID, Name: "Shelf", Category: "Pantry", UnitOfMeasure: "each"}); err != nil {
		t.Fatal(err)
	}
	if err := catalog.UpsertBarcodeMapping(ctx, barcode, productID, "global", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO items (id, user_id, product_id, household_id, shelved) VALUES (?, ?, ?, ?, 0)`,
		itemID, scope.HouseholdID(), productID, scope.HouseholdID()); err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC()
	for i := 0; i < onHand; i++ {
		if _, err := db.ExecContext(ctx, `INSERT INTO item_instances (id, item_id, household_id, stock_in_at) VALUES (?, ?, ?, ?)`,
			fmt.Sprintf("%s-inst-%d", itemID, i), itemID, scope.HouseholdID(), at); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCreateScanEntryTxMergesPendingCard(t *testing.T) {
	queue, db, scope := newTestQueue(t)
	ctx := context.Background()
	catalog := product.NewCatalog(db)
	if err := catalog.CreateProduct(ctx, product.Product{ID: "prod-merge", Name: "Merge", Category: "Pantry", UnitOfMeasure: "each"}); err != nil {
		t.Fatal(err)
	}
	productID := "prod-merge"
	dir := scan.StockIn
	at := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	first, err := queue.CreateScanEntryTx(ctx, scope, tx, scan.ScanEntry{
		UserID: "device-1", Barcode: "4000000000101", ScannedAt: at, Direction: &dir, UnitCount: 1, ProductID: &productID,
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := queue.CreateScanEntryTx(ctx, scope, tx, scan.ScanEntry{
		UserID: "device-1", Barcode: "4000000000101", ScannedAt: at.Add(time.Minute), Direction: &dir, UnitCount: 2, ProductID: &productID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID || second.UnitCount != 3 {
		t.Fatalf("merge ids %q/%q units %d", first.ID, second.ID, second.UnitCount)
	}
}

func TestInsertScanEntryTxInsertsWithoutMerging(t *testing.T) {
	queue, db, scope := newTestQueue(t)
	ctx := context.Background()
	catalog := product.NewCatalog(db)
	if err := catalog.CreateProduct(ctx, product.Product{ID: "prod-no-merge", Name: "No Merge", Category: "Pantry", UnitOfMeasure: "each"}); err != nil {
		t.Fatal(err)
	}
	productID := "prod-no-merge"
	dir := scan.StockIn
	at := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	first, err := queue.CreateScanEntryTx(ctx, scope, tx, scan.ScanEntry{
		UserID: "device-1", Barcode: "4000000000606", ScannedAt: at, Direction: &dir, UnitCount: 3, ProductID: &productID,
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := queue.InsertScanEntryTx(ctx, scope, tx, scan.ScanEntry{
		UserID: "device-1", Barcode: "4000000000606", ScannedAt: at.Add(time.Minute), Direction: &dir, UnitCount: 1, ProductID: &productID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if first.ID == second.ID || second.UnitCount != 1 || first.UnitCount != 3 {
		t.Fatalf("first=%+v second=%+v", first, second)
	}
}

func TestPublishAfterIngestPublishesScanAndInventory(t *testing.T) {
	queue, db, scope := newTestQueue(t)
	ctx := context.Background()
	catalog := product.NewCatalog(db)
	pantry := inventory.NewPantry(db)
	queue.Pantry = pantry
	b := events.NewBroadcaster()
	queue.Broadcaster = b
	msgs, unsub := b.SubscribeHousehold(scope.HouseholdID(), "", "")
	defer unsub()

	if err := catalog.CreateProduct(ctx, product.Product{ID: "prod-live", Name: "Live", Category: "Pantry", UnitOfMeasure: "each"}); err != nil {
		t.Fatal(err)
	}
	if err := catalog.UpsertBarcodeMapping(ctx, "4000000000202", "prod-live", "global", ""); err != nil {
		t.Fatal(err)
	}
	productID := "prod-live"
	dir := scan.StockIn
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	entry, err := queue.CreateScanEntryTx(ctx, scope, tx, scan.ScanEntry{
		UserID: "device-1", Barcode: "4000000000202", ScannedAt: time.Now().UTC(), Direction: &dir, UnitCount: 1, ProductID: &productID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := queue.CommitStockInTx(ctx, scope, tx, entry); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := queue.PublishAfterIngest(ctx, scope.HouseholdID(), entry.ID, true); err != nil {
		t.Fatal(err)
	}
	var scanSeen, invSeen bool
	deadline := time.After(2 * time.Second)
	for !scanSeen || !invSeen {
		select {
		case msg := <-msgs:
			switch msg.EventType {
			case "scan":
				var got scan.ScanEntry
				if err := json.Unmarshal(msg.Data, &got); err != nil {
					t.Fatal(err)
				}
				if got.ID == entry.ID {
					scanSeen = true
				}
			case "inventory":
				invSeen = true
			}
		case <-deadline:
			t.Fatalf("scanSeen=%v invSeen=%v", scanSeen, invSeen)
		}
	}
}

func TestPublishAfterIngestSkipsInventoryWithoutStockMove(t *testing.T) {
	queue, db, scope := newTestQueue(t)
	ctx := context.Background()
	catalog := product.NewCatalog(db)
	pantry := inventory.NewPantry(db)
	queue.Pantry = pantry
	b := events.NewBroadcaster()
	queue.Broadcaster = b
	msgs, unsub := b.SubscribeHousehold(scope.HouseholdID(), "", "")
	defer unsub()

	const productID = "prod-inv-guard"
	const itemID = "item-inv-guard"
	const barcode = "4000000000303"
	seedQueueItemWithStock(t, ctx, db, scope, catalog, productID, itemID, barcode, 1)

	productIDRef := productID
	dir := scan.StockIn
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	entry, err := queue.CreateScanEntryTx(ctx, scope, tx, scan.ScanEntry{
		UserID: scope.HouseholdID(), Barcode: barcode, ScannedAt: time.Now().UTC(), Direction: &dir, UnitCount: 1, ProductID: &productIDRef,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	if err := queue.PublishAfterIngest(ctx, scope.HouseholdID(), entry.ID, false); err != nil {
		t.Fatal(err)
	}
	var scanSeen bool
	deadline := time.After(2 * time.Second)
	for !scanSeen {
		select {
		case msg := <-msgs:
			if msg.EventType == "scan" {
				scanSeen = true
			} else if msg.EventType == "inventory" {
				t.Fatalf("unexpected inventory with stockMoved=false")
			}
		case <-deadline:
			t.Fatal("timed out waiting for scan event")
		}
	}
	for {
		select {
		case extra := <-msgs:
			if extra.EventType == "inventory" {
				t.Fatalf("unexpected inventory event %#v", extra)
			}
		case <-time.After(50 * time.Millisecond):
			goto withStock
		}
	}
withStock:
	if err := queue.PublishAfterIngest(ctx, scope.HouseholdID(), entry.ID, true); err != nil {
		t.Fatal(err)
	}
	var invSeen bool
	deadline = time.After(2 * time.Second)
	for !invSeen {
		select {
		case msg := <-msgs:
			if msg.EventType == "inventory" {
				invSeen = true
			}
		case <-deadline:
			t.Fatalf("stockMoved=true: inventory event missing")
		}
	}
}

func TestPublishAfterIngestNilQueueIsNoOp(t *testing.T) {
	var queue *scan.Queue
	if err := queue.PublishAfterIngest(context.Background(), "house", "scan-1", true); err != nil {
		t.Fatal(err)
	}
}

func TestCreateScanEntryTxRequiresScope(t *testing.T) {
	queue, db, scope := newTestQueue(t)
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	badScope, err := tenant.New("")
	if err == nil {
		_, err = queue.CreateScanEntryTx(ctx, badScope, tx, scan.ScanEntry{UserID: "u", Barcode: "4000000000404", UnitCount: 1})
	}
	if err == nil {
		t.Fatal("expected scope error")
	}
	_ = scope
}

func TestCreateScanEntryTxMergeBumpsScansCursor(t *testing.T) {
	queue, db, scope := newTestQueue(t)
	ctx := context.Background()
	catalog := product.NewCatalog(db)
	if err := catalog.CreateProduct(ctx, product.Product{ID: "prod-bump", Name: "Bump", Category: "Pantry", UnitOfMeasure: "each"}); err != nil {
		t.Fatal(err)
	}
	productID := "prod-bump"
	dir := scan.StockIn
	at := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := queue.CreateScanEntryTx(ctx, scope, tx, scan.ScanEntry{
		UserID: "device-1", Barcode: "4000000000505", ScannedAt: at, Direction: &dir, UnitCount: 1, ProductID: &productID,
	}); err != nil {
		t.Fatal(err)
	}
	beforeMerge, err := changes.Current(ctx, tx, scope.HouseholdID(), changes.ScopeScans)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := queue.CreateScanEntryTx(ctx, scope, tx, scan.ScanEntry{
		UserID: "device-1", Barcode: "4000000000505", ScannedAt: at.Add(time.Minute), Direction: &dir, UnitCount: 1, ProductID: &productID,
	}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	out, err := changes.Compare(ctx, db, scope.HouseholdID(), changes.ScopeScans, beforeMerge)
	if err != nil {
		t.Fatal(err)
	}
	if !out.Changed || out.Cursor == beforeMerge {
		t.Fatalf("merge path did not bump scans cursor: before merge %q after %+v", beforeMerge, out)
	}
}
