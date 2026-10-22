package product

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/Rhionin/stockbot/internal/app"
)

// setupTestDB opens an in-memory SQLite database, applies all migrations, and returns the connection.
func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	conn, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("open in-memory SQLite: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := app.RunMigrations(conn); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}
	return conn
}

// mockOpenFoodFacts creates a mock Open Food Facts API with the given lookup function.
type mockOpenFoodFacts struct {
	lookupFn func(ctx context.Context, barcode string) (*ProductSummary, error)
}

func (m mockOpenFoodFacts) LookupBarcode(ctx context.Context, barcode string) (*ProductSummary, error) {
	return m.lookupFn(ctx, barcode)
}

// Lookup implements the Upstream interface by calling the mock's lookup function
// and converting the result to a FanOutResult.
func (m mockOpenFoodFacts) Lookup(ctx context.Context, barcode string) FanOutResult {
	product, err := m.lookupFn(ctx, barcode)
	if err != nil {
		if errors.Is(err, ErrProductNotFound) {
			return FanOutResult{Outcome: FanOutConfirmedMiss}
		}
		// Other errors (network, timeout) are treated as unresolved.
		return FanOutResult{Outcome: FanOutUnresolved}
	}
	return FanOutResult{
		Outcome: FanOutHit,
		Product: product,
		Source:  ExternalSourceOpenFoodFacts,
	}
}

// LookupIn implements the Upstream interface by delegating to LookupBarcode.
func (m mockOpenFoodFacts) LookupIn(ctx context.Context, source ExternalSource, barcode string) (*ProductSummary, error) {
	return m.LookupBarcode(ctx, barcode)
}

