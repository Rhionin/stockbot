package scan_test

import (
	"context"
	"database/sql"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/Rhionin/stockbot/internal/changes"
	"github.com/Rhionin/stockbot/internal/inventory"
	"github.com/Rhionin/stockbot/internal/product"
	"github.com/Rhionin/stockbot/internal/scan"
	"github.com/Rhionin/stockbot/internal/tenant"
)

// newTestQueue opens an in-memory SQLite database, applies all migrations, and returns a scan Queue.
func newTestQueue(t *testing.T) (*scan.Queue, *sql.DB, tenant.Scope) {
	t.Helper()
	conn, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("open in-memory SQLite: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := app.RunMigrations(conn); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}
	scope, err := auth.PrimaryScope(context.Background(), conn)
	if err != nil {
		t.Fatalf("PrimaryScope: %v", err)
	}
	return scan.NewQueue(conn), conn, scope
}

// TestGetScanEntry_ProductImageURL locks in that ScanEntry.Product.ImageURL is
// projected through the scan_entries-products join, not silently dropped like
// the other product columns would be if a join query omitted it.
func TestGetScanEntry_ProductImageURL(t *testing.T) {
	queue, db, scope := newTestQueue(t)
	ctx := context.Background()

	catalog := product.NewCatalog(db)
	const wantImageURL = "https://images.openfoodfacts.org/thumb.jpg"
	if err := catalog.CreateProduct(ctx, product.Product{
		ID: "prod-1", Name: "Milk", Category: "Dairy", ImageURL: wantImageURL,
	}); err != nil {
		t.Fatalf("CreateProduct: %v", err)
	}

	productID := "prod-1"
	created, err := queue.CreateScanEntry(ctx, scope, scan.ScanEntry{
		UserID:    "actor",
		Barcode:   "000000000001",
		ScannedAt: time.Now(),
		UnitCount: 1,
		ProductID: &productID,
	})
	if err != nil {
		t.Fatalf("CreateScanEntry: %v", err)
	}
	if created.Product == nil || created.Product.ImageURL != wantImageURL {
		t.Errorf("CreateScanEntry: want Product.ImageURL %q, got %+v", wantImageURL, created.Product)
	}

	got, err := queue.GetScanEntry(ctx, scope, created.ID)
	if err != nil {
		t.Fatalf("GetScanEntry: %v", err)
	}
	if got.Product == nil || got.Product.ImageURL != wantImageURL {
		t.Errorf("GetScanEntry: want Product.ImageURL %q, got %+v", wantImageURL, got.Product)
	}

	entries, err := queue.ListScanEntries(ctx, scope, "")
	if err != nil {
		t.Fatalf("ListScanEntries: %v", err)
	}
	if len(entries) != 1 || entries[0].Product == nil || entries[0].Product.ImageURL != wantImageURL {
		t.Fatalf("ListScanEntries: want 1 entry with Product.ImageURL %q, got %+v", wantImageURL, entries)
	}
}

// --------------------------------------------------------------------------
// TestCreateScanEntry
// --------------------------------------------------------------------------

func TestCreateScanEntry(t *testing.T) {
	tests := []struct {
		name          string
		entry         scan.ScanEntry
		wantIDEmpty   bool
		wantStatus    scan.ScanStatus
		wantDirection *scan.ScanDirection
	}{
		{
			name: "explicit ID and pending status",
			entry: scan.ScanEntry{
				ID:        "scan-1",
				UserID:    "actor",
				Barcode:   "123456789012",
				ScannedAt: time.Now(),
				UnitCount: 1,
				Status:    scan.Pending,
			},
			wantIDEmpty:   false,
			wantStatus:    scan.Pending,
			wantDirection: nil,
		},
		{
			name: "generate UUID when ID empty",
			entry: scan.ScanEntry{
				UserID:    "actor",
				Barcode:   "987654321098",
				ScannedAt: time.Now(),
				UnitCount: 2,
			},
			wantIDEmpty:   true,
			wantStatus:    scan.Pending,
			wantDirection: nil,
		},
		{
			name: "with pre-selected direction",
			entry: scan.ScanEntry{
				ID:        "scan-2",
				UserID:    "actor",
				Barcode:   "111222333444",
				ScannedAt: time.Now(),
				Direction: ptrScanDirection(scan.StockIn),
				UnitCount: 3,
			},
			wantIDEmpty:   false,
			wantStatus:    scan.Pending,
			wantDirection: ptrScanDirection(scan.StockIn),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			queue, _, scope := newTestQueue(t)
			ctx := context.Background()

			// Create scan entry
			created, err := queue.CreateScanEntry(ctx, scope, tt.entry)
			if err != nil {
				t.Fatalf("CreateScanEntry: %v", err)
			}

			// Verify ID generation if needed
			if tt.wantIDEmpty && created.ID == "" {
				t.Error("expected generated UUID, got empty string")
			}
			if !tt.wantIDEmpty && created.ID != tt.entry.ID {
				t.Errorf("ID: want %q, got %q", tt.entry.ID, created.ID)
			}

			// Verify status
			if created.Status != tt.wantStatus {
				t.Errorf("Status: want %q, got %q", tt.wantStatus, created.Status)
			}

			// Verify direction
			if tt.wantDirection == nil && created.Direction != nil {
				t.Errorf("Direction: want nil, got %v", *created.Direction)
			}
			if tt.wantDirection != nil {
				if created.Direction == nil {
					t.Error("Direction: want non-nil, got nil")
				} else if *created.Direction != *tt.wantDirection {
					t.Errorf("Direction: want %v, got %v", *tt.wantDirection, *created.Direction)
				}
			}

			// Verify barcode preserved
			if created.Barcode != tt.entry.Barcode {
				t.Errorf("Barcode: want %q, got %q", tt.entry.Barcode, created.Barcode)
			}
		})
	}
}

// --------------------------------------------------------------------------
// TestGetScanEntry
// --------------------------------------------------------------------------

func TestGetScanEntry(t *testing.T) {
	tests := []struct {
		name        string
		id          string
		setup       func(t *testing.T, queue *scan.Queue, ctx context.Context, scope tenant.Scope)
		expectFound bool
	}{
		{
			name: "found",
			id:   "scan-1",
			setup: func(t *testing.T, queue *scan.Queue, ctx context.Context, scope tenant.Scope) {
				entry := scan.ScanEntry{
					ID:        "scan-1",
					UserID:    "actor",
					Barcode:   "123456789012",
					ScannedAt: time.Now(),
					UnitCount: 1,
				}
				_, err := queue.CreateScanEntry(ctx, scope, entry)
				if err != nil {
					t.Fatalf("CreateScanEntry: %v", err)
				}
			},
			expectFound: true,
		},
		{
			name:        "not found",
			id:          "no-such-id",
			setup:       func(t *testing.T, queue *scan.Queue, ctx context.Context, scope tenant.Scope) {},
			expectFound: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			queue, _, scope := newTestQueue(t)
			ctx := context.Background()
			tt.setup(t, queue, ctx, scope)

			got, err := queue.GetScanEntry(ctx, scope, tt.id)
			if err != nil {
				t.Fatalf("GetScanEntry: %v", err)
			}
			if tt.expectFound && got == nil {
				t.Fatal("expected scan entry, got nil")
			}
			if !tt.expectFound && got != nil {
				t.Errorf("expected nil, got %+v", got)
			}
		})
	}
}

