package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/clientmsg"
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

func TestScanUpdateCommittedStatusOnClosedScan(t *testing.T) {
	runHandlerTests(t, []handlerTestCase{
		{
			name: "committed scan rejects status committed",
			setup: func(env testEnv) {
				mustProduct(env, "prod-milk", "Whole Milk")
				insertPendingScanUnits(env, "scan-milk", "prod-milk", scan.StockIn, 2)
				commitScan(env, handlerFromEnv(env), "scan-milk")
			},
			httpExchange: httpExchange{
				method:         "PATCH",
				path:           "/api/scans/scan-milk",
				body:           `{"status":"committed","unitCount":9}`,
				expectedStatus: http.StatusBadRequest,
				assertions: []assertion{
					{path: "$.error", value: "invalid status value"},
					{path: "$.code", absent: true},
				},
			},
			afterRequest: func(env testEnv) {
				assertClosedScanUntouched(env, "scan-milk", scan.Committed, 2, 1)
			},
		},
		{
			name: "cancelled scan rejects status committed",
			setup: func(env testEnv) {
				seedClosedScanProducts(env)
				insertClosedScan(env, "scan-closed", scan.Cancelled)
			},
			httpExchange: httpExchange{
				method:         "PATCH",
				path:           "/api/scans/scan-closed",
				body:           `{"status":"committed","unitCount":9}`,
				expectedStatus: http.StatusBadRequest,
				assertions: []assertion{
					{path: "$.error", value: "invalid status value"},
					{path: "$.code", absent: true},
				},
			},
			afterRequest: func(env testEnv) {
				assertClosedScanUntouched(env, "scan-closed", scan.Cancelled, 2, 0)
			},
		},
	})
}

func TestScanUnknownProduct(t *testing.T) {
	cases := []struct {
		name      string
		kind      string
		status    scan.ScanStatus
		direction scan.ScanDirection
		seedReal  bool
	}{
		{name: "pending stock-in", kind: "patch", status: scan.Pending, direction: scan.StockIn, seedReal: true},
		{name: "pending stock-out", kind: "patch", status: scan.Pending, direction: scan.StockOut, seedReal: true},
		{name: "qr", kind: "patch", status: scan.QR, direction: scan.StockIn},
		{name: "flagged", kind: "patch", status: scan.Flagged, direction: scan.StockIn},
		{name: "repoint a pending scan", kind: "repoint", status: scan.Pending, direction: scan.StockIn, seedReal: true},
		{name: "commit dangling stock-in", kind: "commit", status: scan.Pending, direction: scan.StockIn},
		{name: "commit dangling stock-out", kind: "commit", status: scan.Pending, direction: scan.StockOut},
		{name: "batch commit dangling stock-in", kind: "batch", status: scan.Pending, direction: scan.StockIn},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			runHandlerTests(t, []handlerTestCase{{
				name: tc.name,
				setup: func(env testEnv) {
					mustProduct(env, "prod-milk", "Whole Milk")
					mustProduct(env, "prod-other", "Other Milk")
					insertProductScan(env, "scan-open", tc.status, tc.direction, tc.seedReal, tc.kind != "patch" && tc.kind != "repoint")
				},
				httpExchange: unknownProductExchange(tc.kind),
				afterRequest: func(env testEnv) {
					assertUnknownProductUnchanged(env, tc)
				},
			}})
		})
	}
}

func TestFlaggedResolveRejectsExtraFields(t *testing.T) {
	bodies := []struct {
		name string
		body string
	}{
		{name: "unitCount", body: `{"productId":"prod-soup","unitCount":4}`},
		{name: "direction", body: `{"productId":"prod-soup","direction":"stock_out"}`},
		{name: "expiresAt", body: `{"productId":"prod-soup","expiresAt":"2027-03-01T00:00:00Z"}`},
		{name: "status cancelled", body: `{"productId":"prod-soup","status":"cancelled"}`},
		{name: "status flagged", body: `{"productId":"prod-soup","status":"flagged"}`},
		{name: "other fields with pending", body: `{"productId":"prod-soup","unitCount":4,"direction":"stock_out","expiresAt":"2027-03-01T00:00:00Z","status":"pending"}`},
	}
	tests := make([]handlerTestCase, 0, len(bodies))
	for _, body := range bodies {
		body := body
		tests = append(tests, handlerTestCase{
			name: body.name,
			setup: func(env testEnv) {
				mustProduct(env, "prod-soup", "Tarnwell Soup")
				insertDirectedScan(env, "scan-flagged", scan.Flagged, scan.StockIn, 1, false)
			},
			httpExchange: httpExchange{
				method:         "PATCH",
				path:           "/api/scans/scan-flagged",
				body:           body.body,
				expectedStatus: http.StatusBadRequest,
				assertions: []assertion{
					{path: "$.error", value: clientmsg.InvalidResolveText},
					{path: "$.code", value: clientmsg.InvalidResolveCode},
				},
			},
			afterRequest: func(env testEnv) {
				assertFlaggedUntouched(env, "scan-flagged")
			},
		})
	}
	runHandlerTests(t, tests)
}

