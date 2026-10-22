package product_test

import (
	"context"
	"testing"
	"time"

	_ "modernc.org/sqlite"
	"pgregory.net/rapid"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/product"
)

// newTestCatalog opens an in-memory SQLite database, applies all migrations, and returns a product Catalog.
func newTestCatalog(t *testing.T) *product.Catalog {
	t.Helper()
	conn, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("open in-memory SQLite: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := app.RunMigrations(conn); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}
	return product.NewCatalog(conn)
}

// --------------------------------------------------------------------------
// TestCreateProduct
// --------------------------------------------------------------------------

func TestCreateProduct(t *testing.T) {
	tests := []struct {
		name          string
		id            string
		product       product.Product
		wantIDEmpty   bool
		wantRoundtrip bool
	}{
		{
			name: "explicit ID persisted",
			id:   "prod-1",
			product: product.Product{
				ID: "prod-1", Name: "Whole Milk", Category: "Dairy", UnitOfMeasure: "gallon",
				ImageURL: "https://images.openfoodfacts.org/milk.jpg",
			},
			wantIDEmpty:   false,
			wantRoundtrip: true,
		},
		{
			name:          "generate UUID when ID empty",
			id:            "",
			product:       product.Product{Name: "Bread"},
			wantIDEmpty:   true,
			wantRoundtrip: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			catalog := newTestCatalog(t)
			ctx := context.Background()

			// Create product
			if err := catalog.CreateProduct(ctx, tt.product); err != nil {
				t.Fatalf("CreateProduct: %v", err)
			}

			// If UUID generation test, retrieve from list and verify ID was generated
			if tt.wantIDEmpty {
				products, err := catalog.ListProducts(ctx, "user-1")
				if err != nil {
					t.Fatalf("ListProducts: %v", err)
				}
				if len(products) != 1 {
					t.Fatalf("expected 1 product, got %d", len(products))
				}
				if products[0].ID == "" {
					t.Error("expected generated UUID, got empty string")
				}
				return
			}

			// Roundtrip test: verify GetProductByID retrieves the exact product
			if tt.wantRoundtrip {
				got, err := catalog.GetProductByID(ctx, tt.id)
				if err != nil {
					t.Fatalf("GetProductByID: %v", err)
				}
				if got == nil {
					t.Fatal("expected product, got nil")
				}
				if got.ID != tt.product.ID {
					t.Errorf("ID: want %q, got %q", tt.product.ID, got.ID)
				}
				if got.Name != tt.product.Name {
					t.Errorf("Name: want %q, got %q", tt.product.Name, got.Name)
				}
				if got.Category != tt.product.Category {
					t.Errorf("Category: want %q, got %q", tt.product.Category, got.Category)
				}
				if got.UnitOfMeasure != tt.product.UnitOfMeasure {
					t.Errorf("UnitOfMeasure: want %q, got %q", tt.product.UnitOfMeasure, got.UnitOfMeasure)
				}
				if got.ImageURL != tt.product.ImageURL {
					t.Errorf("ImageURL: want %q, got %q", tt.product.ImageURL, got.ImageURL)
				}
			}
		})
	}
}

// --------------------------------------------------------------------------
// TestGetProductByID
// --------------------------------------------------------------------------

func TestGetProductByID(t *testing.T) {
	tests := []struct {
		name        string
		id          string
		setup       func(t *testing.T, catalog *product.Catalog, ctx context.Context)
		expectFound bool
	}{
		{
			name: "found",
			id:   "prod-1",
			setup: func(t *testing.T, catalog *product.Catalog, ctx context.Context) {
				p := product.Product{ID: "prod-1", Name: "Whole Milk", Category: "Dairy"}
				if err := catalog.CreateProduct(ctx, p); err != nil {
					t.Fatalf("CreateProduct: %v", err)
				}
			},
			expectFound: true,
		},
		{
			name:        "not found",
			id:          "no-such-id",
			setup:       func(t *testing.T, catalog *product.Catalog, ctx context.Context) {},
			expectFound: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			catalog := newTestCatalog(t)
			ctx := context.Background()
			tt.setup(t, catalog, ctx)

			got, err := catalog.GetProductByID(ctx, tt.id)
			if err != nil {
				t.Fatalf("GetProductByID: %v", err)
			}
			if tt.expectFound && got == nil {
				t.Fatal("expected product, got nil")
			}
			if !tt.expectFound && got != nil {
				t.Errorf("expected nil, got %+v", got)
			}
		})
	}
}

