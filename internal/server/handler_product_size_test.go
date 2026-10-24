package server

import (
	"net/http"
	"testing"

	"github.com/Rhionin/stockbot/internal/product"
)

func TestProductNetSizeRoundTrip(t *testing.T) {
	runHandlerTests(t, []handlerTestCase{
		{
			name: "create stores ounces as grams",
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/products",
				body:           `{"name":"Cut Green Beans","category":"Canned","unitOfMeasure":"can","netAmount":14.5,"netUnit":"oz","packCount":6}`,
				expectedStatus: http.StatusCreated,
				assertions: []assertion{
					{path: "$.netDimension", value: "mass"},
					{path: "$.netSizeOrigin", value: "manual"},
					{path: "$.netAmount", value: 14.5},
					{path: "$.netUnit", value: "oz"},
					{path: "$.packCount", value: float64(6)},
					{path: "$.unitOfMeasure", value: "can"},
				},
			},
		},
		{
			name: "update replaces the size and a cleared size stays cleared",
			setup: func(env testEnv) {
				if err := env.ProductStore.CreateProduct(env.T.Context(), product.Product{
					ID: "prod-size", Name: "Beans", Category: "Canned", UnitOfMeasure: "can",
				}); err != nil {
					env.T.Fatal(err)
				}
			},
			httpExchange: httpExchange{
				method:         "PUT",
				path:           "/api/products/prod-size",
				body:           `{"name":"Beans","category":"Canned","unitOfMeasure":"can","netAmount":14.5,"netUnit":"oz"}`,
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.netAmount", value: 14.5},
					{path: "$.netUnit", value: "oz"},
					{path: "$.netSizeOrigin", value: "manual"},
				},
			},
			afterRequest: exchanges(
				httpExchange{
					method:         "GET",
					path:           "/api/products/prod-size",
					expectedStatus: http.StatusOK,
					assertions: []assertion{
						{path: "$.netAmount", value: 14.5},
						{path: "$.netDimension", value: "mass"},
						{path: "$.unitOfMeasure", value: "can"},
					},
				},
				httpExchange{
					method:         "PUT",
					path:           "/api/products/prod-size",
					body:           `{"name":"Beans","category":"Canned","unitOfMeasure":"can","netAmount":null,"netUnit":""}`,
					expectedStatus: http.StatusOK,
					assertions: []assertion{
						{path: "$.netSizeOrigin", value: "manual"},
						{path: "$.netAmount", absent: true},
						{path: "$.netBaseValue", absent: true},
					},
				},
				httpExchange{
					method:         "GET",
					path:           "/api/products/prod-size",
					expectedStatus: http.StatusOK,
					assertions: []assertion{
						{path: "$.netSizeOrigin", value: "manual"},
						{path: "$.netAmount", absent: true},
					},
				},
			),
		},
		{
			name: "renaming a product keeps the new name on the household",
			setup: func(env testEnv) {
				if err := env.ProductStore.CreateProduct(env.T.Context(), product.Product{
					ID: "prod-rename", Name: "Beans", Category: "Canned", UnitOfMeasure: "can",
				}); err != nil {
					env.T.Fatal(err)
				}
			},
			httpExchange: httpExchange{
				method:         "PUT",
				path:           "/api/products/prod-rename",
				body:           `{"name":"Cut green beans","category":"Canned","unitOfMeasure":"can"}`,
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.name", value: "Cut green beans"},
				},
			},
		},
		{
			name: "an update without a name is rejected",
			setup: func(env testEnv) {
				if err := env.ProductStore.CreateProduct(env.T.Context(), product.Product{
					ID: "prod-noname", Name: "Beans",
				}); err != nil {
					env.T.Fatal(err)
				}
			},
			httpExchange: httpExchange{
				method:         "PUT",
				path:           "/api/products/prod-noname",
				body:           `{"name":"","category":"Canned"}`,
				expectedStatus: http.StatusBadRequest,
				bodyContains:   []string{"name is required"},
			},
		},
		{
			name: "a size without a unit is rejected",
			setup: func(env testEnv) {
				if err := env.ProductStore.CreateProduct(env.T.Context(), product.Product{
					ID: "prod-bad-size", Name: "Beans",
				}); err != nil {
					env.T.Fatal(err)
				}
			},
			httpExchange: httpExchange{
				method:         "PUT",
				path:           "/api/products/prod-bad-size",
				body:           `{"name":"Beans","netAmount":14.5}`,
				expectedStatus: http.StatusBadRequest,
			},
		},
		{
			name: "open food facts grams are stored as mass",
			setup: func(env testEnv) {
				grams := 297.67
				env.OpenFoodFacts.Seed("051000012349", &product.ProductSummary{
					ID:           "051000012349",
					Name:         "Campbell's Condensed Cream of Mushroom Soup",
					Category:     "Soups",
					NetBaseValue: &grams,
					NetDimension: product.DimensionMass,
				})
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans",
				body:           `{"barcode":"051000012349","userId":"user-1"}`,
				expectedStatus: http.StatusCreated,
				assertions: []assertion{
					{path: "$.status", value: "pending"},
					{path: "$.productId", value: "051000012349"},
				},
			},
			afterRequest: exchanges(httpExchange{
				method:         "GET",
				path:           "/api/products/051000012349",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.netDimension", value: "mass"},
					{path: "$.netSizeOrigin", value: "off"},
					{path: "$.netBaseValue", value: 297.67},
				},
			}),
		},
	})
}
