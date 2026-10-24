package product

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"

	"pgregory.net/rapid"

	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/Rhionin/stockbot/internal/tenant"
)

func propScope(tb rapidTB, db *sql.DB) tenant.Scope {
	scope, err := auth.PrimaryScope(context.Background(), db)
	if err != nil {
		tb.Fatalf("primary household: %v", err)
	}
	return scope
}

// errNetwork simulates a non-not-found external error (e.g. a timeout), which
// LookupService treats as a not-found result.
var errNetwork = errors.New("network timeout")

// Feature: external-product-persistence, Property 2: Preservation — Non-External Resolutions Unchanged
// **Validates: Requirements 3.1, 3.2, 3.3**
//
// For any resolution where the bug condition does NOT hold (user override, global,
// or not-found), the fixed code SHALL produce the same LookupResult as the original,
// SHALL NOT create any duplicate products or barcodes rows, and SHALL leave the
// inventory view identical to the original.
//
// These tests follow the observation-first methodology: they encode the behavior
// observed on the UNFIXED code for non-bug-condition inputs so that the persistence
// fix (which only touches the Tier-3 external branch) cannot regress it. All cases
// here MUST PASS on unfixed code.

// rowCounts holds the products/barcodes table sizes used to assert that a
// non-external lookup persists nothing.
type rowCounts struct {
	products int
	barcodes int
}

// countRows returns the current products and barcodes row counts.
func countRows(t rapidTB, db *sql.DB) rowCounts {
	ctx := context.Background()
	var c rowCounts
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM products`).Scan(&c.products); err != nil {
		t.Fatalf("count products: %v", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM barcodes`).Scan(&c.barcodes); err != nil {
		t.Fatalf("count barcodes: %v", err)
	}
	return c
}

// rapidTB is the subset of testing helpers shared by *testing.T and *rapid.T
// that these helpers rely on.
type rapidTB interface {
	Fatalf(format string, args ...any)
}

// notFoundLookup is a fake external client that never resolves a barcode.
var notFoundLookup = mockOpenFoodFacts{lookupFn: func(ctx context.Context, barcode string) (*ProductSummary, error) {
	return nil, ErrProductNotFound
}}

// TestProperty2_UserOverridePreservation locks in Tier-1 behavior: a seeded user
// override resolves to that override, and the lookup persists nothing.
//
// Confirms Req 3.1.
func TestProperty2_UserOverridePreservation(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		db := setupTestDB(t)
		catalog := NewCatalog(db)
		ctx := context.Background()

		barcode := rapid.StringMatching(`[0-9]{6,13}`).Draw(rt, "barcode")
		userID := rapid.StringMatching(`user-[a-z0-9]{1,8}`).Draw(rt, "userID")
		overrideID := rapid.StringMatching(`override-[a-z0-9]{1,8}`).Draw(rt, "overrideID")
		overrideName := rapid.StringMatching(`[A-Za-z ]{1,20}`).Draw(rt, "overrideName")

		// Seed a user override (and an unrelated global entry it must outrank).
		globalID := overrideID + "-global"
		if err := catalog.CreateProduct(ctx, Product{ID: globalID, Name: overrideName + " Global"}); err != nil {
			rt.Fatalf("CreateProduct global: %v", err)
		}
		if err := catalog.CreateProduct(ctx, Product{ID: overrideID, Name: overrideName}); err != nil {
			rt.Fatalf("CreateProduct override: %v", err)
		}
		if err := catalog.UpsertBarcodeMapping(ctx, barcode, globalID, "global", ""); err != nil {
			rt.Fatalf("UpsertBarcodeMapping global: %v", err)
		}
		if err := catalog.UpsertBarcodeMapping(ctx, barcode, overrideID, "user_override", userID); err != nil {
			rt.Fatalf("UpsertBarcodeMapping override: %v", err)
		}

		before := countRows(rt, db)

		service := &LookupService{Catalog: catalog, Upstream: notFoundLookup}
		result, err := service.Lookup(ctx, propScope(rt, db), barcode)
		if err != nil {
			rt.Fatalf("Lookup: %v", err)
		}

		// The override product is returned (Tier 1 outranks the global entry).
		if !result.IsFound() {
			rt.Fatalf("expected override found, got not-found")
		}
		if result.Product.ID != overrideID {
			rt.Fatalf("Product.ID: want %q, got %q", overrideID, result.Product.ID)
		}
		if result.Source != "global" {
			rt.Fatalf("Source: want %q (observed baseline), got %q", "global", result.Source)
		}

		// No rows created by the lookup.
		after := countRows(rt, db)
		if after != before {
			rt.Fatalf("row counts changed by lookup: before=%+v after=%+v", before, after)
		}
	})
}

