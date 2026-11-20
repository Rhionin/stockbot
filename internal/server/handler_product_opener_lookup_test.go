package server

import (
	"net/http"
	"testing"

	"github.com/Rhionin/stockbot/internal/product"
)

// TestFanOutAndPrecedence covers the fan-out behavior, precedence, and failure safety.
//
// Product barcodes (digits) use the full tiered lookup. Local-only letter+digit
// tokens are covered in handler_scan_lookup_eligibility_test.go.
func TestFanOutAndPrecedence(t *testing.T) {
	tests := []handlerTestCase{
		{
			name: "lone hit from OFF database resolves with source external",
			setup: func(env testEnv) {
				env.Upstream.Database(product.ExternalSourceOpenFoodFacts).Seed("611122223333", &product.ProductSummary{
					ID: "off-only", Name: "OFF Product", Category: "Food",
				})
			},
			httpExchange: httpExchange{
				method:         "GET",
				path:           "/api/products/lookup",
				query:          map[string]string{"barcode": "611122223333", "user_id": "test-user"},
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.product.name", value: "OFF Product"},
					{path: "$.source", value: "external"},
					{path: "$.product.externalSource", value: "openfoodfacts"},
				},
			},
		},
		{
			name: "lone hit from OPF database",
			setup: func(env testEnv) {
				env.Upstream.Database(product.ExternalSourceOpenProductsFacts).Seed("622233334444", &product.ProductSummary{
					ID: "opf-only", Name: "OPF Product", Category: "Products",
				})
			},
			httpExchange: httpExchange{
				method:         "GET",
				path:           "/api/products/lookup",
				query:          map[string]string{"barcode": "622233334444", "user_id": "test-user"},
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.product.externalSource", value: "openproductsfacts"},
					{path: "$.source", value: "external"},
				},
			},
		},
		{
			name: "lone hit from OBF database",
			setup: func(env testEnv) {
				env.Upstream.Database(product.ExternalSourceOpenBeautyFacts).Seed("633344445555", &product.ProductSummary{
					ID: "obf-only", Name: "Beauty Product", Category: "Beauty",
				})
			},
			httpExchange: httpExchange{
				method:         "GET",
				path:           "/api/products/lookup",
				query:          map[string]string{"barcode": "633344445555", "user_id": "test-user"},
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.product.externalSource", value: "openbeautyfacts"},
					{path: "$.source", value: "external"},
				},
			},
		},
		{
			name: "lone hit from OPetF database",
			setup: func(env testEnv) {
				env.Upstream.Database(product.ExternalSourceOpenPetFoodFacts).Seed("644455556666", &product.ProductSummary{
					ID: "opetf-only", Name: "Pet Food", Category: "Pets",
				})
			},
			httpExchange: httpExchange{
				method:         "GET",
				path:           "/api/products/lookup",
				query:          map[string]string{"barcode": "644455556666", "user_id": "test-user"},
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.product.externalSource", value: "openpetfoodfacts"},
					{path: "$.source", value: "external"},
				},
			},
		},
		{
			name: "precedence: OFF wins when all databases hit",
			setup: func(env testEnv) {
				bc := "655566667777"
				env.Upstream.Database(product.ExternalSourceOpenFoodFacts).Seed(bc, &product.ProductSummary{
					ID: "precedence-test", Name: "OFF Version", Category: "Food",
				})
				env.Upstream.Database(product.ExternalSourceOpenProductsFacts).Seed(bc, &product.ProductSummary{
					ID: "precedence-test", Name: "OPF Version", Category: "Products",
				})
				env.Upstream.Database(product.ExternalSourceOpenBeautyFacts).Seed(bc, &product.ProductSummary{
					ID: "precedence-test", Name: "Beauty Version", Category: "Beauty",
				})
				env.Upstream.Database(product.ExternalSourceOpenPetFoodFacts).Seed(bc, &product.ProductSummary{
					ID: "precedence-test", Name: "Pet Version", Category: "Pets",
				})
			},
			httpExchange: httpExchange{
				method:         "GET",
				path:           "/api/products/lookup",
				query:          map[string]string{"barcode": "655566667777", "user_id": "test-user"},
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.product.name", value: "OFF Version"},
					{path: "$.product.externalSource", value: "openfoodfacts"},
				},
			},
		},
		{
			name: "all-miss returns not-found",
			httpExchange: httpExchange{
				method:         "GET",
				path:           "/api/products/lookup",
				query:          map[string]string{"barcode": "666677778888", "user_id": "test-user"},
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.product", value: nil},
				},
			},
		},
	}

	runHandlerTests(t, tests)
}