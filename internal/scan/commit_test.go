package scan_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/inventory"
	"github.com/Rhionin/stockbot/internal/product"
	"github.com/Rhionin/stockbot/internal/scan"
	"github.com/Rhionin/stockbot/internal/tenant"
	"github.com/google/uuid"
)

// --------------------------------------------------------------------------
// Test Property: Stock-in commit creates exactly N instances
// --------------------------------------------------------------------------
// Feature: pantry-management, Property 5: Stock-in commit creates exactly N instances
//
// For any scan entry with direction `stock_in` and unit count N ≥ 1,
// committing that entry SHALL create exactly N new item instances for the
// corresponding item, each with the scan entry's stock-in timestamp and
// expiration date, and the item's total instance count SHALL increase by
// exactly N.

func TestProperty_StockInCreatesExactlyNInstances(t *testing.T) {
	queue, db, scope := newTestQueue(t)
	ctx := context.Background()
	now := time.Now()

	// Test with various unit counts
	testCases := []struct {
		unitCount int
		expiresAt *time.Time
	}{
		{unitCount: 1, expiresAt: nil},
		{unitCount: 1, expiresAt: ptrTime(now.Add(7 * 24 * time.Hour))},
		{unitCount: 2, expiresAt: ptrTime(now.Add(14 * 24 * time.Hour))},
		{unitCount: 5, expiresAt: nil},
		{unitCount: 10, expiresAt: ptrTime(now.Add(30 * 24 * time.Hour))},
	}

	for _, tc := range testCases {
		t.Run(fmt.Sprintf("unit_count_%d", tc.unitCount), func(t *testing.T) {
			// Create product
			catalog := product.NewCatalog(db)
			productID := uuid.NewString()
			prod := product.Product{
				ID:            productID,
				Name:          fmt.Sprintf("Product %s", productID),
				Category:      "Test",
				UnitOfMeasure: "unit",
			}
			if err := catalog.CreateProduct(ctx, prod); err != nil {
				t.Fatalf("CreateProduct: %v", err)
			}

			// Create scan entry
			userID := uuid.NewString()
			direction := scan.StockIn
			scanEntry := scan.ScanEntry{
				ID:        uuid.NewString(),
				UserID:    userID,
				Barcode:   uuid.NewString(),
				ScannedAt: now,
				Direction: &direction,
				UnitCount: tc.unitCount,
				ExpiresAt: tc.expiresAt,
				ProductID: &productID,
				Status:    scan.Pending,
			}
			created, err := queue.CreateScanEntry(ctx, scope, scanEntry)
			if err != nil {
				t.Fatalf("CreateScanEntry: %v", err)
			}

			// Count instances before commit
			var beforeCount int
			err = db.QueryRowContext(ctx, `
				SELECT COUNT(*)
				FROM item_instances ii
				JOIN items i ON i.id = ii.item_id
				WHERE i.user_id = ? AND i.product_id = ? AND ii.removed_at IS NULL`,
				userID, productID,
			).Scan(&beforeCount)
			if err != nil {
				t.Fatalf("count before: %v", err)
			}

			// Commit stock-in
			if err := queue.CommitStockIn(ctx, scope, created); err != nil {
				t.Fatalf("CommitStockIn: %v", err)
			}

			// Count instances after commit
			var afterCount int
			err = db.QueryRowContext(ctx, `
				SELECT COUNT(*)
				FROM item_instances ii
				JOIN items i ON i.id = ii.item_id
				WHERE i.user_id = ? AND i.product_id = ? AND ii.removed_at IS NULL`,
				userID, productID,
			).Scan(&afterCount)
			if err != nil {
				t.Fatalf("count after: %v", err)
			}

			// Property: instance count increased by exactly N
			increase := afterCount - beforeCount
			if increase != tc.unitCount {
				t.Errorf("Property violation: expected instance count to increase by %d, but increased by %d (before=%d, after=%d)",
					tc.unitCount, increase, beforeCount, afterCount)
			}

			// Verify each instance has correct timestamps
			rows, err := db.QueryContext(ctx, `
				SELECT ii.stock_in_at, ii.expires_at
				FROM item_instances ii
				JOIN items i ON i.id = ii.item_id
				WHERE i.user_id = ? AND i.product_id = ? AND ii.removed_at IS NULL`,
				userID, productID,
			)
			if err != nil {
				t.Fatalf("query instances: %v", err)
			}
			defer rows.Close()

			instanceCount := 0
			for rows.Next() {
				var stockInAt time.Time
				var expiresAt sql.NullTime

				if err := rows.Scan(&stockInAt, &expiresAt); err != nil {
					t.Fatalf("scan instance: %v", err)
				}

				instanceCount++

				// Verify stock_in_at matches scan entry
				if !stockInAt.Equal(now) {
					t.Errorf("instance %d: stock_in_at want %v, got %v", instanceCount, now, stockInAt)
				}

				// Verify expires_at matches scan entry
				if tc.expiresAt == nil && expiresAt.Valid {
					t.Errorf("instance %d: expires_at want NULL, got %v", instanceCount, expiresAt.Time)
				}
				if tc.expiresAt != nil {
					if !expiresAt.Valid {
						t.Errorf("instance %d: expires_at want %v, got NULL", instanceCount, *tc.expiresAt)
					} else if !expiresAt.Time.Equal(*tc.expiresAt) {
						t.Errorf("instance %d: expires_at want %v, got %v", instanceCount, *tc.expiresAt, expiresAt.Time)
					}
				}
			}

			if instanceCount != tc.unitCount {
				t.Errorf("Property violation: expected %d instances with correct timestamps, found %d",
					tc.unitCount, instanceCount)
			}
		})
	}
}