// --------------------------------------------------------------------------
// TestListScanEntries
// --------------------------------------------------------------------------

func TestListScanEntries(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name        string
		userID      string
		status      scan.ScanStatus
		setup       func(t *testing.T, queue *scan.Queue, ctx context.Context, scope tenant.Scope)
		expectCount int
		expectOrder []string // barcodes in expected chronological order
	}{
		{
			name:        "empty",
			userID:      "actor",
			status:      scan.Pending,
			setup:       func(t *testing.T, queue *scan.Queue, ctx context.Context, scope tenant.Scope) {},
			expectCount: 0,
			expectOrder: []string{},
		},
		{
			name:   "single entry",
			userID: "actor",
			status: scan.Pending,
			setup: func(t *testing.T, queue *scan.Queue, ctx context.Context, scope tenant.Scope) {
				entry := scan.ScanEntry{
					ID:        "scan-1",
					UserID:    "actor",
					Barcode:   "111111111111",
					ScannedAt: now,
					UnitCount: 1,
					Status:    scan.Pending,
				}
				_, err := queue.CreateScanEntry(ctx, scope, entry)
				if err != nil {
					t.Fatalf("CreateScanEntry: %v", err)
				}
			},
			expectCount: 1,
			expectOrder: []string{"111111111111"},
		},
		{
			name:   "multiple entries ordered chronologically",
			userID: "actor",
			status: scan.Pending,
			setup: func(t *testing.T, queue *scan.Queue, ctx context.Context, scope tenant.Scope) {
				entries := []scan.ScanEntry{
					{
						ID:        "scan-1",
						UserID:    "actor",
						Barcode:   "333333333333",
						ScannedAt: now.Add(2 * time.Minute),
						UnitCount: 1,
						Status:    scan.Pending,
					},
					{
						ID:        "scan-2",
						UserID:    "actor",
						Barcode:   "111111111111",
						ScannedAt: now,
						UnitCount: 1,
						Status:    scan.Pending,
					},
					{
						ID:        "scan-3",
						UserID:    "actor",
						Barcode:   "222222222222",
						ScannedAt: now.Add(1 * time.Minute),
						UnitCount: 1,
						Status:    scan.Pending,
					},
				}
				for _, e := range entries {
					_, err := queue.CreateScanEntry(ctx, scope, e)
					if err != nil {
						t.Fatalf("CreateScanEntry: %v", err)
					}
				}
			},
			expectCount: 3,
			expectOrder: []string{"111111111111", "222222222222", "333333333333"},
		},
		{
			name:   "filter by status",
			userID: "actor",
			status: scan.Flagged,
			setup: func(t *testing.T, queue *scan.Queue, ctx context.Context, scope tenant.Scope) {
				entries := []scan.ScanEntry{
					{
						ID:        "scan-1",
						UserID:    "actor",
						Barcode:   "111111111111",
						ScannedAt: now,
						UnitCount: 1,
						Status:    scan.Pending,
					},
					{
						ID:        "scan-2",
						UserID:    "actor",
						Barcode:   "222222222222",
						ScannedAt: now.Add(1 * time.Minute),
						UnitCount: 1,
						Status:    scan.Flagged,
					},
					{
						ID:        "scan-3",
						UserID:    "actor",
						Barcode:   "333333333333",
						ScannedAt: now.Add(2 * time.Minute),
						UnitCount: 1,
						Status:    scan.Committed,
					},
				}
				for _, e := range entries {
					_, err := queue.CreateScanEntry(ctx, scope, e)
					if err != nil {
						t.Fatalf("CreateScanEntry: %v", err)
					}
				}
			},
			expectCount: 1,
			expectOrder: []string{"222222222222"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			queue, _, scope := newTestQueue(t)
			ctx := context.Background()
			tt.setup(t, queue, ctx, scope)

			// List and verify
			got, err := queue.ListScanEntries(ctx, scope, tt.status)
			if err != nil {
				t.Fatalf("ListScanEntries: %v", err)
			}
			if len(got) != tt.expectCount {
				t.Fatalf("expected %d entries, got %d", tt.expectCount, len(got))
			}

			// Verify chronological order
			for i, wantBarcode := range tt.expectOrder {
				if got[i].Barcode != wantBarcode {
					t.Errorf("[%d] Barcode: want %q, got %q", i, wantBarcode, got[i].Barcode)
				}
			}
		})
	}
}

// --------------------------------------------------------------------------
// TestUpdateScanEntry
// --------------------------------------------------------------------------

func TestUpdateScanEntry(t *testing.T) {
	now := time.Now()
	expiresAt := now.Add(7 * 24 * time.Hour)

	tests := []struct {
		name          string
		id            string
		direction     *scan.ScanDirection
		unitCount     *int
		expiresAt     *time.Time
		productID     *string
		status        *scan.ScanStatus
		expectError   bool
		verifyUpdated func(t *testing.T, entry *scan.ScanEntry)
	}{
		{
			name:      "update direction",
			id:        "scan-1",
			direction: ptrScanDirection(scan.StockIn),
			verifyUpdated: func(t *testing.T, entry *scan.ScanEntry) {
				if entry.Direction == nil || *entry.Direction != scan.StockIn {
					t.Errorf("Direction: want %v, got %v", scan.StockIn, entry.Direction)
				}
			},
		},
		{
			name:      "update unit count",
			id:        "scan-2",
			unitCount: ptrInt(5),
			verifyUpdated: func(t *testing.T, entry *scan.ScanEntry) {
				if entry.UnitCount != 5 {
					t.Errorf("UnitCount: want 5, got %d", entry.UnitCount)
				}
			},
		},
		{
			name:      "update expiry",
			id:        "scan-3",
			expiresAt: &expiresAt,
			verifyUpdated: func(t *testing.T, entry *scan.ScanEntry) {
				if entry.ExpiresAt == nil {
					t.Error("ExpiresAt: want non-nil, got nil")
				} else if !entry.ExpiresAt.Equal(expiresAt) {
					t.Errorf("ExpiresAt: want %v, got %v", expiresAt, *entry.ExpiresAt)
				}
			},
		},
		{
			name:      "update product ID",
			id:        "scan-4",
			productID: ptrString("prod-123"),
			verifyUpdated: func(t *testing.T, entry *scan.ScanEntry) {
				if entry.ProductID == nil || *entry.ProductID != "prod-123" {
					t.Errorf("ProductID: want prod-123, got %v", entry.ProductID)
				}
			},
		},
		{
			name:   "update status",
			id:     "scan-5",
			status: ptrScanStatus(scan.Committed),
			verifyUpdated: func(t *testing.T, entry *scan.ScanEntry) {
				if entry.Status != scan.Committed {
					t.Errorf("Status: want %v, got %v", scan.Committed, entry.Status)
				}
			},
		},
		{
			name:        "not found",
			id:          "no-such-id",
			direction:   ptrScanDirection(scan.StockIn),
			expectError: true,
			verifyUpdated: func(t *testing.T, entry *scan.ScanEntry) {
				// Not called when expectError is true
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			queue, _, scope := newTestQueue(t)
			ctx := context.Background()

			// Setup: create scan entry
			if !tt.expectError {
				entry := scan.ScanEntry{
					ID:        tt.id,
					UserID:    "actor",
					Barcode:   "123456789012",
					ScannedAt: now,
					UnitCount: 1,
					Status:    scan.Pending,
				}
				_, err := queue.CreateScanEntry(ctx, scope, entry)
				if err != nil {
					t.Fatalf("CreateScanEntry: %v", err)
				}
			}

			// Update
			err := queue.UpdateScanEntry(ctx, scope, tt.id, tt.direction, tt.unitCount, tt.expiresAt, tt.productID, tt.status)
			if tt.expectError && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !tt.expectError && err != nil {
				t.Fatalf("UpdateScanEntry: %v", err)
			}

			// Verify if successful
			if !tt.expectError {
				got, err := queue.GetScanEntry(ctx, scope, tt.id)
				if err != nil {
					t.Fatalf("GetScanEntry: %v", err)
				}
				tt.verifyUpdated(t, got)
			}
		})
	}
}

