package scan_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/events"
	"github.com/Rhionin/stockbot/internal/inventory"
	"github.com/Rhionin/stockbot/internal/product"
	"github.com/Rhionin/stockbot/internal/scan"
	"github.com/Rhionin/stockbot/internal/tenant"
)

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
	b := events.NewBroadcaster()
	queue.Broadcaster = b
	msgs, unsub := b.SubscribeHousehold(scope.HouseholdID(), "", "")
	defer unsub()
	catalog := product.NewCatalog(db)
	if err := catalog.CreateProduct(ctx, product.Product{ID: "prod-scan-only", Name: "ScanOnly", Category: "Pantry", UnitOfMeasure: "each"}); err != nil {
		t.Fatal(err)
	}
	productID := "prod-scan-only"
	dir := scan.StockIn
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	entry, err := queue.CreateScanEntryTx(ctx, scope, tx, scan.ScanEntry{
		UserID: "device-1", Barcode: "4000000000303", ScannedAt: time.Now().UTC(), Direction: &dir, UnitCount: 1, ProductID: &productID,
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
	msg := <-msgs
	if msg.EventType != "scan" {
		t.Fatalf("event %q", msg.EventType)
	}
	for {
		select {
		case extra := <-msgs:
			if extra.EventType == "inventory" {
				t.Fatalf("unexpected inventory event %#v", extra)
			}
		case <-time.After(50 * time.Millisecond):
			return
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