func TestScanUpdateUnexpectedDatabaseError(t *testing.T) {
	h := &ScanUpdateHandler{Queue: scanUpdateFailure{err: errors.New("disk I/O error")}}
	mux := http.NewServeMux()
	mux.HandleFunc("PATCH /api/scans/{id}", HandleJSON(h.Handle))
	req := httptest.NewRequest(http.MethodPatch, "/api/scans/scan-1", strings.NewReader(`{"unitCount":4}`))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json: %v", err)
	}
	if payload["error"] != "Something went wrong. Please try again." {
		t.Fatalf("body %#v", payload)
	}
	if _, ok := payload["code"]; ok {
		t.Fatalf("code leaked: %#v", payload)
	}
	if strings.Contains(rec.Body.String(), "disk I/O") {
		t.Fatalf("detail leaked: %s", rec.Body.String())
	}
}

// TestScanResolveHTTPLosesToCancel is the resolve-versus-remove race at HTTP.
// The resolve waits before its transaction, the cancel commits, then the
// resolve continues. A handler that treated "no longer flagged" as a server
// error returned 500 here.
func TestScanResolveHTTPLosesToCancel(t *testing.T) {
	handler, env := setupTestWithContributor(t, nil)
	mustProduct(env, "prod-soup", "Tarnwell Soup")
	insertDirectedScan(env, "scan-flagged", scan.Flagged, scan.StockIn, 1, false)

	entered := make(chan struct{})
	release := make(chan struct{})
	env.ScanQueue.BeforeResolve = func() {
		close(entered)
		<-release
	}

	type result struct {
		code int
		body string
	}
	resCh := make(chan result, 1)
	go func() {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPatch, "/api/scans/scan-flagged", strings.NewReader(`{"productId":"prod-soup","status":"pending"}`))
		req.Header.Set("Content-Type", "application/json")
		handler.ServeHTTP(rec, req)
		resCh <- result{code: rec.Code, body: rec.Body.String()}
	}()
	<-entered

	cancelRec := httptest.NewRecorder()
	cancelReq := httptest.NewRequest(http.MethodPatch, "/api/scans/scan-flagged", strings.NewReader(`{"status":"cancelled"}`))
	cancelReq.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(cancelRec, cancelReq)
	if cancelRec.Code != http.StatusOK {
		t.Fatalf("cancel %d %s", cancelRec.Code, cancelRec.Body.String())
	}
	close(release)

	got := <-resCh
	if got.code != http.StatusConflict {
		t.Fatalf("resolve %d %s", got.code, got.body)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(got.body), &payload); err != nil {
		t.Fatalf("json: %v body %s", err, got.body)
	}
	if payload["error"] != scan.ErrNotOpen.Error() || payload["code"] != "not_open" {
		t.Fatalf("body %#v", payload)
	}
	assertFlaggedUntouched(env, "scan-flagged")
	var status string
	if err := env.DB.QueryRow(`SELECT status FROM scan_entries WHERE id = ?`, "scan-flagged").Scan(&status); err != nil {
		t.Fatalf("status: %v", err)
	}
	if status != string(scan.Cancelled) {
		t.Fatalf("status %s", status)
	}
}