// --------------------------------------------------------------------------
// Test Property: Stock-out commit removes use-oldest-first
// --------------------------------------------------------------------------
// Feature: pantry-management, Property 6: Stock-out commit removes use-oldest-first
//
// For any scan entry with direction `stock_out`, unit count 1, and no instance
// specified, committing that entry SHALL remove one existing item instance, and the
// instance with the nearest (soonest) non-NULL expiration date (use-oldest-first)
// SHALL be the one removed, and the item's total instance count SHALL decrease
// by exactly 1. A larger unit count removes that many units; the HTTP tests cover it.

func TestProperty_StockOutRemovesUseOldestFirst(t *testing.T) {
	queue, db, scope := newTestQueue(t)
	ctx := context.Background()
	now := time.Now()

	// Test cases with various expiration date combinations
	testCases := []struct {
		name               string
		expirationDates    []*time.Time // instances to create
		expectedRemovedIdx int          // which instance should be removed (0-indexed)
	}{
		{
			name: "all dated instances - select earliest",
			expirationDates: []*time.Time{
				ptrTime(now.Add(10 * 24 * time.Hour)),
				ptrTime(now.Add(3 * 24 * time.Hour)), // earliest - should be removed
				ptrTime(now.Add(7 * 24 * time.Hour)),
			},
			expectedRemovedIdx: 1,
		},
		{
			name: "mix of dated and NULL - select dated over NULL",
			expirationDates: []*time.Time{
				nil,
				ptrTime(now.Add(5 * 24 * time.Hour)), // only dated - should be removed
				nil,
			},
			expectedRemovedIdx: 1,
		},
		{
			name: "all NULL expiration dates - select the smallest id",
			expirationDates: []*time.Time{
				nil,
				nil,
				nil,
			},
			expectedRemovedIdx: -1, // same stock-in time: the smallest id
		},
		{
			name: "single instance - select it",
			expirationDates: []*time.Time{
				ptrTime(now.Add(14 * 24 * time.Hour)),
			},
			expectedRemovedIdx: 0,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create product
			catalog := product.NewCatalog(db)
			productID := uuid.NewString()
			prod := product.Product{
				ID:            productID,
				Name:          fmt.Sprintf("Product %s", productID),
				Category:      "Test",
				UnitOfMeasure: "unit",
			}
			if err := catalog.CreateProduct(ctx, prod); err != nil {
				t.Fatalf("CreateProduct: %v", err)
			}

			// Create item
			userID := uuid.NewString()
			itemID := uuid.NewString()
			_, err := db.ExecContext(ctx, `
				INSERT INTO items (id, user_id, product_id, household_id)
				VALUES (?, ?, ?, ?)`,
				itemID, userID, productID, scope.HouseholdID())
			if err != nil {
				t.Fatalf("create item: %v", err)
			}

			// Create instances with provided expiration dates
			instanceIDs := make([]string, len(tc.expirationDates))
			for i, expiresAt := range tc.expirationDates {
				instanceIDs[i] = uuid.NewString()
				_, err := db.ExecContext(ctx, `
					INSERT INTO item_instances (id, item_id, household_id, stock_in_at, expires_at)
					VALUES (?, ?, ?, ?, ?)`,
					instanceIDs[i], itemID, scope.HouseholdID(), now.Add(-24*time.Hour), nullableTime(expiresAt))
				if err != nil {
					t.Fatalf("create instance %d: %v", i, err)
				}
			}

			// Count instances before commit
			var beforeCount int
			err = db.QueryRowContext(ctx, `
				SELECT COUNT(*)
				FROM item_instances
				WHERE item_id = ? AND removed_at IS NULL`,
				itemID,
			).Scan(&beforeCount)
			if err != nil {
				t.Fatalf("count before: %v", err)
			}

			// Create and commit stock-out scan entry (no specific instanceID)
			direction := scan.StockOut
			scanEntry := scan.ScanEntry{
				ID:        uuid.NewString(),
				UserID:    userID,
				Barcode:   uuid.NewString(),
				ScannedAt: now,
				Direction: &direction,
				UnitCount: 1,
				ProductID: &productID,
				Status:    scan.Pending,
			}
			created, err := queue.CreateScanEntry(ctx, scope, scanEntry)
			if err != nil {
				t.Fatalf("CreateScanEntry: %v", err)
			}

			if _, err := queue.CommitStockOut(ctx, scope, created, nil); err != nil {
				t.Fatalf("CommitStockOut: %v", err)
			}

			// Count instances after commit
			var afterCount int
			err = db.QueryRowContext(ctx, `
				SELECT COUNT(*)
				FROM item_instances
				WHERE item_id = ? AND removed_at IS NULL`,
				itemID,
			).Scan(&afterCount)
			if err != nil {
				t.Fatalf("count after: %v", err)
			}

			// Property: instance count decreased by exactly 1
			decrease := beforeCount - afterCount
			if decrease != 1 {
				t.Errorf("Property violation: expected instance count to decrease by 1, but decreased by %d (before=%d, after=%d)",
					decrease, beforeCount, afterCount)
			}

			// Verify consumption event was created
			var consumptionCount int
			err = db.QueryRowContext(ctx, `
				SELECT COUNT(*)
				FROM consumption_events
				WHERE item_id = ? AND scan_entry_id = ?`,
				itemID, created.ID,
			).Scan(&consumptionCount)
			if err != nil {
				t.Fatalf("count consumption events: %v", err)
			}

			if consumptionCount != 1 {
				t.Errorf("Property violation: expected 1 consumption event, found %d", consumptionCount)
			}

			// Get the removed instance ID
			var removedInstanceID string
			err = db.QueryRowContext(ctx, `
				SELECT id FROM item_instances
				WHERE item_id = ? AND removed_at IS NOT NULL AND removal_reason = 'consumed'
				ORDER BY removed_at DESC
				LIMIT 1`,
				itemID,
			).Scan(&removedInstanceID)
			if err != nil {
				t.Fatalf("query removed instance: %v", err)
			}

			expectedRemovedID := instanceIDs[0]
			if tc.expectedRemovedIdx >= 0 {
				expectedRemovedID = instanceIDs[tc.expectedRemovedIdx]
			} else {
				for _, id := range instanceIDs[1:] {
					if id < expectedRemovedID {
						expectedRemovedID = id
					}
				}
			}
			if removedInstanceID != expectedRemovedID {
				t.Errorf("Property violation: expected instance %s to be removed, but instance %s was removed",
					expectedRemovedID, removedInstanceID)
			}
		})
	}
}

