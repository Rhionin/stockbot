package server

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/scan"
)

func TestScanUpdateRefusesStatusChangeOnCommittedScan(t *testing.T) {
	for _, status := range []string{"cancelled", "pending", "flagged"} {
		status := status
		t.Run(status, func(t *testing.T) {
			var moveID string
			runHandlerTests(t, []handlerTestCase{
				{
					name: "status change is rejected and the stock move stays",
					setup: func(env testEnv) {
						mustProduct(env, "prod-milk", "Whole Milk")
						insertPendingScanUnits(env, "scan-milk", "prod-milk", scan.StockIn, 2)
						commitScan(env, handlerFromEnv(env), "scan-milk")
						moveID = historyMoveID(env, "in")
					},
					httpExchange: httpExchange{
						method:         "PATCH",
						path:           "/api/scans/scan-milk",
						body:           fmt.Sprintf(`{"status":%q,"unitCount":9}`, status),
						expectedStatus: http.StatusConflict,
						assertions: []assertion{
							{path: "$.code", value: "not_open"},
							{path: "$.error", value: scan.ErrNotOpen.Error()},
							{path: "$.members", absent: true},
						},
					},
					afterRequest: func(env testEnv) {
						exchanges(
							httpExchange{
								method:         "GET",
								path:           "/api/scans/history",
								query:          map[string]string{"userId": "user-1"},
								expectedStatus: http.StatusOK,
								assertions: []assertion{
									{path: "$[0].id", value: "scan-milk"},
									{path: "$[0].status", value: "committed"},
									{path: "$[0].unitCount", value: float64(2)},
								},
							},
							httpExchange{
								method:         "GET",
								path:           "/api/scans",
								query:          map[string]string{"userId": "user-1", "status": "cancelled"},
								expectedStatus: http.StatusOK,
								bodyExcludes:   []string{"scan-milk"},
							},
							httpExchange{
								method:         "GET",
								path:           "/api/history",
								expectedStatus: http.StatusOK,
								assertions: []assertion{
									{path: "$.moves[0].id", value: moveID},
									{path: "$.moves[0].direction", value: "in"},
									{path: "$.moves[0].quantity", value: float64(2)},
									{path: "$.moves[0].source", value: "scan"},
									{path: "$.moves[0].productName", value: "Whole Milk"},
									{path: "$.moves[1]", absent: true},
								},
							},
							httpExchange{
								method:         "GET",
								path:           "/api/inventory",
								expectedStatus: http.StatusOK,
								assertions: []assertion{
									{path: "$[0].instanceCount", value: float64(2)},
								},
							},
						)(env)
					},
				},
			})
		})
	}
}

// TestScanUpdateRefusesEveryFieldOnClosedScans fails if the open-scan guard
// is applied only when the body includes status. Each non-status field is
// patched on both a committed scan and a cancelled scan.
func TestScanUpdateRefusesEveryFieldOnClosedScans(t *testing.T) {
	fields := []struct {
		name string
		body string
	}{
		{name: "unitCount", body: `{"unitCount":9}`},
		{name: "direction", body: `{"direction":"stock_out"}`},
		{name: "expiresAt", body: `{"expiresAt":"2030-01-02T00:00:00Z"}`},
		{name: "productId", body: `{"productId":"prod-other"}`},
	}

	var tests []handlerTestCase
	for _, field := range fields {
		field := field
		var moveID string
		tests = append(tests, handlerTestCase{
			name: "committed " + field.name,
			setup: func(env testEnv) {
				seedClosedScanProducts(env)
				insertPendingScanUnits(env, "scan-milk", "prod-milk", scan.StockIn, 2)
				commitScan(env, handlerFromEnv(env), "scan-milk")
				moveID = historyMoveID(env, "in")
			},
			httpExchange: closedScanPatch("scan-milk", field.body),
			afterRequest: func(env testEnv) {
				exchanges(
					committedScanUnchanged(),
					httpExchange{
						method:         "GET",
						path:           "/api/history",
						expectedStatus: http.StatusOK,
						assertions: []assertion{
							{path: "$.moves[0].id", value: moveID},
							{path: "$.moves[0].quantity", value: float64(2)},
							{path: "$.moves[0].direction", value: "in"},
							{path: "$.moves[0].source", value: "scan"},
							{path: "$.moves[1]", absent: true},
						},
					},
				)(env)
			},
		})
		tests = append(tests, handlerTestCase{
			name: "cancelled " + field.name,
			setup: func(env testEnv) {
				seedClosedScanProducts(env)
				insertClosedScan(env, "scan-closed", scan.Cancelled)
			},
			httpExchange: closedScanPatch("scan-closed", field.body),
			afterRequest: exchanges(cancelledScanUnchanged()),
		})
	}
	runHandlerTests(t, tests)
}