// TestScanResolveBodyIsTheWrittenRow cancels the row after the resolve
// transaction commits. The response stays the pending row that transaction wrote.
func TestScanResolveBodyIsTheWrittenRow(t *testing.T) {
	handler, env := setupTestWithContributor(t, nil)
	mustProduct(env, "prod-soup", "Tarnwell Soup")
	insertDirectedScan(env, "scan-flagged", scan.Flagged, scan.StockIn, 1, false)
	env.ScanQueue.AfterResolveCommit = func() {
		if _, err := env.DB.Exec(`UPDATE scan_entries SET status = 'cancelled' WHERE id = ?`, "scan-flagged"); err != nil {
			env.T.Errorf("cancel after commit: %v", err)
		}
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/api/scans/scan-flagged", strings.NewReader(`{"productId":"prod-soup","status":"pending"}`))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("resolve %d %s", rec.Code, rec.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json: %v", err)
	}
	if payload["status"] != string(scan.Pending) || payload["productId"] != "prod-soup" {
		t.Fatalf("body %#v", payload)
	}
	var status string
	if err := env.DB.QueryRow(`SELECT status FROM scan_entries WHERE id = ?`, "scan-flagged").Scan(&status); err != nil {
		t.Fatalf("status: %v", err)
	}
	if status != string(scan.Cancelled) {
		t.Fatalf("stored status %s", status)
	}
}

func unknownProductExchange(kind string) httpExchange {
	switch kind {
	case "repoint":
		return httpExchange{
			method:         "PATCH",
			path:           "/api/scans/scan-open",
			body:           `{"productId":"prod-other","unitCount":4}`,
			expectedStatus: http.StatusOK,
			assertions: []assertion{
				{path: "$.productId", value: "prod-other"},
				{path: "$.unitCount", value: float64(4)},
				{path: "$.status", value: "pending"},
			},
		}
	case "commit":
		return httpExchange{
			method:         "POST",
			path:           "/api/scans/scan-open/commit",
			body:           `{}`,
			expectedStatus: http.StatusBadRequest,
			assertions: []assertion{
				{path: "$.error", value: clientmsg.ProductUnresolvedText},
				{path: "$.code", value: clientmsg.ProductUnresolvedCode},
				{path: "$.suggestions", absent: true},
			},
		}
	case "batch":
		return httpExchange{
			method:         "POST",
			path:           "/api/scans/batch-commit",
			body:           `{"scanEntryIds":["scan-open"],"commit":true}`,
			expectedStatus: http.StatusOK,
			assertions: []assertion{
				{path: "$.failedIds[0]", value: "scan-open"},
				{path: "$.failedReasons[0]", value: clientmsg.ProductUnresolvedText},
				{path: "$.committedIds", absent: true},
			},
		}
	default:
		return httpExchange{
			method:         "PATCH",
			path:           "/api/scans/scan-open",
			body:           `{"productId":"no-such-product","status":"pending"}`,
			expectedStatus: http.StatusBadRequest,
			assertions: []assertion{
				{path: "$.error", value: clientmsg.ProductUnresolvedText},
				{path: "$.code", value: clientmsg.ProductUnresolvedCode},
			},
		}
	}
}

func assertUnknownProductUnchanged(env testEnv, tc struct {
	name      string
	kind      string
	status    scan.ScanStatus
	direction scan.ScanDirection
	seedReal  bool
}) {
	env.T.Helper()
	if tc.kind == "repoint" {
		var productID string
		var units int
		if err := env.DB.QueryRow(`SELECT product_id, unit_count FROM scan_entries WHERE id = ?`, "scan-open").Scan(&productID, &units); err != nil {
			env.T.Fatalf("row: %v", err)
		}
		if productID != "prod-other" || units != 4 {
			env.T.Fatalf("repoint row product %s units %d", productID, units)
		}
		return
	}
	var status string
	var units int
	var productID sql.NullString
	if err := env.DB.QueryRow(`SELECT status, unit_count, product_id FROM scan_entries WHERE id = ?`, "scan-open").Scan(&status, &units, &productID); err != nil {
		env.T.Fatalf("row: %v", err)
	}
	wantProduct := ""
	if tc.seedReal {
		wantProduct = "prod-milk"
	}
	if tc.kind == "commit" || tc.kind == "batch" {
		wantProduct = "no-such-product"
	}
	gotProduct := ""
	if productID.Valid {
		gotProduct = productID.String
	}
	if status != string(tc.status) || units != 1 || gotProduct != wantProduct {
		env.T.Fatalf("row changed: status %s units %d product %q", status, units, gotProduct)
	}
	var moves int
	if err := env.DB.QueryRow(`SELECT COUNT(*) FROM stock_moves`).Scan(&moves); err != nil {
		env.T.Fatalf("moves: %v", err)
	}
	if moves != 0 {
		env.T.Fatalf("stock moves %d", moves)
	}
	var overrides int
	if err := env.DB.QueryRow(`SELECT COUNT(*) FROM barcodes WHERE barcode = ? AND source = 'user_override'`, "scan-open").Scan(&overrides); err != nil {
		env.T.Fatalf("overrides: %v", err)
	}
	if overrides != 0 {
		env.T.Fatalf("override count %d", overrides)
	}
}