// --------------------------------------------------------------------------
// Helper functions
// --------------------------------------------------------------------------

func ptrTime(t time.Time) *time.Time {
	return &t
}

func nullableTime(t *time.Time) sql.NullTime {
	if t == nil || t.IsZero() {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: *t, Valid: true}
}

// --------------------------------------------------------------------------
// TestResolveFlaggedEntry
// --------------------------------------------------------------------------

func TestResolveFlaggedEntry(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name        string
		setup       func(t *testing.T, queue *scan.Queue, db *sql.DB, ctx context.Context, scope tenant.Scope) (scanEntryID, productID string)
		expectError bool
	}{
		{
			name: "successfully resolves flagged entry",
			setup: func(t *testing.T, queue *scan.Queue, db *sql.DB, ctx context.Context, scope tenant.Scope) (string, string) {
				catalog := product.NewCatalog(db)
				productID := uuid.NewString()
				if err := catalog.CreateProduct(ctx, product.Product{
					ID: productID, Name: "Corrected Product", Category: "Test", UnitOfMeasure: "unit",
				}); err != nil {
					t.Fatalf("CreateProduct: %v", err)
				}
				entry := scan.ScanEntry{
					ID:        uuid.NewString(),
					UserID:    uuid.NewString(),
					Barcode:   uuid.NewString(),
					ScannedAt: now,
					UnitCount: 1,
					Status:    scan.Flagged,
				}
				created, err := queue.CreateScanEntry(ctx, scope, entry)
				if err != nil {
					t.Fatalf("CreateScanEntry: %v", err)
				}
				return created.ID, productID
			},
			expectError: false,
		},
		{
			name: "scan entry not found",
			setup: func(t *testing.T, queue *scan.Queue, db *sql.DB, ctx context.Context, scope tenant.Scope) (string, string) {
				catalog := product.NewCatalog(db)
				productID := uuid.NewString()
				if err := catalog.CreateProduct(ctx, product.Product{
					ID: productID, Name: "Product", Category: "Test", UnitOfMeasure: "unit",
				}); err != nil {
					t.Fatalf("CreateProduct: %v", err)
				}
				return "no-such-scan-entry", productID
			},
			expectError: true,
		},
		{
			name: "scan entry is not flagged",
			setup: func(t *testing.T, queue *scan.Queue, db *sql.DB, ctx context.Context, scope tenant.Scope) (string, string) {
				catalog := product.NewCatalog(db)
				productID := uuid.NewString()
				if err := catalog.CreateProduct(ctx, product.Product{
					ID: productID, Name: "Product", Category: "Test", UnitOfMeasure: "unit",
				}); err != nil {
					t.Fatalf("CreateProduct: %v", err)
				}
				entry := scan.ScanEntry{
					ID:        uuid.NewString(),
					UserID:    uuid.NewString(),
					Barcode:   uuid.NewString(),
					ScannedAt: now,
					UnitCount: 1,
					Status:    scan.Pending,
				}
				created, err := queue.CreateScanEntry(ctx, scope, entry)
				if err != nil {
					t.Fatalf("CreateScanEntry: %v", err)
				}
				return created.ID, productID
			},
			expectError: true,
		},
		{
			name: "product not found",
			setup: func(t *testing.T, queue *scan.Queue, db *sql.DB, ctx context.Context, scope tenant.Scope) (string, string) {
				entry := scan.ScanEntry{
					ID:        uuid.NewString(),
					UserID:    uuid.NewString(),
					Barcode:   uuid.NewString(),
					ScannedAt: now,
					UnitCount: 1,
					Status:    scan.Flagged,
				}
				created, err := queue.CreateScanEntry(ctx, scope, entry)
				if err != nil {
					t.Fatalf("CreateScanEntry: %v", err)
				}
				return created.ID, "no-such-product"
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			queue, db, scope := newTestQueue(t)
			ctx := context.Background()
			scanEntryID, productID := tt.setup(t, queue, db, ctx, scope)

			err := queue.ResolveFlaggedEntry(ctx, scope, scanEntryID, productID)
			if tt.expectError {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveFlaggedEntry: %v", err)
			}

			got, err := queue.GetScanEntry(ctx, scope, scanEntryID)
			if err != nil {
				t.Fatalf("GetScanEntry: %v", err)
			}
			if got.Status != scan.Pending {
				t.Errorf("Status: want %q, got %q", scan.Pending, got.Status)
			}
			if got.ProductID == nil || *got.ProductID != productID {
				t.Errorf("ProductID: want %q, got %v", productID, got.ProductID)
			}
		})
	}
}