// TestProperty2_GlobalPreservation locks in Tier-2 behavior: a seeded global
// product + mapping resolves to that product, creating no new rows.
//
// Confirms Req 3.2.
func TestProperty2_GlobalPreservation(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		db := setupTestDB(t)
		catalog := NewCatalog(db)
		ctx := context.Background()

		barcode := rapid.StringMatching(`[0-9]{6,13}`).Draw(rt, "barcode")
		_ = rapid.StringMatching(`user-[a-z0-9]{1,8}`).Draw(rt, "userID")
		globalID := rapid.StringMatching(`global-[a-z0-9]{1,8}`).Draw(rt, "globalID")
		globalName := rapid.StringMatching(`[A-Za-z ]{1,20}`).Draw(rt, "globalName")

		if err := catalog.CreateProduct(ctx, Product{ID: globalID, Name: globalName, Source: SourceExternal}); err != nil {
			rt.Fatalf("CreateProduct: %v", err)
		}
		if err := catalog.UpsertBarcodeMapping(ctx, barcode, globalID, "global", ""); err != nil {
			rt.Fatalf("UpsertBarcodeMapping: %v", err)
		}

		before := countRows(rt, db)

		service := &LookupService{Catalog: catalog, Upstream: notFoundLookup}
		result, err := service.Lookup(ctx, propScope(rt, db), barcode)
		if err != nil {
			rt.Fatalf("Lookup: %v", err)
		}

		if !result.IsFound() {
			rt.Fatalf("expected global product found, got not-found")
		}
		if result.Product.ID != globalID {
			rt.Fatalf("Product.ID: want %q, got %q", globalID, result.Product.ID)
		}
		if result.Source != "global" {
			rt.Fatalf("Source: want %q, got %q", "global", result.Source)
		}

		after := countRows(rt, db)
		if after != before {
			rt.Fatalf("row counts changed by lookup: before=%+v after=%+v", before, after)
		}
	})
}

// TestProperty2_NotFoundPreservation locks in the not-found / external-error
// behavior: the lookup returns a not-found result and persists nothing.
//
// Confirms Req 3.3.
func TestProperty2_NotFoundPreservation(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		db := setupTestDB(t)
		catalog := NewCatalog(db)
		ctx := context.Background()

		barcode := rapid.StringMatching(`[0-9]{6,13}`).Draw(rt, "barcode")
		_ = rapid.StringMatching(`user-[a-z0-9]{1,8}`).Draw(rt, "userID")

		// The external client either reports not-found or errors; both paths
		// yield a not-found LookupResult on unfixed code.
		external := rapid.SampledFrom([]mockOpenFoodFacts{
			{lookupFn: func(ctx context.Context, barcode string) (*ProductSummary, error) {
				return nil, ErrProductNotFound
			}},
			{lookupFn: func(ctx context.Context, barcode string) (*ProductSummary, error) {
				return nil, errNetwork
			}},
		}).Draw(rt, "external")

		before := countRows(rt, db)

		service := &LookupService{Catalog: catalog, Upstream: external}
		result, err := service.Lookup(ctx, propScope(rt, db), barcode)
		if err != nil {
			rt.Fatalf("Lookup: %v", err)
		}

		if result.IsFound() {
			rt.Fatalf("expected not-found, got product %+v", result.Product)
		}
		if result.Source != "" {
			rt.Fatalf("Source: want empty for not-found, got %q", result.Source)
		}

		after := countRows(rt, db)
		if after != before {
			rt.Fatalf("row counts changed by not-found lookup: before=%+v after=%+v", before, after)
		}
	})
}