// --------------------------------------------------------------------------
// TestCommitScanEntry
// --------------------------------------------------------------------------

func TestCommitScanEntry(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name        string
		id          string
		setup       func(t *testing.T, queue *scan.Queue, ctx context.Context, scope tenant.Scope)
		expectError bool
	}{
		{
			name: "commit successfully",
			id:   "scan-1",
			setup: func(t *testing.T, queue *scan.Queue, ctx context.Context, scope tenant.Scope) {
				entry := scan.ScanEntry{
					ID:        "scan-1",
					UserID:    "actor",
					Barcode:   "123456789012",
					ScannedAt: now,
					UnitCount: 1,
					Status:    scan.Pending,
				}
				_, err := queue.CreateScanEntry(ctx, scope, entry)
				if err != nil {
					t.Fatalf("CreateScanEntry: %v", err)
				}
			},
			expectError: false,
		},
		{
			name:        "entry not found",
			id:          "no-such-id",
			setup:       func(t *testing.T, queue *scan.Queue, ctx context.Context, scope tenant.Scope) {},
			expectError: false, // CommitScanEntry doesn't check for existence before updating
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			queue, _, scope := newTestQueue(t)
			ctx := context.Background()
			tt.setup(t, queue, ctx, scope)

			// Commit
			err := queue.CommitScanEntry(ctx, scope, tt.id)
			if tt.expectError && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !tt.expectError && err != nil {
				t.Fatalf("CommitScanEntry: %v", err)
			}

			// Verify committed status
			if !tt.expectError && tt.id == "scan-1" {
				got, err := queue.GetScanEntry(ctx, scope, tt.id)
				if err != nil {
					t.Fatalf("GetScanEntry: %v", err)
				}
				if got == nil {
					return // Entry didn't exist, which is OK in this test
				}
				if got.Status != scan.Committed {
					t.Errorf("Status: want %v, got %v", scan.Committed, got.Status)
				}
				if got.CommittedAt == nil {
					t.Error("CommittedAt: want non-nil, got nil")
				}
			}
		})
	}
}

// --------------------------------------------------------------------------
// TestBatchUpdateScanEntries
// --------------------------------------------------------------------------

func TestBatchUpdateScanEntries(t *testing.T) {
	now := time.Now()
	expiresAt := now.Add(7 * 24 * time.Hour)

	tests := []struct {
		name           string
		ids            []string
		direction      *scan.ScanDirection
		unitCount      *int
		expiresAt      *time.Time
		setup          func(t *testing.T, queue *scan.Queue, ctx context.Context, scope tenant.Scope)
		expectError    bool
		verifyAllMatch func(t *testing.T, queue *scan.Queue, ctx context.Context, scope tenant.Scope, ids []string)
	}{
		{
			name:      "batch update direction and expiry",
			ids:       []string{"scan-1", "scan-2", "scan-3"},
			direction: ptrScanDirection(scan.StockIn),
			expiresAt: &expiresAt,
			setup: func(t *testing.T, queue *scan.Queue, ctx context.Context, scope tenant.Scope) {
				for i, id := range []string{"scan-1", "scan-2", "scan-3"} {
					entry := scan.ScanEntry{
						ID:        id,
						UserID:    "actor",
						Barcode:   string(rune('1' + i)),
						ScannedAt: now.Add(time.Duration(i) * time.Minute),
						UnitCount: 1,
						Status:    scan.Pending,
					}
					_, err := queue.CreateScanEntry(ctx, scope, entry)
					if err != nil {
						t.Fatalf("CreateScanEntry: %v", err)
					}
				}
			},
			expectError: false,
			verifyAllMatch: func(t *testing.T, queue *scan.Queue, ctx context.Context, scope tenant.Scope, ids []string) {
				for _, id := range ids {
					got, err := queue.GetScanEntry(ctx, scope, id)
					if err != nil {
						t.Fatalf("GetScanEntry %s: %v", id, err)
					}
					if got.Direction == nil || *got.Direction != scan.StockIn {
						t.Errorf("%s Direction: want %v, got %v", id, scan.StockIn, got.Direction)
					}
					if got.ExpiresAt == nil {
						t.Errorf("%s ExpiresAt: want non-nil, got nil", id)
					}
				}
			},
		},
		{
			name:      "empty batch",
			ids:       []string{},
			direction: ptrScanDirection(scan.StockIn),
			setup:     func(t *testing.T, queue *scan.Queue, ctx context.Context, scope tenant.Scope) {},
			verifyAllMatch: func(t *testing.T, queue *scan.Queue, ctx context.Context, scope tenant.Scope, ids []string) {
				// Nothing to verify
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			queue, _, scope := newTestQueue(t)
			ctx := context.Background()
			tt.setup(t, queue, ctx, scope)

			// Batch update
			err := queue.BatchUpdateScanEntries(ctx, scope, tt.ids, tt.direction, tt.unitCount, tt.expiresAt)
			if tt.expectError && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !tt.expectError && err != nil {
				t.Fatalf("BatchUpdateScanEntries: %v", err)
			}

			// Verify
			if !tt.expectError {
				tt.verifyAllMatch(t, queue, ctx, scope, tt.ids)
			}
		})
	}
}

// --------------------------------------------------------------------------
// TestScanEntryWithProductJoin
// --------------------------------------------------------------------------

func TestScanEntryWithProductJoin(t *testing.T) {
	now := time.Now()

	queue, db, scope := newTestQueue(t)
	ctx := context.Background()

	// Create a product
	catalog := product.NewCatalog(db)
	prod := product.Product{
		ID:            "prod-1",
		Name:          "Whole Milk",
		Category:      "Dairy",
		UnitOfMeasure: "gallon",
	}
	if err := catalog.CreateProduct(ctx, prod); err != nil {
		t.Fatalf("CreateProduct: %v", err)
	}

	// Create a scan entry with product ID
	productID := "prod-1"
	entry := scan.ScanEntry{
		ID:        "scan-1",
		UserID:    "actor",
		Barcode:   "123456789012",
		ScannedAt: now,
		UnitCount: 1,
		ProductID: &productID,
		Status:    scan.Pending,
	}
	_, err := queue.CreateScanEntry(ctx, scope, entry)
	if err != nil {
		t.Fatalf("CreateScanEntry: %v", err)
	}

	// Retrieve and verify product join
	got, err := queue.GetScanEntry(ctx, scope, "scan-1")
	if err != nil {
		t.Fatalf("GetScanEntry: %v", err)
	}

	if got.Product == nil {
		t.Fatal("Product: want non-nil, got nil")
	}
	if got.Product.ID != "prod-1" {
		t.Errorf("Product.ID: want prod-1, got %s", got.Product.ID)
	}
	if got.Product.Name != "Whole Milk" {
		t.Errorf("Product.Name: want Whole Milk, got %s", got.Product.Name)
	}
}