func TestScanUpdateRefusesStatusChangeOnCancelledScan(t *testing.T) {
	runHandlerTests(t, []handlerTestCase{
		{
			name: "cancelled scan rejects another status",
			setup: func(env testEnv) {
				seedClosedScanProducts(env)
				insertClosedScan(env, "scan-closed", scan.Cancelled)
			},
			httpExchange: closedScanPatch("scan-closed", `{"status":"pending"}`),
			afterRequest: exchanges(cancelledScanUnchanged()),
		},
	})
}

func TestScanUpdateNotOpenCopy(t *testing.T) {
	runHandlerTests(t, []handlerTestCase{
		{
			name: "409 uses the handled sentence",
			setup: func(env testEnv) {
				seedClosedScanProducts(env)
				insertClosedScan(env, "scan-closed", scan.Committed)
			},
			httpExchange: httpExchange{
				method:         "PATCH",
				path:           "/api/scans/scan-closed",
				body:           `{"status":"cancelled"}`,
				expectedStatus: http.StatusConflict,
				assertions: []assertion{
					{path: "$.error", value: "This scan was already handled."},
					{path: "$.code", value: "not_open"},
				},
			},
		},
	})
}

func TestScanUpdateEmptyBodyOnClosedScan(t *testing.T) {
	runHandlerTests(t, []handlerTestCase{
		{
			name: "empty body on a committed scan",
			setup: func(env testEnv) {
				seedClosedScanProducts(env)
				insertClosedScan(env, "scan-closed", scan.Committed)
			},
			httpExchange: httpExchange{
				method:         "PATCH",
				path:           "/api/scans/scan-closed",
				body:           `{}`,
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.id", value: "scan-closed"},
					{path: "$.status", value: "committed"},
					{path: "$.unitCount", value: float64(2)},
					{path: "$.direction", value: "stock_in"},
					{path: "$.productId", value: "prod-milk"},
					{path: "$.expiresAt", value: nil},
				},
			},
			afterRequest: exchanges(httpExchange{
				method:         "GET",
				path:           "/api/scans/history",
				query:          map[string]string{"userId": "user-1"},
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$[0].id", value: "scan-closed"},
					{path: "$[0].status", value: "committed"},
					{path: "$[0].unitCount", value: float64(2)},
				},
			}),
		},
		{
			name: "empty body on a cancelled scan",
			setup: func(env testEnv) {
				seedClosedScanProducts(env)
				insertClosedScan(env, "scan-closed", scan.Cancelled)
			},
			httpExchange: httpExchange{
				method:         "PATCH",
				path:           "/api/scans/scan-closed",
				body:           `{}`,
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.id", value: "scan-closed"},
					{path: "$.status", value: "cancelled"},
					{path: "$.unitCount", value: float64(2)},
					{path: "$.direction", value: "stock_in"},
					{path: "$.productId", value: "prod-milk"},
					{path: "$.expiresAt", value: nil},
				},
			},
			afterRequest: exchanges(cancelledScanUnchanged()),
		},
	})
}