// TestLookupService verifies the three-tier lookup behavior using table-driven tests.
func TestLookupService(t *testing.T) {
	tests := []struct {
		name              string
		setupDB           func(t *testing.T, catalog *Catalog)
		openFoodFacts     func(ctx context.Context, barcode string) (*ProductSummary, error)
		barcode           string
		userID            string
		expectedFound     bool
		expectedSource    string
		expectedProductID string
		errMsg            string
	}{
		{
			name: "user override takes precedence over global",
			setupDB: func(t *testing.T, catalog *Catalog) {
				global := Product{ID: "prod-global", Name: "Global Product"}
				override := Product{ID: "prod-override", Name: "Override Product"}
				if err := catalog.CreateProduct(context.Background(), global); err != nil {
					t.Fatal(err)
				}
				if err := catalog.CreateProduct(context.Background(), override); err != nil {
					t.Fatal(err)
				}
				if err := catalog.UpsertBarcodeMapping(context.Background(), "12345", global.ID, "global", ""); err != nil {
					t.Fatal(err)
				}
				if err := catalog.UpsertBarcodeMapping(context.Background(), "12345", override.ID, "user_override", "user-1"); err != nil {
					t.Fatal(err)
				}
			},
			openFoodFacts: func(ctx context.Context, barcode string) (*ProductSummary, error) {
				return nil, ErrProductNotFound
			},
			barcode:           "12345",
			userID:            "user-1",
			expectedFound:     true,
			expectedSource:    "global",
			expectedProductID: "prod-override",
		},
		{
			name: "global DB used when no user override exists",
			setupDB: func(t *testing.T, catalog *Catalog) {
				product := Product{ID: "prod-1", Name: "Milk"}
				if err := catalog.CreateProduct(context.Background(), product); err != nil {
					t.Fatal(err)
				}
				if err := catalog.UpsertBarcodeMapping(context.Background(), "11111", product.ID, "global", ""); err != nil {
					t.Fatal(err)
				}
			},
			openFoodFacts: func(ctx context.Context, barcode string) (*ProductSummary, error) {
				return nil, ErrProductNotFound
			},
			barcode:           "11111",
			userID:            "user-1",
			expectedFound:     true,
			expectedSource:    "global",
			expectedProductID: "prod-1",
		},
		{
			name:    "external API fallback when no DB match",
			setupDB: func(t *testing.T, catalog *Catalog) {},
			openFoodFacts: func(ctx context.Context, barcode string) (*ProductSummary, error) {
				if barcode == "99999" {
					return &ProductSummary{ID: "99999", Name: "External Product", Category: "Food"}, nil
				}
				return nil, ErrProductNotFound
			},
			barcode:           "99999",
			userID:            "user-1",
			expectedFound:     true,
			expectedSource:    "external",
			expectedProductID: "99999",
		},
		{
			name:    "not found in any tier",
			setupDB: func(t *testing.T, catalog *Catalog) {},
			openFoodFacts: func(ctx context.Context, barcode string) (*ProductSummary, error) {
				return nil, ErrProductNotFound
			},
			barcode:       "00000",
			userID:        "user-1",
			expectedFound: false,
		},
		{
			name:    "external API error treated as not found",
			setupDB: func(t *testing.T, catalog *Catalog) {},
			openFoodFacts: func(ctx context.Context, barcode string) (*ProductSummary, error) {
				return nil, errors.New("network timeout")
			},
			barcode:       "88888",
			userID:        "user-1",
			expectedFound: false,
		},
		{
			name:    "empty barcode returns error",
			setupDB: func(t *testing.T, catalog *Catalog) {},
			openFoodFacts: func(ctx context.Context, barcode string) (*ProductSummary, error) {
				return nil, ErrProductNotFound
			},
			barcode: "",
			userID:  "user-1",
			errMsg:  "barcode cannot be empty",
		},
		{
			name:    "empty userID returns error",
			setupDB: func(t *testing.T, catalog *Catalog) {},
			openFoodFacts: func(ctx context.Context, barcode string) (*ProductSummary, error) {
				return nil, ErrProductNotFound
			},
			barcode: "12345",
			userID:  "",
			errMsg:  "userID cannot be empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := setupTestDB(t)
			catalog := NewCatalog(db)
			tt.setupDB(t, catalog)

			service := &LookupService{
				Catalog:  catalog,
				Upstream: mockOpenFoodFacts{lookupFn: tt.openFoodFacts},
			}
			actual, err := service.Lookup(context.Background(), tt.barcode, tt.userID)

			// Check error
			if tt.errMsg != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.errMsg)
				}
				if !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("error = %q, expected to contain %q", err.Error(), tt.errMsg)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			// Check found status
			if actual.IsFound() != tt.expectedFound {
				t.Errorf("IsFound() = %v, expected %v", actual.IsFound(), tt.expectedFound)
			}

			// Check product details if found
			if tt.expectedFound {
				if actual.Product == nil {
					t.Fatal("Product is nil, expected non-nil")
				}
				if actual.Product.ID != tt.expectedProductID {
					t.Errorf("Product.ID = %q, expected %q", actual.Product.ID, tt.expectedProductID)
				}
				if actual.Source != tt.expectedSource {
					t.Errorf("Source = %q, expected %q", actual.Source, tt.expectedSource)
				}
			} else {
				if actual.Product != nil {
					t.Errorf("Product = %v, expected nil", actual.Product)
				}
			}
		})
	}
}