// --------------------------------------------------------------------------
// TestNewEntryFromLookup
// --------------------------------------------------------------------------

func TestNewEntryFromLookup(t *testing.T) {
	now := time.Now()
	direction := ptrScanDirection(scan.StockOut)

	tests := []struct {
		name          string
		lookup        product.LookupResult
		wantProductID *string
		wantStatus    scan.ScanStatus
	}{
		{
			name: "found product",
			lookup: product.LookupResult{
				Product: &product.ProductSummary{ID: "prod-1", Name: "Milk"},
			},
			wantProductID: ptrString("prod-1"),
			wantStatus:    scan.Pending,
		},
		{
			name:          "not found",
			lookup:        product.LookupResult{},
			wantProductID: nil,
			wantStatus:    scan.Flagged,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entry := scan.NewEntryFromLookup("actor", "123456789012", tt.lookup, direction, now)

			if tt.wantProductID == nil && entry.ProductID != nil {
				t.Errorf("ProductID: want nil, got %v", *entry.ProductID)
			}
			if tt.wantProductID != nil {
				if entry.ProductID == nil {
					t.Error("ProductID: want non-nil, got nil")
				} else if *entry.ProductID != *tt.wantProductID {
					t.Errorf("ProductID: want %q, got %q", *tt.wantProductID, *entry.ProductID)
				}
			}

			if entry.Status != tt.wantStatus {
				t.Errorf("Status: want %q, got %q", tt.wantStatus, entry.Status)
			}
			if entry.UnitCount != 1 {
				t.Errorf("UnitCount: want 1, got %d", entry.UnitCount)
			}

			// UserID, Barcode, ScannedAt, and Direction are carried through unchanged.
			if entry.UserID != "actor" {
				t.Errorf("UserID: want %q, got %q", "actor", entry.UserID)
			}
			if entry.Barcode != "123456789012" {
				t.Errorf("Barcode: want %q, got %q", "123456789012", entry.Barcode)
			}
			if !entry.ScannedAt.Equal(now) {
				t.Errorf("ScannedAt: want %v, got %v", now, entry.ScannedAt)
			}
			if entry.Direction == nil || *entry.Direction != *direction {
				t.Errorf("Direction: want %v, got %v", *direction, entry.Direction)
			}
		})
	}
}

// --------------------------------------------------------------------------
// TestCreateScanEntry_BugCondition_RepeatScanMergesIntoExisting
// --------------------------------------------------------------------------

// TestCreateScanEntry_BugCondition_RepeatScanMergesIntoExisting is the Task 1
// bug-condition exploration test for scan-duplicate-cards-fix (Property 1).
// It encodes the EXPECTED behavior for a repeat scan of the same
// user/barcode/direction while the first entry is still open (pending or
// flagged): the repeat scan should merge into the existing entry - unit
// count incremented, scanned_at moved to the later scan, no second row -
// rather than create a new one. The later scanned_at is what puts the card
// back at the top of a newest-first queue.
//
// CreateScanEntry currently has no merge-or-create check, so it
// unconditionally inserts a new row for every call. These assertions
// therefore FAIL on today's code, producing two one-count rows instead of
// one two-count row; that failure is the counterexample confirming the bug.
// Task 3.3 re-runs this exact test after the fix to confirm it now passes.
func TestCreateScanEntry_BugCondition_RepeatScanMergesIntoExisting(t *testing.T) {
	t0 := time.Now()
	t1 := t0.Add(5 * time.Minute)

	tests := []struct {
		name   string
		first  scan.ScanEntry // creates the open entry at t0
		second scan.ScanEntry // repeat scan of the same user/barcode/direction at t1
		status scan.ScanStatus
	}{
		{
			name: "repeat pending scan merges into existing entry instead of duplicating",
			first: scan.ScanEntry{
				UserID: "actor", Barcode: "123456789012", ScannedAt: t0,
				Direction: ptrScanDirection(scan.StockIn), UnitCount: 1, Status: scan.Pending,
			},
			second: scan.ScanEntry{
				UserID: "actor", Barcode: "123456789012", ScannedAt: t1,
				Direction: ptrScanDirection(scan.StockIn), UnitCount: 1, Status: scan.Pending,
			},
			status: scan.Pending,
		},
		{
			name: "repeat flagged scan merges into existing entry instead of duplicating",
			first: scan.ScanEntry{
				UserID: "actor", Barcode: "000000000099", ScannedAt: t0,
				Direction: ptrScanDirection(scan.StockIn), UnitCount: 1, Status: scan.Flagged,
			},
			second: scan.ScanEntry{
				UserID: "actor", Barcode: "000000000099", ScannedAt: t1,
				Direction: ptrScanDirection(scan.StockIn), UnitCount: 1, Status: scan.Flagged,
			},
			status: scan.Flagged,
		},
		{
			name: "rescan time replaces the original scan time so the card sorts as the most recent",
			first: scan.ScanEntry{
				UserID: "actor", Barcode: "555555555555", ScannedAt: t0,
				Direction: ptrScanDirection(scan.StockOut), UnitCount: 1, Status: scan.Pending,
			},
			second: scan.ScanEntry{
				UserID: "actor", Barcode: "555555555555", ScannedAt: t1,
				Direction: ptrScanDirection(scan.StockOut), UnitCount: 1, Status: scan.Pending,
			},
			status: scan.Pending,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			queue, _, scope := newTestQueue(t)
			ctx := context.Background()

			if _, err := queue.CreateScanEntry(ctx, scope, tt.first); err != nil {
				t.Fatalf("CreateScanEntry (first scan): %v", err)
			}
			if _, err := queue.CreateScanEntry(ctx, scope, tt.second); err != nil {
				t.Fatalf("CreateScanEntry (repeat scan): %v", err)
			}

			entries, err := queue.ListScanEntries(ctx, scope, tt.status)
			if err != nil {
				t.Fatalf("ListScanEntries: %v", err)
			}

			if len(entries) != 1 {
				t.Fatalf("want 1 merged entry for repeat scan, got %d entries: %+v", len(entries), entries)
			}
			if entries[0].UnitCount != 2 {
				t.Errorf("UnitCount: want 2 (merged), got %d", entries[0].UnitCount)
			}
			if !entries[0].ScannedAt.Equal(t1) {
				t.Errorf("ScannedAt: want rescan time %v, got %v", t1, entries[0].ScannedAt)
			}
		})
	}
}

// --------------------------------------------------------------------------
// TestCreateScanEntry_Preservation_InsertsNewRowWhenNotMergeable
// --------------------------------------------------------------------------