// TestProperty2_ResultEquality asserts that across generated override / global /
// not-found scenarios, the returned LookupResult (Product + Source) matches the
// baseline observed by re-running the same lookup — the result is stable and the
// non-external branches remain deterministic.
//
// Confirms Property 2 (Result equality) across Req 3.1, 3.2, 3.3.
func TestProperty2_ResultEquality(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		db := setupTestDB(t)
		catalog := NewCatalog(db)
		ctx := context.Background()

		scenario := rapid.SampledFrom([]string{"override", "global", "not_found"}).Draw(rt, "scenario")
		barcode := rapid.StringMatching(`[0-9]{6,13}`).Draw(rt, "barcode")
		userID := rapid.StringMatching(`user-[a-z0-9]{1,8}`).Draw(rt, "userID")

		var (
			wantFound     bool
			wantProductID string
			wantSource    string
		)

		switch scenario {
		case "override":
			overrideID := "override-" + barcode
			if err := catalog.CreateProduct(ctx, Product{ID: overrideID, Name: "Override"}); err != nil {
				rt.Fatalf("CreateProduct: %v", err)
			}
			if err := catalog.UpsertBarcodeMapping(ctx, barcode, overrideID, "user_override", userID); err != nil {
				rt.Fatalf("UpsertBarcodeMapping: %v", err)
			}
			wantFound, wantProductID, wantSource = true, overrideID, "global"
		case "global":
			globalID := "global-" + barcode
			if err := catalog.CreateProduct(ctx, Product{ID: globalID, Name: "Global", Source: SourceExternal}); err != nil {
				rt.Fatalf("CreateProduct: %v", err)
			}
			if err := catalog.UpsertBarcodeMapping(ctx, barcode, globalID, "global", ""); err != nil {
				rt.Fatalf("UpsertBarcodeMapping: %v", err)
			}
			wantFound, wantProductID, wantSource = true, globalID, "global"
		case "not_found":
			wantFound, wantProductID, wantSource = false, "", ""
		}

		service := &LookupService{Catalog: catalog, Upstream: notFoundLookup}

		// First observation is the baseline; a repeat must produce an identical result.
		first, err := service.Lookup(ctx, propScope(rt, db), barcode)
		if err != nil {
			rt.Fatalf("Lookup (first): %v", err)
		}
		second, err := service.Lookup(ctx, propScope(rt, db), barcode)
		if err != nil {
			rt.Fatalf("Lookup (second): %v", err)
		}

		assertResultEqual(rt, first, second)

		if first.IsFound() != wantFound {
			rt.Fatalf("IsFound: want %v, got %v", wantFound, first.IsFound())
		}
		if first.Source != wantSource {
			rt.Fatalf("Source: want %q, got %q", wantSource, first.Source)
		}
		if wantFound && first.Product.ID != wantProductID {
			rt.Fatalf("Product.ID: want %q, got %q", wantProductID, first.Product.ID)
		}
	})
}

// assertResultEqual fails if two LookupResults differ in found-ness, source, or product.
func assertResultEqual(t rapidTB, a, b LookupResult) {
	if a.IsFound() != b.IsFound() {
		t.Fatalf("IsFound differs: %v vs %v", a.IsFound(), b.IsFound())
	}
	if a.Source != b.Source {
		t.Fatalf("Source differs: %q vs %q", a.Source, b.Source)
	}
	if a.Product == nil || b.Product == nil {
		if a.Product != b.Product {
			t.Fatalf("Product presence differs: %v vs %v", a.Product, b.Product)
		}
		return
	}
	if !reflect.DeepEqual(*a.Product, *b.Product) {
		t.Fatalf("Product differs: %+v vs %+v", *a.Product, *b.Product)
	}
}

// Feature: external-product-persistence, Property 3: Idempotency — Repeated External Resolution Creates No Duplicates
// **Validates: Requirements 2.1**
//
// For any barcode resolved externally two or more times, the persistence path
// SHALL create at most one products row and one global barcodes mapping for that
// barcode/source/user scope (respecting UNIQUE (barcode, source, user_id)), and
// SHALL return the same product ID each time.
//
// This is logic not reachable through the HTTP API: the API resolves a barcode at
// Tier 2 on the second scan (the first scan persisted a global mapping), so the
// repeated Tier-3 persistence path can only be exercised at the package level by
// looping Lookup against an external client that always resolves the barcode.

