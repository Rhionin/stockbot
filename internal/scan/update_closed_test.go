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

func TestUpdateScanEntry_ClosedScanRejectsEveryField(t *testing.T) {
	// A guard that runs only when status is non-nil accepts these writes.
	for _, status := range []scan.ScanStatus{scan.Committed, scan.Cancelled} {
		status := status
		t.Run(string(status), func(t *testing.T) {
			direction := scan.StockOut
			expires := time.Date(2030, 1, 2, 0, 0, 0, 0, time.UTC)
			other := "prod-other"
			nine := 9
			pending := scan.Pending
			cases := []struct {
				name      string
				direction *scan.ScanDirection
				unitCount *int
				expiresAt *time.Time
				productID *string
				status    *scan.ScanStatus
			}{
				{name: "unitCount", unitCount: &nine},
				{name: "direction", direction: &direction},
				{name: "expiresAt", expiresAt: &expires},
				{name: "productId", productID: &other},
				{name: "status", status: &pending},
			}
			for _, tc := range cases {
				tc := tc
				t.Run(tc.name, func(t *testing.T) {
					queue, db := newTestQueue(t)
					ctx := context.Background()
					seedTwoProducts(t, db)
					id := insertOpenScanRow(t, queue, "scan-closed")
					if _, err := db.ExecContext(ctx, `UPDATE scan_entries SET status = ? WHERE id = ?`, string(status), id); err != nil {
						t.Fatalf("close scan: %v", err)
					}

					err := queue.UpdateScanEntry(ctx, id, tc.direction, tc.unitCount, tc.expiresAt, tc.productID, tc.status)
					if !errors.Is(err, scan.ErrNotOpen) {
						t.Fatalf("UpdateScanEntry: %v", err)
					}
					got, err := queue.GetScanEntry(ctx, id)
					if err != nil || got == nil {
						t.Fatalf("GetScanEntry: %v", err)
					}
					if got.Status != status || got.UnitCount != 2 || got.Direction == nil || *got.Direction != scan.StockIn || got.ProductID == nil || *got.ProductID != "prod-milk" || got.ExpiresAt != nil {
						t.Fatalf("row changed: %+v", got)
					}
				})
			}
		})
	}
}

func TestUpdateScanEntry_MissingID(t *testing.T) {
	queue, _ := newTestQueue(t)
	nine := 9
	err := queue.UpdateScanEntry(context.Background(), "missing-scan", nil, &nine, nil, nil, nil)
	if !errors.Is(err, scan.ErrNotFound) {
		t.Fatalf("UpdateScanEntry: %v", err)
	}
}

func TestUpdateScanEntry_ReadErrorIsNotClassified(t *testing.T) {
	queue, db := newTestQueue(t)
	if _, err := db.Exec(`DROP TABLE products`); err != nil {
		t.Fatalf("drop products: %v", err)
	}
	nine := 9
	err := queue.UpdateScanEntry(context.Background(), "missing-scan", nil, &nine, nil, nil, nil)
	if err == nil || errors.Is(err, scan.ErrNotOpen) || errors.Is(err, scan.ErrNotFound) {
		t.Fatalf("want the follow-up read error, got %v", err)
	}
}

func TestUpdateScanEntry_SameUnitCountOnPendingStillApplies(t *testing.T) {
	queue, db := newTestQueue(t)
	ctx := context.Background()
	seedTwoProducts(t, db)
	id := insertOpenScanRow(t, queue, "scan-pending")
	same := 2
	if err := queue.UpdateScanEntry(ctx, id, nil, &same, nil, nil, nil); err != nil {
		t.Fatalf("UpdateScanEntry: %v", err)
	}
	got, err := queue.GetScanEntry(ctx, id)
	if err != nil || got == nil || got.UnitCount != 2 || got.Status != scan.Pending {
		t.Fatalf("pending row: %+v %v", got, err)
	}
}

// The unit-count patch blocks on the connection the commit is holding, then
// runs only after that commit is visible. The scan count and the move stay
// equal either way: the patch does not land on the committed row.
func TestUpdateScanEntry_UnitCountLosesToCommit(t *testing.T) {
	queue, db := newTestQueue(t)
	db.SetMaxOpenConns(1)
	ctx := context.Background()
	seedTwoProducts(t, db)
	id := insertOpenScanRow(t, queue, "scan-race")

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback()

	errCh := make(chan error, 1)
	go func() {
		nine := 9
		errCh <- queue.UpdateScanEntry(ctx, id, nil, &nine, nil, nil, nil)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for db.Stats().WaitCount == 0 {
		select {
		case err := <-errCh:
			t.Fatalf("unit count update finished before the commit: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("unit count update did not wait for the commit")
		}
		time.Sleep(time.Millisecond)
	}

	when := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	if _, err := tx.ExecContext(ctx, `UPDATE scan_entries SET status = ?, committed_at = ? WHERE id = ?`, string(scan.Committed), when, id); err != nil {
		t.Fatalf("commit scan: %v", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO stock_moves (id, item_id, product_id, direction, quantity, at, source, scan_entry_id, tracked)
		VALUES ('move-race', 'item-race', 'prod-milk', 'in', 2, ?, 'scan', ?, 0)`, when, id); err != nil {
		t.Fatalf("record move: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit tx: %v", err)
	}

	if err := <-errCh; !errors.Is(err, scan.ErrNotOpen) {
		t.Fatalf("UpdateScanEntry: %v", err)
	}
	got, err := queue.GetScanEntry(ctx, id)
	if err != nil || got == nil {
		t.Fatalf("GetScanEntry: %v", err)
	}
	var qty int
	if err := db.QueryRowContext(ctx, `SELECT quantity FROM stock_moves WHERE scan_entry_id = ?`, id).Scan(&qty); err != nil {
		t.Fatalf("move: %v", err)
	}
	if got.Status != scan.Committed || got.UnitCount != 2 || qty != got.UnitCount {
		t.Fatalf("mismatch: status %s unitCount %d move %d", got.Status, got.UnitCount, qty)
	}
}

func seedTwoProducts(t *testing.T, db *sql.DB) {
	t.Helper()
	catalog := product.NewCatalog(db)
	for _, item := range []product.Product{
		{ID: "prod-milk", Name: "Whole Milk", Category: "Dairy", UnitOfMeasure: "gallon"},
		{ID: "prod-other", Name: "Other Milk", Category: "Dairy", UnitOfMeasure: "gallon"},
	} {
		if err := catalog.CreateProduct(context.Background(), item); err != nil {
			t.Fatalf("CreateProduct %s: %v", item.ID, err)
		}
	}
}

func insertOpenScanRow(t *testing.T, queue *scan.Queue, id string) string {
	t.Helper()
	direction := scan.StockIn
	productID := "prod-milk"
	created, err := queue.CreateScanEntry(context.Background(), scan.ScanEntry{
		ID:        id,
		UserID:    "user-1",
		Barcode:   id,
		ScannedAt: time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
		Direction: &direction,
		UnitCount: 2,
		ProductID: &productID,
		Status:    scan.Pending,
	})
	if err != nil {
		t.Fatalf("CreateScanEntry: %v", err)
	}
	return created.ID
}