// --------------------------------------------------------------------------
// TestListProducts
// --------------------------------------------------------------------------

func TestListProducts(t *testing.T) {
	tests := []struct {
		name          string
		productsToAdd []product.Product
		expectCount   int
		expectOrder   []string // product names in expected order
	}{
		{
			name:          "empty",
			productsToAdd: []product.Product{},
			expectCount:   0,
			expectOrder:   []string{},
		},
		{
			name: "single product",
			productsToAdd: []product.Product{
				{ID: "p1", Name: "Apple Juice"},
			},
			expectCount: 1,
			expectOrder: []string{"Apple Juice"},
		},
		{
			name: "multiple products ordered by name",
			productsToAdd: []product.Product{
				{ID: "p1", Name: "Apple Juice"},
				{ID: "p2", Name: "Butter"},
				{ID: "p3", Name: "Cheese", Category: "Dairy"},
			},
			expectCount: 3,
			expectOrder: []string{"Apple Juice", "Butter", "Cheese"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			catalog := newTestCatalog(t)
			ctx := context.Background()

			// Add products
			for _, p := range tt.productsToAdd {
				if err := catalog.CreateProduct(ctx, p); err != nil {
					t.Fatalf("CreateProduct %q: %v", p.Name, err)
				}
			}

			// List and verify
			got, err := catalog.ListProducts(ctx, "user-1")
			if err != nil {
				t.Fatalf("ListProducts: %v", err)
			}
			if len(got) != tt.expectCount {
				t.Fatalf("expected %d products, got %d", tt.expectCount, len(got))
			}

			// Verify order by name
			for i, wantName := range tt.expectOrder {
				if got[i].Name != wantName {
					t.Errorf("[%d] Name: want %q, got %q", i, wantName, got[i].Name)
				}
			}
		})
	}
}

// --------------------------------------------------------------------------
// TestUpdateProduct
// --------------------------------------------------------------------------

func TestUpdateProduct(t *testing.T) {
	tests := []struct {
		name            string
		id              string
		originalProd    *product.Product // nil means don't create
		updatedProd     product.Product
		expectError     bool
		expectNameAfter string
	}{
		{
			name: "found and updated",
			id:   "p1",
			originalProd: &product.Product{
				ID:            "p1",
				Name:          "Whole Milk",
				Category:      "Dairy",
				UnitOfMeasure: "gallon",
				ImageURL:      "https://images.openfoodfacts.org/old.jpg",
			},
			updatedProd: product.Product{
				ID:            "p1",
				Name:          "Skim Milk",
				Category:      "Dairy",
				UnitOfMeasure: "half-gallon",
				ImageURL:      "https://images.openfoodfacts.org/new.jpg",
			},
			expectError:     false,
			expectNameAfter: "Skim Milk",
		},
		{
			name:            "not found",
			id:              "ghost",
			originalProd:    nil,
			updatedProd:     product.Product{ID: "ghost", Name: "Ghost Product"},
			expectError:     true,
			expectNameAfter: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			catalog := newTestCatalog(t)
			ctx := context.Background()

			// Setup
			if tt.originalProd != nil {
				if err := catalog.CreateProduct(ctx, *tt.originalProd); err != nil {
					t.Fatalf("CreateProduct: %v", err)
				}
			}

			// Update
			err := catalog.UpdateProduct(ctx, tt.updatedProd)
			if tt.expectError && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !tt.expectError && err != nil {
				t.Fatalf("UpdateProduct: %v", err)
			}

			// Verify if successful
			if !tt.expectError {
				got, err := catalog.GetProductByID(ctx, tt.id)
				if err != nil {
					t.Fatalf("GetProductByID: %v", err)
				}
				if got.Name != tt.expectNameAfter {
					t.Errorf("Name: want %q, got %q", tt.expectNameAfter, got.Name)
				}
				if got.UnitOfMeasure != tt.updatedProd.UnitOfMeasure {
					t.Errorf("UnitOfMeasure: want %q, got %q", tt.updatedProd.UnitOfMeasure, got.UnitOfMeasure)
				}
				if got.ImageURL != tt.updatedProd.ImageURL {
					t.Errorf("ImageURL: want %q, got %q", tt.updatedProd.ImageURL, got.ImageURL)
				}
			}
		})
	}
}

