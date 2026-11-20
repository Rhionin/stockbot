package server

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"testing"

	"github.com/Rhionin/stockbot/internal/product"
)

func TestLookupHandler(t *testing.T) {
	tests := []handlerTestCase{
		{
			name:  "successful lookup",
			setup: setupProductWithBarcode("prod-1", "Test Product", "Test", "123456"),
			httpExchange: httpExchange{
				method:         "GET",
				path:           "/api/products/lookup",
				query:          map[string]string{"barcode": "123456"},
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.product.name", value: "Test Product"},
					{path: "$.product.category", value: "Test"},
					{path: "$.product.id", value: "prod-1"},
				},
			},
		},
		{
			name: "missing barcode parameter",
			httpExchange: httpExchange{
				method:         "GET",
				path:           "/api/products/lookup",
				expectedStatus: http.StatusBadRequest,
			},
		},
		{
			name: "product not found",
			httpExchange: httpExchange{
				method:         "GET",
				path:           "/api/products/lookup",
				query:          map[string]string{"barcode": "nonexistent"},
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.product", value: nil},
				},
			},
		},
		{
			name: "wifi payload is not lookup eligible",
			httpExchange: httpExchange{
				method:         "GET",
				path:           "/api/products/lookup",
				query:          map[string]string{"barcode": "WIFI:S:home;P:secret;;"},
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.product", value: nil},
				},
			},
			afterRequest: func(env testEnv) {
				if env.OpenFoodFacts.TotalCalls() != 0 {
					env.T.Fatalf("upstream calls = %d, want 0", env.OpenFoodFacts.TotalCalls())
				}
				var missCount int
				if err := env.DB.QueryRow(`SELECT COUNT(*) FROM barcode_misses`).Scan(&missCount); err != nil {
					env.T.Fatalf("count barcode_misses: %v", err)
				}
				if missCount != 0 {
					env.T.Fatalf("barcode_misses rows = %d, want 0", missCount)
				}
			},
		},
	}

	runHandlerTests(t, tests)
}