func TestScanUpdateMissingScanIsNotFound(t *testing.T) {
	runHandlerTests(t, []handlerTestCase{
		{
			name: "missing scan id",
			httpExchange: httpExchange{
				method:         "PATCH",
				path:           "/api/scans/missing-scan",
				body:           `{"unitCount":4}`,
				expectedStatus: http.StatusNotFound,
				assertions: []assertion{
					{path: "$.error", value: scan.ErrNotFound.Error()},
					{path: "$.code", absent: true},
				},
			},
		},
	})
}

func closedScanPatch(id, body string) httpExchange {
	return httpExchange{
		method:         "PATCH",
		path:           "/api/scans/" + id,
		body:           body,
		expectedStatus: http.StatusConflict,
		assertions: []assertion{
			{path: "$.code", value: "not_open"},
			{path: "$.error", value: scan.ErrNotOpen.Error()},
		},
	}
}

func committedScanUnchanged() httpExchange {
	return httpExchange{
		method:         "GET",
		path:           "/api/scans/history",
		query:          map[string]string{"userId": "user-1"},
		expectedStatus: http.StatusOK,
		assertions: []assertion{
			{path: "$[0].id", value: "scan-milk"},
			{path: "$[0].status", value: "committed"},
			{path: "$[0].unitCount", value: float64(2)},
			{path: "$[0].direction", value: "stock_in"},
			{path: "$[0].productId", value: "prod-milk"},
			{path: "$[0].expiresAt", value: nil},
		},
	}
}

func cancelledScanUnchanged() httpExchange {
	return httpExchange{
		method:         "GET",
		path:           "/api/scans",
		query:          map[string]string{"userId": "user-1", "status": "cancelled"},
		expectedStatus: http.StatusOK,
		assertions: []assertion{
			{path: "$[0].id", value: "scan-closed"},
			{path: "$[0].status", value: "cancelled"},
			{path: "$[0].unitCount", value: float64(2)},
			{path: "$[0].direction", value: "stock_in"},
			{path: "$[0].productId", value: "prod-milk"},
			{path: "$[0].expiresAt", value: nil},
		},
	}
}

func seedClosedScanProducts(env testEnv) {
	env.T.Helper()
	mustProduct(env, "prod-milk", "Whole Milk")
	mustProduct(env, "prod-other", "Other Milk")
}

func insertClosedScan(env testEnv, id string, status scan.ScanStatus) {
	env.T.Helper()
	direction := scan.StockIn
	productID := "prod-milk"
	queue := scan.NewQueue(env.DB)
	if _, err := queue.CreateScanEntry(context.Background(), scan.ScanEntry{
		ID:        id,
		UserID:    "user-1",
		Barcode:   id,
		ScannedAt: time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
		Direction: &direction,
		UnitCount: 2,
		ProductID: &productID,
		Status:    status,
	}); err != nil {
		env.T.Fatalf("CreateScanEntry %s: %v", id, err)
	}
}

func TestScanUpdateCancelsOpenScans(t *testing.T) {
	runHandlerTests(t, []handlerTestCase{
		{
			name: "cancel a pending scan",
			setup: func(env testEnv) {
				mustProduct(env, "prod-beans", "Pending Beans")
				insertPendingScanUnits(env, "scan-pending", "prod-beans", scan.StockIn, 1)
			},
			httpExchange: httpExchange{
				method:         "PATCH",
				path:           "/api/scans/scan-pending",
				body:           `{"status":"cancelled"}`,
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.id", value: "scan-pending"},
					{path: "$.status", value: "cancelled"},
					{path: "$.unitCount", value: float64(1)},
				},
			},
			afterRequest: openScanCancelled("scan-pending"),
		},
		{
			name: "cancel a flagged scan",
			setup: func(env testEnv) {
				insertOpenScan(env, "scan-flagged", "000111222333", scan.Flagged)
			},
			httpExchange: httpExchange{
				method:         "PATCH",
				path:           "/api/scans/scan-flagged",
				body:           `{"status":"cancelled"}`,
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.id", value: "scan-flagged"},
					{path: "$.status", value: "cancelled"},
				},
			},
			afterRequest: openScanCancelled("scan-flagged"),
		},
		{
			name: "cancel a qr scan",
			setup: func(env testEnv) {
				insertOpenScan(env, "scan-qr", "https://example.com/groceries/milk", scan.QR)
			},
			httpExchange: httpExchange{
				method:         "PATCH",
				path:           "/api/scans/scan-qr",
				body:           `{"status":"cancelled"}`,
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.id", value: "scan-qr"},
					{path: "$.status", value: "cancelled"},
				},
			},
			afterRequest: openScanCancelled("scan-qr"),
		},
	})
}

