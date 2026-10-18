package server

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/product"
	"github.com/Rhionin/stockbot/internal/scan"
)

func TestScanCommitStockIn(t *testing.T) {
	now := time.Now()
	expiresAt := now.Add(7 * 24 * time.Hour)

	tests := []handlerTestCase{
		{
			name: "commit stock-in with unit count 1",
			setup: func(env testEnv) {
				if err := env.ProductStore.CreateProduct(context.Background(), product.Product{
					ID: "prod-1", Name: "Whole Milk", Category: "Dairy", UnitOfMeasure: "gallon",
				}); err != nil {
					env.T.Fatalf("CreateProduct: %v", err)
				}
				scanRepo := scan.NewQueue(env.DB)
				productID := "prod-1"
				direction := scan.StockIn
				entry := scan.ScanEntry{
					ID: "scan-1", UserID: "user-1", Barcode: "123456789012",
					ScannedAt: now, Direction: &direction, UnitCount: 1,
					ExpiresAt: &expiresAt, ProductID: &productID, Status: scan.Pending,
				}
				if _, err := scanRepo.CreateScanEntry(context.Background(), entry); err != nil {
					env.T.Fatalf("CreateScanEntry: %v", err)
				}
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans/scan-1/commit",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.status", value: "committed"},
					{path: "$.id", value: "scan-1"},
					{path: "$.code", absent: true},
				},
				bodyExcludes: []string{"no_item", "Not in your pantry", "Nothing on hand to take out."},
			},
			afterRequest: func(env testEnv) {
				// Verify one instance was created and its expiry is set.
				// stock_in_at precision is not exposed by the API, so we check it via DB.
				var stockInAt time.Time
				var expiresAtDB sql.NullTime
				if err := env.DB.QueryRowContext(context.Background(), `
					SELECT ii.stock_in_at, ii.expires_at FROM item_instances ii
					JOIN items i ON i.id = ii.item_id
					WHERE i.user_id = ? AND i.product_id = ? AND ii.removed_at IS NULL`,
					"user-1", "prod-1",
				).Scan(&stockInAt, &expiresAtDB); err != nil {
					env.T.Fatalf("query instance: %v", err)
				}
				if !stockInAt.Truncate(time.Second).Equal(now.Truncate(time.Second)) {
					env.T.Errorf("stock_in_at: want %v, got %v", now, stockInAt)
				}
				if !expiresAtDB.Valid {
					env.T.Error("expires_at: want non-null, got null")
				} else if !expiresAtDB.Time.Truncate(time.Second).Equal(expiresAt.Truncate(time.Second)) {
					env.T.Errorf("expires_at: want %v, got %v", expiresAt, expiresAtDB.Time)
				}
			},
		},
		{
			name: "commit stock-in with unit count 3",
			setup: func(env testEnv) {
				if err := env.ProductStore.CreateProduct(context.Background(), product.Product{
					ID: "prod-2", Name: "Orange Juice", Category: "Beverages", UnitOfMeasure: "bottle",
				}); err != nil {
					env.T.Fatalf("CreateProduct: %v", err)
				}
				scanRepo := scan.NewQueue(env.DB)
				productID := "prod-2"
				direction := scan.StockIn
				entry := scan.ScanEntry{
					ID: "scan-2", UserID: "user-2", Barcode: "111222333444",
					ScannedAt: now, Direction: &direction, UnitCount: 3,
					ExpiresAt: &expiresAt, ProductID: &productID, Status: scan.Pending,
				}
				if _, err := scanRepo.CreateScanEntry(context.Background(), entry); err != nil {
					env.T.Fatalf("CreateScanEntry: %v", err)
				}
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans/scan-2/commit",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.status", value: "committed"},
					{path: "$.unitCount", value: float64(3)},
				},
			},
		},
		{
			name: "commit stock-in without expiration date",
			setup: func(env testEnv) {
				if err := env.ProductStore.CreateProduct(context.Background(), product.Product{
					ID: "prod-3", Name: "Canned Beans", Category: "Canned Goods", UnitOfMeasure: "can",
				}); err != nil {
					env.T.Fatalf("CreateProduct: %v", err)
				}
				scanRepo := scan.NewQueue(env.DB)
				productID := "prod-3"
				direction := scan.StockIn
				entry := scan.ScanEntry{
					ID: "scan-3", UserID: "user-3", Barcode: "555666777888",
					ScannedAt: now, Direction: &direction, UnitCount: 2,
					ExpiresAt: nil, ProductID: &productID, Status: scan.Pending,
				}
				if _, err := scanRepo.CreateScanEntry(context.Background(), entry); err != nil {
					env.T.Fatalf("CreateScanEntry: %v", err)
				}
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans/scan-3/commit",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.status", value: "committed"},
					{path: "$.unitCount", value: float64(2)},
				},
			},
			afterRequest: exchanges(httpExchange{
				method:         "GET",
				path:           "/api/scans/history",
				query:          map[string]string{"userId": "user-3"},
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					// Committed scan with no expiry — verify it appears in history
					{path: "$[0].id", value: "scan-3"},
					{path: "$[0].status", value: "committed"},
				},
			}),
		},
		{
			name: "reuses existing item for same user+product",
			setup: func(env testEnv) {
				if err := env.ProductStore.CreateProduct(context.Background(), product.Product{
					ID: "prod-7", Name: "Shared Product", Category: "Test", UnitOfMeasure: "unit",
				}); err != nil {
					env.T.Fatalf("CreateProduct: %v", err)
				}
				if _, err := env.DB.ExecContext(context.Background(),
					`INSERT INTO items (id, user_id, product_id) VALUES ('item-existing', 'user-7', 'prod-7')`); err != nil {
					env.T.Fatalf("create existing item: %v", err)
				}
				scanRepo := scan.NewQueue(env.DB)
				productID := "prod-7"
				direction := scan.StockIn
				entry := scan.ScanEntry{
					ID: "scan-7", UserID: "user-7", Barcode: "777888999000",
					ScannedAt: now, Direction: &direction, UnitCount: 1,
					ProductID: &productID, Status: scan.Pending,
				}
				if _, err := scanRepo.CreateScanEntry(context.Background(), entry); err != nil {
					env.T.Fatalf("CreateScanEntry: %v", err)
				}
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans/scan-7/commit",
				expectedStatus: http.StatusOK,
			},
			afterRequest: func(env testEnv) {
				// Item ID reuse is internal state not exposed by any API endpoint.
				var itemID string
				if err := env.DB.QueryRowContext(context.Background(),
					`SELECT id FROM items WHERE user_id = ? AND product_id = ?`,
					"user-7", "prod-7",
				).Scan(&itemID); err != nil {
					env.T.Fatalf("get item id: %v", err)
				}
				if itemID != "item-existing" {
					env.T.Errorf("expected existing item ID 'item-existing', got %q (commit must not create a duplicate)", itemID)
				}
			},
		},
		{
			name: "error when status is not pending",
			setup: func(env testEnv) {
				if err := env.ProductStore.CreateProduct(context.Background(), product.Product{
					ID: "prod-committed", Name: "Test Product", Category: "Test", UnitOfMeasure: "unit",
				}); err != nil {
					env.T.Fatalf("CreateProduct: %v", err)
				}
				scanRepo := scan.NewQueue(env.DB)
				productID := "prod-committed"
				direction := scan.StockIn
				entry := scan.ScanEntry{
					ID: "scan-committed", UserID: "user-committed", Barcode: "999111222333",
					ScannedAt: now, Direction: &direction, UnitCount: 1,
					ProductID: &productID, Status: scan.Committed,
				}
				if _, err := scanRepo.CreateScanEntry(context.Background(), entry); err != nil {
					env.T.Fatalf("CreateScanEntry: %v", err)
				}
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans/scan-committed/commit",
				expectedStatus: http.StatusBadRequest,
				assertions: []assertion{
					{path: "$.error", value: "This scan was already handled."},
					{path: "$.code", value: "not_pending"},
				},
			},
		},
		{
			name: "error when product_id is missing",
			setup: func(env testEnv) {
				scanRepo := scan.NewQueue(env.DB)
				direction := scan.StockIn
				entry := scan.ScanEntry{
					ID: "scan-no-product", UserID: "user-no-product", Barcode: "444555666777",
					ScannedAt: now, Direction: &direction, UnitCount: 1,
					ProductID: nil, Status: scan.Pending,
				}
				if _, err := scanRepo.CreateScanEntry(context.Background(), entry); err != nil {
					env.T.Fatalf("CreateScanEntry: %v", err)
				}
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans/scan-no-product/commit",
				expectedStatus: http.StatusBadRequest,
				assertions: []assertion{
					{path: "$.error", value: "Choose which item this is first."},
					{path: "$.code", value: "product_unresolved"},
				},
			},
		},
	}

	runHandlerTests(t, tests)
}