// TestCreateScanEntry_Preservation_InsertsNewRowWhenNotMergeable is the Task
// 2 preservation test for scan-duplicate-cards-fix (Property 2). It locks in
// - on UNFIXED code, before findMergeableScanEntry exists - that every scan
// without a mergeable prior entry (no prior entry at all, a different
// direction, a different user, or only closed committed/cancelled entries)
// keeps inserting a brand-new row, any prior entries are left completely
// untouched, and exactly one PublishScanEvent call happens per
// CreateScanEntry call. These assertions must keep passing after the merge
// fix lands (task 3.4 re-runs this exact test), so the fix cannot regress
// this behavior.
func TestCreateScanEntry_Preservation_InsertsNewRowWhenNotMergeable(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name          string
		setupPrior    func(t *testing.T, queue *scan.Queue, ctx context.Context, scope tenant.Scope) []*scan.ScanEntry
		incoming      func() scan.ScanEntry
		wantUnitCount int
		wantStatus    scan.ScanStatus
	}{
		{
			name: "no prior entry, product found: creates a pending row",
			setupPrior: func(t *testing.T, queue *scan.Queue, ctx context.Context, scope tenant.Scope) []*scan.ScanEntry {
				return nil
			},
			incoming: func() scan.ScanEntry {
				lookup := product.LookupResult{Product: &product.ProductSummary{ID: "prod-1"}}
				return scan.NewEntryFromLookup("actor", "100000000001", lookup, ptrScanDirection(scan.StockIn), now)
			},
			wantUnitCount: 1,
			wantStatus:    scan.Pending,
		},
		{
			name: "no prior entry, product not found: creates a flagged row",
			setupPrior: func(t *testing.T, queue *scan.Queue, ctx context.Context, scope tenant.Scope) []*scan.ScanEntry {
				return nil
			},
			incoming: func() scan.ScanEntry {
				return scan.NewEntryFromLookup("actor", "100000000002", product.LookupResult{}, ptrScanDirection(scan.StockIn), now)
			},
			wantUnitCount: 1,
			wantStatus:    scan.Flagged,
		},
		{
			name: "different direction: existing pending stock-in entry is untouched, a new stock-out entry is created",
			setupPrior: func(t *testing.T, queue *scan.Queue, ctx context.Context, scope tenant.Scope) []*scan.ScanEntry {
				existing, err := queue.CreateScanEntry(ctx, scope, scan.ScanEntry{
					UserID: "actor", Barcode: "200000000001", ScannedAt: now,
					Direction: ptrScanDirection(scan.StockIn), UnitCount: 1, Status: scan.Pending,
				})
				if err != nil {
					t.Fatalf("CreateScanEntry (prior stock-in entry): %v", err)
				}
				return []*scan.ScanEntry{existing}
			},
			incoming: func() scan.ScanEntry {
				return scan.ScanEntry{
					UserID: "actor", Barcode: "200000000001", ScannedAt: now,
					Direction: ptrScanDirection(scan.StockOut), UnitCount: 1, Status: scan.Pending,
				}
			},
			wantUnitCount: 1,
			wantStatus:    scan.Pending,
		},
		{
			name: "different user: existing pending entry for user A is untouched, a new entry is created for user B",
			setupPrior: func(t *testing.T, queue *scan.Queue, ctx context.Context, scope tenant.Scope) []*scan.ScanEntry {
				existing, err := queue.CreateScanEntry(ctx, scope, scan.ScanEntry{
					UserID: "user-A", Barcode: "300000000001", ScannedAt: now,
					Direction: ptrScanDirection(scan.StockIn), UnitCount: 1, Status: scan.Pending,
				})
				if err != nil {
					t.Fatalf("CreateScanEntry (prior entry for user A): %v", err)
				}
				return []*scan.ScanEntry{existing}
			},
			incoming: func() scan.ScanEntry {
				return scan.ScanEntry{
					UserID: "user-B", Barcode: "300000000001", ScannedAt: now,
					Direction: ptrScanDirection(scan.StockIn), UnitCount: 1, Status: scan.Pending,
				}
			},
			wantUnitCount: 1,
			wantStatus:    scan.Pending,
		},
		{
			name: "only a committed entry exists: a new entry is created rather than merging into the closed entry",
			setupPrior: func(t *testing.T, queue *scan.Queue, ctx context.Context, scope tenant.Scope) []*scan.ScanEntry {
				existing, err := queue.CreateScanEntry(ctx, scope, scan.ScanEntry{
					UserID: "actor", Barcode: "400000000001", ScannedAt: now,
					Direction: ptrScanDirection(scan.StockIn), UnitCount: 1, Status: scan.Committed,
				})
				if err != nil {
					t.Fatalf("CreateScanEntry (prior committed entry): %v", err)
				}
				return []*scan.ScanEntry{existing}
			},
			incoming: func() scan.ScanEntry {
				return scan.ScanEntry{
					UserID: "actor", Barcode: "400000000001", ScannedAt: now,
					Direction: ptrScanDirection(scan.StockIn), UnitCount: 1, Status: scan.Pending,
				}
			},
			wantUnitCount: 1,
			wantStatus:    scan.Pending,
		},
		{
			name: "only a cancelled entry exists: a new entry is created rather than merging into the closed entry",
			setupPrior: func(t *testing.T, queue *scan.Queue, ctx context.Context, scope tenant.Scope) []*scan.ScanEntry {
				existing, err := queue.CreateScanEntry(ctx, scope, scan.ScanEntry{
					UserID: "actor", Barcode: "500000000001", ScannedAt: now,
					Direction: ptrScanDirection(scan.StockIn), UnitCount: 1, Status: scan.Cancelled,
				})
				if err != nil {
					t.Fatalf("CreateScanEntry (prior cancelled entry): %v", err)
				}
				return []*scan.ScanEntry{existing}
			},
			incoming: func() scan.ScanEntry {
				return scan.ScanEntry{
					UserID: "actor", Barcode: "500000000001", ScannedAt: now,
					Direction: ptrScanDirection(scan.StockIn), UnitCount: 1, Status: scan.Pending,
				}
			},
			wantUnitCount: 1,
			wantStatus:    scan.Pending,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			queue, _, scope := newTestQueue(t)
			ctx := context.Background()

			broadcaster := &fakeBroadcaster{}
			queue.Broadcaster = broadcaster

			prior := tt.setupPrior(t, queue, ctx, scope)

			created, err := queue.CreateScanEntry(ctx, scope, tt.incoming())
			if err != nil {
				t.Fatalf("CreateScanEntry (incoming scan): %v", err)
			}

			if created.UnitCount != tt.wantUnitCount {
				t.Errorf("UnitCount: want %d, got %d", tt.wantUnitCount, created.UnitCount)
			}
			if created.Status != tt.wantStatus {
				t.Errorf("Status: want %v, got %v", tt.wantStatus, created.Status)
			}

			// The incoming scan must have produced a brand-new row: distinct
			// from every prior entry, and every prior entry must be
			// completely unchanged (it was never touched by an UPDATE).
			for _, p := range prior {
				if created.ID == p.ID {
					t.Fatalf("expected a new row distinct from prior entry %s, got the same entry", p.ID)
				}
				refetched, err := queue.GetScanEntry(ctx, scope, p.ID)
				if err != nil {
					t.Fatalf("GetScanEntry (prior entry %s): %v", p.ID, err)
				}
				if !reflect.DeepEqual(refetched, p) {
					t.Errorf("prior entry %s changed: want %+v, got %+v", p.ID, p, refetched)
				}
			}

			// Broadcaster behavior preserved: exactly one PublishScanEvent
			// call per CreateScanEntry call (setup calls plus the incoming
			// scan), never more, never fewer.
			wantEvents := len(prior) + 1
			if len(broadcaster.scanEvents) != wantEvents {
				t.Errorf("PublishScanEvent calls: want %d, got %d", wantEvents, len(broadcaster.scanEvents))
			}
		})
	}
}