func openScanCancelled(id string) func(env testEnv) {
	return exchanges(
		httpExchange{
			method:         "GET",
			path:           "/api/scans",
			query:          map[string]string{"userId": "user-1", "status": "cancelled"},
			expectedStatus: http.StatusOK,
			assertions: []assertion{
				{path: "$[0].id", value: id},
				{path: "$[0].status", value: "cancelled"},
			},
		},
		httpExchange{
			method:         "GET",
			path:           "/api/scans/history",
			query:          map[string]string{"userId": "user-1"},
			expectedStatus: http.StatusOK,
			bodyExcludes:   []string{id},
		},
	)
}

func TestScanUpdateRejectsCommittedStatus(t *testing.T) {
	cases := []struct {
		name        string
		status      scan.ScanStatus
		direction   scan.ScanDirection
		units       int
		withProduct bool
		onHand      int
		commitAfter bool
	}{
		{name: "pending stock-in", status: scan.Pending, direction: scan.StockIn, units: 3, withProduct: true, commitAfter: true},
		{name: "pending stock-out", status: scan.Pending, direction: scan.StockOut, units: 2, withProduct: true, onHand: 2},
		{name: "flagged stock-in", status: scan.Flagged, direction: scan.StockIn, units: 1},
		{name: "flagged stock-out", status: scan.Flagged, direction: scan.StockOut, units: 2, onHand: 2},
		{name: "qr stock-in", status: scan.QR, direction: scan.StockIn, units: 1},
		{name: "qr stock-out", status: scan.QR, direction: scan.StockOut, units: 2, onHand: 2},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			runHandlerTests(t, []handlerTestCase{
				{
					name: "status committed is invalid and writes nothing",
					setup: func(env testEnv) {
						mustProduct(env, "prod-milk", "Whole Milk")
						if tc.onHand > 0 {
							seedOnHand(env, "prod-milk", tc.onHand)
						}
						insertDirectedScan(env, "scan-open", tc.status, tc.direction, tc.units, tc.withProduct)
					},
					httpExchange: httpExchange{
						method:         "PATCH",
						path:           "/api/scans/scan-open",
						body:           `{"status":"committed","unitCount":9}`,
						expectedStatus: http.StatusBadRequest,
						assertions: []assertion{
							{path: "$.error", value: "invalid status value"},
							{path: "$.code", absent: true},
						},
					},
					afterRequest: func(env testEnv) {
						assertScanRowUntouched(env, "scan-open", tc.status, tc.units)
						if env.T.Failed() {
							return
						}
						productAssert := assertion{path: "$[0].productId", value: nil}
						if tc.withProduct {
							productAssert = assertion{path: "$[0].productId", value: "prod-milk"}
						}
						inventory := httpExchange{
							method:         "GET",
							path:           "/api/inventory",
							expectedStatus: http.StatusOK,
							assertions:     []assertion{{path: "$", value: []any{}}},
						}
						if tc.onHand > 0 {
							inventory.assertions = []assertion{{path: "$[0].instanceCount", value: float64(tc.onHand)}}
						}
						checks := []httpExchange{
							{
								method:         "GET",
								path:           "/api/scans",
								query:          map[string]string{"userId": "user-1", "status": string(tc.status)},
								expectedStatus: http.StatusOK,
								assertions: []assertion{
									{path: "$[0].id", value: "scan-open"},
									{path: "$[0].status", value: string(tc.status)},
									{path: "$[0].unitCount", value: float64(tc.units)},
									{path: "$[0].direction", value: string(tc.direction)},
									{path: "$[0].committedAt", value: nil},
									productAssert,
								},
							},
							{
								method:         "GET",
								path:           "/api/scans/history",
								query:          map[string]string{"userId": "user-1"},
								expectedStatus: http.StatusOK,
								bodyExcludes:   []string{"scan-open"},
							},
							inventory,
						}
						if tc.onHand == 0 {
							checks = append(checks, httpExchange{
								method:         "GET",
								path:           "/api/history",
								expectedStatus: http.StatusOK,
								assertions:     []assertion{{path: "$.moves", value: []any{}}},
							})
						}
						exchanges(checks...)(env)
						if !tc.commitAfter || env.T.Failed() {
							return
						}
						exchanges(
							httpExchange{
								method:         "POST",
								path:           "/api/scans/scan-open/commit",
								body:           `{}`,
								expectedStatus: http.StatusOK,
								assertions: []assertion{
									{path: "$.status", value: "committed"},
									{path: "$.unitCount", value: float64(tc.units)},
								},
							},
							httpExchange{
								method:         "GET",
								path:           "/api/inventory",
								expectedStatus: http.StatusOK,
								assertions:     []assertion{{path: "$[0].instanceCount", value: float64(tc.units)}},
							},
						)(env)
					},
				},
			})
		})
	}
}