func TestScanCommitStockOut(t *testing.T) {
	now := time.Now()
	expiresEarlier := now.Add(3 * 24 * time.Hour)
	expiresLater := now.Add(10 * 24 * time.Hour)

	tests := []handlerTestCase{
		{
			name: "commit stock-out with use-oldest-first",
			setup: func(env testEnv) {
				if err := env.ProductStore.CreateProduct(context.Background(), product.Product{
					ID: "prod-out-1", Name: "Test Product", Category: "Test", UnitOfMeasure: "unit",
				}); err != nil {
					env.T.Fatalf("CreateProduct: %v", err)
				}
				if _, err := env.DB.ExecContext(context.Background(),
					`INSERT INTO items (id, user_id, product_id) VALUES ('item-out-1', 'user-out-1', 'prod-out-1')`); err != nil {
					env.T.Fatalf("create item: %v", err)
				}
				for _, inst := range []struct {
					id        string
					expiresAt *time.Time
				}{
					{"inst-1", &expiresLater},
					{"inst-2", &expiresEarlier},
					{"inst-3", nil},
				} {
					var exp sql.NullTime
					if inst.expiresAt != nil {
						exp = sql.NullTime{Time: *inst.expiresAt, Valid: true}
					}
					if _, err := env.DB.ExecContext(context.Background(),
						`INSERT INTO item_instances (id, item_id, stock_in_at, expires_at) VALUES (?, 'item-out-1', ?, ?)`,
						inst.id, now.Add(-24*time.Hour), exp); err != nil {
						env.T.Fatalf("create instance %s: %v", inst.id, err)
					}
				}
				scanRepo := scan.NewQueue(env.DB)
				productID := "prod-out-1"
				direction := scan.StockOut
				entry := scan.ScanEntry{
					ID: "scan-out-1", UserID: "user-out-1", Barcode: "123456789012",
					ScannedAt: now, Direction: &direction, UnitCount: 1,
					ProductID: &productID, Status: scan.Pending,
				}
				if _, err := scanRepo.CreateScanEntry(context.Background(), entry); err != nil {
					env.T.Fatalf("CreateScanEntry: %v", err)
				}
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans/scan-out-1/commit",
				expectedStatus: http.StatusOK,
			},
			afterRequest: exchanges(httpExchange{
				method:         "GET",
				path:           "/api/inventory/item-out-1/instances",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					// inst-2 (earliest expiry) was consumed; inst-3 (no expiry, sorted last) and inst-1 remain
					{path: "$[0].id", value: "inst-1"},
					{path: "$[1].id", value: "inst-3"},
				},
			}),
		},
		{
			name: "commit stock-out with specific instanceID",
			setup: func(env testEnv) {
				if err := env.ProductStore.CreateProduct(context.Background(), product.Product{
					ID: "prod-out-2", Name: "Test Product 2", Category: "Test", UnitOfMeasure: "unit",
				}); err != nil {
					env.T.Fatalf("CreateProduct: %v", err)
				}
				if _, err := env.DB.ExecContext(context.Background(),
					`INSERT INTO items (id, user_id, product_id) VALUES ('item-out-2', 'user-out-2', 'prod-out-2')`); err != nil {
					env.T.Fatalf("create item: %v", err)
				}
				for _, inst := range []struct {
					id        string
					expiresAt time.Time
				}{
					{"inst-specific-1", expiresEarlier},
					{"inst-specific-2", expiresLater},
				} {
					if _, err := env.DB.ExecContext(context.Background(),
						`INSERT INTO item_instances (id, item_id, stock_in_at, expires_at) VALUES (?, 'item-out-2', ?, ?)`,
						inst.id, now.Add(-24*time.Hour), sql.NullTime{Time: inst.expiresAt, Valid: true}); err != nil {
						env.T.Fatalf("create instance %s: %v", inst.id, err)
					}
				}
				scanRepo := scan.NewQueue(env.DB)
				productID := "prod-out-2"
				direction := scan.StockOut
				entry := scan.ScanEntry{
					ID: "scan-out-2", UserID: "user-out-2", Barcode: "222333444555",
					ScannedAt: now, Direction: &direction, UnitCount: 1,
					ProductID: &productID, Status: scan.Pending,
				}
				if _, err := scanRepo.CreateScanEntry(context.Background(), entry); err != nil {
					env.T.Fatalf("CreateScanEntry: %v", err)
				}
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans/scan-out-2/commit",
				body:           `{"instanceId":"inst-specific-2"}`,
				expectedStatus: http.StatusOK,
			},
			afterRequest: exchanges(httpExchange{
				method:         "GET",
				path:           "/api/inventory/item-out-2/instances",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					// inst-specific-2 was explicitly consumed; inst-specific-1 remains
					{path: "$[0].id", value: "inst-specific-1"},
				},
			}),
		},
		{
			name: "commit stock-out prefers dated expiration over NULL (NULLS LAST)",
			setup: func(env testEnv) {
				if err := env.ProductStore.CreateProduct(context.Background(), product.Product{
					ID: "prod-out-3", Name: "Test Product 3", Category: "Test", UnitOfMeasure: "unit",
				}); err != nil {
					env.T.Fatalf("CreateProduct: %v", err)
				}
				if _, err := env.DB.ExecContext(context.Background(),
					`INSERT INTO items (id, user_id, product_id) VALUES ('item-out-3', 'user-out-3', 'prod-out-3')`); err != nil {
					env.T.Fatalf("create item: %v", err)
				}
				for _, inst := range []struct {
					id        string
					expiresAt sql.NullTime
				}{
					{"inst-null", sql.NullTime{}},
					{"inst-dated", sql.NullTime{Time: expiresLater, Valid: true}},
				} {
					if _, err := env.DB.ExecContext(context.Background(),
						`INSERT INTO item_instances (id, item_id, stock_in_at, expires_at) VALUES (?, 'item-out-3', ?, ?)`,
						inst.id, now.Add(-24*time.Hour), inst.expiresAt); err != nil {
						env.T.Fatalf("create instance %s: %v", inst.id, err)
					}
				}
				scanRepo := scan.NewQueue(env.DB)
				productID := "prod-out-3"
				direction := scan.StockOut
				entry := scan.ScanEntry{
					ID: "scan-out-3", UserID: "user-out-3", Barcode: "333444555666",
					ScannedAt: now, Direction: &direction, UnitCount: 1,
					ProductID: &productID, Status: scan.Pending,
				}
				if _, err := scanRepo.CreateScanEntry(context.Background(), entry); err != nil {
					env.T.Fatalf("CreateScanEntry: %v", err)
				}
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans/scan-out-3/commit",
				expectedStatus: http.StatusOK,
			},
			afterRequest: exchanges(httpExchange{
				method:         "GET",
				path:           "/api/inventory/item-out-3/instances",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					// inst-dated was consumed (expiry date sorts before NULL); inst-null remains
					{path: "$[0].id", value: "inst-null"},
				},
			}),
		},
		{
			name: "error when no available instances",
			setup: func(env testEnv) {
				if err := env.ProductStore.CreateProduct(context.Background(), product.Product{
					ID: "prod-out-empty", Name: "Empty Product", Category: "Test", UnitOfMeasure: "unit",
				}); err != nil {
					env.T.Fatalf("CreateProduct: %v", err)
				}
				if _, err := env.DB.ExecContext(context.Background(),
					`INSERT INTO items (id, user_id, product_id) VALUES ('item-empty', 'user-empty', 'prod-out-empty')`); err != nil {
					env.T.Fatalf("create item: %v", err)
				}
				scanRepo := scan.NewQueue(env.DB)
				productID := "prod-out-empty"
				direction := scan.StockOut
				entry := scan.ScanEntry{
					ID: "scan-out-empty", UserID: "user-empty", Barcode: "888999000111",
					ScannedAt: now, Direction: &direction, UnitCount: 1,
					ProductID: &productID, Status: scan.Pending,
				}
				if _, err := scanRepo.CreateScanEntry(context.Background(), entry); err != nil {
					env.T.Fatalf("CreateScanEntry: %v", err)
				}
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans/scan-out-empty/commit",
				expectedStatus: http.StatusConflict,
				assertions: []assertion{
					{path: "$.error", value: "Nothing on hand to take out."},
					{path: "$.code", value: "none_on_hand"},
					{path: "$.members", absent: true},
					{path: "$.suggestions", absent: true},
				},
			},
			afterRequest: exchanges(httpExchange{
				method:         "GET",
				path:           "/api/scans",
				query:          map[string]string{"userId": "user-empty", "status": "pending"},
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$[0].id", value: "scan-out-empty"},
					{path: "$[0].status", value: "pending"},
					{path: "$[1]", absent: true},
				},
			}),
		},
	}

	runHandlerTests(t, tests)
}