// --------------------------------------------------------------------------
// Publish-behavior tests for ResolveFlaggedEntry, CommitStockIn, and
// CommitStockOut (task 8.2)
// --------------------------------------------------------------------------

// createFlaggedEntry creates a flagged scan entry (no product_id) ready to
// be resolved.
func createFlaggedEntry(t *testing.T, queue *scan.Queue, ctx context.Context, scope tenant.Scope) *scan.ScanEntry {
	t.Helper()
	created, err := queue.CreateScanEntry(ctx, scope, scan.ScanEntry{
		ID:        uuid.NewString(),
		UserID:    uuid.NewString(),
		Barcode:   uuid.NewString(),
		ScannedAt: time.Now(),
		UnitCount: 1,
		Status:    scan.Flagged,
	})
	if err != nil {
		t.Fatalf("CreateScanEntry: %v", err)
	}
	return created
}

// createStockInEntry creates a pending stock-in scan entry for a freshly
// created product, ready to be committed.
func createStockInEntry(t *testing.T, queue *scan.Queue, db *sql.DB, ctx context.Context, scope tenant.Scope, unitCount int) *scan.ScanEntry {
	t.Helper()
	catalog := product.NewCatalog(db)
	productID := uuid.NewString()
	if err := catalog.CreateProduct(ctx, product.Product{
		ID: productID, Name: "Product " + productID, Category: "Test", UnitOfMeasure: "unit",
	}); err != nil {
		t.Fatalf("CreateProduct: %v", err)
	}
	direction := scan.StockIn
	created, err := queue.CreateScanEntry(ctx, scope, scan.ScanEntry{
		ID:        uuid.NewString(),
		UserID:    uuid.NewString(),
		Barcode:   uuid.NewString(),
		ScannedAt: time.Now(),
		Direction: &direction,
		UnitCount: unitCount,
		ProductID: &productID,
		Status:    scan.Pending,
	})
	if err != nil {
		t.Fatalf("CreateScanEntry: %v", err)
	}
	return created
}

