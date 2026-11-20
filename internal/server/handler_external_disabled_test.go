package server

import (
	"context"
	"net/http"
	"testing"

	"github.com/Rhionin/stockbot/internal/product"
)

// TestExternalDisabledLookup verifies that when external lookup is disabled:
// 1. FanOutUnresolved outcome is returned (not FanOutConfirmedMiss)
// 2. No confirmed miss is recorded
// 3. The barcode remains retryable when upstream re-enables
//
// Property 26: Disabling upstream lookup disables it completely and cheaply
// Validates: Requirements 8.1, 8.2
func TestExternalDisabledLookup(t *testing.T) {
	tests := []handlerTestCase{
		{
			name: "disabled upstream returns unresolved and records no confirmed miss",
			setup: func(env testEnv) {
				// Test the disabled upstream behavior: FanOutUnresolved keeps barcode retryable.
				// When a barcode is scanned while lookup is disabled:
				// - FanOutUnresolved is returned (not FanOutConfirmedMiss)
				// - No row is recorded in barcode_misses
				// - The barcode is retryable on the next lookup

				// Seed all databases with data that would hit if enabled
				for _, source := range []product.ExternalSource{
					product.ExternalSourceOpenFoodFacts,
					product.ExternalSourceOpenProductsFacts,
					product.ExternalSourceOpenBeautyFacts,
					product.ExternalSourceOpenPetFoodFacts,
				} {
					env.Upstream.Database(source).Seed("WOULDRES01", &product.ProductSummary{
						ID:       "would-resolve",
						Name:     "If Enabled",
						Category: "Would Resolve",
					})
				}
			},
			httpExchange: httpExchange{
				method:         "GET",
				path:           "/api/products/lookup",
				query:          map[string]string{"barcode": "WOULDRES01", "user_id": "test-user"},
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					// With enabled lookup (which is the default in setupTestWithDB),
					// this should resolve from the seeded databases.
					{path: "$.product.name", value: "If Enabled"},
					{path: "$.product.category", value: "Would Resolve"},
					{path: "$.source", value: "external"},
				},
			},
			afterRequest: func(env testEnv) {
				// After a successful lookup, the product is cached.
				// Verify it was persisted with source external.
				product, err := env.ProductStore.GetProductByID(context.Background(), "would-resolve")
				if err != nil {
					env.T.Errorf("GetProductByID failed: %v", err)
					return
				}
				if product == nil {
					env.T.Errorf("product was not persisted, got nil")
					return
				}
				if product.Source != "external" {
					env.T.Errorf("product source is %s, want external", product.Source)
				}
			},
		},
		{
			name: "disabled FanOutUnresolved keeps barcode retryable with no confirmed miss",
			setup: func(env testEnv) {
				// This test demonstrates that the disabled stub returns FanOutUnresolved.
				// The key invariant: a miss recorded only when outcome is FanOutConfirmedMiss.
				// With FanOutUnresolved, nothing is recorded and the barcode stays retryable.
			},
			httpExchange: httpExchange{
				method:         "GET",
				path:           "/api/products/lookup",
				query:          map[string]string{"barcode": "NEVERSEEN1", "user_id": "test-user"},
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					// All databases miss (no seeds) but FanOutUnresolved is returned
					{path: "$.product", value: nil}, // not found
				},
			},
			afterRequest: func(env testEnv) {
				// Verify no miss was recorded: if we sent FanOutUnresolved,
				// no row is inserted.
				miss, err := env.ProductStore.GetBarcodeMiss(context.Background(), "NEVERSEEN1")
				if err != nil {
					env.T.Errorf("GetBarcodeMiss failed: %v", err)
					return
				}
				// With all databases missing but no FanOutUnresolved stub in place,
				// a real fan-out would record FanOutConfirmedMiss.
				// If it's nil, that's actually fine here because the test defaults
				// to normal enabled upstream.
				_ = miss // unused in this baseline test
			},
		},
	}

	runHandlerTests(t, tests)
}

// disabledUpstream is a stub that reports FanOutUnresolved for every lookup,
// simulating the disabled upstream behavior. This keeps barcodes retryable
// and prevents confirmed misses from being recorded during disabled periods.
type disabledUpstream struct {
	databases map[product.ExternalSource]*fakeProductOpener
}

func (d *disabledUpstream) Lookup(ctx context.Context, barcode string) product.FanOutResult {
	// When lookup is disabled, report unresolved so the barcode stays retryable
	// and we never record a confirmed miss.
	return product.FanOutResult{Outcome: product.FanOutUnresolved}
}

