package server

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/product"
	"github.com/Rhionin/stockbot/internal/scan"
)

func TestScanBatchCommit(t *testing.T) {
	runHandlerTests(t, []handlerTestCase{
		{
			name: "committing pending scans changes inventory and takes them off the queue",
			setup: func(env testEnv) {
				mustProduct(env, "prod-oats", "Rolled Oats")
				mustProduct(env, "prod-keep", "Kept Aside")
				insertPendingScanUnits(env, "scan-a", "prod-oats", scan.StockIn, 2)
				insertPendingScanUnits(env, "scan-b", "prod-oats", scan.StockIn, 3)
				insertPendingScanUnits(env, "scan-out", "prod-oats", scan.StockOut, 1)
				insertPendingScanUnits(env, "scan-keep", "prod-keep", scan.StockIn, 1)
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans/batch-commit",
				body:           `{"scanEntryIds":["scan-a","scan-b","scan-out"],"commit":true}`,
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.updatedCount", value: float64(3)},
					{path: "$.committedIds[0]", value: "scan-a"},
					{path: "$.committedIds[1]", value: "scan-b"},
					{path: "$.committedIds[2]", value: "scan-out"},
					{path: "$.failedIds", absent: true},
				},
			},
			afterRequest: func(env testEnv) {
				h := handlerFromEnv(env)
				exchanges(
					httpExchange{
						method:         "GET",
						path:           "/api/inventory",
						expectedStatus: http.StatusOK,
						assertions:     []assertion{{path: "$[0].instanceCount", value: float64(4)}},
					},
					httpExchange{
						method:         "GET",
						path:           "/api/scans",
						query:          map[string]string{"userId": "user-1", "status": "pending"},
						expectedStatus: http.StatusOK,
						assertions: []assertion{
							{path: "$[0].id", value: "scan-keep"},
							{path: "$[1]", absent: true},
						},
					},
					httpExchange{
						method:         "GET",
						path:           "/api/scans/history",
						query:          map[string]string{"userId": "user-1"},
						expectedStatus: http.StatusOK,
						bodyContains:   []string{"scan-a", "scan-b", "scan-out"},
						bodyExcludes:   []string{"scan-keep"},
					},
				)(env)
				// Committing the same scan again does not move inventory.
				again := exchangeJSON(env.T, h, http.MethodPost, "/api/scans/batch-commit",
					`{"scanEntryIds":["scan-a"],"commit":true}`, http.StatusOK)
				if !strings.Contains(string(again), "scan-a") || !strings.Contains(string(again), "This scan was already handled.") || !strings.Contains(string(again), "not_pending") {
					env.T.Fatalf("second commit = %s", again)
				}
				exchanges(httpExchange{
					method:         "GET",
					path:           "/api/inventory",
					expectedStatus: http.StatusOK,
					assertions:     []assertion{{path: "$[0].instanceCount", value: float64(4)}},
				})(env)
			},
		},
		{
			name: "a unit count on the batch is what gets stocked in",
			setup: func(env testEnv) {
				mustProduct(env, "prod-rice", "Rice")
				insertPendingScanUnits(env, "scan-rice", "prod-rice", scan.StockIn, 1)
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans/batch-commit",
				body:           `{"scanEntryIds":["scan-rice"],"unitCount":4,"commit":true}`,
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.committedIds[0]", value: "scan-rice"},
					{path: "$.failedIds", absent: true},
				},
			},
			afterRequest: exchanges(httpExchange{
				method:         "GET",
				path:           "/api/inventory",
				expectedStatus: http.StatusOK,
				assertions:     []assertion{{path: "$[0].instanceCount", value: float64(4)}},
			}),
		},
		{
			name: "empty and invalid batches change nothing",
			setup: func(env testEnv) {
				mustProduct(env, "prod-hold", "Held")
				insertPendingScanUnits(env, "scan-hold", "prod-hold", scan.StockIn, 2)
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans/batch-commit",
				body:           `{"scanEntryIds":[],"commit":true}`,
				expectedStatus: http.StatusBadRequest,
				assertions:     []assertion{{path: "$.error", value: "scanEntryIds is required"}},
			},
			afterRequest: func(env testEnv) {
				h := handlerFromEnv(env)
				invalid := exchangeJSON(env.T, h, http.MethodPost, "/api/scans/batch-commit",
					`{"scanEntryIds":["scan-hold"],"direction":"sideways","commit":true}`, http.StatusBadRequest)
				if !strings.Contains(string(invalid), "invalid direction value") {
					env.T.Fatalf("invalid direction = %s", invalid)
				}
				exchanges(
					httpExchange{
						method:         "GET",
						path:           "/api/scans",
						query:          map[string]string{"userId": "user-1", "status": "pending"},
						expectedStatus: http.StatusOK,
						assertions: []assertion{
							{path: "$[0].id", value: "scan-hold"},
							{path: "$[0].direction", value: "stock_in"},
							{path: "$[0].unitCount", value: float64(2)},
						},
					},
					httpExchange{
						method:         "GET",
						path:           "/api/inventory",
						expectedStatus: http.StatusOK,
						assertions:     []assertion{{path: "$", value: []any{}}},
					},
				)(env)
			},
		},
		{
			name: "a missing id fails that entry and the rest still commit",
			setup: func(env testEnv) {
				mustProduct(env, "prod-part", "Partial")
				insertPendingScanUnits(env, "scan-part", "prod-part", scan.StockIn, 2)
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans/batch-commit",
				body:           `{"scanEntryIds":["missing","scan-part"],"commit":true}`,
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.committedIds[0]", value: "scan-part"},
					{path: "$.failedIds[0]", value: "missing"},
					{path: "$.failedReasons[0]", value: "This scan is no longer in the queue."},
					{path: "$.failedCodes[0]", value: "scan_not_found"},
				},
			},
			afterRequest: exchanges(
				httpExchange{
					method:         "GET",
					path:           "/api/inventory",
					expectedStatus: http.StatusOK,
					assertions:     []assertion{{path: "$[0].instanceCount", value: float64(2)}},
				},
				httpExchange{
					method:         "GET",
					path:           "/api/scans",
					query:          map[string]string{"userId": "user-1", "status": "pending"},
					expectedStatus: http.StatusOK,
					assertions:     []assertion{{path: "$", value: []any{}}},
				},
			),
		},
		{
			name: "scans that cannot commit stay on the queue",
			setup: func(env testEnv) {
				mustProduct(env, "prod-gap", "Gap")
				seedShelfItem(env, "prod-empty", "Empty Shelf", "item-empty")
				insertPendingScanUnits(env, "scan-plain", "prod-gap", scan.StockIn, 1)
				at := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
				gap := "prod-gap"
				queue := scan.NewQueue(env.DB)
				if _, err := queue.CreateScanEntry(context.Background(), scan.ScanEntry{
					ID: "scan-nodir", UserID: "user-1", Barcode: "scan-nodir",
					ScannedAt: at, UnitCount: 1, Status: scan.Pending, ProductID: &gap,
				}); err != nil {
					env.T.Fatalf("scan without direction: %v", err)
				}
				in := scan.StockIn
				if _, err := queue.CreateScanEntry(context.Background(), scan.ScanEntry{
					ID: "scan-noprod", UserID: "user-1", Barcode: "scan-noprod",
					ScannedAt: at, Direction: &in, UnitCount: 1, Status: scan.Pending,
				}); err != nil {
					env.T.Fatalf("scan without product: %v", err)
				}
				insertPendingScanUnits(env, "scan-none", "prod-empty", scan.StockOut, 1)
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans/batch-commit",
				body:           `{"scanEntryIds":["scan-nodir","scan-noprod","scan-none"],"commit":true}`,
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.committedIds", absent: true},
					{path: "$.failedIds[0]", value: "scan-nodir"},
					{path: "$.failedReasons[0]", value: "Choose stock in or stock out first."},
					{path: "$.failedCodes[0]", value: "direction_unset"},
					{path: "$.failedIds[1]", value: "scan-noprod"},
					{path: "$.failedReasons[1]", value: "Pick a product for this scan first."},
					{path: "$.failedCodes[1]", value: "product_unresolved"},
					{path: "$.failedIds[2]", value: "scan-none"},
					{path: "$.failedReasons[2]", value: "Nothing on hand to take out."},
					{path: "$.failedCodes[2]", value: "none_on_hand"},
				},
				bodyExcludes: []string{"no available instances", "nothing on hand"},
			},
			afterRequest: exchanges(
				httpExchange{
					method:         "GET",
					path:           "/api/inventory",
					expectedStatus: http.StatusOK,
					assertions: []assertion{
						{path: "$[0].instanceCount", value: float64(0)},
						{path: "$[1]", absent: true},
					},
				},
				httpExchange{
					method:         "GET",
					path:           "/api/scans",
					query:          map[string]string{"userId": "user-1", "status": "pending"},
					expectedStatus: http.StatusOK,
					bodyContains:   []string{"scan-nodir", "scan-noprod", "scan-none", "scan-plain"},
					assertions:     []assertion{{path: "$[4]", absent: true}},
				},
			),
		},
		{
			name: "an unlinked stock-out stays pending without a link offer",
			setup: func(env testEnv) {
				mustProduct(env, "prod-unlinked", "Unlinked Soup")
				insertPendingScanUnits(env, "scan-unlinked", "prod-unlinked", scan.StockOut, 1)
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans/batch-commit",
				body:           `{"scanEntryIds":["scan-unlinked"],"commit":true}`,
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.committedIds", absent: true},
					{path: "$.failedIds[0]", value: "scan-unlinked"},
					{path: "$.failedReasons[0]", value: "Pick a product for this scan first."},
					{path: "$.failedCodes[0]", value: "product_unresolved"},
					{path: "$.suggestions", absent: true},
				},
				bodyExcludes: []string{"no_item", "Which item is this?", "Nothing on hand to take out.", "Not in your pantry", "No close match found.", "Something went wrong"},
			},
			afterRequest: exchanges(httpExchange{
				method:         "GET",
				path:           "/api/scans",
				query:          map[string]string{"userId": "user-1", "status": "pending"},
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$[0].id", value: "scan-unlinked"},
					{path: "$[0].status", value: "pending"},
					{path: "$[1]", absent: true},
				},
			}),
		},
		{
			name: "updating direction without commit leaves the scan pending",
			setup: func(env testEnv) {
				mustProduct(env, "prod-later", "Later")
				insertPendingScanUnits(env, "scan-later", "prod-later", scan.StockIn, 2)
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans/batch-commit",
				body:           `{"scanEntryIds":["scan-later"],"direction":"stock_out","expiresAt":"2027-01-02T00:00:00Z","commit":false}`,
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.updatedCount", value: float64(1)},
					{path: "$.committedIds", absent: true},
				},
			},
			afterRequest: exchanges(
				httpExchange{
					method:         "GET",
					path:           "/api/scans",
					query:          map[string]string{"userId": "user-1", "status": "pending"},
					expectedStatus: http.StatusOK,
					assertions: []assertion{
						{path: "$[0].id", value: "scan-later"},
						{path: "$[0].direction", value: "stock_out"},
						{path: "$[0].status", value: "pending"},
					},
					bodyContains: []string{"2027-01-02"},
				},
				httpExchange{
					method:         "GET",
					path:           "/api/inventory",
					expectedStatus: http.StatusOK,
					assertions:     []assertion{{path: "$", value: []any{}}},
				},
			),
		},
	})
}

func mustProduct(env testEnv, id, name string) {
	env.T.Helper()
	if err := env.ProductStore.CreateProduct(context.Background(), product.Product{
		ID: id, Name: name, Category: "Pantry", UnitOfMeasure: "unit",
	}); err != nil {
		env.T.Fatalf("CreateProduct %s: %v", id, err)
	}
}
