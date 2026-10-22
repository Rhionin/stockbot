package server

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/scan"
)

func TestOpenScanCommit(t *testing.T) {
	soon := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	runHandlerTests(t, []handlerTestCase{
		{
			name: "a flagged stock-out with a product commits",
			setup: func(env testEnv) {
				mustProduct(env, "prod-flag-out", "Flagged Oats")
				stockShelf(env, "item-flag-out", "user-1", "prod-flag-out", []shelfUnit{
					{"inst-flag-out", &soon},
				})
				insertOpenScan(env, "scan-flag-out", "prod-flag-out", scan.StockOut, 1, scan.Flagged)
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans/scan-flag-out/commit",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.status", value: "committed"},
					{path: "$.removedCount", value: float64(1)},
					{path: "$.requestedCount", value: float64(1)},
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
			name: "a flagged stock-in with a product commits",
			setup: func(env testEnv) {
				mustProduct(env, "prod-flag-in", "Flagged Beans")
				insertOpenScan(env, "scan-flag-in", "prod-flag-in", scan.StockIn, 2, scan.Flagged)
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans/scan-flag-in/commit",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.status", value: "committed"},
					{path: "$.removedCount", absent: true},
				},
			},
			afterRequest: exchanges(httpExchange{
				method:         "GET",
				path:           "/api/inventory",
				expectedStatus: http.StatusOK,
				assertions:     []assertion{{path: "$[0].instanceCount", value: float64(2)}},
			}),
		},
		{
			name: "a flagged link commit removes the linked item",
			setup: func(env testEnv) {
				mustProduct(env, "prod-flag-link", "Linked Pears")
				mustProduct(env, "prod-flag-unknown", "Unknown Pears")
				stockShelf(env, "item-flag-link", "user-1", "prod-flag-link", []shelfUnit{
					{"inst-flag-link", &soon},
				})
				insertOpenScan(env, "scan-flag-link", "prod-flag-unknown", scan.StockOut, 1, scan.Flagged)
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans/scan-flag-link/commit",
				body:           `{"linkProductId":"prod-flag-link"}`,
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.status", value: "committed"},
					{path: "$.productId", value: "prod-flag-link"},
					{path: "$.removedCount", value: float64(1)},
				},
			},
		},
		{
			name: "a flagged scan with no product is product not resolved",
			setup: func(env testEnv) {
				insertOpenScan(env, "scan-flag-none", "", scan.StockOut, 1, scan.Flagged)
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans/scan-flag-none/commit",
				expectedStatus: http.StatusBadRequest,
				assertions: []assertion{
					{path: "$.error", value: "Choose which item this is first."},
					{path: "$.code", value: "product_unresolved"},
				},
			},
		},
		{
			name: "a qr scan is not committable",
			setup: func(env testEnv) {
				mustProduct(env, "prod-qr", "QR Oats")
				insertOpenScan(env, "scan-qr", "prod-qr", scan.StockOut, 1, scan.QR)
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans/scan-qr/commit",
				expectedStatus: http.StatusBadRequest,
				assertions: []assertion{
					{path: "$.error", value: "This scan was already handled."},
					{path: "$.code", value: "not_pending"},
				},
			},
		},
		{
			name: "a cancelled scan is not committable",
			setup: func(env testEnv) {
				mustProduct(env, "prod-cancel", "Cancelled Oats")
				insertOpenScan(env, "scan-cancel", "prod-cancel", scan.StockIn, 1, scan.Cancelled)
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans/scan-cancel/commit",
				expectedStatus: http.StatusBadRequest,
				assertions: []assertion{
					{path: "$.error", value: "This scan was already handled."},
					{path: "$.code", value: "not_pending"},
				},
			},
		},
		{
			name: "a batch commits a flagged stock-out with a product",
			setup: func(env testEnv) {
				mustProduct(env, "prod-batch-flag", "Batch Flag Oats")
				stockShelf(env, "item-batch-flag", "user-1", "prod-batch-flag", []shelfUnit{
					{"inst-batch-flag", &soon},
				})
				insertOpenScan(env, "scan-batch-flag", "prod-batch-flag", scan.StockOut, 1, scan.Flagged)
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans/batch-commit",
				body:           `{"scanEntryIds":["scan-batch-flag"],"commit":true}`,
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.committedIds[0]", value: "scan-batch-flag"},
					{path: "$.failedIds", absent: true},
					{path: `$.stockOutCounts["scan-batch-flag"].removedCount`, value: float64(1)},
					{path: `$.stockOutCounts["scan-batch-flag"].requestedCount`, value: float64(1)},
				},
			},
		},
		{
			name: "a batch commits a flagged stock-in with a product",
			setup: func(env testEnv) {
				mustProduct(env, "prod-batch-flag-in", "Batch Flag Beans")
				insertOpenScan(env, "scan-batch-flag-in", "prod-batch-flag-in", scan.StockIn, 2, scan.Flagged)
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans/batch-commit",
				body:           `{"scanEntryIds":["scan-batch-flag-in"],"commit":true}`,
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.committedIds[0]", value: "scan-batch-flag-in"},
					{path: "$.failedIds", absent: true},
					{path: "$.stockOutCounts", absent: true},
				},
			},
			afterRequest: exchanges(httpExchange{
				method:         "GET",
				path:           "/api/inventory",
				expectedStatus: http.StatusOK,
				assertions:     []assertion{{path: "$[0].instanceCount", value: float64(2)}},
			}),
		},
		{
			name: "a batch flagged scan with no product is product not resolved",
			setup: func(env testEnv) {
				insertOpenScan(env, "scan-batch-flag-none", "", scan.StockOut, 1, scan.Flagged)
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans/batch-commit",
				body:           `{"scanEntryIds":["scan-batch-flag-none"],"commit":true}`,
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.committedIds", absent: true},
					{path: "$.failedIds[0]", value: "scan-batch-flag-none"},
					{path: "$.failedCodes[0]", value: "product_unresolved"},
					{path: "$.failedReasons[0]", value: "Choose which item this is first."},
				},
			},
		},
		{
			name: "a batch rejects a qr scan",
			setup: func(env testEnv) {
				mustProduct(env, "prod-batch-qr", "Batch QR")
				insertOpenScan(env, "scan-batch-qr", "prod-batch-qr", scan.StockIn, 1, scan.QR)
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans/batch-commit",
				body:           `{"scanEntryIds":["scan-batch-qr"],"commit":true}`,
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.committedIds", absent: true},
					{path: "$.failedIds[0]", value: "scan-batch-qr"},
					{path: "$.failedCodes[0]", value: "not_pending"},
					{path: "$.failedReasons[0]", value: "This scan was already handled."},
				},
			},
		},
		{
			name: "a batch rejects a cancelled scan",
			setup: func(env testEnv) {
				mustProduct(env, "prod-batch-cancel", "Batch Cancel")
				insertOpenScan(env, "scan-batch-cancel", "prod-batch-cancel", scan.StockOut, 1, scan.Cancelled)
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans/batch-commit",
				body:           `{"scanEntryIds":["scan-batch-cancel"],"commit":true}`,
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.committedIds", absent: true},
					{path: "$.failedIds[0]", value: "scan-batch-cancel"},
					{path: "$.failedCodes[0]", value: "not_pending"},
					{path: "$.failedReasons[0]", value: "This scan was already handled."},
				},
			},
		},
	})
}

func insertOpenScan(env testEnv, id, productID string, direction scan.ScanDirection, units int, status scan.ScanStatus) {
	env.T.Helper()
	var product *string
	if productID != "" {
		product = &productID
	}
	if _, err := scan.NewQueue(env.DB).CreateScanEntry(context.Background(), scan.ScanEntry{
		ID:        id,
		UserID:    "user-1",
		Barcode:   id,
		ScannedAt: time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
		Direction: &direction,
		UnitCount: units,
		ProductID: product,
		Status:    status,
	}); err != nil {
		env.T.Fatalf("CreateScanEntry %s: %v", id, err)
	}
}
