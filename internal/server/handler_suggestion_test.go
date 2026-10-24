package server

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/product"
	"github.com/Rhionin/stockbot/internal/scan"
)

// createItemViaStockIn creates a product and inserts an item row with the given
// deterministic itemID, then seeds one committed stock-in so the item exists in inventory.
func createItemViaStockIn(t *testing.T, db *sql.DB, catalog *product.Catalog, userID, productID, productName, itemID string) {
	t.Helper()

	if err := catalog.CreateProduct(context.Background(), product.Product{
		ID:            productID,
		Name:          productName,
		Category:      "Test",
		UnitOfMeasure: "unit",
	}); err != nil {
		t.Fatalf("CreateProduct: %v", err)
	}

	if _, err := db.ExecContext(context.Background(),
		`INSERT INTO items (id, user_id, product_id) VALUES (?, ?, ?)`,
		itemID, userID, productID,
	); err != nil {
		t.Fatalf("insert item: %v", err)
	}

	scanQueue := scan.NewQueue(db)
	direction := scan.StockIn
	entry := scan.ScanEntry{
		ID:        "seed-scan-" + productID,
		UserID:    userID,
		Barcode:   "000000000000",
		ScannedAt: time.Now(),
		Direction: &direction,
		UnitCount: 1,
		ProductID: &productID,
		Status:    scan.Pending,
	}
	scope := primaryScope(t, db)
	if _, err := scanQueue.CreateScanEntry(context.Background(), scope, entry); err != nil {
		t.Fatalf("CreateScanEntry: %v", err)
	}
	if err := scanQueue.CommitStockIn(context.Background(), scope, &entry); err != nil {
		t.Fatalf("CommitStockIn: %v", err)
	}
}

// insertConsumptionEvent inserts a single consumption event for test setup.
func insertConsumptionEvent(t *testing.T, db *sql.DB, id, itemID string, consumedAt time.Time) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(),
		`INSERT INTO consumption_events (id, item_id, consumed_at) VALUES (?, ?, ?)`,
		id, itemID, consumedAt,
	); err != nil {
		t.Fatalf("insertConsumptionEvent: %v", err)
	}
}