func TestScanCommitStockInErrors(t *testing.T) {
	now := time.Now()
	expiresAt := now.Add(7 * 24 * time.Hour)

	tests := []handlerTestCase{
		{
			name: "stock-out with no item is a client error",
			setup: func(env testEnv) {
				if err := env.ProductStore.CreateProduct(context.Background(), product.Product{
					ID: "prod-wrong-dir", Name: "Test Product", Category: "Test", UnitOfMeasure: "unit",
				}); err != nil {
					env.T.Fatalf("CreateProduct: %v", err)
				}
				scanRepo := scan.NewQueue(env.DB)
				productID := "prod-wrong-dir"
				direction := scan.StockOut
				entry := scan.ScanEntry{
					ID: "scan-wrong-dir", UserID: "user-wrong-dir", Barcode: "111222333444",
					ScannedAt: now, Direction: &direction, UnitCount: 1,
					ExpiresAt: &expiresAt, ProductID: &productID, Status: scan.Pending,
				}
				if _, err := scanRepo.CreateScanEntry(context.Background(), entry); err != nil {
					env.T.Fatalf("CreateScanEntry: %v", err)
				}
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans/scan-wrong-dir/commit",
				expectedStatus: http.StatusConflict,
				assertions: []assertion{
					{path: "$.code", value: "no_item"},
					{path: "$.error", value: "Which item is this?"},
					{path: "$.suggestions", absent: true},
					{path: "$.members", absent: true},
				},
				bodyExcludes: []string{"Not in your pantry", "Nothing on hand to take out.", "No close match found.", "Choose which item this is first."},
			},
		},
		{
			name: "a missing scan is not found",
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans/scan-not-in-queue/commit",
				expectedStatus: http.StatusNotFound,
				assertions: []assertion{
					{path: "$.error", value: "This scan is no longer in the queue."},
					{path: "$.code", value: "scan_not_found"},
				},
			},
		},
		{
			name: "error when unit count is zero",
			setup: func(env testEnv) {
				if err := env.ProductStore.CreateProduct(context.Background(), product.Product{
					ID: "prod-zero-count", Name: "Test Product", Category: "Test", UnitOfMeasure: "unit",
				}); err != nil {
					env.T.Fatalf("CreateProduct: %v", err)
				}
				scanRepo := scan.NewQueue(env.DB)
				productID := "prod-zero-count"
				direction := scan.StockIn
				entry := scan.ScanEntry{
					ID: "scan-zero-count", UserID: "user-zero-count", Barcode: "555666777888",
					ScannedAt: now, Direction: &direction, UnitCount: 0,
					ProductID: &productID, Status: scan.Pending,
				}
				if _, err := scanRepo.CreateScanEntry(context.Background(), entry); err != nil {
					env.T.Fatalf("CreateScanEntry: %v", err)
				}
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans/scan-zero-count/commit",
				expectedStatus: http.StatusInternalServerError,
				assertions: []assertion{
					{path: "$.error", value: "Something went wrong. Try again."},
					{path: "$.code", value: "internal"},
				},
				bodyExcludes: []string{"unit count must be at least 1"},
			},
		},
	}

	runHandlerTests(t, tests)
}