// TestLookupService_MissGate tests the confirmed-miss gate behavior.
func TestLookupService_MissGate(t *testing.T) {
	tests := []struct {
		name          string
		setupDB       func(t *testing.T, catalog *Catalog, now time.Time)
		upstream      func(ctx context.Context, barcode string) FanOutResult
		missBarcode   string
		missTime      time.Time
		missTTL       time.Duration
		barcode       string
		userID        string
		expectedFound bool
	}{
		{
			name: "confirmed miss suppresses external lookup within TTL",
			setupDB: func(t *testing.T, catalog *Catalog, now time.Time) {
				if err := catalog.RecordBarcodeMiss(context.Background(), "miss-barcode", now); err != nil {
					t.Fatal(err)
				}
			},
			upstream: func(ctx context.Context, barcode string) FanOutResult {
				// Upstream should not be called
				t.Errorf("upstream.Lookup called for barcode %q, should have been suppressed by miss gate", barcode)
				return FanOutResult{Outcome: FanOutUnresolved}
			},
			missBarcode:   "miss-barcode",
			missTime:      time.Unix(1000, 0),
			missTTL:       1 * time.Hour,
			barcode:       "miss-barcode",
			userID:        "user-1",
			expectedFound: false,
		},
		{
			name: "expired confirmed miss allows external lookup",
			setupDB: func(t *testing.T, catalog *Catalog, now time.Time) {
				// Record a miss from 2 hours ago, but TTL is 1 hour
				pastTime := now.Add(-2 * time.Hour)
				if err := catalog.RecordBarcodeMiss(context.Background(), "expired-miss", pastTime); err != nil {
					t.Fatal(err)
				}
			},
			upstream: func(ctx context.Context, barcode string) FanOutResult {
				if barcode == "expired-miss" {
					return FanOutResult{
						Outcome: FanOutHit,
						Product: &ProductSummary{ID: "expired-miss", Name: "Found", Category: "Food"},
						Source:  ExternalSourceOpenFoodFacts,
					}
				}
				return FanOutResult{Outcome: FanOutUnresolved}
			},
			missBarcode:   "expired-miss",
			missTime:      time.Unix(1000, 0),
			missTTL:       1 * time.Hour,
			barcode:       "expired-miss",
			userID:        "user-1",
			expectedFound: true,
		},
		{
			name:    "no miss gate entry allows external lookup",
			setupDB: func(t *testing.T, catalog *Catalog, now time.Time) {},
			upstream: func(ctx context.Context, barcode string) FanOutResult {
				if barcode == "new-barcode" {
					return FanOutResult{
						Outcome: FanOutHit,
						Product: &ProductSummary{ID: "new-barcode", Name: "Found", Category: "Food"},
						Source:  ExternalSourceOpenFoodFacts,
					}
				}
				return FanOutResult{Outcome: FanOutUnresolved}
			},
			missBarcode:   "",
			missTime:      time.Time{},
			missTTL:       1 * time.Hour,
			barcode:       "new-barcode",
			userID:        "user-1",
			expectedFound: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := setupTestDB(t)
			catalog := NewCatalog(db)
			now := time.Unix(2000, 0)
			tt.setupDB(t, catalog, now)

			service := &LookupService{
				Catalog:  catalog,
				Upstream: mockUpstream{fn: tt.upstream},
				Now:      func() time.Time { return now },
				MissTTL:  tt.missTTL,
			}
			actual, err := service.Lookup(context.Background(), tt.barcode, tt.userID)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if actual.IsFound() != tt.expectedFound {
				t.Errorf("IsFound() = %v, expected %v", actual.IsFound(), tt.expectedFound)
			}
		})
	}
}

// TestLookupService_PersistExternalProduct tests persistence of external products.
func TestLookupService_PersistExternalProduct(t *testing.T) {
	db := setupTestDB(t)
	catalog := NewCatalog(db)

	service := &LookupService{
		Catalog:  catalog,
		Upstream: mockOpenFoodFacts{lookupFn: func(ctx context.Context, barcode string) (*ProductSummary, error) { return nil, nil }},
	}

	product := &ProductSummary{
		ID:       "test-barcode",
		Name:     "Test Product",
		Category: "Food",
	}

	err := service.persistExternalProduct(context.Background(), product, ExternalSourceOpenFoodFacts)
	if err != nil {
		t.Fatalf("persistExternalProduct failed: %v", err)
	}

	// Verify the product was created
	created, err := catalog.GetProductByID(context.Background(), "test-barcode")
	if err != nil {
		t.Fatalf("GetProductByID failed: %v", err)
	}
	if created == nil {
		t.Fatal("expected product to be created")
	}
	if created.ExternalSource != ExternalSourceOpenFoodFacts {
		t.Errorf("ExternalSource = %q, expected %q", created.ExternalSource, ExternalSourceOpenFoodFacts)
	}

	// Verify the barcode mapping was created
	mapped, err := catalog.LookupByBarcode(context.Background(), "test-barcode", "")
	if err != nil {
		t.Fatalf("LookupByBarcode failed: %v", err)
	}
	if mapped == nil {
		t.Fatal("expected barcode mapping to be created")
	}
	if mapped.ID != "test-barcode" {
		t.Errorf("mapped product ID = %q, expected %q", mapped.ID, "test-barcode")
	}
}