// TestLookupHandlerBackgroundRevalidation covers the stale-while-revalidate
// path scheduled from LookupService.Lookup's Tier-2 branch:
//
//   - Property 4: A lookup response is unaffected by revalidation
//   - Property 5: A stale lookup revalidates behind the response
//   - Property 8: Every attempt stamps refreshed_at
//
// Every case here resolves at Tier 2 (an existing local row plus a barcode
// mapping — for external rows the product ID equals the barcode by
// convention), so ScheduleRefresh's synchronous gate has a row to evaluate.
func TestLookupHandlerBackgroundRevalidation(t *testing.T) {
	tests := []handlerTestCase{
		{
			name: "stale external row returns the cached name and merges the new one after Wait",
			setup: func(env testEnv) {
				setupExternalProduct("ext-stale-merge", "Old Name", "Old Category", nil)(env)
				if err := env.ProductStore.UpsertBarcodeMapping(context.Background(), "EXTSTALE01", "ext-stale-merge", "global", ""); err != nil {
					env.T.Fatalf("failed to create barcode mapping: %v", err)
				}
				// Re-seeded before the GET, so the response below can only carry
				// "Old Name" if it came from the cache rather than from a
				// revalidation that ran ahead of the response.
				env.OpenFoodFacts.Seed("ext-stale-merge", &product.ProductSummary{
					ID:       "ext-stale-merge",
					Name:     "New Name",
					Category: "New Category",
				})
			},
			httpExchange: httpExchange{
				method:         "GET",
				path:           "/api/products/lookup",
				query:          map[string]string{"barcode": "EXTSTALE01"},
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.product.name", value: "Old Name"},
					{path: "$.product.category", value: "Old Category"},
					{path: "$.source", value: "global"},
				},
			},
			afterRequest: func(env testEnv) {
				env.Refresher.Wait()

				exchanges(httpExchange{
					method:         "GET",
					path:           "/api/products",
					expectedStatus: http.StatusOK,
					assertions: []assertion{
						{path: "$[0].name", value: "New Name"},
						{path: "$[0].category", value: "New Category"},
					},
				})(env)
			},
		},
		{
			name: "fresh external row does not revalidate",
			setup: func(env testEnv) {
				now := env.Clock.Now()
				setupExternalProduct("ext-fresh-lookup", "Fresh Name", "Fresh Category", &now)(env)
				if err := env.ProductStore.UpsertBarcodeMapping(context.Background(), "EXTFRESH01", "ext-fresh-lookup", "global", ""); err != nil {
					env.T.Fatalf("failed to create barcode mapping: %v", err)
				}
				// Seeded with a value that would prove an accidental upstream
				// call rather than passing by luck if ScheduleRefresh's
				// staleness gate failed to reject this row.
				env.OpenFoodFacts.Seed("EXTFRESH01", &product.ProductSummary{
					ID:   "ext-fresh-lookup",
					Name: "Should Not Apply",
				})
			},
			httpExchange: httpExchange{
				method:         "GET",
				path:           "/api/products/lookup",
				query:          map[string]string{"barcode": "EXTFRESH01"},
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.product.name", value: "Fresh Name"},
					{path: "$.product.category", value: "Fresh Category"},
				},
			},
			afterRequest: func(env testEnv) {
				env.Refresher.Wait()
				if got := env.OpenFoodFacts.CallCount("EXTFRESH01"); got != 0 {
					env.T.Errorf("CallCount(EXTFRESH01) after Wait: want 0, got %d", got)
				}
			},
		},
		{
			name: "stale lookup where the fake errors is unaffected and the row survives",
			setup: func(env testEnv) {
				setupExternalProduct("ext-stale-error", "Cached Name", "Cached Category", nil)(env)
				if err := env.ProductStore.UpsertBarcodeMapping(context.Background(), "EXTSTALE02", "ext-stale-error", "global", ""); err != nil {
					env.T.Fatalf("failed to create barcode mapping: %v", err)
				}
				// A transport-like error, distinct from product.ErrProductNotFound,
				// so Refresh's background attempt takes the "any other error"
				// branch: MarkRefreshed only, no field values touched.
				env.OpenFoodFacts.SeedError("EXTSTALE02", errors.New("network timeout"))
			},
			httpExchange: httpExchange{
				method:         "GET",
				path:           "/api/products/lookup",
				query:          map[string]string{"barcode": "EXTSTALE02"},
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					// Byte-identical to a lookup that scheduled no revalidation
					// at all: the response is built from values read before
					// ScheduleRefresh is called, so a background failure cannot
					// change it.
					{path: "$.product.id", value: "ext-stale-error"},
					{path: "$.product.name", value: "Cached Name"},
					{path: "$.product.category", value: "Cached Category"},
					{path: "$.source", value: "global"},
				},
			},
			afterRequest: func(env testEnv) {
				env.Refresher.Wait()

				exchanges(httpExchange{
					method:         "GET",
					path:           "/api/products",
					expectedStatus: http.StatusOK,
					assertions: []assertion{
						{path: "$[0].name", value: "Cached Name"},
						{path: "$[0].category", value: "Cached Category"},
					},
				})(env)
			},
		},
	}

	runHandlerTests(t, tests)
}

// TestLookupHandlerTier3PersistenceProvenance covers Property 2: writes
// record provenance, for the Tier-3 branch of LookupService.Lookup. The
// barcode is absent from both the products and barcodes tables, so the fake
// resolves it only through Open Food Facts, and persistExternalProduct
// stores the result with source = 'external' and refreshed_at stamped from
// the clock.
//
// refreshed_at's exact stamp against env.Clock is not expressible through
// the API (Product.RefreshedAt carries omitempty and the lookup response
// returns a ProductSummary, which has no refreshed_at field at all), so this
// is the one case in this task that reads env.DB directly in afterRequest.
func TestLookupHandlerTier3PersistenceProvenance(t *testing.T) {
	tests := []handlerTestCase{
		{
			name: "tier-3 resolution persists external provenance stamped from the clock",
			setup: func(env testEnv) {
				env.OpenFoodFacts.Seed("9900000000001", &product.ProductSummary{
					ID:            "9900000000001",
					Name:          "Brand New Product",
					Category:      "New Category",
					UnitOfMeasure: "each",
				})
			},
			httpExchange: httpExchange{
				method:         "GET",
				path:           "/api/products/lookup",
				query:          map[string]string{"barcode": "9900000000001"},
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.product.name", value: "Brand New Product"},
					{path: "$.source", value: "external"},
				},
			},
			afterRequest: func(env testEnv) {
				// env.Clock is a fakeClock and does not auto-advance, so the
				// value captured here is the same value LookupService.Now
				// returned when persistExternalProduct stamped the row.
				capturedNow := env.Clock.Now()

				var source string
				var nameOverridden bool
				var refreshedAt sql.NullTime
				row := env.DB.QueryRowContext(context.Background(),
					`SELECT source, name_overridden, refreshed_at FROM products WHERE id = ?`, "9900000000001")
				if err := row.Scan(&source, &nameOverridden, &refreshedAt); err != nil {
					env.T.Fatalf("query product: %v", err)
				}

				if source != product.SourceExternal {
					env.T.Errorf("source: want %q, got %q", product.SourceExternal, source)
				}
				if nameOverridden {
					env.T.Error("name_overridden: want false, got true")
				}
				if !refreshedAt.Valid {
					env.T.Fatal("refreshed_at: want non-NULL, got NULL")
				}
				if !refreshedAt.Time.Equal(capturedNow) {
					env.T.Errorf("refreshed_at: want %v, got %v", capturedNow, refreshedAt.Time)
				}
			},
		},
	}

	runHandlerTests(t, tests)
}