func TestScanCommitStockOutErrors(t *testing.T) {
	now := time.Now()
	expiresAt := now.Add(7 * 24 * time.Hour)

	tests := []handlerTestCase{
		{
			name: "shelved item is nothing on hand",
			setup: func(env testEnv) {
				if err := env.ProductStore.CreateProduct(context.Background(), product.Product{
					ID: "prod-shelved", Name: "Wiped Soup", Category: "Test", UnitOfMeasure: "unit",
				}); err != nil {
					env.T.Fatalf("CreateProduct: %v", err)
				}
				if _, err := env.DB.ExecContext(context.Background(),
					`INSERT INTO items (id, user_id, product_id, shelved) VALUES ('item-shelved', 'user-shelved', 'prod-shelved', 1)`); err != nil {
					env.T.Fatalf("create item: %v", err)
				}
				scanRepo := scan.NewQueue(env.DB)
				productID := "prod-shelved"
				direction := scan.StockOut
				entry := scan.ScanEntry{
					ID: "scan-shelved", UserID: "user-shelved", Barcode: "444555666777",
					ScannedAt: now, Direction: &direction, UnitCount: 1,
					ProductID: &productID, Status: scan.Pending,
				}
				if _, err := scanRepo.CreateScanEntry(context.Background(), entry); err != nil {
					env.T.Fatalf("CreateScanEntry: %v", err)
				}
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans/scan-shelved/commit",
				expectedStatus: http.StatusConflict,
				assertions: []assertion{
					{path: "$.code", value: "none_on_hand"},
					{path: "$.error", value: "Nothing on hand to take out."},
					{path: "$.suggestions", absent: true},
				},
				bodyExcludes: []string{"no_item", "Which item is this?", "Not in your pantry", "No close match found.", "Choose which item this is first."},
			},
			afterRequest: exchanges(httpExchange{
				method:         "GET",
				path:           "/api/scans",
				query:          map[string]string{"userId": "user-shelved", "status": "pending"},
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$[0].id", value: "scan-shelved"},
					{path: "$[0].status", value: "pending"},
					{path: "$[1]", absent: true},
				},
			}),
		},
		{
			name: "error when no item exists for user+product",
			setup: func(env testEnv) {
				if err := env.ProductStore.CreateProduct(context.Background(), product.Product{
					ID: "prod-no-item", Name: "Test Product", Category: "Test", UnitOfMeasure: "unit",
				}); err != nil {
					env.T.Fatalf("CreateProduct: %v", err)
				}
				scanRepo := scan.NewQueue(env.DB)
				productID := "prod-no-item"
				direction := scan.StockOut
				entry := scan.ScanEntry{
					ID: "scan-no-item", UserID: "user-no-item", Barcode: "333444555666",
					ScannedAt: now, Direction: &direction, UnitCount: 1,
					ProductID: &productID, Status: scan.Pending,
				}
				if _, err := scanRepo.CreateScanEntry(context.Background(), entry); err != nil {
					env.T.Fatalf("CreateScanEntry: %v", err)
				}
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans/scan-no-item/commit",
				expectedStatus: http.StatusConflict,
				assertions: []assertion{
					{path: "$.code", value: "no_item"},
					{path: "$.error", value: "Which item is this?"},
					{path: "$.suggestions", absent: true},
				},
				bodyExcludes: []string{"Not in your pantry", "Nothing on hand to take out.", "No close match found.", "Choose which item this is first.", `no item found for user`},
			},
			afterRequest: exchanges(httpExchange{
				method:         "GET",
				path:           "/api/scans",
				query:          map[string]string{"userId": "user-no-item", "status": "pending"},
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$[0].id", value: "scan-no-item"},
					{path: "$[0].status", value: "pending"},
					{path: "$[1]", absent: true},
				},
			}),
		},
		{
			name: "error when specified instanceID does not exist",
			setup: func(env testEnv) {
				if err := env.ProductStore.CreateProduct(context.Background(), product.Product{
					ID: "prod-bad-inst", Name: "Test Product", Category: "Test", UnitOfMeasure: "unit",
				}); err != nil {
					env.T.Fatalf("CreateProduct: %v", err)
				}
				if _, err := env.DB.ExecContext(context.Background(),
					`INSERT INTO items (id, user_id, product_id) VALUES ('item-bad-inst', 'user-bad-inst', 'prod-bad-inst')`); err != nil {
					env.T.Fatalf("create item: %v", err)
				}
				if _, err := env.DB.ExecContext(context.Background(),
					`INSERT INTO item_instances (id, item_id, stock_in_at, expires_at) VALUES ('inst-exists', 'item-bad-inst', ?, ?)`,
					now.Add(-24*time.Hour), sql.NullTime{Time: expiresAt, Valid: true}); err != nil {
					env.T.Fatalf("create instance: %v", err)
				}
				scanRepo := scan.NewQueue(env.DB)
				productID := "prod-bad-inst"
				direction := scan.StockOut
				entry := scan.ScanEntry{
					ID: "scan-bad-inst", UserID: "user-bad-inst", Barcode: "444555666777",
					ScannedAt: now, Direction: &direction, UnitCount: 1,
					ProductID: &productID, Status: scan.Pending,
				}
				if _, err := scanRepo.CreateScanEntry(context.Background(), entry); err != nil {
					env.T.Fatalf("CreateScanEntry: %v", err)
				}
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans/scan-bad-inst/commit",
				body:           `{"instanceId":"inst-does-not-exist"}`,
				expectedStatus: http.StatusConflict,
				assertions: []assertion{
					{path: "$.error", value: "That one was already taken out. Pick another."},
					{path: "$.code", value: "instance_gone"},
					{path: "$.members", absent: true},
					{path: "$.suggestions", absent: true},
				},
			},
		},
		{
			name: "error when specified instanceID is already removed",
			setup: func(env testEnv) {
				if err := env.ProductStore.CreateProduct(context.Background(), product.Product{
					ID: "prod-removed-inst", Name: "Test Product", Category: "Test", UnitOfMeasure: "unit",
				}); err != nil {
					env.T.Fatalf("CreateProduct: %v", err)
				}
				if _, err := env.DB.ExecContext(context.Background(),
					`INSERT INTO items (id, user_id, product_id) VALUES ('item-removed-inst', 'user-removed-inst', 'prod-removed-inst')`); err != nil {
					env.T.Fatalf("create item: %v", err)
				}
				if _, err := env.DB.ExecContext(context.Background(), `
					INSERT INTO item_instances (id, item_id, stock_in_at, expires_at, removed_at, removal_reason)
					VALUES ('inst-removed', 'item-removed-inst', ?, ?, ?, ?)`,
					now.Add(-48*time.Hour),
					sql.NullTime{Time: expiresAt, Valid: true},
					sql.NullTime{Time: now.Add(-24 * time.Hour), Valid: true},
					sql.NullString{String: "consumed", Valid: true}); err != nil {
					env.T.Fatalf("create removed instance: %v", err)
				}
				scanRepo := scan.NewQueue(env.DB)
				productID := "prod-removed-inst"
				direction := scan.StockOut
				entry := scan.ScanEntry{
					ID: "scan-removed-inst", UserID: "user-removed-inst", Barcode: "555666777888",
					ScannedAt: now, Direction: &direction, UnitCount: 1,
					ProductID: &productID, Status: scan.Pending,
				}
				if _, err := scanRepo.CreateScanEntry(context.Background(), entry); err != nil {
					env.T.Fatalf("CreateScanEntry: %v", err)
				}
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans/scan-removed-inst/commit",
				body:           `{"instanceId":"inst-removed"}`,
				expectedStatus: http.StatusConflict,
				assertions: []assertion{
					{path: "$.error", value: "Nothing on hand to take out."},
					{path: "$.code", value: "none_on_hand"},
					{path: "$.members", absent: true},
					{path: "$.suggestions", absent: true},
				},
			},
			afterRequest: exchanges(
				httpExchange{
					method:         "GET",
					path:           "/api/scans",
					query:          map[string]string{"userId": "user-removed-inst", "status": "pending"},
					expectedStatus: http.StatusOK,
					assertions: []assertion{
						{path: "$[0].id", value: "scan-removed-inst"},
						{path: "$[0].status", value: "pending"},
						{path: "$[1]", absent: true},
					},
				},
				httpExchange{
					method:         "GET",
					path:           "/api/inventory/item-removed-inst/instances",
					expectedStatus: http.StatusOK,
					assertions:     []assertion{{path: "$[0]", absent: true}},
				},
			),
		},
		{
			name: "removed instance with another unit still on hand asks for a different one",
			setup: func(env testEnv) {
				if err := env.ProductStore.CreateProduct(context.Background(), product.Product{
					ID: "prod-stale", Name: "Test Product", Category: "Test", UnitOfMeasure: "unit",
				}); err != nil {
					env.T.Fatalf("CreateProduct: %v", err)
				}
				if _, err := env.DB.ExecContext(context.Background(),
					`INSERT INTO items (id, user_id, product_id) VALUES ('item-stale', 'user-stale', 'prod-stale')`); err != nil {
					env.T.Fatalf("create item: %v", err)
				}
				if _, err := env.DB.ExecContext(context.Background(), `
					INSERT INTO item_instances (id, item_id, stock_in_at, removed_at, removal_reason)
					VALUES ('inst-stale', 'item-stale', ?, ?, 'consumed')`,
					now.Add(-48*time.Hour), now.Add(-time.Hour)); err != nil {
					env.T.Fatalf("create removed instance: %v", err)
				}
				if _, err := env.DB.ExecContext(context.Background(),
					`INSERT INTO item_instances (id, item_id, stock_in_at) VALUES ('inst-still', 'item-stale', ?)`,
					now.Add(-24*time.Hour)); err != nil {
					env.T.Fatalf("create remaining instance: %v", err)
				}
				scanRepo := scan.NewQueue(env.DB)
				productID := "prod-stale"
				direction := scan.StockOut
				entry := scan.ScanEntry{
					ID: "scan-stale", UserID: "user-stale", Barcode: "666777888999",
					ScannedAt: now, Direction: &direction, UnitCount: 1,
					ProductID: &productID, Status: scan.Pending,
				}
				if _, err := scanRepo.CreateScanEntry(context.Background(), entry); err != nil {
					env.T.Fatalf("CreateScanEntry: %v", err)
				}
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans/scan-stale/commit",
				body:           `{"instanceId":"inst-stale"}`,
				expectedStatus: http.StatusConflict,
				assertions: []assertion{
					{path: "$.error", value: "That one was already taken out. Pick another."},
					{path: "$.code", value: "instance_gone"},
					{path: "$.members", absent: true},
					{path: "$.suggestions", absent: true},
				},
			},
			afterRequest: exchanges(
				httpExchange{
					method:         "GET",
					path:           "/api/scans",
					query:          map[string]string{"userId": "user-stale", "status": "pending"},
					expectedStatus: http.StatusOK,
					assertions: []assertion{
						{path: "$[0].id", value: "scan-stale"},
						{path: "$[0].status", value: "pending"},
						{path: "$[1]", absent: true},
					},
				},
				httpExchange{
					method:         "GET",
					path:           "/api/inventory/item-stale/instances",
					expectedStatus: http.StatusOK,
					assertions: []assertion{
						{path: "$[0].id", value: "inst-still"},
						{path: "$[1]", absent: true},
					},
				},
			),
		},
	}

	runHandlerTests(t, tests)
}