// TestLookupService_DeleteMissOnPersist tests that confirmed misses are deleted when a barcode resolves.
func TestLookupService_DeleteMissOnPersist(t *testing.T) {
	db := setupTestDB(t)
	catalog := NewCatalog(db)

	barcode := "test-barcode"
	now := time.Now()

	// Record a miss
	if err := catalog.RecordBarcodeMiss(context.Background(), barcode, now); err != nil {
		t.Fatal(err)
	}

	// Verify it was recorded
	miss, err := catalog.GetBarcodeMiss(context.Background(), barcode)
	if err != nil {
		t.Fatal(err)
	}
	if miss == nil {
		t.Fatal("expected miss to be recorded")
	}

	service := &LookupService{
		Catalog:  catalog,
		Upstream: mockOpenFoodFacts{lookupFn: func(ctx context.Context, barcode string) (*ProductSummary, error) { return nil, nil }},
	}

	product := &ProductSummary{
		ID:   barcode,
		Name: "Test Product",
	}

	// Persist the external product
	err = service.persistExternalProduct(context.Background(), product, ExternalSourceOpenFoodFacts)
	if err != nil {
		t.Fatalf("persistExternalProduct failed: %v", err)
	}

	// Verify the miss was deleted
	miss, err = catalog.GetBarcodeMiss(context.Background(), barcode)
	if err != nil {
		t.Fatal(err)
	}
	if miss != nil {
		t.Error("expected miss to be deleted after product is persisted")
	}
}

// mockUpstream is a simple mock implementing the Upstream interface.
type mockUpstream struct {
	fn func(ctx context.Context, barcode string) FanOutResult
}

func (m mockUpstream) Lookup(ctx context.Context, barcode string) FanOutResult {
	return m.fn(ctx, barcode)
}

// TestLookupService_ConfirmedMissRecording tests that confirmed misses are recorded properly.
func TestLookupService_ConfirmedMissRecording(t *testing.T) {
	db := setupTestDB(t)
	catalog := NewCatalog(db)

	now := time.Unix(2000, 0)

	service := &LookupService{
		Catalog: catalog,
		Upstream: mockUpstream{fn: func(ctx context.Context, barcode string) FanOutResult {
			return FanOutResult{Outcome: FanOutConfirmedMiss}
		}},
		Now:     func() time.Time { return now },
		MissTTL: 1 * time.Hour,
	}

	// Lookup with confirmed miss outcome
	result, err := service.Lookup(context.Background(), "test-barcode", "user-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.IsFound() {
		t.Error("expected not found for confirmed miss")
	}

	// Verify the miss was recorded
	miss, err := catalog.GetBarcodeMiss(context.Background(), "test-barcode")
	if err != nil {
		t.Fatalf("GetBarcodeMiss failed: %v", err)
	}
	if miss == nil {
		t.Fatal("expected miss to be recorded")
	}
	if !miss.Equal(now) {
		t.Errorf("miss time = %v, expected %v", miss, now)
	}
}