func TestScanUpdateKeepsCancelAndResolve(t *testing.T) {
	runHandlerTests(t, []handlerTestCase{
		{
			name: "cancel a pending scan",
			setup: func(env testEnv) {
				mustProduct(env, "prod-beans", "Beans")
				insertPendingScanUnits(env, "scan-pending", "prod-beans", scan.StockIn, 1)
			},
			httpExchange: httpExchange{
				method:         "PATCH",
				path:           "/api/scans/scan-pending",
				body:           `{"status":"cancelled"}`,
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.status", value: "cancelled"},
					{path: "$.unitCount", value: float64(1)},
				},
			},
		},
		{
			name: "resolve a flagged scan with productId and pending",
			setup: func(env testEnv) {
				mustProduct(env, "prod-soup", "Tarnwell Soup")
				insertDirectedScan(env, "scan-flagged", scan.Flagged, scan.StockIn, 1, false)
			},
			httpExchange: httpExchange{
				method:         "PATCH",
				path:           "/api/scans/scan-flagged",
				body:           `{"productId":"prod-soup","status":"pending"}`,
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.status", value: "pending"},
					{path: "$.productId", value: "prod-soup"},
				},
			},
			afterRequest: func(env testEnv) {
				var productID string
				if err := env.DB.QueryRow(`SELECT product_id FROM barcodes WHERE barcode = ? AND source = 'user_override' AND user_id = ?`, "scan-flagged", "user-1").Scan(&productID); err != nil {
					env.T.Fatalf("override: %v", err)
				}
				if productID != "prod-soup" {
					env.T.Fatalf("override product %s", productID)
				}
			},
		},
	})
}

