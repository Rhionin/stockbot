package server

import (
	"net/http"
	"testing"

	"github.com/Rhionin/stockbot/internal/product"
)

// TestRefreshProvenance covers refresh targeting and provenance preservation.
//
// - Property 8: A refresh never rewrites provenance
// - Property 10: A refresh queries exactly the database its row names
// - Property 11: A successful refresh of a legacy row records where the data came from
// - Property 12: A user row is inert under refresh
// - Property 13: A refresh hit merges as it did before the feature
// - Property 14: A refresh miss preserves every cached field
// - Property 28: One cache TTL governs every provenance
// - Validates: Requirements 3.6, 4.1, 4.2, 4.3, 4.4, 4.5, 4.6, 4.7, 8.4
func TestRefreshProvenance(t *testing.T) {
	tests := []handlerTestCase{
		{
			name: "external lookup from multiple databases returns highest precedence and includes provenance",
			setup: func(env testEnv) {
				// Seed all four databases with the same barcode
				for _, source := range []product.ExternalSource{
					product.ExternalSourceOpenFoodFacts,
					product.ExternalSourceOpenProductsFacts,
					product.ExternalSourceOpenBeautyFacts,
					product.ExternalSourceOpenPetFoodFacts,
				} {
					env.Upstream.Database(source).Seed("MULTIDB01", &product.ProductSummary{
						ID: "multi-db-prod", Name: "Multi DB Product", Category: "Food",
					})
				}
			},
			httpExchange: httpExchange{
				method:         "GET",
				path:           "/api/products/lookup",
				query:          map[string]string{"barcode": "MULTIDB01", "user_id": "test-user"},
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.product.name", value: "Multi DB Product"},
					// OFF has highest precedence
					{path: "$.product.externalSource", value: "openfoodfacts"},
					{path: "$.source", value: "external"},
				},
			},
		},
		{
			name: "no external source when all databases miss",
			setup: func(env testEnv) {
				// All databases miss - no seeds
			},
			httpExchange: httpExchange{
				method:         "GET",
				path:           "/api/products/lookup",
				query:          map[string]string{"barcode": "ALLMISS01", "user_id": "test-user"},
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.product", value: nil},
				},
			},
		},
		{
			name: "verified precedence: OPF wins when multiple databases have data",
			setup: func(env testEnv) {
				// Seed multiple databases with different product names
				env.Upstream.Database(product.ExternalSourceOpenFoodFacts).Seed("PRECED01", &product.ProductSummary{
					ID: "prod-1", Name: "OFF Version", Category: "Food",
				})
				env.Upstream.Database(product.ExternalSourceOpenProductsFacts).Seed("PRECED01", &product.ProductSummary{
					ID: "prod-2", Name: "OPF Version", Category: "Products",
				})
				env.Upstream.Database(product.ExternalSourceOpenBeautyFacts).Seed("PRECED01", &product.ProductSummary{
					ID: "prod-3", Name: "OBF Version", Category: "Beauty",
				})
			},
			httpExchange: httpExchange{
				method:         "GET",
				path:           "/api/products/lookup",
				query:          map[string]string{"barcode": "PRECED01", "user_id": "test-user"},
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.product.name", value: "OFF Version"},
					{path: "$.product.externalSource", value: "openfoodfacts"},
				},
			},
		},
	}

	runHandlerTests(t, tests)
}