// --------------------------------------------------------------------------
// TestUpsertBarcodeMapping
// --------------------------------------------------------------------------

func TestUpsertBarcodeMapping(t *testing.T) {
	tests := []struct {
		name           string
		barcode        string
		productID1     string
		productID2     string // for replace test
		expectIDAfter1 string
		expectIDAfter2 string // for replace test
	}{
		{
			name:           "insert new mapping",
			barcode:        "012345678901",
			productID1:     "p1",
			productID2:     "",
			expectIDAfter1: "p1",
			expectIDAfter2: "",
		},
		{
			name:           "replace existing mapping",
			barcode:        "012345678901",
			productID1:     "p1",
			productID2:     "p2",
			expectIDAfter1: "p1",
			expectIDAfter2: "p2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			catalog := newTestCatalog(t)
			ctx := context.Background()

			// Create products
			p1 := product.Product{ID: "p1", Name: "Product One"}
			p2 := product.Product{ID: "p2", Name: "Product Two"}
			for _, p := range []product.Product{p1, p2} {
				if err := catalog.CreateProduct(ctx, p); err != nil {
					t.Fatalf("CreateProduct: %v", err)
				}
			}

			// Insert mapping
			if err := catalog.UpsertBarcodeMapping(ctx, tt.barcode, tt.productID1, "global", ""); err != nil {
				t.Fatalf("UpsertBarcodeMapping (insert): %v", err)
			}

			single, err := catalog.LookupByBarcode(ctx, tt.barcode, "user-123")
			if err != nil {
				t.Fatalf("LookupByBarcode: %v", err)
			}
			if single == nil || single.ID != tt.expectIDAfter1 {
				t.Fatalf("after insert: expected %s, got %v", tt.expectIDAfter1, single)
			}

			// If we're testing replace, do it now
			if tt.productID2 != "" {
				if err := catalog.UpsertBarcodeMapping(ctx, tt.barcode, tt.productID2, "global", ""); err != nil {
					t.Fatalf("UpsertBarcodeMapping (replace): %v", err)
				}

				single, err = catalog.LookupByBarcode(ctx, tt.barcode, "user-123")
				if err != nil {
					t.Fatalf("LookupByBarcode after replace: %v", err)
				}
				if single == nil || single.ID != tt.expectIDAfter2 {
					t.Fatalf("after replace: expected %s, got %v", tt.expectIDAfter2, single)
				}
			}
		})
	}
}

// --------------------------------------------------------------------------
// TestLookupByBarcode
// --------------------------------------------------------------------------