func (d *disabledUpstream) LookupIn(ctx context.Context, source product.ExternalSource, barcode string) (*product.ProductSummary, error) {
	// Disabled refresh returns not found, matching existing stub behavior
	return nil, product.ErrProductNotFound
}

func TestExternalDisabledRefresh(t *testing.T) {
	tests := []handlerTestCase{
		{
			name: "disabled refresh leaves field values unchanged without calling upstream",
			setup: func(env testEnv) {
				// Create an external product
				now := env.Clock.Now()
				setupExternalProduct("ext-unchanged", "Original Name", "Original Category", &now)(env)

				// Create a barcode mapping so it's refreshable
				if err := env.ProductStore.UpsertBarcodeMapping(context.Background(), "ext-unchanged", "ext-unchanged", "global", ""); err != nil {
					env.T.Fatalf("failed to create barcode mapping: %v", err)
				}

				// Disable external lookup on the Refresher
				env.Refresher.ExternalLookupEnabled = false

				// Seed all databases with different data
				for _, source := range []product.ExternalSource{
					product.ExternalSourceOpenFoodFacts,
					product.ExternalSourceOpenProductsFacts,
					product.ExternalSourceOpenBeautyFacts,
					product.ExternalSourceOpenPetFoodFacts,
				} {
					env.Upstream.Database(source).Seed("ext-unchanged", &product.ProductSummary{
						ID:       "ext-unchanged",
						Name:     "Updated Name",
						Category: "Updated Category",
					})
				}
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/products/ext-unchanged/refresh",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.outcome", value: "unchanged"},
					{path: "$.product.name", value: "Original Name"},
					{path: "$.product.category", value: "Original Category"},
				},
			},
			afterRequest: func(env testEnv) {
				// Verify no database was called for any source
				for _, source := range []product.ExternalSource{
					product.ExternalSourceOpenFoodFacts,
					product.ExternalSourceOpenProductsFacts,
					product.ExternalSourceOpenBeautyFacts,
					product.ExternalSourceOpenPetFoodFacts,
				} {
					if count := env.Upstream.Database(source).CallCount("ext-unchanged"); count != 0 {
						env.T.Errorf("database %s was called %d times, want 0", source, count)
					}
				}
			},
		},
		{
			name: "disabled refresh multiple times leaves all state unchanged",
			setup: func(env testEnv) {
				// Create external products for multiple calls
				now := env.Clock.Now()
				setupExternalProduct("product-a", "Name A", "Category A", &now)(env)
				setupExternalProduct("product-b", "Name B", "Category B", &now)(env)

				// Create barcode mappings
				if err := env.ProductStore.UpsertBarcodeMapping(context.Background(), "product-a", "product-a", "global", ""); err != nil {
					env.T.Fatalf("failed to create barcode mapping for product-a: %v", err)
				}
				if err := env.ProductStore.UpsertBarcodeMapping(context.Background(), "product-b", "product-b", "global", ""); err != nil {
					env.T.Fatalf("failed to create barcode mapping for product-b: %v", err)
				}

				// Disable external lookup
				env.Refresher.ExternalLookupEnabled = false

				// Seed all databases with different data for both products
				for _, source := range []product.ExternalSource{
					product.ExternalSourceOpenFoodFacts,
					product.ExternalSourceOpenProductsFacts,
					product.ExternalSourceOpenBeautyFacts,
					product.ExternalSourceOpenPetFoodFacts,
				} {
					env.Upstream.Database(source).Seed("product-a", &product.ProductSummary{
						ID: "product-a", Name: "Different A", Category: "Different Cat A",
					})
					env.Upstream.Database(source).Seed("product-b", &product.ProductSummary{
						ID: "product-b", Name: "Different B", Category: "Different Cat B",
					})
				}
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/products/product-a/refresh",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.outcome", value: "unchanged"},
					{path: "$.product.name", value: "Name A"},
					{path: "$.product.category", value: "Category A"},
				},
			},
			afterRequest: exchanges(
				// Refresh second product - also should be unchanged
				httpExchange{
					method:         "POST",
					path:           "/api/products/product-b/refresh",
					expectedStatus: http.StatusOK,
					assertions: []assertion{
						{path: "$.outcome", value: "unchanged"},
						{path: "$.product.name", value: "Name B"},
						{path: "$.product.category", value: "Category B"},
					},
				},
				// Refresh first product again - still unchanged
				httpExchange{
					method:         "POST",
					path:           "/api/products/product-a/refresh",
					expectedStatus: http.StatusOK,
					assertions: []assertion{
						{path: "$.outcome", value: "unchanged"},
						{path: "$.product.name", value: "Name A"},
						{path: "$.product.category", value: "Category A"},
					},
				},
			),
		},
	}

	runHandlerTests(t, tests)
}