// --------------------------------------------------------------------------
// fakeBroadcaster and Queue publish-behavior tests
// --------------------------------------------------------------------------

// fakeBroadcaster records every PublishScanEvent/PublishInventoryEvent call
// it receives, so tests can assert on Queue's publish behavior without
// depending on internal/events.
type fakeBroadcaster struct {
	scanEvents      []scan.ScanEntry
	inventoryEvents []inventory.InventoryItem
}

func (f *fakeBroadcaster) PublishScanEvent(householdID string, entry scan.ScanEntry) {
	f.scanEvents = append(f.scanEvents, entry)
}

func (f *fakeBroadcaster) PublishInventoryEvent(householdID string, item inventory.InventoryItem) {
	f.inventoryEvents = append(f.inventoryEvents, item)
}

// TestQueueBroadcaster_NilIsSafe locks in that leaving queue.Broadcaster
// unset, as every other test in this file does, is a supported mode: none of
// CreateScanEntry, UpdateScanEntry, or BatchUpdateScanEntries panics or
// changes its return value because of it.
func TestQueueBroadcaster_NilIsSafe(t *testing.T) {
	queue, _, scope := newTestQueue(t)
	ctx := context.Background()

	created, err := queue.CreateScanEntry(ctx, scope, scan.ScanEntry{
		ID:        "scan-1",
		UserID:    "actor",
		Barcode:   "123456789012",
		ScannedAt: time.Now(),
		UnitCount: 1,
	})
	if err != nil {
		t.Fatalf("CreateScanEntry: %v", err)
	}
	if created == nil {
		t.Fatal("CreateScanEntry: expected entry, got nil")
	}

	if err := queue.UpdateScanEntry(ctx, scope, "scan-1", ptrScanDirection(scan.StockIn), nil, nil, nil, nil); err != nil {
		t.Fatalf("UpdateScanEntry: %v", err)
	}

	if err := queue.BatchUpdateScanEntries(ctx, scope, []string{"scan-1"}, ptrScanDirection(scan.StockOut), nil, nil); err != nil {
		t.Fatalf("BatchUpdateScanEntries: %v", err)
	}
}

func TestCreateScanEntry_PublishesScanEvent(t *testing.T) {
	queue, _, scope := newTestQueue(t)
	ctx := context.Background()

	broadcaster := &fakeBroadcaster{}
	queue.Broadcaster = broadcaster

	created, err := queue.CreateScanEntry(ctx, scope, scan.ScanEntry{
		ID:        "scan-1",
		UserID:    "actor",
		Barcode:   "123456789012",
		ScannedAt: time.Now(),
		UnitCount: 1,
	})
	if err != nil {
		t.Fatalf("CreateScanEntry: %v", err)
	}

	if len(broadcaster.scanEvents) != 1 {
		t.Fatalf("expected 1 published scan event, got %d", len(broadcaster.scanEvents))
	}
	if !reflect.DeepEqual(broadcaster.scanEvents[0], *created) {
		t.Errorf("published event: got %+v, want %+v", broadcaster.scanEvents[0], *created)
	}
}

func TestUpdateScanEntry_PublishesScanEvent(t *testing.T) {
	queue, _, scope := newTestQueue(t)
	ctx := context.Background()

	_, err := queue.CreateScanEntry(ctx, scope, scan.ScanEntry{
		ID:        "scan-1",
		UserID:    "actor",
		Barcode:   "123456789012",
		ScannedAt: time.Now(),
		UnitCount: 1,
	})
	if err != nil {
		t.Fatalf("CreateScanEntry: %v", err)
	}

	broadcaster := &fakeBroadcaster{}
	queue.Broadcaster = broadcaster

	if err := queue.UpdateScanEntry(ctx, scope, "scan-1", ptrScanDirection(scan.StockIn), nil, nil, nil, nil); err != nil {
		t.Fatalf("UpdateScanEntry: %v", err)
	}

	want, err := queue.GetScanEntry(ctx, scope, "scan-1")
	if err != nil {
		t.Fatalf("GetScanEntry: %v", err)
	}
	if len(broadcaster.scanEvents) != 1 {
		t.Fatalf("expected 1 published scan event, got %d", len(broadcaster.scanEvents))
	}
	if !reflect.DeepEqual(broadcaster.scanEvents[0], *want) {
		t.Errorf("published event: got %+v, want %+v", broadcaster.scanEvents[0], *want)
	}
}