func TestScanResolveFlaggedEntry(t *testing.T) {
	now := time.Now()

	tests := []handlerTestCase{
		{
			name: "successfully resolve flagged entry with new product override",
			setup: func(env testEnv) {
				if err := env.ProductStore.CreateProduct(context.Background(), product.Product{
					ID: "prod-resolve-1", Name: "Corrected Product", Category: "Test", UnitOfMeasure: "unit",
				}); err != nil {
					env.T.Fatalf("CreateProduct: %v", err)
				}
				scanRepo := scan.NewQueue(env.DB)
				entry := scan.ScanEntry{
					ID: "scan-resolve-1", UserID: "user-resolve-1", Barcode: "123456789999",
					ScannedAt: now, UnitCount: 1, Status: scan.Flagged,
				}
				if _, err := scanRepo.CreateScanEntry(context.Background(), entry); err != nil {
					env.T.Fatalf("CreateScanEntry: %v", err)
				}
			},
			httpExchange: httpExchange{
				method:         "PATCH",
				path:           "/api/scans/scan-resolve-1",
				body:           `{"productId":"prod-resolve-1","status":"pending"}`,
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.status", value: "pending"},
					{path: "$.productId", value: "prod-resolve-1"},
				},
			},
			afterRequest: func(env testEnv) {
				// The barcode override mapping is not exposed by any list endpoint.
				var productID string
				if err := env.DB.QueryRowContext(context.Background(),
					`SELECT product_id FROM barcodes WHERE barcode = ? AND source = 'user_override' AND user_id = ?`,
					"123456789999", "user-resolve-1",
				).Scan(&productID); err != nil {
					env.T.Fatalf("query override: %v", err)
				}
				if productID != "prod-resolve-1" {
					env.T.Errorf("expected product_id 'prod-resolve-1', got %q", productID)
				}
			},
		},
		{
			name: "successfully update existing product override (resolve same barcode twice)",
			setup: func(env testEnv) {
				if err := env.ProductStore.CreateProduct(context.Background(), product.Product{
					ID: "prod-resolve-2a", Name: "Original Product", Category: "Test", UnitOfMeasure: "unit",
				}); err != nil {
					env.T.Fatalf("CreateProduct 1: %v", err)
				}
				if err := env.ProductStore.CreateProduct(context.Background(), product.Product{
					ID: "prod-resolve-2b", Name: "Corrected Product", Category: "Test", UnitOfMeasure: "unit",
				}); err != nil {
					env.T.Fatalf("CreateProduct 2: %v", err)
				}
				if _, err := env.DB.ExecContext(context.Background(),
					`INSERT INTO barcodes (barcode, product_id, source, user_id) VALUES (?, ?, 'user_override', ?)`,
					"888999000111", "prod-resolve-2a", "user-resolve-2"); err != nil {
					env.T.Fatalf("create existing override: %v", err)
				}
				scanRepo := scan.NewQueue(env.DB)
				entry := scan.ScanEntry{
					ID: "scan-resolve-2", UserID: "user-resolve-2", Barcode: "888999000111",
					ScannedAt: now, UnitCount: 1, Status: scan.Flagged,
				}
				if _, err := scanRepo.CreateScanEntry(context.Background(), entry); err != nil {
					env.T.Fatalf("CreateScanEntry: %v", err)
				}
			},
			httpExchange: httpExchange{
				method:         "PATCH",
				path:           "/api/scans/scan-resolve-2",
				body:           `{"productId":"prod-resolve-2b","status":"pending"}`,
				expectedStatus: http.StatusOK,
			},
			afterRequest: func(env testEnv) {
				var productID string
				if err := env.DB.QueryRowContext(context.Background(),
					`SELECT product_id FROM barcodes WHERE barcode = ? AND source = 'user_override' AND user_id = ?`,
					"888999000111", "user-resolve-2",
				).Scan(&productID); err != nil {
					env.T.Fatalf("query override: %v", err)
				}
				if productID != "prod-resolve-2b" {
					env.T.Errorf("expected updated product_id 'prod-resolve-2b', got %q", productID)
				}
				var count int
				if err := env.DB.QueryRowContext(context.Background(),
					`SELECT COUNT(*) FROM barcodes WHERE barcode = ? AND source = 'user_override' AND user_id = ?`,
					"888999000111", "user-resolve-2",
				).Scan(&count); err != nil {
					env.T.Fatalf("count overrides: %v", err)
				}
				if count != 1 {
					env.T.Errorf("expected 1 override, got %d", count)
				}
			},
		},
		{
			name: "error when scan entry not found",
			httpExchange: httpExchange{
				method:         "PATCH",
				path:           "/api/scans/scan-does-not-exist",
				body:           `{"productId":"prod-x","status":"pending"}`,
				expectedStatus: http.StatusNotFound,
			},
		},
		{
			name: "resolving a non-flagged entry succeeds",
			setup: func(env testEnv) {
				if err := env.ProductStore.CreateProduct(context.Background(), product.Product{
					ID: "prod-resolve-pending", Name: "Test Product", Category: "Test", UnitOfMeasure: "unit",
				}); err != nil {
					env.T.Fatalf("CreateProduct: %v", err)
				}
				scanRepo := scan.NewQueue(env.DB)
				productID := "prod-resolve-pending"
				direction := scan.StockIn
				entry := scan.ScanEntry{
					ID: "scan-resolve-pending", UserID: "user-resolve-pending", Barcode: "777888999000",
					ScannedAt: now, Direction: &direction, UnitCount: 1,
					ProductID: &productID, Status: scan.Pending,
				}
				if _, err := scanRepo.CreateScanEntry(context.Background(), entry); err != nil {
					env.T.Fatalf("CreateScanEntry: %v", err)
				}
			},
			httpExchange: httpExchange{
				method:         "PATCH",
				path:           "/api/scans/scan-resolve-pending",
				body:           `{"productId":"prod-resolve-pending"}`,
				expectedStatus: http.StatusOK,
			},
		},
		{
			name: "error when product does not exist",
			setup: func(env testEnv) {
				scanRepo := scan.NewQueue(env.DB)
				entry := scan.ScanEntry{
					ID: "scan-resolve-bad-prod", UserID: "user-resolve-bad-prod", Barcode: "666777888999",
					ScannedAt: now, UnitCount: 1, Status: scan.Flagged,
				}
				if _, err := scanRepo.CreateScanEntry(context.Background(), entry); err != nil {
					env.T.Fatalf("CreateScanEntry: %v", err)
				}
			},
			httpExchange: httpExchange{
				method:         "PATCH",
				path:           "/api/scans/scan-resolve-bad-prod",
				body:           `{"productId":"prod-does-not-exist","status":"pending"}`,
				expectedStatus: http.StatusInternalServerError,
			},
		},
	}

	runHandlerTests(t, tests)
}