func TestLookupByBarcode(t *testing.T) {
	tests := []struct {
		name          string
		barcode       string
		userID        string
		setupProducts []product.Product
		setupMappings []struct{ barcode, productID, source, userID string }
		expectSingle  *string // nil means expect nil single, otherwise product ID
	}{
		{
			name:          "no match",
			barcode:       "000000000000",
			userID:        "user-1",
			setupProducts: []product.Product{},
			setupMappings: []struct{ barcode, productID, source, userID string }{},
			expectSingle:  nil,
		},
		{
			name:    "user override takes precedence over global",
			barcode: "111222333444",
			userID:  "user-abc",
			setupProducts: []product.Product{
				{ID: "global-prod", Name: "Global Product"},
				{ID: "override-prod", Name: "Override Product"},
			},
			setupMappings: []struct{ barcode, productID, source, userID string }{
				{"111222333444", "global-prod", "global", ""},
				{"111222333444", "override-prod", "user_override", "user-abc"},
			},
			expectSingle: stringPtr("override-prod"),
		},
		{
			name:    "different user sees global (not another user's override)",
			barcode: "555666777888",
			userID:  "user-B",
			setupProducts: []product.Product{
				{ID: "global-prod", Name: "Global Product"},
				{ID: "override-prod", Name: "Override Product"},
			},
			setupMappings: []struct{ barcode, productID, source, userID string }{
				{"555666777888", "global-prod", "global", ""},
				{"555666777888", "override-prod", "user_override", "user-A"},
			},
			expectSingle: stringPtr("global-prod"),
		},
		{
			name:    "global-only lookup",
			barcode: "999888777666",
			userID:  "user-xyz",
			setupProducts: []product.Product{
				{ID: "p1", Name: "Product A", ImageURL: "https://images.openfoodfacts.org/a.jpg"},
			},
			setupMappings: []struct{ barcode, productID, source, userID string }{
				{"999888777666", "p1", "global", ""},
			},
			expectSingle: stringPtr("p1"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			catalog := newTestCatalog(t)
			ctx := context.Background()

			// Create products
			for _, p := range tt.setupProducts {
				if err := catalog.CreateProduct(ctx, p); err != nil {
					t.Fatalf("CreateProduct: %v", err)
				}
			}

			// Create mappings
			for _, m := range tt.setupMappings {
				if err := catalog.UpsertBarcodeMapping(ctx, m.barcode, m.productID, m.source, m.userID); err != nil {
					t.Fatalf("UpsertBarcodeMapping: %v", err)
				}
			}

			// Lookup
			single, err := catalog.LookupByBarcode(ctx, tt.barcode, tt.userID)
			if err != nil {
				t.Fatalf("LookupByBarcode: %v", err)
			}

			// Verify single result
			if tt.expectSingle == nil {
				if single != nil {
					t.Errorf("expected nil single, got %v", single)
				}
			} else {
				if single == nil {
					t.Fatal("expected single result, got nil")
				}
				if single.ID != *tt.expectSingle {
					t.Errorf("expected single ID %q, got %q", *tt.expectSingle, single.ID)
				}
				if tt.name == "global-only lookup" && single.ImageURL != "https://images.openfoodfacts.org/a.jpg" {
					t.Errorf("expected ImageURL to round-trip through LookupByBarcode, got %q", single.ImageURL)
				}
			}
		})
	}
}

// stringPtr is a helper to convert a string to *string for table-driven tests.
func stringPtr(s string) *string {
	return &s
}

// --------------------------------------------------------------------------
// TestCreateProductSourceValidation
// --------------------------------------------------------------------------

// Feature: product-cache-freshness, Property 2: Writes record provenance
//
// Validates: Requirements 1.7
//
// CreateProduct stores the requested Source when it is "external" or "user",
// defaults an empty Source to SourceUser, and rejects any other value. The
// product API never submits an invalid source; this is the only guard on the
// {'external','user'} constraint SQLite cannot enforce via ALTER TABLE.
func TestCreateProductSourceValidation(t *testing.T) {
	tests := []struct {
		name       string
		source     string
		wantErr    bool
		wantStored string
	}{
		{
			name:       "empty source defaults to user",
			source:     "",
			wantStored: product.SourceUser,
		},
		{
			name:       "external source accepted",
			source:     product.SourceExternal,
			wantStored: product.SourceExternal,
		},
		{
			name:       "user source accepted",
			source:     product.SourceUser,
			wantStored: product.SourceUser,
		},
		{
			name:    "invalid source rejected",
			source:  "bogus",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			catalog := newTestCatalog(t)
			ctx := context.Background()

			p := product.Product{ID: "p1", Name: "Test Product", Source: tt.source}
			err := catalog.CreateProduct(ctx, p)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("CreateProduct: %v", err)
			}

			got, err := catalog.GetProductByID(ctx, "p1")
			if err != nil {
				t.Fatalf("GetProductByID: %v", err)
			}
			if got == nil {
				t.Fatal("expected product, got nil")
			}
			if got.Source != tt.wantStored {
				t.Errorf("Source: want %q, got %q", tt.wantStored, got.Source)
			}
		})
	}
}