func TestUpdateScanEntry_UnknownID_NoPublish(t *testing.T) {
	queue, _, scope := newTestQueue(t)
	ctx := context.Background()

	broadcaster := &fakeBroadcaster{}
	queue.Broadcaster = broadcaster

	err := queue.UpdateScanEntry(ctx, scope, "no-such-id", ptrScanDirection(scan.StockIn), nil, nil, nil, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if len(broadcaster.scanEvents) != 0 {
		t.Errorf("expected 0 published scan events, got %d", len(broadcaster.scanEvents))
	}
}

func TestBatchUpdateScanEntriesSkipsMissingIDs(t *testing.T) {
	queue, _, scope := newTestQueue(t)
	ctx := context.Background()
	now := time.Now()
	if _, err := queue.CreateScanEntry(ctx, scope, scan.ScanEntry{
		ID: "scan-keep", UserID: "actor", Barcode: "1",
		ScannedAt: now, UnitCount: 1, Status: scan.Pending,
	}); err != nil {
		t.Fatalf("CreateScanEntry: %v", err)
	}

	broadcaster := &fakeBroadcaster{}
	queue.Broadcaster = broadcaster
	expires := time.Date(2027, 3, 4, 0, 0, 0, 0, time.UTC)
	count := 3
	direction := scan.StockOut
	if err := queue.BatchUpdateScanEntries(ctx, scope, []string{"missing-upd", "scan-keep"}, &direction, &count, &expires); err != nil {
		t.Fatalf("BatchUpdateScanEntries: %v", err)
	}

	got, err := queue.GetScanEntry(ctx, scope, "scan-keep")
	if err != nil {
		t.Fatalf("GetScanEntry: %v", err)
	}
	if got == nil || got.Direction == nil || *got.Direction != scan.StockOut || got.UnitCount != 3 || got.ExpiresAt == nil || !got.ExpiresAt.Equal(expires) {
		t.Fatalf("known card = %+v", got)
	}
	missing, err := queue.GetScanEntry(ctx, scope, "missing-upd")
	if err != nil {
		t.Fatalf("GetScanEntry missing: %v", err)
	}
	if missing != nil {
		t.Fatalf("missing card = %+v", missing)
	}
	if len(broadcaster.scanEvents) != 1 || broadcaster.scanEvents[0].ID != "scan-keep" {
		t.Fatalf("events = %+v", broadcaster.scanEvents)
	}

	broadcaster.scanEvents = nil
	if err := queue.BatchUpdateScanEntries(ctx, scope, []string{"missing-only"}, &direction, nil, nil); err != nil {
		t.Fatalf("unknown-only batch: %v", err)
	}
	if len(broadcaster.scanEvents) != 0 {
		t.Fatalf("unknown-only events = %+v", broadcaster.scanEvents)
	}
}

type cancelAfterPublish struct {
	fakeBroadcaster
	cancel context.CancelFunc
}

func (c *cancelAfterPublish) PublishScanEvent(householdID string, entry scan.ScanEntry) {
	c.fakeBroadcaster.PublishScanEvent(householdID, entry)
	c.cancel()
}

func TestBatchUpdateScanEntriesEventReadFailureStillSaves(t *testing.T) {
	queue, _, scope := newTestQueue(t)
	ctx := context.Background()
	now := time.Now()
	for i, id := range []string{"scan-evt-a", "scan-evt-b"} {
		if _, err := queue.CreateScanEntry(ctx, scope, scan.ScanEntry{
			ID: id, UserID: "actor", Barcode: string(rune('1' + i)),
			ScannedAt: now.Add(time.Duration(i) * time.Minute),
			UnitCount: 1, Status: scan.Pending,
		}); err != nil {
			t.Fatalf("CreateScanEntry %s: %v", id, err)
		}
	}

	updateCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	broadcaster := &cancelAfterPublish{cancel: cancel}
	queue.Broadcaster = broadcaster

	var slogBuf strings.Builder
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&slogBuf, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })

	direction := scan.StockOut
	if err := queue.BatchUpdateScanEntries(updateCtx, scope, []string{"scan-evt-a", "scan-evt-b"}, &direction, nil, nil); err != nil {
		t.Fatalf("saved update reported as failed: %v", err)
	}
	for _, id := range []string{"scan-evt-a", "scan-evt-b"} {
		got, err := queue.GetScanEntry(ctx, scope, id)
		if err != nil {
			t.Fatalf("GetScanEntry %s: %v", id, err)
		}
		if got == nil || got.Direction == nil || *got.Direction != scan.StockOut {
			t.Fatalf("%s = %+v", id, got)
		}
	}
	if len(broadcaster.scanEvents) != 1 || broadcaster.scanEvents[0].ID != "scan-evt-a" {
		t.Fatalf("events = %+v", broadcaster.scanEvents)
	}
	logged := slogBuf.String()
	if strings.Count(logged, "scan update saved but its event was not sent") != 1 || !strings.Contains(logged, "err=") || strings.Contains(logged, "actor") {
		t.Fatalf("log = %q", logged)
	}
}

func TestBatchUpdateScanEntries_PublishesScanEventPerEntry(t *testing.T) {
	queue, _, scope := newTestQueue(t)
	ctx := context.Background()
	now := time.Now()

	ids := []string{"scan-1", "scan-2", "scan-3"}
	for i, id := range ids {
		_, err := queue.CreateScanEntry(ctx, scope, scan.ScanEntry{
			ID:        id,
			UserID:    "actor",
			Barcode:   string(rune('1' + i)),
			ScannedAt: now.Add(time.Duration(i) * time.Minute),
			UnitCount: 1,
		})
		if err != nil {
			t.Fatalf("CreateScanEntry %s: %v", id, err)
		}
	}

	broadcaster := &fakeBroadcaster{}
	queue.Broadcaster = broadcaster

	if err := queue.BatchUpdateScanEntries(ctx, scope, ids, ptrScanDirection(scan.StockIn), nil, nil); err != nil {
		t.Fatalf("BatchUpdateScanEntries: %v", err)
	}

	if len(broadcaster.scanEvents) != len(ids) {
		t.Fatalf("expected %d published scan events, got %d", len(ids), len(broadcaster.scanEvents))
	}

	published := make(map[string]scan.ScanEntry, len(broadcaster.scanEvents))
	for _, e := range broadcaster.scanEvents {
		published[e.ID] = e
	}

	for _, id := range ids {
		want, err := queue.GetScanEntry(ctx, scope, id)
		if err != nil {
			t.Fatalf("GetScanEntry %s: %v", id, err)
		}
		got, ok := published[id]
		if !ok {
			t.Errorf("expected a published event for %s, found none", id)
			continue
		}
		if !reflect.DeepEqual(got, *want) {
			t.Errorf("published event for %s: got %+v, want %+v", id, got, *want)
		}
	}
}

// --------------------------------------------------------------------------
// Helper functions
// --------------------------------------------------------------------------

func ptrScanDirection(d scan.ScanDirection) *scan.ScanDirection {
	return &d
}

func ptrInt(i int) *int {
	return &i
}

func ptrString(s string) *string {
	return &s
}

func ptrScanStatus(s scan.ScanStatus) *scan.ScanStatus {
	return &s
}

// --------------------------------------------------------------------------
// TestCreateScanEntry_MergeDefaultsZeroUnitCountToOne
// --------------------------------------------------------------------------

// TestCreateScanEntry_MergeDefaultsZeroUnitCountToOne is a Task 4.2 unit test
// for scan-duplicate-cards-fix. It locks in that merging a scan with
// UnitCount == 0 (the zero value for an unset field) increments the existing
// entry's unit_count by exactly 1, not 0 — the same default-to-1 convention
// CreateScanEntry already applies on the create path (see NewEntryFromLookup).
func TestCreateScanEntry_MergeDefaultsZeroUnitCountToOne(t *testing.T) {
	queue, _, scope := newTestQueue(t)
	ctx := context.Background()
	now := time.Now()

	existing, err := queue.CreateScanEntry(ctx, scope, scan.ScanEntry{
		UserID: "actor", Barcode: "600000000001", ScannedAt: now,
		Direction: ptrScanDirection(scan.StockIn), UnitCount: 3, Status: scan.Pending,
	})
	if err != nil {
		t.Fatalf("CreateScanEntry (existing entry): %v", err)
	}

	merged, err := queue.CreateScanEntry(ctx, scope, scan.ScanEntry{
		UserID: "actor", Barcode: "600000000001", ScannedAt: now.Add(time.Minute),
		Direction: ptrScanDirection(scan.StockIn), UnitCount: 0, Status: scan.Pending,
	})
	if err != nil {
		t.Fatalf("CreateScanEntry (repeat scan with UnitCount 0): %v", err)
	}

	if merged.ID != existing.ID {
		t.Fatalf("expected merge into existing entry %s, got a different entry %s", existing.ID, merged.ID)
	}
	if merged.UnitCount != existing.UnitCount+1 {
		t.Errorf("UnitCount: want %d (existing %d + default delta 1), got %d", existing.UnitCount+1, existing.UnitCount, merged.UnitCount)
	}
}

// --------------------------------------------------------------------------
// TestCreateScanEntry_MergePublishesOneEventPerCall
// --------------------------------------------------------------------------