func TestScanCommitStockOutNoItemLink(t *testing.T) {
	now := time.Now()
	seed := func(env testEnv) {
		products := []product.Product{
			{ID: "prod-off", Name: "Lite Pear Slices In Pear Juice", Category: "Fruit", UnitOfMeasure: "can"},
			{ID: "prod-pears", Name: "Sliced Pears", Category: "Fruit", UnitOfMeasure: "can"},
			{ID: "prod-crackers", Name: "Unrelated Crackers", Category: "Snacks", UnitOfMeasure: "box"},
			{ID: "prod-slices", Name: "Pear Slices", Category: "Fruit", UnitOfMeasure: "can"},
			{ID: "prod-tuna", Name: "Canned Tuna", Category: "Canned", UnitOfMeasure: "can"},
			{ID: "prod-beans", Name: "Black Beans", Category: "Canned", UnitOfMeasure: "can"},
			{ID: "prod-empty-pear", Name: "Empty Pear", Category: "Fruit", UnitOfMeasure: "can"},
			{ID: "prod-shelved-pear", Name: "Shelved Pear", Category: "Fruit", UnitOfMeasure: "can"},
		}
		for _, item := range products {
			if err := env.ProductStore.CreateProduct(context.Background(), item); err != nil {
				env.T.Fatalf("CreateProduct %s: %v", item.ID, err)
			}
		}
		barcodes := []struct{ code, productID, source, userID string }{
			{"011110828385", "prod-off", "global", ""},
			{"828385", "prod-pears", "user_override", "user-1"},
			{"8385", "prod-crackers", "global", ""},
			{"999000111222", "prod-slices", "global", ""},
			{"491", "prod-tuna", "user_override", "user-1"},
			{"1552", "prod-beans", "global", ""},
			{"110828385", "prod-empty-pear", "user_override", "user-1"},
			{"011110828385", "prod-shelved-pear", "user_override", "user-1"},
		}
		for _, barcode := range barcodes {
			if _, err := env.DB.ExecContext(context.Background(),
				`INSERT INTO barcodes (barcode, product_id, source, user_id) VALUES (?, ?, ?, ?)`,
				barcode.code, barcode.productID, barcode.source, barcode.userID); err != nil {
				env.T.Fatalf("barcode %s: %v", barcode.code, err)
			}
		}
		items := []struct {
			id, productID string
			count         int
		}{
			{"item-pears", "prod-pears", 3},
			{"item-crackers", "prod-crackers", 4},
			{"item-slices", "prod-slices", 2},
			{"item-tuna", "prod-tuna", 1},
			{"item-beans", "prod-beans", 2},
			{"item-empty-pear", "prod-empty-pear", 0},
			{"item-shelved-pear", "prod-shelved-pear", 2},
		}
		for _, item := range items {
			if _, err := env.DB.ExecContext(context.Background(),
				`INSERT INTO items (id, user_id, product_id) VALUES (?, 'user-1', ?)`,
				item.id, item.productID); err != nil {
				env.T.Fatalf("item %s: %v", item.id, err)
			}
			for n := 0; n < item.count; n++ {
				exp := sql.NullTime{}
				if item.id == "item-pears" && n == 0 {
					exp = sql.NullTime{Time: now.Add(3 * 24 * time.Hour), Valid: true}
				}
				if item.id == "item-pears" && n == 1 {
					exp = sql.NullTime{Time: now.Add(10 * 24 * time.Hour), Valid: true}
				}
				if _, err := env.DB.ExecContext(context.Background(),
					`INSERT INTO item_instances (id, item_id, stock_in_at, expires_at) VALUES (?, ?, ?, ?)`,
					fmt.Sprintf("%s-%d", item.id, n), item.id, now.Add(-24*time.Hour), exp); err != nil {
					env.T.Fatalf("instance %s-%d: %v", item.id, n, err)
				}
			}
		}
		if _, err := env.DB.ExecContext(context.Background(),
			`UPDATE items SET shelved = 1 WHERE id = 'item-shelved-pear'`); err != nil {
			env.T.Fatalf("shelve pear: %v", err)
		}
		scanRepo := scan.NewQueue(env.DB)
		productID := "prod-off"
		direction := scan.StockOut
		entry := scan.ScanEntry{
			ID: "scan-pears", UserID: "user-1", Barcode: "011110828385",
			ScannedAt: now, Direction: &direction, UnitCount: 1,
			ProductID: &productID, Status: scan.Pending,
		}
		if _, err := scanRepo.CreateScanEntry(context.Background(), entry); err != nil {
			env.T.Fatalf("CreateScanEntry: %v", err)
		}
	}

	tests := []handlerTestCase{
		{
			name:  "stock-out of an unlinked barcode offers ranked links and writes nothing",
			setup: seed,
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans/scan-pears/commit",
				expectedStatus: http.StatusConflict,
				assertions: []assertion{
					{path: "$.code", value: "no_item"},
					{path: "$.error", value: "Which item is this?"},
					{path: "$.suggestions[0].name", value: "Sliced Pears"},
					{path: "$.suggestions[0].productId", value: "prod-pears"},
					{path: "$.suggestions[0].itemId", value: "item-pears"},
					{path: "$.suggestions[0].onHand", value: float64(3)},
					{path: "$.suggestions[1].name", value: "Unrelated Crackers"},
					{path: "$.suggestions[2].name", value: "Pear Slices"},
					{path: "$.suggestions[3]", absent: true},
					{path: "$.members", absent: true},
				},
				bodyExcludes: []string{"Not in your pantry", "No close match found.", "Nothing on hand to take out.", "Choose which item this is first.", "Canned Tuna", "Black Beans", "Empty Pear", "Shelved Pear", "no item found"},
			},
			afterRequest: exchanges(
				httpExchange{
					method:         "GET",
					path:           "/api/scans",
					query:          map[string]string{"userId": "user-1", "status": "pending"},
					expectedStatus: http.StatusOK,
					assertions: []assertion{
						{path: "$[0].id", value: "scan-pears"},
						{path: "$[0].status", value: "pending"},
						{path: "$[0].productId", value: "prod-off"},
					},
				},
				httpExchange{
					method:         "GET",
					path:           "/api/products/prod-pears",
					expectedStatus: http.StatusOK,
					assertions: []assertion{
						{path: "$.barcodes[0]", value: "828385"},
						{path: "$.barcodes[1]", absent: true},
					},
				},
			),
		},
		{
			name:  "linking a suggestion decrements that item and keeps both barcodes",
			setup: seed,
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans/scan-pears/commit",
				body:           `{"linkProductId":"prod-pears"}`,
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.status", value: "committed"},
					{path: "$.productId", value: "prod-pears"},
					{path: "$.barcode", value: "011110828385"},
					{path: "$.onHand", value: float64(2)},
				},
			},
			afterRequest: exchanges(
				httpExchange{
					method:         "GET",
					path:           "/api/inventory/item-pears/instances",
					expectedStatus: http.StatusOK,
					assertions: []assertion{
						{path: "$[0].id", value: "item-pears-1"},
						{path: "$[1].id", value: "item-pears-2"},
						{path: "$[2]", absent: true},
					},
				},
				httpExchange{
					method:         "GET",
					path:           "/api/products/prod-pears",
					expectedStatus: http.StatusOK,
					assertions: []assertion{
						{path: "$.barcodes[0]", value: "011110828385"},
						{path: "$.barcodes[1]", value: "828385"},
					},
				},
				httpExchange{
					method:         "GET",
					path:           "/api/products/prod-off",
					expectedStatus: http.StatusOK,
					assertions: []assertion{
						{path: "$.barcodes[0]", value: "011110828385"},
						{path: "$.barcodes[1]", absent: true},
					},
				},
				httpExchange{
					method:         "GET",
					path:           "/api/inventory/item-slices/instances",
					expectedStatus: http.StatusOK,
					bodyContains:   []string{"item-slices-0", "item-slices-1"},
					assertions: []assertion{
						{path: "$[2]", absent: true},
					},
				},
			),
		},
		{
			name:  "linking a product the household does not stock leaves the scan pending",
			setup: seed,
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans/scan-pears/commit",
				body:           `{"linkProductId":"prod-off"}`,
				expectedStatus: http.StatusConflict,
				assertions: []assertion{
					{path: "$.error", value: "That item isn't available anymore. Pick another."},
					{path: "$.code", value: "link_target_gone"},
					{path: "$.suggestions", absent: true},
					{path: "$.members", absent: true},
				},
				bodyExcludes: []string{"That product is not in your pantry"},
			},
			afterRequest: exchanges(httpExchange{
				method:         "GET",
				path:           "/api/scans",
				query:          map[string]string{"userId": "user-1", "status": "pending"},
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$[0].id", value: "scan-pears"},
					{path: "$[0].status", value: "pending"},
					{path: "$[0].productId", value: "prod-off"},
					{path: "$[1]", absent: true},
				},
			}),
		},
		{
			name: "linking a shelved product leaves the scan pending",
			setup: func(env testEnv) {
				seed(env)
				if _, err := env.DB.ExecContext(context.Background(),
					`INSERT INTO items (id, user_id, product_id, shelved) VALUES ('item-wiped', 'user-1', 'prod-off', 1)`); err != nil {
					env.T.Fatalf("shelve item: %v", err)
				}
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans/scan-pears/commit",
				body:           `{"linkProductId":"prod-off"}`,
				expectedStatus: http.StatusConflict,
				assertions: []assertion{
					{path: "$.error", value: "That item isn't available anymore. Pick another."},
					{path: "$.code", value: "link_target_gone"},
					{path: "$.suggestions", absent: true},
				},
				bodyExcludes: []string{"That product is not in your pantry"},
			},
			afterRequest: exchanges(httpExchange{
				method:         "GET",
				path:           "/api/scans",
				query:          map[string]string{"userId": "user-1", "status": "pending"},
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$[0].id", value: "scan-pears"},
					{path: "$[0].status", value: "pending"},
					{path: "$[1]", absent: true},
				},
			}),
		},
		{
			name:  "linking an unknown product leaves the scan pending",
			setup: seed,
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans/scan-pears/commit",
				body:           `{"linkProductId":"prod-missing"}`,
				expectedStatus: http.StatusConflict,
				assertions: []assertion{
					{path: "$.error", value: "That item isn't available anymore. Pick another."},
					{path: "$.code", value: "link_target_gone"},
					{path: "$.suggestions", absent: true},
				},
				bodyExcludes: []string{"That product is not in your pantry"},
			},
			afterRequest: exchanges(httpExchange{
				method:         "GET",
				path:           "/api/scans",
				query:          map[string]string{"userId": "user-1", "status": "pending"},
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$[0].id", value: "scan-pears"},
					{path: "$[0].status", value: "pending"},
					{path: "$[0].productId", value: "prod-off"},
					{path: "$[1]", absent: true},
				},
			}),
		},
	}

	runHandlerTests(t, tests)
}