// --------------------------------------------------------------------------
// TestExternalSourceValidation
// --------------------------------------------------------------------------

func TestExternalSourceValidation(t *testing.T) {
	tests := []struct {
		name             string
		externalSource   product.ExternalSource
		shouldBeAccepted bool
	}{
		{
			name:             "empty is valid",
			externalSource:   "",
			shouldBeAccepted: true,
		},
		{
			name:             "openfoodfacts is valid",
			externalSource:   product.ExternalSourceOpenFoodFacts,
			shouldBeAccepted: true,
		},
		{
			name:             "openproductsfacts is valid",
			externalSource:   product.ExternalSourceOpenProductsFacts,
			shouldBeAccepted: true,
		},
		{
			name:             "openbeautyfacts is valid",
			externalSource:   product.ExternalSourceOpenBeautyFacts,
			shouldBeAccepted: true,
		},
		{
			name:             "openpetfoodfacts is valid",
			externalSource:   product.ExternalSourceOpenPetFoodFacts,
			shouldBeAccepted: true,
		},
		{
			name:             "invalid external source rejected",
			externalSource:   product.ExternalSource("invalid"),
			shouldBeAccepted: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			catalog := newTestCatalog(t)
			ctx := context.Background()

			p := product.Product{
				ID:             "test-prod",
				Name:           "Test Product",
				Source:         product.SourceExternal,
				ExternalSource: tt.externalSource,
			}

			err := catalog.CreateProduct(ctx, p)
			if tt.shouldBeAccepted {
				if err != nil {
					t.Fatalf("expected CreateProduct to succeed, got error: %v", err)
				}

				got, err := catalog.GetProductByID(ctx, "test-prod")
				if err != nil {
					t.Fatalf("GetProductByID: %v", err)
				}
				if got == nil {
					t.Fatal("expected product, got nil")
				}
				if got.ExternalSource != tt.externalSource {
					t.Errorf("ExternalSource: want %q, got %q", tt.externalSource, got.ExternalSource)
				}
			} else {
				if err == nil {
					t.Fatal("expected CreateProduct to fail, got nil")
				}
			}
		})
	}
}

// --------------------------------------------------------------------------
//
// Feature: open-products-facts-lookup, Property 9: Only the four values are storable
//
// **Validates: Requirements 3.7**
//
// Only the empty string, "openfoodfacts", "openproductsfacts", "openbeautyfacts",
// and "openpetfoodfacts" are acceptable external_source values. Any string that is
// neither empty nor one of those four causes CreateProduct to fail and write no row.
func TestProperty9_OnlyFourValuesAreStorable(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate strings that are NOT empty and NOT one of the four valid values
		invalidSource := rapid.StringMatching(`[a-z0-9]{1,20}`).Filter(func(s string) bool {
			// Filter out the four valid values
			return s != string(product.ExternalSourceOpenFoodFacts) &&
				s != string(product.ExternalSourceOpenProductsFacts) &&
				s != string(product.ExternalSourceOpenBeautyFacts) &&
				s != string(product.ExternalSourceOpenPetFoodFacts)
		}).Draw(rt, "invalidSource")

		catalog := newTestCatalog(t)
		ctx := context.Background()

		p := product.Product{
			ID:             "test-invalid-source",
			Name:           "Test Product",
			Source:         product.SourceExternal,
			ExternalSource: product.ExternalSource(invalidSource),
		}

		// CreateProduct should reject this invalid external_source
		err := catalog.CreateProduct(ctx, p)
		if err == nil {
			t.Fatal("expected CreateProduct to reject invalid external_source, got nil")
		}

		// Verify no row was written
		got, err := catalog.GetProductByID(ctx, "test-invalid-source")
		if err != nil {
			t.Fatalf("GetProductByID: %v", err)
		}
		if got != nil {
			t.Fatal("expected no product row to be written, but found one")
		}
	})
}