// TestCreateScanEntry_MergePublishesOneEventPerCall is a Task 6.3 integration
// test for scan-duplicate-cards-fix. It confirms that a merge broadcasts
// exactly once per CreateScanEntry call rather than once per row: two
// CreateScanEntry calls for the same user/barcode/direction (the second
// merging into the first) must produce exactly two PublishScanEvent calls
// total, and the second call's published entry must carry the incremented
// UnitCount.
func TestCreateScanEntry_MergePublishesOneEventPerCall(t *testing.T) {
	queue, _, scope := newTestQueue(t)
	ctx := context.Background()
	now := time.Now()

	broadcaster := &fakeBroadcaster{}
	queue.Broadcaster = broadcaster

	first, err := queue.CreateScanEntry(ctx, scope, scan.ScanEntry{
		UserID: "actor", Barcode: "700000000001", ScannedAt: now,
		Direction: ptrScanDirection(scan.StockIn), UnitCount: 1, Status: scan.Pending,
	})
	if err != nil {
		t.Fatalf("CreateScanEntry (first scan): %v", err)
	}

	second, err := queue.CreateScanEntry(ctx, scope, scan.ScanEntry{
		UserID: "actor", Barcode: "700000000001", ScannedAt: now.Add(time.Minute),
		Direction: ptrScanDirection(scan.StockIn), UnitCount: 1, Status: scan.Pending,
	})
	if err != nil {
		t.Fatalf("CreateScanEntry (repeat scan): %v", err)
	}

	if second.ID != first.ID {
		t.Fatalf("expected repeat scan to merge into %s, got a different entry %s", first.ID, second.ID)
	}

	if len(broadcaster.scanEvents) != 2 {
		t.Fatalf("PublishScanEvent calls: want 2 (one per CreateScanEntry call), got %d: %+v", len(broadcaster.scanEvents), broadcaster.scanEvents)
	}
	if broadcaster.scanEvents[1].UnitCount != second.UnitCount {
		t.Errorf("second published event UnitCount: want %d (incremented), got %d", second.UnitCount, broadcaster.scanEvents[1].UnitCount)
	}
	if broadcaster.scanEvents[1].UnitCount != first.UnitCount+1 {
		t.Errorf("second published event UnitCount: want %d (first UnitCount %d + 1), got %d", first.UnitCount+1, first.UnitCount, broadcaster.scanEvents[1].UnitCount)
	}
	if !broadcaster.scanEvents[1].ScannedAt.Equal(second.ScannedAt) || !broadcaster.scanEvents[1].ScannedAt.Equal(now.Add(time.Minute)) {
		t.Errorf("second published event ScannedAt: want rescan time %v, got %v", second.ScannedAt, broadcaster.scanEvents[1].ScannedAt)
	}
}

// TestCreateScanEntry_RescanBecomesMostRecent lists two open cards and
// rescans the older one. scanned_at is the queue's activity time, so the
// rescanned row becomes the newest. ListScanEntries returns oldest-first,
// which puts that barcode last, the top of the newest-first queue. The
// published event carries the rescan time, and the count increments once.
// An older incoming timestamp does not move the card backward.
func TestCreateScanEntry_RescanBecomesMostRecent(t *testing.T) {
	queue, _, scope := newTestQueue(t)
	ctx := context.Background()
	t0 := time.Now().Add(-2 * time.Minute).UTC().Truncate(time.Millisecond)
	t1 := t0.Add(time.Minute)
	t2 := t0.Add(2 * time.Minute)

	broadcaster := &fakeBroadcaster{}
	queue.Broadcaster = broadcaster

	older, err := queue.CreateScanEntry(ctx, scope, scan.ScanEntry{
		UserID: "actor", Barcode: "111111111111", ScannedAt: t0,
		Direction: ptrScanDirection(scan.StockIn), UnitCount: 1, Status: scan.Pending,
	})
	if err != nil {
		t.Fatalf("CreateScanEntry (older): %v", err)
	}
	if _, err := queue.CreateScanEntry(ctx, scope, scan.ScanEntry{
		UserID: "actor", Barcode: "222222222222", ScannedAt: t1,
		Direction: ptrScanDirection(scan.StockIn), UnitCount: 1, Status: scan.Pending,
	}); err != nil {
		t.Fatalf("CreateScanEntry (newer): %v", err)
	}

	rescanned, err := queue.CreateScanEntry(ctx, scope, scan.ScanEntry{
		UserID: "actor", Barcode: "111111111111", ScannedAt: t2,
		Direction: ptrScanDirection(scan.StockIn), UnitCount: 1, Status: scan.Pending,
	})
	if err != nil {
		t.Fatalf("CreateScanEntry (rescan): %v", err)
	}
	if rescanned.ID != older.ID {
		t.Fatalf("rescan id: want %s, got %s", older.ID, rescanned.ID)
	}
	if rescanned.UnitCount != 2 {
		t.Errorf("UnitCount: want 2, got %d", rescanned.UnitCount)
	}
	if !rescanned.ScannedAt.Equal(t2) {
		t.Errorf("ScannedAt: want rescan time %v, got %v", t2, rescanned.ScannedAt)
	}

	entries, err := queue.ListScanEntries(ctx, scope, scan.Pending)
	if err != nil {
		t.Fatalf("ListScanEntries: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("want 2 entries, got %d: %+v", len(entries), entries)
	}
	// The list is oldest-activity first. The rescanned card is last, which
	// is the top of the newest-first queue.
	if entries[0].Barcode != "222222222222" || entries[1].Barcode != "111111111111" {
		t.Fatalf("oldest-first list: want newer barcode then rescanned barcode, got %s then %s", entries[0].Barcode, entries[1].Barcode)
	}
	if !entries[1].ScannedAt.Equal(t2) {
		t.Errorf("listed rescanned ScannedAt: want %v, got %v", t2, entries[1].ScannedAt)
	}

	published := broadcaster.scanEvents[len(broadcaster.scanEvents)-1]
	if published.ID != older.ID || published.UnitCount != 2 || !published.ScannedAt.Equal(t2) {
		t.Errorf("published rescan: want id %s unitCount 2 scannedAt %v, got id %s unitCount %d scannedAt %v",
			older.ID, t2, published.ID, published.UnitCount, published.ScannedAt)
	}

	held, err := queue.CreateScanEntry(ctx, scope, scan.ScanEntry{
		UserID: "actor", Barcode: "111111111111", ScannedAt: t0,
		Direction: ptrScanDirection(scan.StockIn), UnitCount: 1, Status: scan.Pending,
	})
	if err != nil {
		t.Fatalf("CreateScanEntry (older timestamp): %v", err)
	}
	if !held.ScannedAt.Equal(t2) {
		t.Errorf("older rescan timestamp: want scanned_at held at %v, got %v", t2, held.ScannedAt)
	}
	if held.UnitCount != 3 {
		t.Errorf("UnitCount after older timestamp: want 3, got %d", held.UnitCount)
	}
}

func TestCreateScanEntryMovesTheScansCursor(t *testing.T) {
	queue, db, scope := newTestQueue(t)
	ctx := context.Background()
	before, err := changes.Current(ctx, db, scope.HouseholdID(), changes.ScopeScans)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := queue.CreateScanEntry(ctx, scope, scan.ScanEntry{
		UserID: "actor", Barcode: "123456789012", ScannedAt: time.Now().UTC(), UnitCount: 1,
	}); err != nil {
		t.Fatal(err)
	}
	out, err := changes.Compare(ctx, db, scope.HouseholdID(), changes.ScopeScans, before)
	if err != nil {
		t.Fatal(err)
	}
	if !out.Changed || out.Cursor == before {
		t.Fatalf("scans cursor did not move: before %s after %+v", before, out)
	}
}
