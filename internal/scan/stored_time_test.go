package scan_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/product"
	"github.com/Rhionin/stockbot/internal/scan"
)

func TestScanWritesStoreCleanUTC(t *testing.T) {
	queue, db := newTestQueue(t)
	ctx := context.Background()
	created, err := queue.CreateScanEntry(ctx, scan.ScanEntry{
		UserID:  "user-1",
		Barcode: "000111222333",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := queue.CommitScanEntry(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	var scanned, committed string
	if err := db.QueryRow(`
		SELECT CAST(scanned_at AS TEXT), CAST(committed_at AS TEXT)
		FROM scan_entries WHERE id = ?`, created.ID).Scan(&scanned, &committed); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{scanned, committed} {
		if !strings.Contains(raw, "+0000 UTC") || strings.Contains(raw, "m=") {
			t.Fatalf("stored %q", raw)
		}
	}
}

func TestCommitStockInStoresTheInjectedClock(t *testing.T) {
	queue, db := newTestQueue(t)
	ctx := context.Background()
	stamped := time.Date(2020, 1, 2, 3, 4, 5, 6, time.UTC)
	queue.Now = func() time.Time { return stamped }

	const productID = "prod-clock"
	if err := product.NewCatalog(db).CreateProduct(ctx, product.Product{
		ID: productID, Name: "Clock oats", Category: "Pantry",
	}); err != nil {
		t.Fatal(err)
	}
	direction := scan.StockIn
	pid := productID
	created, err := queue.CreateScanEntry(ctx, scan.ScanEntry{
		UserID:    "user-1",
		Barcode:   "clock",
		ScannedAt: time.Date(2019, 6, 1, 0, 0, 0, 0, time.UTC),
		Direction: &direction,
		UnitCount: 1,
		ProductID: &pid,
		Status:    scan.Pending,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := queue.CommitStockIn(ctx, created); err != nil {
		t.Fatal(err)
	}
	var stored string
	if err := db.QueryRow(`SELECT CAST(committed_at AS TEXT) FROM scan_entries WHERE id = ?`, created.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	const want = "2020-01-02 03:04:05.000000006 +0000 UTC"
	if stored != want {
		t.Fatalf("committed_at = %q, want %q", stored, want)
	}
}

func TestSuffixedScanTimesReadBackInOrder(t *testing.T) {
	queue, db := newTestQueue(t)
	const suffix = "2026-10-17 18:00:00.5 +0000 UTC m=+9"
	const later = "2026-10-17 18:00:01 +0000 UTC"
	want := time.Date(2026, 10, 17, 18, 0, 0, 500000000, time.UTC)
	if _, err := db.Exec(`
		INSERT INTO scan_entries (id, user_id, barcode, scanned_at, unit_count, status, committed_at) VALUES
			('later', 'user-1', '111', ?, 1, 'pending', NULL),
			('suffix', 'user-1', '222', ?, 1, 'committed', ?)`,
		later, suffix, suffix); err != nil {
		t.Fatal(err)
	}
	got, err := queue.GetScanEntry(context.Background(), "suffix")
	if err != nil {
		t.Fatal(err)
	}
	if !got.ScannedAt.Equal(want) || got.CommittedAt == nil || !got.CommittedAt.Equal(want) {
		t.Fatalf("scanned %s committed %v", got.ScannedAt, got.CommittedAt)
	}
	listed, err := queue.ListScanEntries(context.Background(), "user-1", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 2 || listed[0].ID != "suffix" || listed[1].ID != "later" {
		t.Fatalf("order = %s, %s", listed[0].ID, listed[1].ID)
	}
}