// --------------------------------------------------------------------------
// TestRecordBarcodeMiss
// --------------------------------------------------------------------------

func TestRecordBarcodeMiss(t *testing.T) {
	tests := []struct {
		name string
		test func(t *testing.T, catalog *product.Catalog, ctx context.Context)
	}{
		{
			name: "record new miss",
			test: func(t *testing.T, catalog *product.Catalog, ctx context.Context) {
				barcode := "123456789"
				checkedAt := mustParseTime(t, "2025-01-01T10:00:00Z")

				err := catalog.RecordBarcodeMiss(ctx, barcode, checkedAt)
				if err != nil {
					t.Fatalf("RecordBarcodeMiss: %v", err)
				}

				got, err := catalog.GetBarcodeMiss(ctx, barcode)
				if err != nil {
					t.Fatalf("GetBarcodeMiss: %v", err)
				}
				if got == nil {
					t.Fatal("expected miss record, got nil")
				}
				if got.Unix() != checkedAt.Unix() {
					t.Errorf("checked_at: want %v, got %v", checkedAt, *got)
				}
			},
		},
		{
			name: "re-stamp existing miss",
			test: func(t *testing.T, catalog *product.Catalog, ctx context.Context) {
				barcode := "987654321"
				t1 := mustParseTime(t, "2025-01-01T10:00:00Z")
				t2 := mustParseTime(t, "2025-01-02T10:00:00Z")

				// Record the initial miss
				err := catalog.RecordBarcodeMiss(ctx, barcode, t1)
				if err != nil {
					t.Fatalf("RecordBarcodeMiss (first): %v", err)
				}

				// Re-stamp with a new time
				err = catalog.RecordBarcodeMiss(ctx, barcode, t2)
				if err != nil {
					t.Fatalf("RecordBarcodeMiss (second): %v", err)
				}

				// Verify the checked_at is updated
				got, err := catalog.GetBarcodeMiss(ctx, barcode)
				if err != nil {
					t.Fatalf("GetBarcodeMiss: %v", err)
				}
				if got == nil {
					t.Fatal("expected miss record, got nil")
				}
				if got.Unix() != t2.Unix() {
					t.Errorf("checked_at: want %v, got %v", t2, *got)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			catalog := newTestCatalog(t)
			ctx := context.Background()
			tt.test(t, catalog, ctx)
		})
	}
}

// --------------------------------------------------------------------------
// TestGetBarcodeMiss
// --------------------------------------------------------------------------

func TestGetBarcodeMiss(t *testing.T) {
	tests := []struct {
		name        string
		setup       func(t *testing.T, catalog *product.Catalog, ctx context.Context)
		barcode     string
		expectFound bool
	}{
		{
			name: "found",
			setup: func(t *testing.T, catalog *product.Catalog, ctx context.Context) {
				checkedAt := mustParseTime(t, "2025-01-01T10:00:00Z")
				if err := catalog.RecordBarcodeMiss(ctx, "111222333", checkedAt); err != nil {
					t.Fatalf("RecordBarcodeMiss: %v", err)
				}
			},
			barcode:     "111222333",
			expectFound: true,
		},
		{
			name:        "not found",
			setup:       func(t *testing.T, catalog *product.Catalog, ctx context.Context) {},
			barcode:     "no-such-barcode",
			expectFound: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			catalog := newTestCatalog(t)
			ctx := context.Background()
			tt.setup(t, catalog, ctx)

			got, err := catalog.GetBarcodeMiss(ctx, tt.barcode)
			if err != nil {
				t.Fatalf("GetBarcodeMiss: %v", err)
			}

			if tt.expectFound {
				if got == nil {
					t.Fatal("expected miss record, got nil")
				}
			} else {
				if got != nil {
					t.Fatal("expected nil, got miss record")
				}
			}
		})
	}
}