// TestProperty3_ExternalPersistenceIdempotent looks up a generated external-only
// barcode N>=2 times and asserts a single products row, a single global barcodes
// mapping, and a stable returned product ID.
func TestProperty3_ExternalPersistenceIdempotent(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		db := setupTestDB(t)
		catalog := NewCatalog(db)
		ctx := context.Background()

		barcode := rapid.StringMatching(`[0-9]{6,13}`).Draw(rt, "barcode")
		name := rapid.StringMatching(`[A-Za-z ]{1,20}`).Draw(rt, "name")
		_ = rapid.StringMatching(`user-[a-z0-9]{1,8}`).Draw(rt, "userID")
		n := rapid.IntRange(2, 5).Draw(rt, "lookups")

		// The external client resolves this barcode to a fixed product every time.
		external := mockOpenFoodFacts{lookupFn: func(ctx context.Context, b string) (*ProductSummary, error) {
			if b == barcode {
				return &ProductSummary{ID: barcode, Name: name, Category: "Food"}, nil
			}
			return nil, ErrProductNotFound
		}}

		service := &LookupService{Catalog: catalog, Upstream: external}

		var firstID string
		for i := 0; i < n; i++ {
			result, err := service.Lookup(ctx, propScope(rt, db), barcode)
			if err != nil {
				rt.Fatalf("Lookup #%d: %v", i, err)
			}
			if !result.IsFound() {
				rt.Fatalf("Lookup #%d: expected found, got not-found", i)
			}
			if result.Product.ID != barcode {
				rt.Fatalf("Lookup #%d: Product.ID want %q, got %q", i, barcode, result.Product.ID)
			}
			if i == 0 {
				firstID = result.Product.ID
			} else if result.Product.ID != firstID {
				rt.Fatalf("Lookup #%d: returned ID %q differs from first %q", i, result.Product.ID, firstID)
			}
		}

		// Exactly one products row for this barcode/ID.
		var productRows int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM products WHERE id = ?`, barcode).Scan(&productRows); err != nil {
			rt.Fatalf("count products: %v", err)
		}
		if productRows != 1 {
			rt.Fatalf("products rows for %q: want 1, got %d", barcode, productRows)
		}

		// Exactly one global barcodes mapping for this barcode/source/user scope.
		var barcodeRows int
		if err := db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM barcodes WHERE barcode = ? AND source = 'global' AND user_id = ''`,
			barcode,
		).Scan(&barcodeRows); err != nil {
			rt.Fatalf("count barcodes: %v", err)
		}
		if barcodeRows != 1 {
			rt.Fatalf("global barcode mappings for %q: want 1, got %d", barcode, barcodeRows)
		}

		// The persisted product is retrievable by ID with the resolved name.
		stored, err := catalog.GetProductByID(ctx, barcode)
		if err != nil {
			rt.Fatalf("GetProductByID: %v", err)
		}
		if stored == nil {
			rt.Fatalf("GetProductByID(%q): want product, got nil", barcode)
		}
		if stored.Name != name {
			rt.Fatalf("stored product name: want %q, got %q", name, stored.Name)
		}

		// A subsequent barcode lookup now resolves at Tier 2 as a global product.
		summary, err := catalog.LookupByBarcode(ctx, propScope(rt, db), barcode)
		if err != nil {
			rt.Fatalf("LookupByBarcode: %v", err)
		}
		if summary == nil {
			rt.Fatalf("LookupByBarcode(%q): want global product, got nil", barcode)
		}
		if summary.ID != barcode {
			rt.Fatalf("LookupByBarcode ID: want %q, got %q", barcode, summary.ID)
		}
	})
}

// TestExternalLookupPersistsImageURL locks in that persistExternalProduct
// copies ImageURL from the OpenFoodFacts-resolved ProductSummary into the
// persisted Product row. Regression test for a bug where the CreateProduct
// call in persistExternalProduct omitted ImageURL, so every OpenFoodFacts
// thumbnail was silently dropped on the very first (persisting) scan.
func TestExternalLookupPersistsImageURL(t *testing.T) {
	db := setupTestDB(t)
	catalog := NewCatalog(db)
	ctx := context.Background()

	const barcode = "012345678905"
	const wantImageURL = "https://images.openfoodfacts.org/thumb.jpg"

	external := mockOpenFoodFacts{lookupFn: func(ctx context.Context, b string) (*ProductSummary, error) {
		return &ProductSummary{ID: barcode, Name: "Coca-Cola", Category: "Beverages", ImageURL: wantImageURL}, nil
	}}

	service := &LookupService{Catalog: catalog, Upstream: external}
	result, err := service.Lookup(ctx, propScope(t, db), barcode)
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if !result.IsFound() || result.Product.ImageURL != wantImageURL {
		t.Fatalf("Lookup result: want ImageURL %q, got %+v", wantImageURL, result.Product)
	}

	stored, err := catalog.GetProductByID(ctx, barcode)
	if err != nil {
		t.Fatalf("GetProductByID: %v", err)
	}
	if stored == nil || stored.ImageURL != wantImageURL {
		t.Fatalf("persisted product: want ImageURL %q, got %+v", wantImageURL, stored)
	}
}
