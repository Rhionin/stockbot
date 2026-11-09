package server

import (
	"net/http"
	"testing"

	"github.com/Rhionin/stockbot/internal/product"
	_ "modernc.org/sqlite"
)

func TestCreateHandler(t *testing.T) {
	tests := []handlerTestCase{
		{
			name: "successful create",
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/products",
				body:           `{"name":"Orange","category":"Fruit","unitOfMeasure":"each"}`,
				expectedStatus: http.StatusCreated,
				assertions: []assertion{
					{path: "$.name", value: "Orange"},
					{path: "$.category", value: "Fruit"},
					{path: "$.unitOfMeasure", value: "each"},
				},
			},
			afterRequest: exchanges(httpExchange{
				method:         "GET",
				path:           "/api/products",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$[0].name", value: "Orange"},
					{path: "$[0].category", value: "Fruit"},
				},
			}),
		},
		{
			name: "missing name",
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/products",
				body:           `{"category":"Fruit"}`,
				expectedStatus: http.StatusBadRequest,
			},
		},
		{
			name: "empty JSON",
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/products",
				body:           `{}`,
				expectedStatus: http.StatusBadRequest,
			},
		},
		{
			name: "create flow - empty list, create, verify list",
			httpExchange: httpExchange{
				method:         "GET",
				path:           "/api/products",
				expectedStatus: http.StatusOK,
			},
			afterRequest: exchanges([]httpExchange{
				{
					method:         "GET",
					path:           "/api/products",
					expectedStatus: http.StatusOK,
					assertions: []assertion{
						{path: "$", value: []interface{}{}},
					},
				},
				{
					method:         "POST",
					path:           "/api/products",
					body:           `{"name":"Apple","category":"Fruit"}`,
					expectedStatus: http.StatusCreated,
					assertions: []assertion{
						{path: "$.name", value: "Apple"},
						{path: "$.category", value: "Fruit"},
					},
				},
				{
					method:         "GET",
					path:           "/api/products",
					expectedStatus: http.StatusOK,
					assertions: []assertion{
						{path: "$[0].name", value: "Apple"},
						{path: "$[0].category", value: "Fruit"},
					},
				},
				{
					method:         "POST",
					path:           "/api/products",
					body:           `{"name":"Banana","category":"Fruit"}`,
					expectedStatus: http.StatusCreated,
				},
				{
					method:         "GET",
					path:           "/api/products",
					expectedStatus: http.StatusOK,
					assertions: []assertion{
						{path: "$[0].name", value: "Apple"},
						{path: "$[1].name", value: "Banana"},
					},
				},
			}...),
		},
		{
			name: "create typed product does not suggest lookalike with orphan catalog",
			setup: func(env testEnv) {
				if err := env.ProductStore.CreateProduct(env.T.Context(), product.Product{
					ID:             "nym-catalog-orphan-569",
					Name:           "Nym Blend Cleaner Refill",
					Source:         product.SourceExternal,
					ExternalSource: "openproductsfacts",
				}); err != nil {
					env.T.Fatal(err)
				}
			},
			httpExchange: httpExchange{
				method:         http.MethodPost,
				path:           "/api/products",
				body:           `{"name":"Nym Blend Cleaner Refill","category":"Bath","unitOfMeasure":"each","createItem":true}`,
				expectedStatus: http.StatusCreated,
			},
			afterRequest: exchanges(httpExchange{
				method:         http.MethodGet,
				path:           "/api/group-suggestions",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$[0]", absent: true},
				},
			}),
		},
	}

	runHandlerTests(t, tests)
}