// TestLookupHandlerUserOverridePrecedenceAcrossRevalidation covers Property
// 15: a user override outranks freshness. The same barcode carries both a
// user_override mapping to one product and a stale global mapping to a
// second, shadowed product. The override wins the lookup before and after
// the shadowed global row is revalidated, and revalidating it never touches
// either barcodes mapping.
func TestLookupHandlerUserOverridePrecedenceAcrossRevalidation(t *testing.T) {
	tests := []handlerTestCase{
		{
			name: "override product wins the lookup and survives a refresh of the shadowed global row",
			setup: func(env testEnv) {
				setupProduct("override-prod", "Override Product", "Override Category")(env)
				setupExternalProduct("global-prod", "Global Product", "Global Category", nil)(env)
				if err := env.ProductStore.UpsertBarcodeMapping(context.Background(), "OVRIDEBC01", "override-prod", "user_override", "user-1"); err != nil {
					env.T.Fatalf("failed to create user_override barcode mapping: %v", err)
				}
				if err := env.ProductStore.UpsertBarcodeMapping(context.Background(), "OVRIDEBC01", "global-prod", "global", ""); err != nil {
					env.T.Fatalf("failed to create global barcode mapping: %v", err)
				}
			},
			httpExchange: httpExchange{
				method:         "GET",
				path:           "/api/products/lookup",
				query:          map[string]string{"barcode": "OVRIDEBC01"},
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.product.id", value: "override-prod"},
					{path: "$.product.name", value: "Override Product"},
				},
			},
			afterRequest: func(env testEnv) {
				// Re-seeded so a genuine change is available to merge into the
				// shadowed global row; the point is exercising Refresh against it
				// while it stays shadowed, not the merged values themselves.
				env.OpenFoodFacts.Seed("global-prod", &product.ProductSummary{
					ID:       "global-prod",
					Name:     "Refreshed Global Name",
					Category: "Refreshed Global Category",
				})

				exchanges(
					httpExchange{
						method:         "POST",
						path:           "/api/products/global-prod/refresh",
						expectedStatus: http.StatusOK,
						assertions: []assertion{
							{path: "$.outcome", value: "updated"},
						},
					},
					httpExchange{
						method:         "GET",
						path:           "/api/products/lookup",
						query:          map[string]string{"barcode": "OVRIDEBC01"},
						expectedStatus: http.StatusOK,
						assertions: []assertion{
							{path: "$.product.id", value: "override-prod"},
							{path: "$.product.name", value: "Override Product"},
						},
					},
				)(env)

				// Refresh and the refresh handler write only to products, never to
				// barcodes, so both mappings must retain their original barcode,
				// source, and user_id.
				assertBarcodeMapping := func(source, wantUserID, wantProductID string) {
					var gotProductID, gotUserID string
					row := env.DB.QueryRowContext(context.Background(),
						`SELECT product_id, user_id FROM barcodes WHERE barcode = ? AND source = ?`,
						"OVRIDEBC01", source)
					if err := row.Scan(&gotProductID, &gotUserID); err != nil {
						env.T.Fatalf("query barcode mapping (source=%s): %v", source, err)
					}
					if gotProductID != wantProductID {
						env.T.Errorf("barcode mapping (source=%s) product_id: want %q, got %q", source, wantProductID, gotProductID)
					}
					if gotUserID != wantUserID {
						env.T.Errorf("barcode mapping (source=%s) user_id: want %q, got %q", source, wantUserID, gotUserID)
					}
				}
				assertBarcodeMapping("user_override", "user-1", "override-prod")
				assertBarcodeMapping("global", "", "global-prod")
			},
		},
	}

	runHandlerTests(t, tests)
}
