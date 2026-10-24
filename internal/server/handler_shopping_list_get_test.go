package server

import (
	"context"
	"database/sql"
	"net/http"
	"testing"
	"time"
)

func TestShoppingListGet(t *testing.T) {
	tests := []handlerTestCase{
		{
			name: "empty list when no items",
			httpExchange: httpExchange{
				method:         "GET",
				path:           "/api/shopping-list",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$", value: []interface{}{}},
				},
			},
		},
		{
			name: "returns manual item",
			setup: func(env testEnv) {
				createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-sl-1", "Milk", "item-sl-1")
				insertManualShoppingItem(env.T, env.DB, "sli-1", "user-1", "item-sl-1", 2)
			},
			httpExchange: httpExchange{
				method:         "GET",
				path:           "/api/shopping-list",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$[0].itemId", value: "item-sl-1"},
					{path: "$[0].quantity", value: float64(2)},
					{path: "$[0].source", value: "manual"},
				},
			},
		},
		{
			name: "a stored target does not create a shopping line",
			setup: func(env testEnv) {
				createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-sl-2", "Eggs", "item-sl-2")
				setTargetQuantity(env.T, env.DB, "item-sl-2", 3)
			},
			httpExchange: httpExchange{
				method:         "GET",
				path:           "/api/shopping-list",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$", value: []interface{}{}},
				},
			},
		},
		{
			name: "ungrouped store brands are separate shopping lines",
			setup: func(env testEnv) {
				createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-gv", "Great Value Cut Green Beans", "item-gv")
				createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-kr", "Kroger Cut Green Beans", "item-kr")
				createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-wf", "Western Family Cut Green Beans", "item-wf")
				createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-corn", "Kroger Whole Kernel Corn", "item-corn")
				beginUsing(env.T, env.DB, time.Now())
				setSupplyQuantity(env.T, env.DB, "prod-gv", 4)
				setSupplyQuantity(env.T, env.DB, "prod-kr", 4)
				setSupplyQuantity(env.T, env.DB, "prod-corn", 2)
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/shopping-list/fill",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$[0].itemId", value: "item-corn"},
					{path: "$[0].quantity", value: float64(1)},
					{path: "$[0].source", value: "auto"},
					{path: "$[1].itemId", value: "item-gv"},
					{path: "$[1].quantity", value: float64(3)},
					{path: "$[2].itemId", value: "item-kr"},
					{path: "$[2].quantity", value: float64(3)},
					{path: "$[3]", absent: true},
				},
			},
			afterRequest: exchanges(httpExchange{
				method:         "POST",
				path:           "/api/shopping-list/export",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					// Nothing is sent while no provider is configured. The pooled
					// lines above are what a configured provider would receive.
					{path: "$.exported", value: float64(0)},
				},
			}),
		},
		{
			name: "ungrouped stock does not count toward another product",
			setup: func(env testEnv) {
				createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-gv-notarget", "Great Value Cut Green Beans", "item-gv-notarget")
				createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-kr-target", "Kroger Cut Green Beans", "item-kr-target")
				beginUsing(env.T, env.DB, time.Now())
				setSupplyQuantity(env.T, env.DB, "prod-kr-target", 4)
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/shopping-list/fill",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$[0].itemId", value: "item-kr-target"},
					{path: "$[0].quantity", value: float64(3)},
					{path: "$[0].source", value: "auto"},
					{path: "$[1]", absent: true},
				},
			},
		},
		{
			name: "manual line for one brand suppresses the shared auto gap",
			setup: func(env testEnv) {
				createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-gv-manual", "Great Value Cut Green Beans", "item-gv-manual")
				createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-kr-manual", "Kroger Cut Green Beans", "item-kr-manual")
				insertManualShoppingItem(env.T, env.DB, "sli-beans", "user-1", "item-kr-manual", 5)
			},
			httpExchange: httpExchange{
				method:         "GET",
				path:           "/api/shopping-list",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$[0].itemId", value: "item-kr-manual"},
					{path: "$[0].quantity", value: float64(5)},
					{path: "$[0].source", value: "manual"},
					{path: "$[1]", absent: true},
				},
			},
		},
		{
			name: "unlisted national brand is its own replenishment need",
			setup: func(env testEnv) {
				createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-delmonte", "Del Monte Cut Green Beans", "item-delmonte")
				createItemViaStockIn(env.T, env.DB, env.ProductStore, "user-1", "prod-kroger-nat", "Kroger Cut Green Beans", "item-kroger-nat")
				beginUsing(env.T, env.DB, time.Now())
				setSupplyQuantity(env.T, env.DB, "prod-delmonte", 4)
				setSupplyQuantity(env.T, env.DB, "prod-kroger-nat", 4)
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/shopping-list/fill",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$[0].itemId", value: "item-delmonte"},
					{path: "$[0].quantity", value: float64(3)},
					{path: "$[1].itemId", value: "item-kroger-nat"},
					{path: "$[1].quantity", value: float64(3)},
					{path: "$[2]", absent: true},
				},
			},
		},
	}

	runHandlerTests(t, tests)
}

// insertManualShoppingItem inserts a manual shopping list item directly into the DB.
func insertManualShoppingItem(t *testing.T, db *sql.DB, id, userID, itemID string, quantity int) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(),
		`INSERT INTO shopping_list_items (id, user_id, item_id, quantity, source) VALUES (?, ?, ?, ?, 'manual')`,
		id, userID, itemID, quantity,
	); err != nil {
		t.Fatalf("insertManualShoppingItem: %v", err)
	}
}

// setTargetQuantity sets the target_quantity for an item in the DB.
func setTargetQuantity(t *testing.T, db *sql.DB, itemID string, qty int) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(),
		`UPDATE items SET target_quantity = ? WHERE id = ?`, qty, itemID,
	); err != nil {
		t.Fatalf("setTargetQuantity: %v", err)
	}
}

func beginUsing(t *testing.T, db *sql.DB, started time.Time) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(),
		`INSERT INTO app_settings (key, value) VALUES ('onboarding_started_at', ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		started.UTC().Format(time.RFC3339),
	); err != nil {
		t.Fatalf("beginUsing: %v", err)
	}
}

func setSupplyQuantity(t *testing.T, db *sql.DB, productID string, qty int) {
	t.Helper()
	res, err := db.ExecContext(context.Background(),
		`INSERT INTO item_targets (household_id, item_id, kind, value)
		 SELECT (SELECT id FROM households WHERE is_primary = 1), i.id, 'amount', ?
		 FROM items i
		 WHERE i.product_id = ? AND i.user_id = 'user-1'
		 ON CONFLICT(household_id, item_id) DO UPDATE SET kind = 'amount', value = excluded.value`,
		qty, productID,
	)
	if err != nil {
		t.Fatalf("setSupplyQuantity: %v", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		t.Fatalf("setSupplyQuantity: no household item for %s", productID)
	}
}

func insertStockIn(t *testing.T, db *sql.DB, productID string, at time.Time) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(),
		`INSERT INTO stock_in_events (product_id, at) VALUES (?, ?)`,
		productID, at.UTC(),
	); err != nil {
		t.Fatalf("insertStockIn: %v", err)
	}
}
