package scan_test

import (
	"context"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/product"
)

func TestResolveFlaggedEntry_UserOverrideUsesTrimmedBarcodeForPaddedScanRow(t *testing.T) {
	queue, db, scope := newTestQueue(t)
	ctx := context.Background()
	catalog := product.NewCatalog(db)
	const upc = "036000291452"
	padded := " " + upc + "\t\r\n"
	productID := "prod-resolve-trim"
	if err := catalog.CreateProduct(ctx, product.Product{ID: productID, Name: "Resolve Trim", Category: "Test", UnitOfMeasure: "each"}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if _, err := db.ExecContext(ctx, `
		INSERT INTO scan_entries (id, user_id, household_id, barcode, scanned_at, direction, unit_count, status)
		VALUES ('flagged-padded', 'user-1', ?, ?, ?, 'stock_in', 1, 'flagged')`,
		scope.HouseholdID(), padded, now); err != nil {
		t.Fatal(err)
	}
	if err := queue.ResolveFlaggedEntry(ctx, scope, "flagged-padded", productID); err != nil {
		t.Fatal(err)
	}
	var stored string
	if err := db.QueryRowContext(ctx,
		`SELECT barcode FROM barcodes WHERE barcode = ? AND source = 'user_override' AND household_id = ?`,
		upc, scope.HouseholdID()).Scan(&stored); err != nil {
		t.Fatalf("trimmed override row: %v", err)
	}
	if stored != upc {
		t.Fatalf("override barcode = %q, want trimmed %q", stored, upc)
	}
}

func TestLinkStockOutProduct_UserOverrideUsesTrimmedBarcodeForPaddedScanRow(t *testing.T) {
	queue, db, scope := newTestQueue(t)
	ctx := context.Background()
	catalog := product.NewCatalog(db)
	const upc = "088800000000"
	padded := "\t" + upc + "\r\n"
	productID := "prod-link-trim"
	if err := catalog.CreateProduct(ctx, product.Product{ID: productID, Name: "Link Trim", Category: "Test", UnitOfMeasure: "each"}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if _, err := db.ExecContext(ctx, `
		INSERT INTO scan_entries (id, user_id, household_id, barcode, scanned_at, direction, unit_count, status)
		VALUES ('stockout-padded', 'user-1', ?, ?, ?, 'stock_out', 1, 'pending')`,
		scope.HouseholdID(), padded, now); err != nil {
		t.Fatal(err)
	}
	if _, err := queue.LinkStockOutProduct(ctx, scope, "stockout-padded", productID); err != nil {
		t.Fatal(err)
	}
	var stored string
	if err := db.QueryRowContext(ctx,
		`SELECT barcode FROM barcodes WHERE barcode = ? AND source = 'user_override' AND household_id = ?`,
		upc, scope.HouseholdID()).Scan(&stored); err != nil {
		t.Fatalf("trimmed override row: %v", err)
	}
	if stored != upc {
		t.Fatalf("override barcode = %q, want trimmed %q", stored, upc)
	}
}