// --------------------------------------------------------------------------
// TestDeleteBarcodeMiss
// --------------------------------------------------------------------------

func TestDeleteBarcodeMiss(t *testing.T) {
	tests := []struct {
		name string
		test func(t *testing.T, catalog *product.Catalog, ctx context.Context)
	}{
		{
			name: "delete existing miss",
			test: func(t *testing.T, catalog *product.Catalog, ctx context.Context) {
				barcode := "555666777"
				checkedAt := mustParseTime(t, "2025-01-01T10:00:00Z")

				// Record a miss
				err := catalog.RecordBarcodeMiss(ctx, barcode, checkedAt)
				if err != nil {
					t.Fatalf("RecordBarcodeMiss: %v", err)
				}

				// Verify it exists
				got, err := catalog.GetBarcodeMiss(ctx, barcode)
				if err != nil {
					t.Fatalf("GetBarcodeMiss (before delete): %v", err)
				}
				if got == nil {
					t.Fatal("expected miss record before delete")
				}

				// Delete it
				err = catalog.DeleteBarcodeMiss(ctx, barcode)
				if err != nil {
					t.Fatalf("DeleteBarcodeMiss: %v", err)
				}

				// Verify it's gone
				got, err = catalog.GetBarcodeMiss(ctx, barcode)
				if err != nil {
					t.Fatalf("GetBarcodeMiss (after delete): %v", err)
				}
				if got != nil {
					t.Fatal("expected nil after delete")
				}
			},
		},
		{
			name: "delete non-existent miss returns no error",
			test: func(t *testing.T, catalog *product.Catalog, ctx context.Context) {
				// Deleting a non-existent barcode should not error
				err := catalog.DeleteBarcodeMiss(ctx, "no-such-barcode")
				if err != nil {
					t.Fatalf("DeleteBarcodeMiss: %v", err)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			catalog := newTestCatalog(t)
			ctx := context.Background()
			tt.test(t, catalog, ctx)
		})
	}
}

// mustParseTime parses a time string in RFC3339 format and panics on error.
func mustParseTime(t *testing.T, s string) time.Time {
	t.Helper()
	tm, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("mustParseTime: %v", err)
	}
	return tm
}

// --------------------------------------------------------------------------
//
// Feature: open-products-facts-lookup, Property 5: Confirmed misses behave correctly
//
// **Validates: Requirements 5.1, 5.5, 5.7**
//
// RecordBarcodeMiss, GetBarcodeMiss, and DeleteBarcodeMiss form the complete
// lifecycle for confirmed misses. RecordBarcodeMiss both inserts a new miss
// and updates an existing one. GetBarcodeMiss returns the record when present
// and nil when absent. DeleteBarcodeMiss removes the record. barcode_misses
// never appears in queries of products, inventory, or scans, so its data never
// leaks into responses.
func TestProperty_BarcodeMissLifecycle(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		catalog := newTestCatalog(t)
		ctx := context.Background()

		// Generate a random barcode
		barcode := rapid.StringMatching(`[0-9]{1,20}`).Draw(rt, "barcode")

		// Initially no miss record
		miss, err := catalog.GetBarcodeMiss(ctx, barcode)
		if err != nil {
			t.Fatalf("GetBarcodeMiss (initial): %v", err)
		}
		if miss != nil {
			t.Fatal("expected no miss record initially")
		}

		// Record first miss
		t1 := time.Now()
		if err := catalog.RecordBarcodeMiss(ctx, barcode, t1); err != nil {
			t.Fatalf("RecordBarcodeMiss (first): %v", err)
		}

		// Verify it's recorded
		miss, err = catalog.GetBarcodeMiss(ctx, barcode)
		if err != nil {
			t.Fatalf("GetBarcodeMiss (after first record): %v", err)
		}
		if miss == nil {
			t.Fatal("expected miss record after RecordBarcodeMiss")
		}
		if miss.Unix() != t1.Unix() {
			t.Errorf("checked_at after first record: want %v, got %v", t1, *miss)
		}

		// Re-stamp with a newer time
		t2 := t1.Add(time.Hour)
		if err := catalog.RecordBarcodeMiss(ctx, barcode, t2); err != nil {
			t.Fatalf("RecordBarcodeMiss (second): %v", err)
		}

		// Verify it's updated
		miss, err = catalog.GetBarcodeMiss(ctx, barcode)
		if err != nil {
			t.Fatalf("GetBarcodeMiss (after second record): %v", err)
		}
		if miss == nil {
			t.Fatal("expected miss record after second RecordBarcodeMiss")
		}
		if miss.Unix() != t2.Unix() {
			t.Errorf("checked_at after re-stamp: want %v, got %v", t2, *miss)
		}

		// Delete the miss
		if err := catalog.DeleteBarcodeMiss(ctx, barcode); err != nil {
			t.Fatalf("DeleteBarcodeMiss: %v", err)
		}

		// Verify it's gone
		miss, err = catalog.GetBarcodeMiss(ctx, barcode)
		if err != nil {
			t.Fatalf("GetBarcodeMiss (after delete): %v", err)
		}
		if miss != nil {
			t.Fatal("expected no miss record after DeleteBarcodeMiss")
		}

		// Deleting non-existent miss should not error
		if err := catalog.DeleteBarcodeMiss(ctx, "nonexistent-"+barcode); err != nil {
			t.Fatalf("DeleteBarcodeMiss (nonexistent): %v", err)
		}
	})
}