// createStockOutEntry creates a product and item with one existing instance,
// then a pending stock-out scan entry for that item, ready to be committed.
func createStockOutEntry(t *testing.T, queue *scan.Queue, db *sql.DB, ctx context.Context, scope tenant.Scope) *scan.ScanEntry {
	t.Helper()
	catalog := product.NewCatalog(db)
	productID := uuid.NewString()
	if err := catalog.CreateProduct(ctx, product.Product{
		ID: productID, Name: "Product " + productID, Category: "Test", UnitOfMeasure: "unit",
	}); err != nil {
		t.Fatalf("CreateProduct: %v", err)
	}
	userID := uuid.NewString()
	itemID := uuid.NewString()
	if _, err := db.ExecContext(ctx, `INSERT INTO items (id, user_id, product_id, household_id) VALUES (?, ?, ?, ?)`,
		itemID, userID, productID, scope.HouseholdID()); err != nil {
		t.Fatalf("create item: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO item_instances (id, item_id, household_id, stock_in_at, expires_at)
		VALUES (?, ?, ?, ?, ?)`,
		uuid.NewString(), itemID, scope.HouseholdID(), time.Now(), nullableTime(nil)); err != nil {
		t.Fatalf("create instance: %v", err)
	}

	direction := scan.StockOut
	created, err := queue.CreateScanEntry(ctx, scope, scan.ScanEntry{
		ID:        uuid.NewString(),
		UserID:    userID,
		Barcode:   uuid.NewString(),
		ScannedAt: time.Now(),
		Direction: &direction,
		UnitCount: 1,
		ProductID: &productID,
		Status:    scan.Pending,
	})
	if err != nil {
		t.Fatalf("CreateScanEntry: %v", err)
	}
	return created
}

func TestResolveFlaggedEntry_PublishesScanEvent(t *testing.T) {
	queue, db, scope := newTestQueue(t)
	ctx := context.Background()

	catalog := product.NewCatalog(db)
	productID := uuid.NewString()
	if err := catalog.CreateProduct(ctx, product.Product{
		ID: productID, Name: "Corrected Product", Category: "Test", UnitOfMeasure: "unit",
	}); err != nil {
		t.Fatalf("CreateProduct: %v", err)
	}
	entry := createFlaggedEntry(t, queue, ctx, scope)

	broadcaster := &fakeBroadcaster{}
	queue.Broadcaster = broadcaster

	if err := queue.ResolveFlaggedEntry(ctx, scope, entry.ID, productID); err != nil {
		t.Fatalf("ResolveFlaggedEntry: %v", err)
	}

	want, err := queue.GetScanEntry(ctx, scope, entry.ID)
	if err != nil {
		t.Fatalf("GetScanEntry: %v", err)
	}
	if len(broadcaster.scanEvents) != 1 {
		t.Fatalf("expected 1 published scan event, got %d", len(broadcaster.scanEvents))
	}
	if !reflect.DeepEqual(broadcaster.scanEvents[0], *want) {
		t.Errorf("published event: got %+v, want %+v", broadcaster.scanEvents[0], *want)
	}
	if len(broadcaster.inventoryEvents) != 0 {
		t.Errorf("expected 0 published inventory events, got %d", len(broadcaster.inventoryEvents))
	}
}

func TestResolveFlaggedEntry_ErrorConditions_NoPublish(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, queue *scan.Queue, db *sql.DB, ctx context.Context, scope tenant.Scope) (scanEntryID, productID string)
	}{
		{
			name: "scan entry not found",
			setup: func(t *testing.T, queue *scan.Queue, db *sql.DB, ctx context.Context, scope tenant.Scope) (string, string) {
				catalog := product.NewCatalog(db)
				productID := uuid.NewString()
				if err := catalog.CreateProduct(ctx, product.Product{
					ID: productID, Name: "Product", Category: "Test", UnitOfMeasure: "unit",
				}); err != nil {
					t.Fatalf("CreateProduct: %v", err)
				}
				return "no-such-scan-entry", productID
			},
		},
		{
			name: "scan entry is not flagged",
			setup: func(t *testing.T, queue *scan.Queue, db *sql.DB, ctx context.Context, scope tenant.Scope) (string, string) {
				catalog := product.NewCatalog(db)
				productID := uuid.NewString()
				if err := catalog.CreateProduct(ctx, product.Product{
					ID: productID, Name: "Product", Category: "Test", UnitOfMeasure: "unit",
				}); err != nil {
					t.Fatalf("CreateProduct: %v", err)
				}
				created, err := queue.CreateScanEntry(ctx, scope, scan.ScanEntry{
					ID:        uuid.NewString(),
					UserID:    uuid.NewString(),
					Barcode:   uuid.NewString(),
					ScannedAt: time.Now(),
					UnitCount: 1,
					Status:    scan.Pending,
				})
				if err != nil {
					t.Fatalf("CreateScanEntry: %v", err)
				}
				return created.ID, productID
			},
		},
		{
			name: "product not found",
			setup: func(t *testing.T, queue *scan.Queue, db *sql.DB, ctx context.Context, scope tenant.Scope) (string, string) {
				entry := createFlaggedEntry(t, queue, ctx, scope)
				return entry.ID, "no-such-product"
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			queue, db, scope := newTestQueue(t)
			ctx := context.Background()
			scanEntryID, productID := tt.setup(t, queue, db, ctx, scope)

			broadcaster := &fakeBroadcaster{}
			queue.Broadcaster = broadcaster

			if err := queue.ResolveFlaggedEntry(ctx, scope, scanEntryID, productID); err == nil {
				t.Fatal("expected error, got nil")
			}
			if len(broadcaster.scanEvents) != 0 {
				t.Errorf("expected 0 published scan events, got %d", len(broadcaster.scanEvents))
			}
		})
	}
}

func TestCommitStockIn_PublishesScanEvent(t *testing.T) {
	queue, db, scope := newTestQueue(t)
	ctx := context.Background()
	entry := createStockInEntry(t, queue, db, ctx, scope, 3)

	broadcaster := &fakeBroadcaster{}
	queue.Broadcaster = broadcaster
	// queue.Pantry left nil: no Inventory_Event should be published.

	if err := queue.CommitStockIn(ctx, scope, entry); err != nil {
		t.Fatalf("CommitStockIn: %v", err)
	}

	want, err := queue.GetScanEntry(ctx, scope, entry.ID)
	if err != nil {
		t.Fatalf("GetScanEntry: %v", err)
	}
	if len(broadcaster.scanEvents) != 1 {
		t.Fatalf("expected 1 published scan event, got %d", len(broadcaster.scanEvents))
	}
	if !reflect.DeepEqual(broadcaster.scanEvents[0], *want) {
		t.Errorf("published event: got %+v, want %+v", broadcaster.scanEvents[0], *want)
	}
	if len(broadcaster.inventoryEvents) != 0 {
		t.Errorf("expected 0 published inventory events (Pantry unset), got %d", len(broadcaster.inventoryEvents))
	}
}

func TestCommitStockIn_PublishesInventoryEvent_WithPantry(t *testing.T) {
	queue, db, scope := newTestQueue(t)
	ctx := context.Background()
	entry := createStockInEntry(t, queue, db, ctx, scope, 3)

	pantry := inventory.NewPantry(db)
	broadcaster := &fakeBroadcaster{}
	queue.Broadcaster = broadcaster
	queue.Pantry = pantry

	if err := queue.CommitStockIn(ctx, scope, entry); err != nil {
		t.Fatalf("CommitStockIn: %v", err)
	}

	wantEntry, err := queue.GetScanEntry(ctx, scope, entry.ID)
	if err != nil {
		t.Fatalf("GetScanEntry: %v", err)
	}
	item, err := pantry.GetOrCreateItem(ctx, scope, *entry.ProductID)
	if err != nil {
		t.Fatalf("GetOrCreateItem: %v", err)
	}
	wantInventoryItem, err := pantry.GetInventoryItem(ctx, scope, item.ID, time.Now(), inventory.DefaultWarningDays)
	if err != nil {
		t.Fatalf("GetInventoryItem: %v", err)
	}

	if len(broadcaster.scanEvents) != 1 {
		t.Fatalf("expected 1 published scan event, got %d", len(broadcaster.scanEvents))
	}
	if !reflect.DeepEqual(broadcaster.scanEvents[0], *wantEntry) {
		t.Errorf("published scan event: got %+v, want %+v", broadcaster.scanEvents[0], *wantEntry)
	}
	if len(broadcaster.inventoryEvents) != 1 {
		t.Fatalf("expected 1 published inventory event, got %d", len(broadcaster.inventoryEvents))
	}
	if !reflect.DeepEqual(broadcaster.inventoryEvents[0], *wantInventoryItem) {
		t.Errorf("published inventory event: got %+v, want %+v", broadcaster.inventoryEvents[0], *wantInventoryItem)
	}
}

func TestCommitStockIn_ErrorConditions_NoPublish(t *testing.T) {
	tests := []struct {
		name  string
		entry func(t *testing.T, queue *scan.Queue, db *sql.DB, ctx context.Context, scope tenant.Scope) *scan.ScanEntry
	}{
		{
			name: "direction is not stock_in",
			entry: func(t *testing.T, queue *scan.Queue, db *sql.DB, ctx context.Context, scope tenant.Scope) *scan.ScanEntry {
				direction := scan.StockOut
				productID := "prod-1"
				return &scan.ScanEntry{ID: uuid.NewString(), UserID: "actor", Direction: &direction, ProductID: &productID, UnitCount: 1}
			},
		},
		{
			name: "missing product_id",
			entry: func(t *testing.T, queue *scan.Queue, db *sql.DB, ctx context.Context, scope tenant.Scope) *scan.ScanEntry {
				direction := scan.StockIn
				return &scan.ScanEntry{ID: uuid.NewString(), UserID: "actor", Direction: &direction, UnitCount: 1}
			},
		},
		{
			name: "unit count less than 1",
			entry: func(t *testing.T, queue *scan.Queue, db *sql.DB, ctx context.Context, scope tenant.Scope) *scan.ScanEntry {
				direction := scan.StockIn
				productID := "prod-1"
				return &scan.ScanEntry{ID: uuid.NewString(), UserID: "actor", Direction: &direction, ProductID: &productID, UnitCount: 0}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			queue, db, scope := newTestQueue(t)
			ctx := context.Background()
			entry := tt.entry(t, queue, db, ctx, scope)

			broadcaster := &fakeBroadcaster{}
			queue.Broadcaster = broadcaster
			queue.Pantry = inventory.NewPantry(db)

			if err := queue.CommitStockIn(ctx, scope, entry); err == nil {
				t.Fatal("expected error, got nil")
			}
			if len(broadcaster.scanEvents) != 0 {
				t.Errorf("expected 0 published scan events, got %d", len(broadcaster.scanEvents))
			}
			if len(broadcaster.inventoryEvents) != 0 {
				t.Errorf("expected 0 published inventory events, got %d", len(broadcaster.inventoryEvents))
			}
		})
	}
}

func TestCommitStockOut_PublishesScanEvent(t *testing.T) {
	queue, db, scope := newTestQueue(t)
	ctx := context.Background()
	entry := createStockOutEntry(t, queue, db, ctx, scope)

	broadcaster := &fakeBroadcaster{}
	queue.Broadcaster = broadcaster
	// queue.Pantry left nil: no Inventory_Event should be published.

	if _, err := queue.CommitStockOut(ctx, scope, entry, nil); err != nil {
		t.Fatalf("CommitStockOut: %v", err)
	}

	want, err := queue.GetScanEntry(ctx, scope, entry.ID)
	if err != nil {
		t.Fatalf("GetScanEntry: %v", err)
	}
	if len(broadcaster.scanEvents) != 1 {
		t.Fatalf("expected 1 published scan event, got %d", len(broadcaster.scanEvents))
	}
	if !reflect.DeepEqual(broadcaster.scanEvents[0], *want) {
		t.Errorf("published event: got %+v, want %+v", broadcaster.scanEvents[0], *want)
	}
	if len(broadcaster.inventoryEvents) != 0 {
		t.Errorf("expected 0 published inventory events (Pantry unset), got %d", len(broadcaster.inventoryEvents))
	}
}

func TestCommitStockOut_PublishesInventoryEvent_WithPantry(t *testing.T) {
	queue, db, scope := newTestQueue(t)
	ctx := context.Background()
	entry := createStockOutEntry(t, queue, db, ctx, scope)

	pantry := inventory.NewPantry(db)
	broadcaster := &fakeBroadcaster{}
	queue.Broadcaster = broadcaster
	queue.Pantry = pantry

	if _, err := queue.CommitStockOut(ctx, scope, entry, nil); err != nil {
		t.Fatalf("CommitStockOut: %v", err)
	}

	wantEntry, err := queue.GetScanEntry(ctx, scope, entry.ID)
	if err != nil {
		t.Fatalf("GetScanEntry: %v", err)
	}
	item, err := pantry.GetOrCreateItem(ctx, scope, *entry.ProductID)
	if err != nil {
		t.Fatalf("GetOrCreateItem: %v", err)
	}
	wantInventoryItem, err := pantry.GetInventoryItem(ctx, scope, item.ID, time.Now(), inventory.DefaultWarningDays)
	if err != nil {
		t.Fatalf("GetInventoryItem: %v", err)
	}

	if len(broadcaster.scanEvents) != 1 {
		t.Fatalf("expected 1 published scan event, got %d", len(broadcaster.scanEvents))
	}
	if !reflect.DeepEqual(broadcaster.scanEvents[0], *wantEntry) {
		t.Errorf("published scan event: got %+v, want %+v", broadcaster.scanEvents[0], *wantEntry)
	}
	if len(broadcaster.inventoryEvents) != 1 {
		t.Fatalf("expected 1 published inventory event, got %d", len(broadcaster.inventoryEvents))
	}
	if !reflect.DeepEqual(broadcaster.inventoryEvents[0], *wantInventoryItem) {
		t.Errorf("published inventory event: got %+v, want %+v", broadcaster.inventoryEvents[0], *wantInventoryItem)
	}
}

func TestCommitStockOut_ErrorConditions_NoPublish(t *testing.T) {
	tests := []struct {
		name  string
		entry func(t *testing.T, queue *scan.Queue, db *sql.DB, ctx context.Context, scope tenant.Scope) (*scan.ScanEntry, *string)
	}{
		{
			name: "direction is not stock_out",
			entry: func(t *testing.T, queue *scan.Queue, db *sql.DB, ctx context.Context, scope tenant.Scope) (*scan.ScanEntry, *string) {
				direction := scan.StockIn
				productID := "prod-1"
				return &scan.ScanEntry{ID: uuid.NewString(), UserID: "actor", Direction: &direction, ProductID: &productID}, nil
			},
		},
		{
			name: "missing product_id",
			entry: func(t *testing.T, queue *scan.Queue, db *sql.DB, ctx context.Context, scope tenant.Scope) (*scan.ScanEntry, *string) {
				direction := scan.StockOut
				return &scan.ScanEntry{ID: uuid.NewString(), UserID: "actor", Direction: &direction}, nil
			},
		},
		{
			name: "no item found for user and product",
			entry: func(t *testing.T, queue *scan.Queue, db *sql.DB, ctx context.Context, scope tenant.Scope) (*scan.ScanEntry, *string) {
				direction := scan.StockOut
				productID := "no-such-product"
				return &scan.ScanEntry{ID: uuid.NewString(), UserID: "actor", Direction: &direction, ProductID: &productID}, nil
			},
		},
		{
			name: "specific instance not found or already removed",
			entry: func(t *testing.T, queue *scan.Queue, db *sql.DB, ctx context.Context, scope tenant.Scope) (*scan.ScanEntry, *string) {
				entry := createStockOutEntry(t, queue, db, ctx, scope)
				missing := "no-such-instance"
				return entry, &missing
			},
		},
		{
			name: "no available instances",
			entry: func(t *testing.T, queue *scan.Queue, db *sql.DB, ctx context.Context, scope tenant.Scope) (*scan.ScanEntry, *string) {
				catalog := product.NewCatalog(db)
				productID := uuid.NewString()
				if err := catalog.CreateProduct(ctx, product.Product{
					ID: productID, Name: "Product " + productID, Category: "Test", UnitOfMeasure: "unit",
				}); err != nil {
					t.Fatalf("CreateProduct: %v", err)
				}
				userID := uuid.NewString()
				itemID := uuid.NewString()
				if _, err := db.ExecContext(ctx, `INSERT INTO items (id, user_id, product_id, household_id) VALUES (?, ?, ?, ?)`,
					itemID, userID, productID, scope.HouseholdID()); err != nil {
					t.Fatalf("create item: %v", err)
				}
				direction := scan.StockOut
				created, err := queue.CreateScanEntry(ctx, scope, scan.ScanEntry{
					ID:        uuid.NewString(),
					UserID:    userID,
					Barcode:   uuid.NewString(),
					ScannedAt: time.Now(),
					Direction: &direction,
					UnitCount: 1,
					ProductID: &productID,
					Status:    scan.Pending,
				})
				if err != nil {
					t.Fatalf("CreateScanEntry: %v", err)
				}
				return created, nil
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			queue, db, scope := newTestQueue(t)
			ctx := context.Background()
			entry, instanceID := tt.entry(t, queue, db, ctx, scope)

			broadcaster := &fakeBroadcaster{}
			queue.Broadcaster = broadcaster
			queue.Pantry = inventory.NewPantry(db)

			_, err := queue.CommitStockOut(ctx, scope, entry, instanceID)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if tt.name == "no available instances" && !errors.Is(err, scan.ErrNoneOnHand) {
				t.Fatalf("CommitStockOut: %v", err)
			}
			if len(broadcaster.scanEvents) != 0 {
				t.Errorf("expected 0 published scan events, got %d", len(broadcaster.scanEvents))
			}
			if len(broadcaster.inventoryEvents) != 0 {
				t.Errorf("expected 0 published inventory events, got %d", len(broadcaster.inventoryEvents))
			}
		})
	}
}

// TestCommitBroadcaster_NilIsSafe locks in that leaving queue.Broadcaster
// and queue.Pantry unset, as every pre-existing test in this file does, is a
// supported mode: none of ResolveFlaggedEntry, CommitStockIn, or
// CommitStockOut panics or changes its return value because of it.
func TestCommitBroadcaster_NilIsSafe(t *testing.T) {
	queue, db, scope := newTestQueue(t)
	ctx := context.Background()

	catalog := product.NewCatalog(db)
	productID := uuid.NewString()
	if err := catalog.CreateProduct(ctx, product.Product{
		ID: productID, Name: "Corrected Product", Category: "Test", UnitOfMeasure: "unit",
	}); err != nil {
		t.Fatalf("CreateProduct: %v", err)
	}
	flagged := createFlaggedEntry(t, queue, ctx, scope)
	if err := queue.ResolveFlaggedEntry(ctx, scope, flagged.ID, productID); err != nil {
		t.Fatalf("ResolveFlaggedEntry: %v", err)
	}

	stockIn := createStockInEntry(t, queue, db, ctx, scope, 2)
	if err := queue.CommitStockIn(ctx, scope, stockIn); err != nil {
		t.Fatalf("CommitStockIn: %v", err)
	}

	stockOut := createStockOutEntry(t, queue, db, ctx, scope)
	if _, err := queue.CommitStockOut(ctx, scope, stockOut, nil); err != nil {
		t.Fatalf("CommitStockOut: %v", err)
	}
}