// TestLookupService_UnresolvedNotRecorded tests that unresolved lookups don't record a miss.
func TestLookupService_UnresolvedNotRecorded(t *testing.T) {
	db := setupTestDB(t)
	catalog := NewCatalog(db)

	now := time.Unix(2000, 0)

	service := &LookupService{
		Catalog: catalog,
		Upstream: mockUpstream{fn: func(ctx context.Context, barcode string) FanOutResult {
			return FanOutResult{Outcome: FanOutUnresolved}
		}},
		Now:     func() time.Time { return now },
		MissTTL: 1 * time.Hour,
	}

	// Lookup with unresolved outcome
	result, err := service.Lookup(context.Background(), "test-barcode", "user-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.IsFound() {
		t.Error("expected not found for unresolved")
	}

	// Verify the miss was NOT recorded
	miss, err := catalog.GetBarcodeMiss(context.Background(), "test-barcode")
	if err != nil {
		t.Fatalf("GetBarcodeMiss failed: %v", err)
	}
	if miss != nil {
		t.Error("expected no miss to be recorded for unresolved outcome")
	}
}

// TestLookupService_MissTTLDefault tests the default MissTTL value.
func TestLookupService_MissTTLDefault(t *testing.T) {
	db := setupTestDB(t)
	catalog := NewCatalog(db)

	now := time.Unix(2000, 0)
	// Record a miss from 8 days ago, which is older than the default 7-day TTL
	missTime := now.Add(-8 * 24 * time.Hour)

	if err := catalog.RecordBarcodeMiss(context.Background(), "test-barcode", missTime); err != nil {
		t.Fatal(err)
	}

	service := &LookupService{
		Catalog: catalog,
		Upstream: mockUpstream{fn: func(ctx context.Context, barcode string) FanOutResult {
			return FanOutResult{
				Outcome: FanOutHit,
				Product: &ProductSummary{ID: "test-barcode", Name: "Found"},
				Source:  ExternalSourceOpenFoodFacts,
			}
		}},
		Now:     func() time.Time { return now },
		MissTTL: 0, // Will default to defaultMissTTL (7 days)
	}

	// Lookup should skip the expired miss and query upstream
	result, err := service.Lookup(context.Background(), "test-barcode", "user-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.IsFound() {
		t.Error("expected to find product from upstream after expired miss")
	}
}

// TestLookupService_RefreshScheduled tests that refresher is called for found products.
func TestLookupService_RefreshScheduled(t *testing.T) {
	db := setupTestDB(t)
	catalog := NewCatalog(db)

	// Create a product in the database
	product := Product{ID: "refresh-test", Name: "Test"}
	if err := catalog.CreateProduct(context.Background(), product); err != nil {
		t.Fatal(err)
	}

	// Add a barcode mapping
	if err := catalog.UpsertBarcodeMapping(context.Background(), "refresh-barcode", product.ID, "global", ""); err != nil {
		t.Fatal(err)
	}

	refreshCalled := false
	refreshProductID := ""

	refresher := mockRefresher{
		scheduleFn: func(ctx context.Context, productID string) {
			refreshCalled = true
			refreshProductID = productID
		},
	}

	service := &LookupService{
		Catalog:   catalog,
		Upstream:  mockOpenFoodFacts{lookupFn: func(ctx context.Context, barcode string) (*ProductSummary, error) { return nil, nil }},
		Refresher: refresher,
	}

	// Lookup should find the product and schedule refresh
	result, err := service.Lookup(context.Background(), "refresh-barcode", "user-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.IsFound() {
		t.Error("expected to find product")
	}

	if !refreshCalled {
		t.Error("expected refresher.ScheduleRefresh to be called")
	}

	if refreshProductID != product.ID {
		t.Errorf("refresher called with product ID %q, expected %q", refreshProductID, product.ID)
	}
}

// mockRefresher is a simple mock implementing the Refresher interface.
type mockRefresher struct {
	scheduleFn func(ctx context.Context, productID string)
}

func (m mockRefresher) ScheduleRefresh(ctx context.Context, productID string) {
	m.scheduleFn(ctx, productID)
}