// TestScanOutOfEmptyShelf is the #277 repro: stock a product in, scan it out
// until the shelf is empty, then commit another scan-out. The handler suite
// opens a temporary in-memory database and applies migrations before the request.
func TestScanOutOfEmptyShelf(t *testing.T) {
	runHandlerTests(t, []handlerTestCase{
		{
			name: "committing a scan-out when nothing is on hand is a conflict and writes nothing",
			setup: func(env testEnv) {
				finishOpening(env)
				seedShelfItem(env, "prod-zero", "Oat Milk", "item-zero")
				seedScanAt(env, "scan-in", "prod-zero", scan.StockIn, 1, time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC))
				seedScanAt(env, "scan-out-1", "prod-zero", scan.StockOut, 1, time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC))
				h := handlerFromEnv(env)
				commitScan(env, h, "scan-in")
				commitScan(env, h, "scan-out-1")
				// The first stock-out is committed, so scanning the product out again is a new card.
				seedScanAt(env, "scan-out-2", "prod-zero", scan.StockOut, 1, time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC))
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans/scan-out-2/commit",
				expectedStatus: http.StatusConflict,
				assertions: []assertion{
					{path: "$.error", value: "Nothing on hand to take out."},
					{path: "$.code", value: "none_on_hand"},
					{path: "$.members", absent: true},
					{path: "$.suggestions", absent: true},
				},
				bodyExcludes: []string{"Something went wrong", "no available instances", "item-zero"},
			},
			afterRequest: exchanges(
				httpExchange{
					method:         "GET",
					path:           "/api/inventory",
					expectedStatus: http.StatusOK,
					assertions: []assertion{
						{path: "$[0].item.id", value: "item-zero"},
						{path: "$[0].instanceCount", value: float64(0)},
						{path: "$[1]", absent: true},
					},
				},
				httpExchange{
					method:         "GET",
					path:           "/api/scans",
					query:          map[string]string{"userId": "user-1", "status": "pending"},
					expectedStatus: http.StatusOK,
					assertions: []assertion{
						{path: "$[0].id", value: "scan-out-2"},
						{path: "$[0].status", value: "pending"},
						{path: "$[0].productId", value: "prod-zero"},
						{path: "$[1]", absent: true},
					},
				},
				httpExchange{
					method:         "GET",
					path:           "/api/scans/history",
					query:          map[string]string{"userId": "user-1"},
					expectedStatus: http.StatusOK,
					bodyContains:   []string{"scan-in", "scan-out-1"},
					bodyExcludes:   []string{"scan-out-2"},
				},
				httpExchange{
					method:         "GET",
					path:           "/api/items/item-zero/history",
					expectedStatus: http.StatusOK,
					assertions: []assertion{
						{path: "$.onHand", value: float64(0)},
						{path: "$.moves[0].direction", value: "out"},
						{path: "$.moves[0].quantity", value: float64(1)},
						{path: "$.moves[0].source", value: "scan"},
						{path: "$.moves[1].direction", value: "in"},
						{path: "$.moves[1].quantity", value: float64(1)},
						{path: "$.moves[2]", absent: true},
					},
				},
			),
		},
		{
			name: "linking a barcode onto an empty shelf keeps the link",
			setup: func(env testEnv) {
				finishOpening(env)
				seedShelfItem(env, "prod-empty", "Empty Oats", "item-empty")
				seedShelfItem(env, "prod-other", "Other Oats", "item-other")
				seedScanAt(env, "scan-link", "prod-other", scan.StockOut, 1, time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC))
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans/scan-link/commit",
				body:           `{"linkProductId":"prod-empty"}`,
				expectedStatus: http.StatusConflict,
				assertions: []assertion{
					{path: "$.error", value: "Nothing on hand to take out."},
					{path: "$.code", value: "none_on_hand"},
					{path: "$.itemId", value: "item-empty"},
					{path: "$.onHand", value: float64(0)},
					{path: "$.members", absent: true},
					{path: "$.suggestions", absent: true},
				},
				bodyExcludes: []string{"link_target_gone", "That item isn't available anymore. Pick another."},
			},
			afterRequest: exchanges(
				httpExchange{
					method:         "POST",
					path:           "/api/scans/scan-link/commit",
					expectedStatus: http.StatusConflict,
					assertions: []assertion{
						{path: "$.error", value: "Nothing on hand to take out."},
						{path: "$.code", value: "none_on_hand"},
					},
				},
				httpExchange{
					method:         "GET",
					path:           "/api/scans",
					query:          map[string]string{"userId": "user-1", "status": "pending"},
					expectedStatus: http.StatusOK,
					assertions: []assertion{
						{path: "$[0].id", value: "scan-link"},
						{path: "$[0].status", value: "pending"},
						{path: "$[0].productId", value: "prod-empty"},
						{path: "$[0].onHand", absent: true},
					},
				},
				httpExchange{
					method:         "GET",
					path:           "/api/products/prod-empty",
					expectedStatus: http.StatusOK,
					assertions: []assertion{
						{path: "$.barcodes[0]", value: "scan-link"},
						{path: "$.barcodes[1]", absent: true},
					},
				},
				httpExchange{
					method:         "GET",
					path:           "/api/items/item-empty/history",
					expectedStatus: http.StatusOK,
					assertions: []assertion{
						{path: "$.onHand", value: float64(0)},
						{path: "$.moves", value: []any{}},
					},
				},
				httpExchange{
					method:         "GET",
					path:           "/api/inventory",
					expectedStatus: http.StatusOK,
					assertions: []assertion{
						{path: "$[0].instanceCount", value: float64(0)},
						{path: "$[1].instanceCount", value: float64(0)},
						{path: "$[2]", absent: true},
					},
				},
			),
		},
	})
}