func TestScanResolveAfterCancelIsNotOpen(t *testing.T) {
	runHandlerTests(t, []handlerTestCase{
		{
			name: "remove then resolve",
			setup: func(env testEnv) {
				mustProduct(env, "prod-soup", "Tarnwell Soup")
				insertDirectedScan(env, "scan-flagged", scan.Flagged, scan.StockIn, 1, false)
			},
			httpExchange: httpExchange{
				method:         "PATCH",
				path:           "/api/scans/scan-flagged",
				body:           `{"status":"cancelled"}`,
				expectedStatus: http.StatusOK,
				assertions:     []assertion{{path: "$.status", value: "cancelled"}},
			},
			afterRequest: func(env testEnv) {
				exchanges(httpExchange{
					method:         "PATCH",
					path:           "/api/scans/scan-flagged",
					body:           `{"productId":"prod-soup","status":"pending"}`,
					expectedStatus: http.StatusConflict,
					assertions: []assertion{
						{path: "$.error", value: "This scan was already handled."},
						{path: "$.code", value: "not_open"},
					},
				}, httpExchange{
					method:         "GET",
					path:           "/api/scans",
					query:          map[string]string{"userId": "user-1", "status": "cancelled"},
					expectedStatus: http.StatusOK,
					assertions: []assertion{
						{path: "$[0].id", value: "scan-flagged"},
						{path: "$[0].status", value: "cancelled"},
						{path: "$[0].productId", value: nil},
					},
				})(env)
				var productID sql.NullString
				if err := env.DB.QueryRow(`SELECT product_id FROM scan_entries WHERE id = ?`, "scan-flagged").Scan(&productID); err != nil {
					env.T.Fatalf("scan row: %v", err)
				}
				if productID.Valid {
					env.T.Fatalf("product written: %s", productID.String)
				}
				var overrides int
				if err := env.DB.QueryRow(`SELECT COUNT(*) FROM barcodes WHERE source = 'user_override' AND barcode = ?`, "scan-flagged").Scan(&overrides); err != nil {
					env.T.Fatalf("overrides: %v", err)
				}
				if overrides != 0 {
					env.T.Fatalf("override count %d", overrides)
				}
			},
		},
	})
}

func insertDirectedScan(env testEnv, id string, status scan.ScanStatus, direction scan.ScanDirection, units int, withProduct bool) {
	env.T.Helper()
	entry := scan.ScanEntry{
		ID:        id,
		UserID:    "user-1",
		Barcode:   id,
		ScannedAt: time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
		Direction: &direction,
		UnitCount: units,
		Status:    status,
	}
	if withProduct {
		productID := "prod-milk"
		entry.ProductID = &productID
	}
	if _, err := scan.NewQueue(env.DB).CreateScanEntry(context.Background(), entry); err != nil {
		env.T.Fatalf("CreateScanEntry %s: %v", id, err)
	}
}

func seedOnHand(env testEnv, productID string, n int) {
	env.T.Helper()
	itemID := "item-" + productID
	if _, err := env.DB.Exec(`INSERT INTO items (id, user_id, product_id) VALUES (?, 'user-1', ?)`, itemID, productID); err != nil {
		env.T.Fatalf("item: %v", err)
	}
	for i := 0; i < n; i++ {
		if _, err := env.DB.Exec(`INSERT INTO item_instances (id, item_id, stock_in_at) VALUES (?, ?, ?)`,
			fmt.Sprintf("%s-%d", itemID, i), itemID, time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)); err != nil {
			env.T.Fatalf("instance: %v", err)
		}
	}
}

func assertScanRowUntouched(env testEnv, id string, status scan.ScanStatus, units int) {
	env.T.Helper()
	var gotStatus string
	var gotUnits int
	var committedAt sql.NullTime
	if err := env.DB.QueryRow(`SELECT status, unit_count, committed_at FROM scan_entries WHERE id = ?`, id).Scan(&gotStatus, &gotUnits, &committedAt); err != nil {
		env.T.Fatalf("scan row: %v", err)
	}
	if gotStatus != string(status) || gotUnits != units || committedAt.Valid {
		env.T.Fatalf("row changed: status %s units %d committed_at %v", gotStatus, gotUnits, committedAt)
	}
	var moves int
	if err := env.DB.QueryRow(`SELECT COUNT(*) FROM stock_moves`).Scan(&moves); err != nil {
		env.T.Fatalf("moves: %v", err)
	}
	if moves != 0 {
		env.T.Fatalf("stock moves %d", moves)
	}
}

func insertOpenScan(env testEnv, id, barcode string, status scan.ScanStatus) {
	env.T.Helper()
	queue := scan.NewQueue(env.DB)
	if _, err := queue.CreateScanEntry(context.Background(), scan.ScanEntry{
		ID:        id,
		UserID:    "user-1",
		Barcode:   barcode,
		ScannedAt: time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
		UnitCount: 1,
		Status:    status,
	}); err != nil {
		env.T.Fatalf("CreateScanEntry %s: %v", id, err)
	}
}
