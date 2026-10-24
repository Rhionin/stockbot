package server

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/scan"
)

func TestHistoryHandler(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	day := 24 * time.Hour

	tests := []handlerTestCase{
		{
			name: "missing product",
			httpExchange: httpExchange{
				method:         "GET",
				path:           "/api/items/missing/history",
				expectedStatus: http.StatusNotFound,
				assertions: []assertion{
					{path: "$.error", value: "That product was not found."},
				},
			},
		},
		{
			name: "missing group",
			httpExchange: httpExchange{
				method:         "GET",
				path:           "/api/groups/missing/history",
				expectedStatus: http.StatusNotFound,
				assertions: []assertion{
					{path: "$.error", value: "That group was not found."},
				},
			},
		},
		{
			name: "missing move",
			httpExchange: httpExchange{
				method:         "PATCH",
				path:           "/api/moves/missing",
				body:           `{"quantity":2}`,
				expectedStatus: http.StatusNotFound,
				assertions: []assertion{
					{path: "$.error", value: "That move is no longer in the history."},
				},
			},
		},
		{
			name: "quantity below 1 asks for undo",
			setup: func(env testEnv) {
				finishOpening(env)
				seedShelfItem(env, "prod-beans", "Black Beans", "item-beans")
				insertPendingScanUnits(env, "scan-beans", "prod-beans", scan.StockIn, 2)
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans/scan-beans/commit",
				expectedStatus: http.StatusOK,
			},
			afterRequest: func(env testEnv) {
				id := historyMoveID(env, "in")
				exchanges(httpExchange{
					method:         "PATCH",
					path:           "/api/moves/" + id,
					body:           `{"quantity":0}`,
					expectedStatus: http.StatusBadRequest,
					assertions: []assertion{
						{path: "$.error", value: "Quantity has to be at least 1. Undo the move to remove it."},
					},
				})(env)
			},
		},
		{
			name: "pace and trail for a product, then a corrected quantity",
			setup: func(env testEnv) {
				finishOpening(env)
				seedShelfItem(env, "prod-pace", "Black Beans", "item-pace")
				if _, err := env.DB.Exec(`
					UPDATE products
					SET unit_of_measure = 'cans', net_base_value = ?, net_dimension = 'mass'
					WHERE id = 'prod-pace'`, 15*28.349523125); err != nil {
					env.T.Fatalf("size: %v", err)
				}
				stocked := now.Add(-25 * day)
				for i, removed := range []bool{true, true, true, true, false, false} {
					removedAt := any(nil)
					reason := any(nil)
					if removed {
						removedAt = stocked
						reason = "consumed"
					}
					if _, err := env.DB.Exec(`
						INSERT INTO item_instances (id, item_id, stock_in_at, removed_at, removal_reason)
						VALUES (?, 'item-pace', ?, ?, ?)`,
						"inst-"+string(rune('a'+i)), stocked, removedAt, reason); err != nil {
						env.T.Fatalf("instance: %v", err)
					}
				}
				uses := []time.Time{
					now.Add(-50 * day), now.Add(-40 * day),
					now.Add(-20 * day), now.Add(-14 * day), now.Add(-8 * day), now.Add(-2 * day),
				}
				for i, at := range uses {
					if _, err := env.DB.Exec(`
						INSERT INTO consumption_events (id, item_id, consumed_at)
						VALUES (?, 'item-pace', ?)`, "use-"+string(rune('a'+i)), at); err != nil {
						env.T.Fatalf("use: %v", err)
					}
				}
			},
			httpExchange: httpExchange{
				method:         "GET",
				path:           "/api/items/item-pace/history",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.kind", value: "product"},
					{path: "$.name", value: "Black Beans"},
					{path: "$.detail", value: "15 oz cans"},
					{path: "$.onHand", value: float64(2)},
					{path: "$.pace.known", value: true},
					{path: "$.pace.daysBetweenUses", value: float64(5)},
					{path: "$.pace.daysLeft", value: float64(10)},
					{path: "$.pace.trend", value: "faster"},
					{path: "$.moves[0].direction", value: "out"},
					{path: "$.moves[0].quantity", value: float64(1)},
					{path: "$.moves[0].productName", value: "Black Beans"},
				},
			},
			afterRequest: func(env testEnv) {
				// Raise the newest use from 1 to 2. One more unit leaves the shelf
				// and the pace shortens.
				id := historyMoveID(env, "out")
				exchanges(
					httpExchange{
						method:         "PATCH",
						path:           "/api/moves/" + id,
						body:           `{"quantity":2}`,
						expectedStatus: http.StatusOK,
						assertions: []assertion{
							{path: "$.onHand", value: float64(1)},
							{path: "$.pace.known", value: true},
							{path: "$.moves[0].quantity", value: float64(2)},
						},
					},
					httpExchange{
						method:         "PATCH",
						path:           "/api/moves/" + id,
						body:           `{"quantity":5}`,
						expectedStatus: http.StatusConflict,
						assertions: []assertion{
							{path: "$.error", value: "Only 1 is on the shelf, so this can't be 5."},
						},
					},
					httpExchange{
						method:         "PATCH",
						path:           "/api/moves/" + id,
						body:           `{"quantity":1}`,
						expectedStatus: http.StatusOK,
						assertions: []assertion{
							{path: "$.onHand", value: float64(2)},
							{path: "$.moves[0].quantity", value: float64(1)},
							{path: "$.pace.daysBetweenUses", value: float64(5)},
						},
					},
				)(env)
			},
		},
		{
			name: "edit and undo a stock in, and refuse when a unit was already used",
			setup: func(env testEnv) {
				finishOpening(env)
				seedShelfItem(env, "prod-in", "Oats", "item-in")
				insertPendingScanUnits(env, "scan-in", "prod-in", scan.StockIn, 3)
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans/scan-in/commit",
				expectedStatus: http.StatusOK,
			},
			afterRequest: func(env testEnv) {
				inID := historyMoveID(env, "in")
				exchanges(
					httpExchange{
						method:         "GET",
						path:           "/api/items/item-in/history",
						expectedStatus: http.StatusOK,
						assertions: []assertion{
							{path: "$.onHand", value: float64(3)},
							{path: "$.moves[0].direction", value: "in"},
							{path: "$.moves[0].quantity", value: float64(3)},
							{path: "$.moves[0].source", value: "scan"},
							{path: "$.pace.known", value: false},
						},
					},
					httpExchange{
						method:         "PATCH",
						path:           "/api/moves/" + inID,
						body:           `{"quantity":4}`,
						expectedStatus: http.StatusOK,
						assertions:     []assertion{{path: "$.onHand", value: float64(4)}},
					},
					httpExchange{
						method:         "POST",
						path:           "/api/inventory/item-in/stock-out",
						expectedStatus: http.StatusOK,
					},
					httpExchange{
						method:         "DELETE",
						path:           "/api/moves/" + inID,
						expectedStatus: http.StatusConflict,
						assertions: []assertion{
							{path: "$.error", value: "1 of these was already used. Undo that use first."},
						},
					},
				)(env)
				outID := historyMoveID(env, "out")
				exchanges(
					httpExchange{
						method:         "DELETE",
						path:           "/api/moves/" + outID,
						expectedStatus: http.StatusOK,
						assertions:     []assertion{{path: "$.onHand", value: float64(4)}},
					},
					httpExchange{
						method:         "DELETE",
						path:           "/api/moves/" + inID,
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
						assertions:     []assertion{{path: "$[0].instanceCount", value: float64(0)}},
					},
				)(env)
			},
		},
		{
			name: "a group adds its products together",
			setup: func(env testEnv) {
				finishOpening(env)
				seedShelfItem(env, "prod-kidney", "Kidney Beans", "item-kidney")
				seedShelfItem(env, "prod-black", "Black Beans", "item-black")
				if _, err := env.DB.Exec(`
					UPDATE products SET unit_of_measure = 'cans', net_base_value = ?, net_dimension = 'mass'
					WHERE id IN ('prod-kidney', 'prod-black')`, 15*28.349523125); err != nil {
					env.T.Fatalf("size: %v", err)
				}
				if _, err := env.DB.Exec(`
					INSERT INTO product_groups (id, user_id, name, name_key, rule, rule_confirmed)
					VALUES ('group-beans', 'user-1', 'Beans', 'beans', 'same_as_ran_out', 1)`); err != nil {
					env.T.Fatalf("group: %v", err)
				}
				if _, err := env.DB.Exec(`
					INSERT INTO product_group_members (product_id, group_id) VALUES
					('prod-kidney', 'group-beans'), ('prod-black', 'group-beans')`); err != nil {
					env.T.Fatalf("members: %v", err)
				}
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/inventory/item-kidney/instances",
				body:           `{}`,
				expectedStatus: http.StatusCreated,
			},
			afterRequest: func(env testEnv) {
				exchanges(
					httpExchange{
						method:         "POST",
						path:           "/api/inventory/item-black/instances",
						body:           `{}`,
						expectedStatus: http.StatusCreated,
					},
					httpExchange{
						method:         "POST",
						path:           "/api/inventory/item-kidney/stock-out",
						expectedStatus: http.StatusOK,
					},
					httpExchange{
						method:         "GET",
						path:           "/api/groups/group-beans/history",
						expectedStatus: http.StatusOK,
						bodyContains:   []string{"Kidney Beans", "Black Beans"},
						assertions: []assertion{
							{path: "$.kind", value: "group"},
							{path: "$.name", value: "Beans"},
							{path: "$.detail", value: "15 oz cans"},
							{path: "$.onHand", value: float64(1)},
							{path: "$.moves[0].direction", value: "out"},
						},
					},
				)(env)
			},
		},
		{
			name: "empty household history",
			httpExchange: httpExchange{
				method:         "GET",
				path:           "/api/history",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.moves", value: []any{}},
					{path: "$.setupAt", absent: true},
				},
			},
		},
		{
			name: "unknown direction",
			httpExchange: httpExchange{
				method:         "GET",
				path:           "/api/history",
				query:          map[string]string{"direction": "sideways"},
				expectedStatus: http.StatusBadRequest,
				assertions: []assertion{
					{path: "$.error", value: "Choose in, out, or all."},
				},
			},
		},
		{
			name: "household history is newest first and can be filtered",
			setup: func(env testEnv) {
				finishOpening(env)
				seedShelfItem(env, "prod-oats", "Oats", "item-oats")
				seedShelfItem(env, "prod-peas", "Chickpeas", "item-peas")
				seedShelfItem(env, "prod-milk", "Milk", "item-milk")
				seedShelfItem(env, "prod-salsa", "Secret Salsa", "item-salsa")
				if _, err := env.DB.Exec(`UPDATE items SET user_id = 'user-2' WHERE id = 'item-salsa'`); err != nil {
					env.T.Fatalf("other household: %v", err)
				}
				if _, err := env.DB.Exec(`UPDATE products SET image_url = ? WHERE id = 'prod-oats'`, "https://images.example.com/oats.jpg"); err != nil {
					env.T.Fatalf("photo: %v", err)
				}
				if _, err := env.DB.Exec(`UPDATE products SET image_url = ? WHERE id = 'prod-milk'`, "http://127.0.0.1/secret.jpg"); err != nil {
					env.T.Fatalf("private photo: %v", err)
				}
				if _, err := env.DB.Exec(`
					INSERT INTO product_groups (id, user_id, name, name_key, rule, rule_confirmed)
					VALUES ('group-legumes', 'user-1', 'Legumes', 'legumes', 'same_as_ran_out', 1)`); err != nil {
					env.T.Fatalf("group: %v", err)
				}
				if _, err := env.DB.Exec(`
					INSERT INTO product_group_members (product_id, group_id) VALUES ('prod-peas', 'group-legumes')`); err != nil {
					env.T.Fatalf("member: %v", err)
				}
				insertStockMove(env, "move-oats", "item-oats", "prod-oats", "in", 4, time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC), "manual")
				insertStockMove(env, "move-peas", "item-peas", "prod-peas", "out", 1, time.Date(2026, 10, 13, 18, 0, 0, 0, time.UTC), "scan")
				insertStockMove(env, "move-milk", "item-milk", "prod-milk", "in", 2, time.Date(2026, 10, 14, 15, 0, 0, 0, time.UTC), "scan")
				insertStockMove(env, "move-salsa", "item-salsa", "prod-salsa", "in", 1, time.Date(2026, 10, 14, 16, 0, 0, 0, time.UTC), "manual")
			},
			httpExchange: httpExchange{
				method:         "GET",
				path:           "/api/history",
				expectedStatus: http.StatusOK,
				bodyExcludes:   []string{"Secret Salsa", "127.0.0.1"},
				assertions: []assertion{
					{path: "$.moves[0].productName", value: "Milk"},
					{path: "$.moves[0].direction", value: "in"},
					{path: "$.moves[0].quantity", value: float64(2)},
					{path: "$.moves[0].at", value: "2026-10-14T15:00:00Z"},
					{path: "$.moves[0].source", value: "scan"},
					{path: "$.moves[0].imageUrl", absent: true},
					{path: "$.moves[1].productName", value: "Chickpeas"},
					{path: "$.moves[1].direction", value: "out"},
					{path: "$.moves[1].quantity", value: float64(1)},
					{path: "$.moves[1].groupName", value: "Legumes"},
					{path: "$.moves[1].groupId", value: "group-legumes"},
					{path: "$.moves[2].productName", value: "Oats"},
					{path: "$.moves[2].imageUrl", value: "https://images.example.com/oats.jpg"},
					{path: "$.moves[2].source", value: "manual"},
					{path: "$.setupAt", value: "2026-01-01T00:00:00Z"},
				},
			},
			afterRequest: exchanges(
				httpExchange{
					method:         "GET",
					path:           "/api/history",
					query:          map[string]string{"direction": "OUT"},
					expectedStatus: http.StatusOK,
					assertions: []assertion{
						{path: "$.moves[0].productName", value: "Chickpeas"},
						{path: "$.moves[1]", absent: true},
						{path: "$.setupAt", value: "2026-01-01T00:00:00Z"},
					},
				},
				httpExchange{
					method:         "GET",
					path:           "/api/history",
					query:          map[string]string{"direction": "in"},
					expectedStatus: http.StatusOK,
					assertions: []assertion{
						{path: "$.moves[0].productName", value: "Milk"},
						{path: "$.moves[1].productName", value: "Oats"},
						{path: "$.moves[2]", absent: true},
					},
				},
				httpExchange{
					method:         "GET",
					path:           "/api/history",
					query:          map[string]string{"q": "legume"},
					expectedStatus: http.StatusOK,
					assertions: []assertion{
						{path: "$.moves[0].productName", value: "Chickpeas"},
						{path: "$.moves[0].groupName", value: "Legumes"},
						{path: "$.moves[1]", absent: true},
					},
				},
				httpExchange{
					method:         "GET",
					path:           "/api/history",
					query:          map[string]string{"q": "OATS"},
					expectedStatus: http.StatusOK,
					assertions: []assertion{
						{path: "$.moves[0].productName", value: "Oats"},
						{path: "$.moves[1]", absent: true},
					},
				},
				httpExchange{
					method:         "GET",
					path:           "/api/history",
					query:          map[string]string{"direction": "all", "q": "%"},
					expectedStatus: http.StatusOK,
					assertions: []assertion{
						{path: "$.moves", value: []any{}},
					},
				},
			),
		},
		{
			name: "editing and undoing a move updates the household history",
			setup: func(env testEnv) {
				finishOpening(env)
				seedShelfItem(env, "prod-ledger", "Rolled Oats", "item-ledger")
				insertPendingScanUnits(env, "scan-ledger", "prod-ledger", scan.StockIn, 2)
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans/scan-ledger/commit",
				expectedStatus: http.StatusOK,
			},
			afterRequest: func(env testEnv) {
				id := historyMoveID(env, "in")
				exchanges(
					httpExchange{
						method:         "GET",
						path:           "/api/history",
						expectedStatus: http.StatusOK,
						assertions: []assertion{
							{path: "$.moves[0].id", value: id},
							{path: "$.moves[0].productName", value: "Rolled Oats"},
							{path: "$.moves[0].direction", value: "in"},
							{path: "$.moves[0].quantity", value: float64(2)},
							{path: "$.moves[0].source", value: "scan"},
						},
					},
					httpExchange{
						method:         "PATCH",
						path:           "/api/moves/" + id,
						body:           `{"quantity":4}`,
						expectedStatus: http.StatusOK,
						assertions:     []assertion{{path: "$.onHand", value: float64(4)}},
					},
					httpExchange{
						method:         "GET",
						path:           "/api/history",
						expectedStatus: http.StatusOK,
						assertions: []assertion{
							{path: "$.moves[0].quantity", value: float64(4)},
						},
					},
					httpExchange{
						method:         "GET",
						path:           "/api/inventory",
						expectedStatus: http.StatusOK,
						assertions:     []assertion{{path: "$[0].instanceCount", value: float64(4)}},
					},
					httpExchange{
						method:         "DELETE",
						path:           "/api/moves/" + id,
						expectedStatus: http.StatusOK,
					},
					httpExchange{
						method:         "GET",
						path:           "/api/history",
						expectedStatus: http.StatusOK,
						assertions:     []assertion{{path: "$.moves", value: []any{}}},
					},
					httpExchange{
						method:         "GET",
						path:           "/api/inventory",
						expectedStatus: http.StatusOK,
						assertions:     []assertion{{path: "$[0].instanceCount", value: float64(0)}},
					},
				)(env)
			},
		},
	}

	runHandlerTests(t, tests)
}