// TestScanCommitRemovedInstance is the #285 regression. On master, naming an
// instance that was already removed returned 500. The commit must refuse with
// 409 and leave every stock move, instance, and scan row as it was.
func TestScanCommitRemovedInstance(t *testing.T) {
	now := time.Date(2026, 6, 3, 15, 0, 0, 0, time.UTC)
	expiresAt := now.Add(48 * time.Hour)
	snaps := map[string]string{}

	runHandlerTests(t, []handlerTestCase{
		{
			name: "removed instance with nothing else on hand is an empty shelf",
			setup: func(env testEnv) {
				seedRemovedInstanceScan(env, now, expiresAt, "only")
				snaps["only"] = pantrySnapshot(env, "item-only", "scan-only")
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans/scan-only/commit",
				body:           `{"instanceId":"inst-only"}`,
				expectedStatus: http.StatusConflict,
				assertions: []assertion{
					{path: "$.error", value: "Nothing on hand to take out."},
					{path: "$.code", value: "none_on_hand"},
				},
			},
			afterRequest: func(env testEnv) {
				assertPantryUnchanged(env, snaps["only"], "item-only", "scan-only")
			},
		},
		{
			name: "removed instance with another unit still on hand",
			setup: func(env testEnv) {
				seedRemovedInstanceScan(env, now, expiresAt, "stale")
				if _, err := env.DB.ExecContext(context.Background(),
					`INSERT INTO item_instances (id, item_id, stock_in_at) VALUES ('inst-stale-left', 'item-stale', ?)`,
					now.Add(-time.Hour)); err != nil {
					env.T.Fatalf("create remaining instance: %v", err)
				}
				snaps["stale"] = pantrySnapshot(env, "item-stale", "scan-stale")
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans/scan-stale/commit",
				body:           `{"instanceId":"inst-stale"}`,
				expectedStatus: http.StatusConflict,
				assertions: []assertion{
					{path: "$.error", value: "That one was already taken out. Pick another."},
					{path: "$.code", value: "instance_gone"},
				},
			},
			afterRequest: func(env testEnv) {
				assertPantryUnchanged(env, snaps["stale"], "item-stale", "scan-stale")
			},
		},
		{
			name: "instance id that never existed still has another unit",
			setup: func(env testEnv) {
				seedRemovedInstanceScan(env, now, expiresAt, "missing")
				if _, err := env.DB.ExecContext(context.Background(),
					`UPDATE item_instances SET removed_at = NULL, removal_reason = NULL WHERE id = 'inst-missing'`); err != nil {
					env.T.Fatalf("restore instance: %v", err)
				}
				snaps["missing"] = pantrySnapshot(env, "item-missing", "scan-missing")
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans/scan-missing/commit",
				body:           `{"instanceId":"inst-never"}`,
				expectedStatus: http.StatusConflict,
				assertions: []assertion{
					{path: "$.error", value: "That one was already taken out. Pick another."},
					{path: "$.code", value: "instance_gone"},
				},
			},
			afterRequest: func(env testEnv) {
				assertPantryUnchanged(env, snaps["missing"], "item-missing", "scan-missing")
			},
		},
		{
			name: "instance id from a different item is not on this shelf",
			setup: func(env testEnv) {
				seedRemovedInstanceScan(env, now, expiresAt, "home")
				if _, err := env.DB.ExecContext(context.Background(),
					`UPDATE item_instances SET removed_at = NULL, removal_reason = NULL WHERE id = 'inst-home'`); err != nil {
					env.T.Fatalf("restore instance: %v", err)
				}
				if err := env.ProductStore.CreateProduct(context.Background(), product.Product{
					ID: "prod-other-home", Name: "Other", Category: "Test", UnitOfMeasure: "unit",
				}); err != nil {
					env.T.Fatalf("CreateProduct: %v", err)
				}
				if _, err := env.DB.ExecContext(context.Background(),
					`INSERT INTO items (id, user_id, product_id) VALUES ('item-other-home', 'user-home', 'prod-other-home')`); err != nil {
					env.T.Fatalf("create other item: %v", err)
				}
				if _, err := env.DB.ExecContext(context.Background(),
					`INSERT INTO item_instances (id, item_id, stock_in_at) VALUES ('inst-foreign', 'item-other-home', ?)`,
					now); err != nil {
					env.T.Fatalf("create foreign instance: %v", err)
				}
				snaps["home"] = pantrySnapshot(env, "item-home", "scan-home")
				snaps["foreign"] = pantrySnapshot(env, "item-other-home", "scan-home")
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans/scan-home/commit",
				body:           `{"instanceId":"inst-foreign"}`,
				expectedStatus: http.StatusConflict,
				assertions: []assertion{
					{path: "$.error", value: "That one was already taken out. Pick another."},
					{path: "$.code", value: "instance_gone"},
				},
			},
			afterRequest: func(env testEnv) {
				assertPantryUnchanged(env, snaps["home"], "item-home", "scan-home")
				assertPantryUnchanged(env, snaps["foreign"], "item-other-home", "scan-home")
			},
		},
		{
			name: "linking while the named instance is gone does not keep the link",
			setup: func(env testEnv) {
				seedRemovedInstanceScan(env, now, expiresAt, "link")
				if _, err := env.DB.ExecContext(context.Background(),
					`INSERT INTO item_instances (id, item_id, stock_in_at) VALUES ('inst-link-left', 'item-link', ?)`,
					now.Add(-time.Hour)); err != nil {
					env.T.Fatalf("create remaining instance: %v", err)
				}
				if err := env.ProductStore.CreateProduct(context.Background(), product.Product{
					ID: "prod-link-src", Name: "Scanned", Category: "Test", UnitOfMeasure: "unit",
				}); err != nil {
					env.T.Fatalf("CreateProduct: %v", err)
				}
				if _, err := env.DB.ExecContext(context.Background(),
					`UPDATE scan_entries SET product_id = 'prod-link-src', barcode = '999000111222' WHERE id = 'scan-link'`); err != nil {
					env.T.Fatalf("point scan at the other product: %v", err)
				}
				snaps["link"] = pantrySnapshot(env, "item-link", "scan-link") + barcodeSnapshot(env, "999000111222")
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans/scan-link/commit",
				body:           `{"instanceId":"inst-link","linkProductId":"prod-link"}`,
				expectedStatus: http.StatusConflict,
				assertions: []assertion{
					{path: "$.error", value: "That one was already taken out. Pick another."},
					{path: "$.code", value: "instance_gone"},
				},
			},
			afterRequest: func(env testEnv) {
				got := pantrySnapshot(env, "item-link", "scan-link") + barcodeSnapshot(env, "999000111222")
				if got != snaps["link"] {
					env.T.Fatalf("rows changed\nbefore:\n%s\nafter:\n%s", snaps["link"], got)
				}
			},
		},
	})
}

func seedRemovedInstanceScan(env testEnv, now, expiresAt time.Time, suffix string) {
	env.T.Helper()
	productID := "prod-" + suffix
	itemID := "item-" + suffix
	instanceID := "inst-" + suffix
	scanID := "scan-" + suffix
	userID := "user-" + suffix
	if err := env.ProductStore.CreateProduct(context.Background(), product.Product{
		ID: productID, Name: "Removed " + suffix, Category: "Test", UnitOfMeasure: "unit",
	}); err != nil {
		env.T.Fatalf("CreateProduct: %v", err)
	}
	if _, err := env.DB.ExecContext(context.Background(),
		`INSERT INTO items (id, user_id, product_id) VALUES (?, ?, ?)`, itemID, userID, productID); err != nil {
		env.T.Fatalf("create item: %v", err)
	}
	if _, err := env.DB.ExecContext(context.Background(), `
		INSERT INTO item_instances (id, item_id, stock_in_at, expires_at, removed_at, removal_reason)
		VALUES (?, ?, ?, ?, ?, 'consumed')`,
		instanceID, itemID, now.Add(-48*time.Hour), expiresAt, now.Add(-time.Hour)); err != nil {
		env.T.Fatalf("create removed instance: %v", err)
	}
	direction := scan.StockOut
	if _, err := scan.NewQueue(env.DB).CreateScanEntry(context.Background(), scan.ScanEntry{
		ID: scanID, UserID: userID, Barcode: "barcode-" + suffix,
		ScannedAt: now, Direction: &direction, UnitCount: 1,
		ProductID: &productID, Status: scan.Pending,
	}); err != nil {
		env.T.Fatalf("CreateScanEntry: %v", err)
	}
}

func assertPantryUnchanged(env testEnv, before, itemID, scanID string) {
	env.T.Helper()
	after := pantrySnapshot(env, itemID, scanID)
	if after != before {
		env.T.Fatalf("rows changed\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func pantrySnapshot(env testEnv, itemID, scanID string) string {
	env.T.Helper()
	var b strings.Builder
	dumpQuery(&b, env, "instances", `
		SELECT id, item_id, IFNULL(CAST(removed_at AS TEXT), ''), IFNULL(removal_reason, ''),
		       IFNULL(stock_move_id, ''), IFNULL(removed_by_move_id, '')
		FROM item_instances WHERE item_id = ? ORDER BY id`, itemID)
	dumpQuery(&b, env, "moves", `
		SELECT id, direction, quantity, source, IFNULL(scan_entry_id, '')
		FROM stock_moves WHERE item_id = ? ORDER BY id`, itemID)
	dumpQuery(&b, env, "scan", `
		SELECT id, status, IFNULL(product_id, ''), IFNULL(CAST(committed_at AS TEXT), '')
		FROM scan_entries WHERE id = ?`, scanID)
	return b.String()
}

func barcodeSnapshot(env testEnv, barcode string) string {
	env.T.Helper()
	var b strings.Builder
	dumpQuery(&b, env, "barcodes", `
		SELECT barcode, product_id, source, user_id FROM barcodes WHERE barcode = ? ORDER BY source, user_id`, barcode)
	return b.String()
}

func dumpQuery(b *strings.Builder, env testEnv, title, query string, args ...any) {
	env.T.Helper()
	fmt.Fprintf(b, "[%s]\n", title)
	rows, err := env.DB.Query(query, args...)
	if err != nil {
		env.T.Fatalf("%s: %v", title, err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		env.T.Fatal(err)
	}
	for rows.Next() {
		vals := make([]sql.RawBytes, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			env.T.Fatal(err)
		}
		for i, col := range cols {
			fmt.Fprintf(b, "%s=%s ", col, string(vals[i]))
		}
		b.WriteByte('\n')
	}
	if err := rows.Err(); err != nil {
		env.T.Fatal(err)
	}
}
