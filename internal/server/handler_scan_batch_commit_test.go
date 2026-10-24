package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/product"
	"github.com/Rhionin/stockbot/internal/scan"
)

func TestScanBatchCommit(t *testing.T) {
	mixed := loadCommitContract(t, "scan-batch-commit-partial.json")
	retry := loadCommitContract(t, "scan-batch-commit-retry.json")
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
					{path: `$.stockOutCounts["scan-out"].removedCount`, value: float64(1)},
					{path: `$.stockOutCounts["scan-out"].requestedCount`, value: float64(1)},
					{path: `$.stockOutCounts["scan-a"]`, absent: true},
					{path: `$.stockOutCounts["scan-b"]`, absent: true},
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
				// A stock-in that already committed is reported committed again.
				// It is not a not_pending failure, and nothing else is stocked.
				again := exchangeJSON(env.T, h, http.MethodPost, "/api/scans/batch-commit",
					`{"scanEntryIds":["scan-a"],"unitCount":4,"commit":true}`, http.StatusOK)
				var againBody struct {
					CommittedIDs   []string       `json:"committedIds"`
					FailedIDs      []string       `json:"failedIds"`
					StockOutCounts map[string]any `json:"stockOutCounts"`
				}
				if err := json.Unmarshal(again, &againBody); err != nil {
					env.T.Fatalf("second commit: %v\n%s", err, again)
				}
				if len(againBody.CommittedIDs) != 1 || againBody.CommittedIDs[0] != "scan-a" ||
					len(againBody.FailedIDs) != 0 || len(againBody.StockOutCounts) != 0 ||
					strings.Contains(string(again), "not_pending") {
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
					{path: "$.stockOutCounts", absent: true},
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
			name: "field updates with an unknown id are per item, not a 500",
			setup: func(env testEnv) {
				mustProduct(env, "prod-field-mix", "Field Peas")
				insertPendingScanUnits(env, "scan-field-a", "prod-field-mix", scan.StockIn, 1)
				insertPendingScanUnits(env, "scan-field-b", "prod-field-mix", scan.StockIn, 1)
			},
			httpExchange: httpExchange{
				method: "POST",
				path:   "/api/scans/batch-commit",
				body:   `{"scanEntryIds":["scan-field-a","missing-field","scan-field-b"],"direction":"stock_in","unitCount":4,"expiresAt":"2026-12-01T00:00:00Z","commit":true}`,
				// A 500 here is the bug: the unknown id used to fail the whole request.
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.updatedCount", value: float64(3)},
					{path: "$.committedIds[0]", value: "scan-field-a"},
					{path: "$.committedIds[1]", value: "scan-field-b"},
					{path: "$.failedIds[0]", value: "missing-field"},
					{path: "$.failedReasons[0]", value: "This scan is no longer in the queue."},
					{path: "$.failedCodes[0]", value: "scan_not_found"},
					{path: "$.error", absent: true},
				},
				bodyExcludes: []string{"Something went wrong"},
			},
			afterRequest: func(env testEnv) {
				h := handlerFromEnv(env)
				const body = `{"scanEntryIds":["scan-field-a","missing-field","scan-field-b"],"direction":"stock_in","unitCount":4,"expiresAt":"2026-12-01T00:00:00Z","commit":true}`
				exchanges(
					httpExchange{
						method:         "GET",
						path:           "/api/inventory",
						expectedStatus: http.StatusOK,
						assertions:     []assertion{{path: "$[0].instanceCount", value: float64(8)}},
					},
					httpExchange{
						method:         "GET",
						path:           "/api/scans",
						query:          map[string]string{"userId": "user-1", "status": "committed"},
						expectedStatus: http.StatusOK,
						assertions: []assertion{
							{path: "$[0].unitCount", value: float64(4)},
							{path: "$[1].unitCount", value: float64(4)},
							{path: "$[0].expiresAt", value: "2026-12-01T00:00:00Z"},
							{path: "$[1].expiresAt", value: "2026-12-01T00:00:00Z"},
						},
					},
				)(env)
				again := exchangeJSON(env.T, h, http.MethodPost, "/api/scans/batch-commit", body, http.StatusOK)
				if !strings.Contains(string(again), `"scan_not_found"`) || strings.Contains(string(again), "Something went wrong") {
					env.T.Fatalf("retry = %s", again)
				}
				exchanges(httpExchange{
					method:         "GET",
					path:           "/api/inventory",
					expectedStatus: http.StatusOK,
					assertions:     []assertion{{path: "$[0].instanceCount", value: float64(8)}},
				})(env)
			},
		},
		{
			name: "an update-only request reports an unknown id per item",
			setup: func(env testEnv) {
				mustProduct(env, "prod-upd-mix", "Update Peas")
				insertPendingScanUnits(env, "scan-upd-a", "prod-upd-mix", scan.StockIn, 1)
				insertPendingScanUnits(env, "scan-upd-b", "prod-upd-mix", scan.StockIn, 1)
			},
			httpExchange: httpExchange{
				method: "POST",
				path:   "/api/scans/batch-commit",
				body:   `{"scanEntryIds":["scan-upd-a","missing-upd","scan-upd-b"],"direction":"stock_out","unitCount":3,"expiresAt":"2027-03-04T00:00:00Z","commit":false}`,
				// A 500 here is the bug: the unknown id used to fail the whole update.
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.updatedCount", value: float64(3)},
					{path: "$.committedIds", absent: true},
					{path: "$.failedIds[0]", value: "missing-upd"},
					{path: "$.failedReasons[0]", value: "This scan is no longer in the queue."},
					{path: "$.failedCodes[0]", value: "scan_not_found"},
					{path: "$.failedIds[1]", absent: true},
					{path: "$.error", absent: true},
				},
				bodyExcludes: []string{"Something went wrong"},
			},
			afterRequest: exchanges(
				httpExchange{
					method:         "GET",
					path:           "/api/scans",
					query:          map[string]string{"userId": "user-1", "status": "pending"},
					expectedStatus: http.StatusOK,
					bodyContains:   []string{"scan-upd-a", "scan-upd-b"},
					assertions: []assertion{
						{path: "$[0].direction", value: "stock_out"},
						{path: "$[0].unitCount", value: float64(3)},
						{path: "$[0].expiresAt", value: "2027-03-04T00:00:00Z"},
						{path: "$[0].status", value: "pending"},
						{path: "$[1].direction", value: "stock_out"},
						{path: "$[1].unitCount", value: float64(3)},
						{path: "$[1].expiresAt", value: "2027-03-04T00:00:00Z"},
						{path: "$[1].status", value: "pending"},
						{path: "$[2]", absent: true},
					},
				},
				httpExchange{
					method:         "GET",
					path:           "/api/inventory",
					expectedStatus: http.StatusOK,
					assertions:     []assertion{{path: "$", value: []any{}}},
				},
			),
		},
		{
			name: "expiresAt alone is applied on confirm",
			setup: func(env testEnv) {
				mustProduct(env, "prod-expiry", "Expiry Peas")
				insertPendingScanUnits(env, "scan-expiry", "prod-expiry", scan.StockIn, 1)
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans/batch-commit",
				body:           `{"scanEntryIds":["scan-expiry"],"expiresAt":"2026-11-15T00:00:00Z","commit":true}`,
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.updatedCount", value: float64(1)},
					{path: "$.committedIds[0]", value: "scan-expiry"},
					{path: "$.failedIds", absent: true},
				},
			},
			afterRequest: exchanges(
				httpExchange{
					method:         "GET",
					path:           "/api/inventory",
					expectedStatus: http.StatusOK,
					assertions:     []assertion{{path: "$[0].instanceCount", value: float64(1)}},
				},
				httpExchange{
					method:         "GET",
					path:           "/api/scans",
					query:          map[string]string{"userId": "user-1", "status": "committed"},
					expectedStatus: http.StatusOK,
					assertions: []assertion{
						{path: "$[0].id", value: "scan-expiry"},
						{path: "$[0].unitCount", value: float64(1)},
						{path: "$[0].expiresAt", value: "2026-11-15T00:00:00Z"},
						{path: "$[1]", absent: true},
					},
				},
			),
		},
		{
			name: "a confirm with fields leaves a flagged card unedited",
			setup: func(env testEnv) {
				mustProduct(env, "prod-flag-mix", "Flag Peas")
				insertPendingScanUnits(env, "scan-open", "prod-flag-mix", scan.StockIn, 1)
				flagged := scan.Flagged
				in := scan.StockIn
				productID := "prod-flag-mix"
				if _, err := scan.NewQueue(env.DB).CreateScanEntry(context.Background(), primaryScope(env.T, env.DB), scan.ScanEntry{
					ID: "scan-held", UserID: "user-1", Barcode: "scan-held",
					ScannedAt: time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC),
					Direction: &in, UnitCount: 1, Status: flagged, ProductID: &productID,
				}); err != nil {
					env.T.Fatalf("flagged scan: %v", err)
				}
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans/batch-commit",
				body:           `{"scanEntryIds":["scan-open","scan-held"],"direction":"stock_in","unitCount":6,"expiresAt":"2026-10-02T00:00:00Z","commit":true}`,
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.committedIds[0]", value: "scan-open"},
					{path: "$.committedIds[1]", absent: true},
					{path: "$.failedIds[0]", value: "scan-held"},
					{path: "$.failedCodes[0]", value: "not_pending"},
					{path: "$.failedReasons[0]", value: "This scan was already handled."},
				},
			},
			afterRequest: exchanges(
				httpExchange{
					method:         "GET",
					path:           "/api/inventory",
					expectedStatus: http.StatusOK,
					assertions:     []assertion{{path: "$[0].instanceCount", value: float64(6)}},
				},
				httpExchange{
					method:         "GET",
					path:           "/api/scans",
					query:          map[string]string{"userId": "user-1", "status": "committed"},
					expectedStatus: http.StatusOK,
					assertions: []assertion{
						{path: "$[0].id", value: "scan-open"},
						{path: "$[0].unitCount", value: float64(6)},
						{path: "$[0].expiresAt", value: "2026-10-02T00:00:00Z"},
						{path: "$[1]", absent: true},
					},
				},
				httpExchange{
					method:         "GET",
					path:           "/api/scans",
					query:          map[string]string{"userId": "user-1", "status": "flagged"},
					expectedStatus: http.StatusOK,
					assertions: []assertion{
						{path: "$[0].id", value: "scan-held"},
						{path: "$[0].status", value: "flagged"},
						{path: "$[0].unitCount", value: float64(1)},
						{path: "$[0].expiresAt", value: nil},
						{path: "$[1]", absent: true},
					},
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
				if _, err := queue.CreateScanEntry(context.Background(), primaryScope(env.T, env.DB), scan.ScanEntry{
					ID: "scan-nodir", UserID: "user-1", Barcode: "scan-nodir",
					ScannedAt: at, UnitCount: 1, Status: scan.Pending, ProductID: &gap,
				}); err != nil {
					env.T.Fatalf("scan without direction: %v", err)
				}
				in := scan.StockIn
				if _, err := queue.CreateScanEntry(context.Background(), primaryScope(env.T, env.DB), scan.ScanEntry{
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
					{path: "$.failedReasons[1]", value: "Choose which item this is first."},
					{path: "$.failedCodes[1]", value: "product_unresolved"},
					{path: "$.failedIds[2]", value: "scan-none"},
					{path: "$.failedReasons[2]", value: "Nothing on hand to take out."},
					{path: "$.failedCodes[2]", value: "none_on_hand"},
					{path: "$.stockOutCounts", absent: true},
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
			name: "a shelved stock-out stays pending with nothing on hand",
			setup: func(env testEnv) {
				seedShelfItem(env, "prod-wiped", "Wiped Soup", "item-wiped")
				if _, err := env.DB.ExecContext(context.Background(), `UPDATE items SET shelved = 1 WHERE id = 'item-wiped'`); err != nil {
					env.T.Fatalf("shelve item: %v", err)
				}
				insertPendingScanUnits(env, "scan-wiped", "prod-wiped", scan.StockOut, 1)
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans/batch-commit",
				body:           `{"scanEntryIds":["scan-wiped"],"commit":true}`,
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.committedIds", absent: true},
					{path: "$.failedIds[0]", value: "scan-wiped"},
					{path: "$.failedReasons[0]", value: "Nothing on hand to take out."},
					{path: "$.failedCodes[0]", value: "none_on_hand"},
					{path: "$.suggestions", absent: true},
				},
				bodyExcludes: []string{"no_item", "Which item is this?", "Choose which item this is first.", "Something went wrong"},
			},
			afterRequest: exchanges(httpExchange{
				method:         "GET",
				path:           "/api/scans",
				query:          map[string]string{"userId": "user-1", "status": "pending"},
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$[0].id", value: "scan-wiped"},
					{path: "$[0].status", value: "pending"},
					{path: "$[1]", absent: true},
				},
			}),
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
					{path: "$.failedReasons[0]", value: "Choose which item this is first."},
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
					{path: "$.stockOutCounts", absent: true},
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
		{
			name: "a stock-out unit count is how many the batch takes off",
			setup: func(env testEnv) {
				mustProduct(env, "prod-batch-out", "Batch Oats")
				soon := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
				mid := soon.Add(24 * time.Hour)
				late := soon.Add(48 * time.Hour)
				stockShelf(env, "item-batch-out", "user-1", "prod-batch-out", []shelfUnit{
					{"inst-batch-old", &soon},
					{"inst-batch-mid", &mid},
					{"inst-batch-new", &late},
				})
				insertPendingScanUnits(env, "scan-batch-out", "prod-batch-out", scan.StockOut, 2)
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans/batch-commit",
				body:           `{"scanEntryIds":["scan-batch-out"],"commit":true}`,
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.committedIds[0]", value: "scan-batch-out"},
					{path: "$.failedIds", absent: true},
					{path: `$.stockOutCounts["scan-batch-out"].removedCount`, value: float64(2)},
					{path: `$.stockOutCounts["scan-batch-out"].requestedCount`, value: float64(2)},
				},
			},
			afterRequest: exchanges(
				httpExchange{
					method:         "GET",
					path:           "/api/inventory",
					expectedStatus: http.StatusOK,
					assertions:     []assertion{{path: "$[0].instanceCount", value: float64(1)}},
				},
				httpExchange{
					method:         "GET",
					path:           "/api/inventory/item-batch-out/instances",
					expectedStatus: http.StatusOK,
					assertions: []assertion{
						{path: "$[0].id", value: "inst-batch-new"},
						{path: "$[1]", absent: true},
					},
				},
			),
		},
		{
			name: "a unit count on the batch is what gets taken out",
			setup: func(env testEnv) {
				mustProduct(env, "prod-batch-set", "Batch Rice")
				soon := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
				mid := soon.Add(24 * time.Hour)
				late := soon.Add(48 * time.Hour)
				stockShelf(env, "item-batch-set", "user-1", "prod-batch-set", []shelfUnit{
					{"inst-set-old", &soon},
					{"inst-set-mid", &mid},
					{"inst-set-new", &late},
				})
				insertPendingScanUnits(env, "scan-batch-set", "prod-batch-set", scan.StockOut, 1)
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans/batch-commit",
				body:           `{"scanEntryIds":["scan-batch-set"],"unitCount":2,"commit":true}`,
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.committedIds[0]", value: "scan-batch-set"},
					{path: "$.failedIds", absent: true},
					{path: `$.stockOutCounts["scan-batch-set"].removedCount`, value: float64(2)},
					{path: `$.stockOutCounts["scan-batch-set"].requestedCount`, value: float64(2)},
				},
			},
			afterRequest: exchanges(httpExchange{
				method:         "GET",
				path:           "/api/inventory",
				expectedStatus: http.StatusOK,
				assertions:     []assertion{{path: "$[0].instanceCount", value: float64(1)}},
			}),
		},
		{
			name: "a batch stock-out that exceeds the shelf still commits",
			setup: func(env testEnv) {
				mustProduct(env, "prod-batch-short", "Batch Short")
				soon := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
				stockShelf(env, "item-batch-short", "user-1", "prod-batch-short", []shelfUnit{
					{"inst-short", &soon},
				})
				insertPendingScanUnits(env, "scan-batch-short", "prod-batch-short", scan.StockOut, 3)
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans/batch-commit",
				body:           `{"scanEntryIds":["scan-batch-short"],"commit":true}`,
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.committedIds[0]", value: "scan-batch-short"},
					{path: "$.failedIds", absent: true},
					{path: `$.stockOutCounts["scan-batch-short"].removedCount`, value: float64(1)},
					{path: `$.stockOutCounts["scan-batch-short"].requestedCount`, value: float64(3)},
				},
			},
			afterRequest: exchanges(httpExchange{
				method:         "GET",
				path:           "/api/inventory",
				expectedStatus: http.StatusOK,
				assertions:     []assertion{{path: "$[0].instanceCount", value: float64(0)}},
			}),
		},
		{
			name: "a mixed batch reports each committed stock-out",
			setup: func(env testEnv) {
				soon := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
				mid := soon.Add(24 * time.Hour)
				late := soon.Add(48 * time.Hour)
				mustProduct(env, "prod-batch-full", "Batch Full")
				stockShelf(env, "item-batch-full", "user-1", "prod-batch-full", []shelfUnit{
					{"inst-full-old", &soon},
					{"inst-full-mid", &mid},
					{"inst-full-new", &late},
				})
				insertPendingScanUnits(env, "scan-batch-full", "prod-batch-full", scan.StockOut, 2)
				mustProduct(env, "prod-batch-partial", "Batch Partial")
				stockShelf(env, "item-batch-partial", "user-1", "prod-batch-partial", []shelfUnit{
					{"inst-partial-only", &soon},
				})
				insertPendingScanUnits(env, "scan-batch-partial", "prod-batch-partial", scan.StockOut, 3)
				mustProduct(env, "prod-batch-in", "Batch In")
				insertPendingScanUnits(env, "scan-batch-in", "prod-batch-in", scan.StockIn, 2)
				mustProduct(env, "prod-batch-none", "Batch Empty")
				stockShelf(env, "item-batch-none", "user-1", "prod-batch-none", nil)
				insertPendingScanUnits(env, "scan-batch-none", "prod-batch-none", scan.StockOut, 1)
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans/batch-commit",
				body:           `{"scanEntryIds":["scan-batch-full","scan-batch-partial","scan-batch-in","scan-batch-none"],"commit":true}`,
				expectedStatus: http.StatusOK,
			},
			afterRequest: func(env testEnv) {
				raw, err := io.ReadAll(env.Res.Body)
				if err != nil {
					env.T.Fatalf("read batch: %v", err)
				}
				if env.Res.StatusCode != mixed.Status {
					env.T.Fatalf("status %d, contract %d", env.Res.StatusCode, mixed.Status)
				}
				assertContractBody(env.T, raw, mixed)
				exchanges(
					httpExchange{
						method:         "GET",
						path:           "/api/inventory",
						expectedStatus: http.StatusOK,
						assertions: []assertion{
							{path: "$[0].item.productId", value: "prod-batch-none"},
							{path: "$[0].instanceCount", value: float64(0)},
							{path: "$[1].item.productId", value: "prod-batch-full"},
							{path: "$[1].instanceCount", value: float64(1)},
							{path: "$[2].item.productId", value: "prod-batch-in"},
							{path: "$[2].instanceCount", value: float64(2)},
							{path: "$[3].item.productId", value: "prod-batch-partial"},
							{path: "$[3].instanceCount", value: float64(0)},
							{path: "$[4]", absent: true},
						},
					},
					httpExchange{
						method:         "GET",
						path:           "/api/inventory/item-batch-full/instances",
						expectedStatus: http.StatusOK,
						assertions: []assertion{
							{path: "$[0].id", value: "inst-full-new"},
							{path: "$[1]", absent: true},
						},
					},
					httpExchange{
						method:         "GET",
						path:           "/api/scans",
						query:          map[string]string{"userId": "user-1", "status": "pending"},
						expectedStatus: http.StatusOK,
						assertions: []assertion{
							{path: "$[0].id", value: "scan-batch-none"},
							{path: "$[0].status", value: "pending"},
							{path: "$[1]", absent: true},
						},
					},
				)(env)
			},
		},
		{
			name: "a repeated scan id is committed once",
			setup: func(env testEnv) {
				mustProduct(env, "prod-dup", "Dup Oats")
				insertPendingScanUnits(env, "scan-dup", "prod-dup", scan.StockIn, 1)
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans/batch-commit",
				body:           `{"scanEntryIds":["scan-dup","scan-dup"],"commit":true}`,
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.updatedCount", value: float64(1)},
					{path: "$.committedIds[0]", value: "scan-dup"},
					{path: "$.committedIds[1]", absent: true},
					{path: "$.failedIds", absent: true},
				},
				bodyExcludes: []string{"not_pending", "already handled"},
			},
			afterRequest: exchanges(httpExchange{
				method:         "GET",
				path:           "/api/inventory",
				expectedStatus: http.StatusOK,
				assertions:     []assertion{{path: "$[0].instanceCount", value: float64(1)}},
			}),
		},
		{
			name: "a committed stock-out stays committed with its original counts",
			setup: func(env testEnv) {
				soon := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
				mustProduct(env, "prod-retry", "Retry Oats")
				stockShelf(env, "item-retry", "user-1", "prod-retry", []shelfUnit{
					{"inst-retry-only", &soon},
				})
				insertPendingScanUnits(env, "scan-retry", "prod-retry", scan.StockOut, 3)
				h := handlerFromEnv(env)
				exchangeJSON(env.T, h, http.MethodPost, "/api/scans/batch-commit",
					`{"scanEntryIds":["scan-retry"],"commit":true}`, http.StatusOK)
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans/batch-commit",
				body:           `{"scanEntryIds":["scan-retry"],"unitCount":4,"direction":"stock_in","expiresAt":"2026-08-01T00:00:00Z","commit":true}`,
				expectedStatus: http.StatusOK,
			},
			afterRequest: func(env testEnv) {
				raw, err := io.ReadAll(env.Res.Body)
				if err != nil {
					env.T.Fatalf("read retry: %v", err)
				}
				if env.Res.StatusCode != retry.Status {
					env.T.Fatalf("status %d, contract %d", env.Res.StatusCode, retry.Status)
				}
				assertContractBody(env.T, raw, retry)
				exchanges(
					httpExchange{
						method:         "GET",
						path:           "/api/inventory",
						expectedStatus: http.StatusOK,
						assertions:     []assertion{{path: "$[0].instanceCount", value: float64(0)}},
					},
					httpExchange{
						method:         "GET",
						path:           "/api/scans/history",
						query:          map[string]string{"userId": "user-1"},
						expectedStatus: http.StatusOK,
						assertions: []assertion{
							{path: "$[0].id", value: "scan-retry"},
							{path: "$[0].unitCount", value: float64(3)},
							{path: "$[0].direction", value: "stock_out"},
							{path: "$[0].expiresAt", value: nil},
						},
						bodyExcludes: []string{"2026-08-01"},
					},
				)(env)
			},
		},
		{
			name: "a retry sending unitCount 4 leaves the committed count at 1",
			setup: func(env testEnv) {
				soon := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
				mustProduct(env, "prod-keep-count", "Keep Count")
				stockShelf(env, "item-keep-count", "user-1", "prod-keep-count", []shelfUnit{
					{"inst-keep-1", &soon},
					{"inst-keep-2", &soon},
					{"inst-keep-3", &soon},
					{"inst-keep-4", &soon},
				})
				insertPendingScanUnits(env, "scan-keep-count", "prod-keep-count", scan.StockOut, 1)
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans/batch-commit",
				body:           `{"scanEntryIds":["scan-keep-count"],"commit":true}`,
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: `$.stockOutCounts["scan-keep-count"].removedCount`, value: float64(1)},
					{path: `$.stockOutCounts["scan-keep-count"].requestedCount`, value: float64(1)},
				},
			},
			afterRequest: func(env testEnv) {
				h := handlerFromEnv(env)
				again := exchangeJSON(env.T, h, http.MethodPost, "/api/scans/batch-commit",
					`{"scanEntryIds":["scan-keep-count"],"unitCount":4,"direction":"stock_in","commit":true}`, http.StatusOK)
				var body struct {
					CommittedIDs   []string `json:"committedIds"`
					FailedIDs      []string `json:"failedIds"`
					StockOutCounts map[string]struct {
						RemovedCount   int `json:"removedCount"`
						RequestedCount int `json:"requestedCount"`
					} `json:"stockOutCounts"`
				}
				if err := json.Unmarshal(again, &body); err != nil {
					env.T.Fatalf("retry: %v\n%s", err, again)
				}
				counts := body.StockOutCounts["scan-keep-count"]
				if len(body.CommittedIDs) != 1 || body.CommittedIDs[0] != "scan-keep-count" ||
					len(body.FailedIDs) != 0 || counts.RemovedCount != 1 || counts.RequestedCount != 1 {
					env.T.Fatalf("retry = %s", again)
				}
				single := exchangeJSON(env.T, h, http.MethodPost, "/api/scans/scan-keep-count/commit", "", http.StatusBadRequest)
				if !strings.Contains(string(single), "This scan was already handled.") || !strings.Contains(string(single), "not_pending") {
					env.T.Fatalf("single retry = %s", single)
				}
				exchanges(
					httpExchange{
						method:         "GET",
						path:           "/api/scans/history",
						query:          map[string]string{"userId": "user-1"},
						expectedStatus: http.StatusOK,
						assertions: []assertion{
							{path: "$[0].id", value: "scan-keep-count"},
							{path: "$[0].unitCount", value: float64(1)},
							{path: "$[0].direction", value: "stock_out"},
						},
					},
					httpExchange{
						method:         "GET",
						path:           "/api/inventory",
						expectedStatus: http.StatusOK,
						assertions:     []assertion{{path: "$[0].instanceCount", value: float64(3)}},
					},
				)(env)
			},
		},
		{
			name: "a cancelled scan stays not_pending beside a committed retry",
			setup: func(env testEnv) {
				soon := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
				mustProduct(env, "prod-mix-retry", "Mix Retry")
				stockShelf(env, "item-mix-retry", "user-1", "prod-mix-retry", []shelfUnit{
					{"inst-mix-retry", &soon},
				})
				insertPendingScanUnits(env, "scan-mix-committed", "prod-mix-retry", scan.StockOut, 1)
				insertPendingScanUnits(env, "scan-mix-cancelled", "prod-mix-retry", scan.StockOut, 1)
				if _, err := env.DB.Exec(`UPDATE scan_entries SET status = 'cancelled' WHERE id = ?`, "scan-mix-cancelled"); err != nil {
					env.T.Fatalf("cancel scan: %v", err)
				}
				h := handlerFromEnv(env)
				exchangeJSON(env.T, h, http.MethodPost, "/api/scans/batch-commit",
					`{"scanEntryIds":["scan-mix-committed"],"commit":true}`, http.StatusOK)
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans/batch-commit",
				body:           `{"scanEntryIds":["scan-mix-committed","scan-mix-cancelled"],"unitCount":4,"commit":true}`,
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.updatedCount", value: float64(2)},
					{path: "$.committedIds[0]", value: "scan-mix-committed"},
					{path: "$.committedIds[1]", absent: true},
					{path: "$.failedIds[0]", value: "scan-mix-cancelled"},
					{path: "$.failedCodes[0]", value: "not_pending"},
					{path: "$.failedReasons[0]", value: "This scan was already handled."},
					{path: `$.stockOutCounts["scan-mix-committed"].removedCount`, value: float64(1)},
					{path: `$.stockOutCounts["scan-mix-committed"].requestedCount`, value: float64(1)},
					{path: `$.stockOutCounts["scan-mix-cancelled"]`, absent: true},
				},
			},
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