func insertPendingScanUnits(env testEnv, id, productID string, direction scan.ScanDirection, units int) {
	env.T.Helper()
	queue := scan.NewQueue(env.DB)
	if _, err := queue.CreateScanEntry(context.Background(), primaryScope(env.T, env.DB), scan.ScanEntry{
		ID:        id,
		UserID:    "user-1",
		Barcode:   id,
		ScannedAt: time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
		Direction: &direction,
		UnitCount: units,
		ProductID: &productID,
		Status:    scan.Pending,
	}); err != nil {
		env.T.Fatalf("CreateScanEntry: %v", err)
	}
}

func insertStockMove(env testEnv, id, itemID, productID, direction string, quantity int, at time.Time, source string) {
	env.T.Helper()
	if _, err := env.DB.Exec(`
		INSERT INTO stock_moves (id, item_id, product_id, direction, quantity, at, source)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, itemID, productID, direction, quantity, at, source); err != nil {
		env.T.Fatalf("stock move: %v", err)
	}
}

func historyMoveID(env testEnv, direction string) string {
	env.T.Helper()
	var id string
	err := env.DB.QueryRow(`
		SELECT id FROM stock_moves WHERE direction = ? ORDER BY at DESC, id DESC LIMIT 1`, direction).Scan(&id)
	if err != nil {
		env.T.Fatalf("move id %s: %v", direction, err)
	}
	return id
}