// Property: Confirmed misses are invisible to products queries
// **Validates: Requirement 5.7**
// A barcode with a recorded confirmed miss should not appear in the products
// list or in a barcode lookup.
func TestProperty_MissesNotInProductQueries(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		catalog := newTestCatalog(t)
		ctx := context.Background()

		// Record a confirmed miss for a barcode
		missBarcode := rapid.StringMatching(`[0-9]{10}`).Draw(rt, "missBarcode")
		now := time.Now()
		if err := catalog.RecordBarcodeMiss(ctx, missBarcode, now); err != nil {
			t.Fatalf("RecordBarcodeMiss: %v", err)
		}

		// Query should not return a product for the miss barcode
		productSummary, err := catalog.LookupByBarcode(ctx, missBarcode, "")
		if err != nil {
			t.Fatalf("LookupByBarcode: %v", err)
		}
		if productSummary != nil {
			t.Fatal("LookupByBarcode should not return a product for a miss barcode")
		}

		// Get the list of products - should still be empty
		products, err := catalog.ListProducts(ctx, "user-1")
		if err != nil {
			t.Fatalf("ListProducts: %v", err)
		}

		// Verify no product with the miss barcode is present
		for _, p := range products {
			if p.ID == missBarcode {
				t.Fatal("ListProducts should not include a product with a miss barcode")
			}
		}

		// Create a real product and verify it shows up
		realBarcode := rapid.StringMatching(`[0-9]{10}`).Draw(rt, "realBarcode")
		realProduct := product.Product{
			ID:   realBarcode,
			Name: "Test Product",
		}
		if err := catalog.CreateProduct(ctx, realProduct); err != nil {
			t.Fatalf("CreateProduct: %v", err)
		}

		// Map the barcode to the product
		if err := catalog.UpsertBarcodeMapping(ctx, realBarcode, realBarcode, "global", ""); err != nil {
			t.Fatalf("UpsertBarcodeMapping: %v", err)
		}

		// Now LookupByBarcode should find the real product
		found, err := catalog.LookupByBarcode(ctx, realBarcode, "")
		if err != nil {
			t.Fatalf("LookupByBarcode (real): %v", err)
		}
		if found == nil {
			t.Fatal("LookupByBarcode should find the real product")
		}
		if found.ID != realBarcode {
			t.Errorf("LookupByBarcode returned wrong product: want %s, got %s", realBarcode, found.ID)
		}
	})
}
