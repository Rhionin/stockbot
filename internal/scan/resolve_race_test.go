package scan_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/product"
	"github.com/Rhionin/stockbot/internal/scan"
)

func TestResolveFlaggedEntry_CancelledIsNotOpen(t *testing.T) {
	queue, db := newTestQueue(t)
	ctx := context.Background()
	id, productID := seedFlaggedResolve(t, queue, db, "scan-cancelled")
	if _, err := db.ExecContext(ctx, `UPDATE scan_entries SET status = ? WHERE id = ?`, string(scan.Cancelled), id); err != nil {
		t.Fatalf("cancel: %v", err)
	}

	err := queue.ResolveFlaggedEntry(ctx, id, productID)
	if !errors.Is(err, scan.ErrNotOpen) {
		t.Fatalf("ResolveFlaggedEntry: %v", err)
	}
	assertResolveUnchanged(t, db, id, scan.Cancelled)
}

func TestResolveFlaggedEntry_PendingIsNotFlagged(t *testing.T) {
	queue, db := newTestQueue(t)
	ctx := context.Background()
	id, productID := seedFlaggedResolve(t, queue, db, "scan-pending")
	if _, err := db.ExecContext(ctx, `UPDATE scan_entries SET status = ? WHERE id = ?`, string(scan.Pending), id); err != nil {
		t.Fatalf("pending: %v", err)
	}

	err := queue.ResolveFlaggedEntry(ctx, id, productID)
	if !errors.Is(err, scan.ErrNotFlagged) || errors.Is(err, scan.ErrNotOpen) {
		t.Fatalf("ResolveFlaggedEntry: %v", err)
	}
	assertResolveUnchanged(t, db, id, scan.Pending)
}

func TestResolveFlaggedEntry_MissingProduct(t *testing.T) {
	queue, db := newTestQueue(t)
	ctx := context.Background()
	id, _ := seedFlaggedResolve(t, queue, db, "scan-missing-product")

	err := queue.ResolveFlaggedEntry(ctx, id, "no-such-product")
	if !errors.Is(err, scan.ErrProductNotResolved) {
		t.Fatalf("ResolveFlaggedEntry: %v", err)
	}
	got, getErr := queue.GetScanEntry(ctx, id)
	if getErr != nil || got == nil || got.Status != scan.Flagged || got.ProductID != nil {
		t.Fatalf("row: %+v %v", got, getErr)
	}
}

// Cancel holds the only connection, so the resolve blocks until that cancel
// is visible. The resolve then returns ErrNotOpen and writes nothing.
func TestResolveFlaggedEntry_LosesToCancel(t *testing.T) {
	queue, db := newTestQueue(t)
	db.SetMaxOpenConns(1)
	ctx := context.Background()
	id, productID := seedFlaggedResolve(t, queue, db, "scan-race")

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback()

	errCh := make(chan error, 1)
	go func() {
		errCh <- queue.ResolveFlaggedEntry(ctx, id, productID)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for db.Stats().WaitCount == 0 {
		select {
		case err := <-errCh:
			t.Fatalf("resolve finished before the cancel: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("resolve did not wait for the cancel")
		}
		time.Sleep(time.Millisecond)
	}

	if _, err := tx.ExecContext(ctx, `UPDATE scan_entries SET status = ? WHERE id = ?`, string(scan.Cancelled), id); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	if err := <-errCh; !errors.Is(err, scan.ErrNotOpen) {
		t.Fatalf("ResolveFlaggedEntry: %v", err)
	}
	assertResolveUnchanged(t, db, id, scan.Cancelled)
	got, err := queue.GetScanEntry(ctx, id)
	if err != nil || got == nil || got.Status != scan.Cancelled {
		t.Fatalf("row came back: %+v %v", got, err)
	}
}

// The resolve completes, then cancel runs. The scan ends cancelled with the
// product still linked, which is the other legal ending of the race.
func TestResolveFlaggedEntry_ThenCancelStaysCancelled(t *testing.T) {
	queue, db := newTestQueue(t)
	ctx := context.Background()
	id, productID := seedFlaggedResolve(t, queue, db, "scan-then-cancel")

	if err := queue.ResolveFlaggedEntry(ctx, id, productID); err != nil {
		t.Fatalf("ResolveFlaggedEntry: %v", err)
	}
	cancelled := scan.Cancelled
	if err := queue.UpdateScanEntry(ctx, id, nil, nil, nil, nil, &cancelled); err != nil {
		t.Fatalf("cancel: %v", err)
	}

	got, err := queue.GetScanEntry(ctx, id)
	if err != nil || got == nil {
		t.Fatalf("GetScanEntry: %v", err)
	}
	if got.Status != scan.Cancelled || got.ProductID == nil || *got.ProductID != productID {
		t.Fatalf("row: %+v", got)
	}
	var overrides int
	if err := db.QueryRow(`SELECT COUNT(*) FROM barcodes WHERE barcode = ? AND source = 'user_override' AND product_id = ?`, id, productID).Scan(&overrides); err != nil {
		t.Fatalf("overrides: %v", err)
	}
	if overrides != 1 {
		t.Fatalf("override count %d", overrides)
	}
}

func seedFlaggedResolve(t *testing.T, queue *scan.Queue, db *sql.DB, id string) (string, string) {
	t.Helper()
	const productID = "prod-soup"
	catalog := product.NewCatalog(db)
	if err := catalog.CreateProduct(context.Background(), product.Product{
		ID: productID, Name: "Tarnwell Soup", Category: "Pantry", UnitOfMeasure: "can",
	}); err != nil {
		t.Fatalf("CreateProduct: %v", err)
	}
	direction := scan.StockIn
	created, err := queue.CreateScanEntry(context.Background(), scan.ScanEntry{
		ID:        id,
		UserID:    "user-1",
		Barcode:   id,
		ScannedAt: time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
		Direction: &direction,
		UnitCount: 1,
		Status:    scan.Flagged,
	})
	if err != nil {
		t.Fatalf("CreateScanEntry: %v", err)
	}
	return created.ID, productID
}

func assertResolveUnchanged(t *testing.T, db *sql.DB, id string, status scan.ScanStatus) {
	t.Helper()
	var gotStatus string
	var productID sql.NullString
	if err := db.QueryRow(`SELECT status, product_id FROM scan_entries WHERE id = ?`, id).Scan(&gotStatus, &productID); err != nil {
		t.Fatalf("scan row: %v", err)
	}
	if gotStatus != string(status) || productID.Valid {
		t.Fatalf("row changed: status %s product %v", gotStatus, productID)
	}
	var overrides int
	if err := db.QueryRow(`SELECT COUNT(*) FROM barcodes WHERE barcode = ? AND source = 'user_override'`, id).Scan(&overrides); err != nil {
		t.Fatalf("overrides: %v", err)
	}
	if overrides != 0 {
		t.Fatalf("override count %d", overrides)
	}
}
