package server

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/scan"
)

func TestScanUpdateRefusesStatusChangeOnCommittedScan(t *testing.T) {
	for _, status := range []string{"cancelled", "pending", "flagged", "committed"} {
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
							{path: "$.error", value: "This scan was already handled."},
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
