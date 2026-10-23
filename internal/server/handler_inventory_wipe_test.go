package server

import (
	"context"
	"database/sql"
	"net/http"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/product"
)

func TestInventoryWipeHandler(t *testing.T) {
	now := time.Now()

	tests := []handlerTestCase{
		{
			name:  "rejects a missing confirmation and leaves inventory in place",
			setup: seedStockedInventory(now),
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/inventory/wipe",
				expectedStatus: http.StatusBadRequest,
				assertions: []assertion{
					{path: "$.error", value: "Type WIPE INVENTORY to confirm wiping the inventory."},
					{path: "$.code", value: "confirmation_mismatch"},
				},
			},
			afterRequest: exchanges(httpExchange{
				method:         "GET",
				path:           "/api/inventory",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$[0].item.product.name", value: "Milk"},
					{path: "$[0].instanceCount", value: float64(1)},
				},
			}),
		},
		{
			name:  "rejects a confirmation that is not exact",
			setup: seedStockedInventory(now),
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/inventory/wipe",
				body:           `{"confirmation":"wipe inventory"}`,
				expectedStatus: http.StatusBadRequest,
			},
			afterRequest: exchanges(httpExchange{
				method:         "GET",
				path:           "/api/inventory",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$[0].item.product.name", value: "Milk"},
				},
			}),
		},
		{
			name:  "wipes stock but keeps the product lookup cache and scan queue",
			setup: seedStockedInventory(now),
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/inventory/wipe",
				body:           `{"confirmation":"WIPE INVENTORY"}`,
				expectedStatus: http.StatusOK,
			},
			afterRequest: exchanges(
				httpExchange{
					method:         "GET",
					path:           "/api/inventory",
					expectedStatus: http.StatusOK,
					assertions: []assertion{
						{path: "$", value: []interface{}{}},
					},
				},
				httpExchange{
					method:         "GET",
					path:           "/api/inventory/item-wipe/instances",
					expectedStatus: http.StatusOK,
					assertions: []assertion{
						{path: "$", value: []interface{}{}},
					},
				},
				httpExchange{
					method:         "GET",
					path:           "/api/products",
					expectedStatus: http.StatusOK,
					assertions: []assertion{
						{path: "$", value: []interface{}{}},
					},
				},
				httpExchange{
					method:         "GET",
					path:           "/api/products/prod-wipe",
					expectedStatus: http.StatusOK,
					assertions: []assertion{
						{path: "$.id", value: "prod-wipe"},
						{path: "$.name", value: "Milk"},
					},
				},
				httpExchange{
					method:         "GET",
					path:           "/api/products/lookup",
					query:          map[string]string{"barcode": "000111222333"},
					expectedStatus: http.StatusOK,
					assertions: []assertion{
						{path: "$.product.id", value: "prod-wipe"},
						{path: "$.product.name", value: "Milk"},
						{path: "$.source", value: "global"},
					},
				},
				httpExchange{
					method:         "GET",
					path:           "/api/shopping-list",
					expectedStatus: http.StatusOK,
					assertions: []assertion{
						{path: "$", value: []interface{}{}},
					},
				},
				httpExchange{
					method:         "GET",
					path:           "/api/scans",
					query:          map[string]string{"userId": "user-1"},
					expectedStatus: http.StatusOK,
					assertions: []assertion{
						{path: "$[0].id", value: "scan-wipe"},
						{path: "$[0].barcode", value: "000111222333"},
						{path: "$[0].product.name", value: "Milk"},
					},
				},
			),
		},
		{
			name: "succeeds when the inventory is already empty",
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/inventory/wipe",
				body:           `{"confirmation":"WIPE INVENTORY"}`,
				expectedStatus: http.StatusOK,
			},
			afterRequest: exchanges(httpExchange{
				method:         "GET",
				path:           "/api/inventory",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$", value: []interface{}{}},
				},
			}),
		},
	}

	runHandlerTests(t, tests)
}

func seedStockedInventory(now time.Time) func(env testEnv) {
	return func(env testEnv) {
		if err := env.ProductStore.CreateProduct(context.Background(), product.Product{
			ID: "prod-wipe", Name: "Milk", Category: "Dairy", UnitOfMeasure: "gallon",
		}); err != nil {
			env.T.Fatalf("CreateProduct: %v", err)
		}
		if err := env.ProductStore.UpsertBarcodeMapping(context.Background(), "000111222333", "prod-wipe", "global", ""); err != nil {
			env.T.Fatalf("UpsertBarcodeMapping: %v", err)
		}
		if _, err := env.DB.ExecContext(context.Background(),
			`INSERT INTO items (id, user_id, product_id, target_quantity) VALUES ('item-wipe', 'user-1', 'prod-wipe', 2)`); err != nil {
			env.T.Fatalf("create item: %v", err)
		}
		if _, err := env.DB.ExecContext(context.Background(), `
			INSERT INTO item_instances (id, item_id, stock_in_at, expires_at)
			VALUES ('inst-wipe', 'item-wipe', ?, ?)`,
			now, sql.NullTime{Time: now.Add(7 * 24 * time.Hour), Valid: true}); err != nil {
			env.T.Fatalf("create instance: %v", err)
		}
		if _, err := env.DB.ExecContext(context.Background(), `
			INSERT INTO shopping_list_items (id, user_id, item_id, quantity, source)
			VALUES ('shop-wipe', 'user-1', 'item-wipe', 1, 'manual')`); err != nil {
			env.T.Fatalf("create shopping list item: %v", err)
		}
		if _, err := env.DB.ExecContext(context.Background(), `
			INSERT INTO scan_entries (id, user_id, barcode, scanned_at, direction, unit_count, status, product_id)
			VALUES ('scan-wipe', 'user-1', '000111222333', ?, 'stock_in', 1, 'pending', 'prod-wipe')`,
			now); err != nil {
			env.T.Fatalf("create scan entry: %v", err)
		}
	}
}
