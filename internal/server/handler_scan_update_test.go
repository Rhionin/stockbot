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