func insertProductScan(env testEnv, id string, status scan.ScanStatus, direction scan.ScanDirection, seedReal, dangling bool) {
	env.T.Helper()
	entry := scan.ScanEntry{
		ID:        id,
		UserID:    "user-1",
		Barcode:   id,
		ScannedAt: time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
		Direction: &direction,
		UnitCount: 1,
		Status:    status,
	}
	if seedReal {
		productID := "prod-milk"
		entry.ProductID = &productID
	}
	if dangling {
		productID := "no-such-product"
		entry.ProductID = &productID
	}
	if _, err := scan.NewQueue(env.DB).CreateScanEntry(context.Background(), entry); err != nil {
		env.T.Fatalf("CreateScanEntry %s: %v", id, err)
	}
}

func assertClosedScanUntouched(env testEnv, id string, status scan.ScanStatus, units, moves int) {
	env.T.Helper()
	var gotStatus string
	var gotUnits int
	if err := env.DB.QueryRow(`SELECT status, unit_count FROM scan_entries WHERE id = ?`, id).Scan(&gotStatus, &gotUnits); err != nil {
		env.T.Fatalf("scan row: %v", err)
	}
	if gotStatus != string(status) || gotUnits != units {
		env.T.Fatalf("row changed: status %s units %d", gotStatus, gotUnits)
	}
	var gotMoves int
	if err := env.DB.QueryRow(`SELECT COUNT(*) FROM stock_moves`).Scan(&gotMoves); err != nil {
		env.T.Fatalf("moves: %v", err)
	}
	if gotMoves != moves {
		env.T.Fatalf("stock moves %d", gotMoves)
	}
}

func assertFlaggedUntouched(env testEnv, id string) {
	env.T.Helper()
	var status, direction string
	var units int
	var productID sql.NullString
	var expiresAt sql.NullTime
	if err := env.DB.QueryRow(`SELECT status, direction, unit_count, product_id, expires_at FROM scan_entries WHERE id = ?`, id).Scan(&status, &direction, &units, &productID, &expiresAt); err != nil {
		env.T.Fatalf("scan row: %v", err)
	}
	if status != string(scan.Flagged) && status != string(scan.Cancelled) {
		env.T.Fatalf("status %s", status)
	}
	if status == string(scan.Flagged) && (direction != string(scan.StockIn) || units != 1 || productID.Valid || expiresAt.Valid) {
		env.T.Fatalf("flagged row changed: direction %s units %d product %v expires %v", direction, units, productID, expiresAt)
	}
	if productID.Valid {
		env.T.Fatalf("product written: %s", productID.String)
	}
	var overrides int
	if err := env.DB.QueryRow(`SELECT COUNT(*) FROM barcodes WHERE barcode = ? AND source = 'user_override'`, id).Scan(&overrides); err != nil {
		env.T.Fatalf("overrides: %v", err)
	}
	if overrides != 0 {
		env.T.Fatalf("override count %d", overrides)
	}
}

type scanUpdateFailure struct {
	err error
}

func (q scanUpdateFailure) UpdateScanEntry(context.Context, string, *scan.ScanDirection, *int, *time.Time, *string, *scan.ScanStatus) error {
	return q.err
}

func (q scanUpdateFailure) GetScanEntry(context.Context, string) (*scan.ScanEntry, error) {
	return nil, q.err
}

func (q scanUpdateFailure) ResolveFlaggedEntry(context.Context, string, string, ...scan.ResolveFields) (*scan.ScanEntry, error) {
	return nil, q.err
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
