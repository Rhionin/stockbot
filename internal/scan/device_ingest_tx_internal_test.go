package scan

import (
	"context"
	"testing"
	"time"
)

func TestRecordMergedScanTxMissingEntry(t *testing.T) {
	queue, db, scope := newTestQueueInternal(t)
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := queue.recordMergedScanTx(ctx, scope, tx, "missing-entry", 2, time.Now().UTC()); err == nil {
		t.Fatal("expected error")
	}
}

func TestGetScanEntryTxMissingReturnsNil(t *testing.T) {
	queue, db, scope := newTestQueueInternal(t)
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	entry, err := queue.getScanEntryTx(ctx, scope, tx, "missing-entry")
	if err != nil || entry != nil {
		t.Fatalf("entry=%v err=%v", entry, err)
	}
}

func TestCreateScanEntryTxMergeDefaultsZeroUnitCount(t *testing.T) {
	queue, db, scope := newTestQueueInternal(t)
	ctx := context.Background()
	dir := StockIn
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	first, err := queue.CreateScanEntryTx(ctx, scope, tx, ScanEntry{
		UserID: scope.HouseholdID(), Barcode: "4000000000555", ScannedAt: time.Now().UTC(), Direction: &dir, UnitCount: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := queue.CreateScanEntryTx(ctx, scope, tx, ScanEntry{
		UserID: scope.HouseholdID(), Barcode: "4000000000555", ScannedAt: time.Now().UTC(), Direction: &dir, UnitCount: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.UnitCount != 2 || first.ID != second.ID {
		t.Fatalf("first=%v second=%v", first, second)
	}
}
