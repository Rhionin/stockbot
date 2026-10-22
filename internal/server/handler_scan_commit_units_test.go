package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/scan"
)

// commitContract is a shared example frontend tests can read.
// body is the response with timestamps left out. The test builds its own
// request from the seeded shelf and compares the handler output to body.
type commitContract struct {
	Status int            `json:"status"`
	Body   map[string]any `json:"body"`
}

func loadCommitContract(t *testing.T, name string) commitContract {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate contract fixture")
	}
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "testdata", "contract", name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	var contract commitContract
	if err := json.Unmarshal(raw, &contract); err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	if contract.Status == 0 || len(contract.Body) == 0 {
		t.Fatalf("contract %s is missing a status or body", name)
	}
	return contract
}

// assertContractBody compares the handler JSON to the fixture body.
// scannedAt, committedAt, and createdAt are the only fields dropped.
func assertContractBody(t *testing.T, raw []byte, contract commitContract) {
	t.Helper()
	var actual map[string]any
	if err := json.Unmarshal(raw, &actual); err != nil {
		t.Fatalf("response JSON: %v\n%s", err, raw)
	}
	for _, key := range []string{"scannedAt", "committedAt", "createdAt"} {
		delete(actual, key)
	}
	if reflect.DeepEqual(actual, contract.Body) {
		return
	}
	got, err := json.Marshal(actual)
	if err != nil {
		t.Fatalf("encode actual: %v", err)
	}
	want, err := json.Marshal(contract.Body)
	if err != nil {
		t.Fatalf("encode contract: %v", err)
	}
	t.Fatalf("contract body\n got %s\nwant %s", got, want)
}

type shelfUnit struct {
	id      string
	expires *time.Time
}

func stockShelf(env testEnv, itemID, userID, productID string, units []shelfUnit) {
	env.T.Helper()
	if _, err := env.DB.ExecContext(context.Background(),
		`INSERT INTO items (id, user_id, product_id) VALUES (?, ?, ?)`,
		itemID, userID, productID); err != nil {
		env.T.Fatalf("create item: %v", err)
	}
	stocked := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	for _, unit := range units {
		var exp any
		if unit.expires != nil {
			exp = *unit.expires
		}
		if _, err := env.DB.ExecContext(context.Background(),
			`INSERT INTO item_instances (id, item_id, stock_in_at, expires_at) VALUES (?, ?, ?, ?)`,
			unit.id, itemID, stocked, exp); err != nil {
			env.T.Fatalf("create instance %s: %v", unit.id, err)
		}
	}
}

func TestStockOutRemovesUnitCount(t *testing.T) {
	partial := loadCommitContract(t, "scan-commit-partial.json")
	soon := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	mid := soon.Add(24 * time.Hour)
	late := soon.Add(48 * time.Hour)

	runHandlerTests(t, []handlerTestCase{
		{
			name: "three on hand and unit count 2 leaves one",
			setup: func(env testEnv) {
				mustProduct(env, "prod-three", "Three Oats")
				stockShelf(env, "item-three", "user-1", "prod-three", []shelfUnit{
					{"inst-three-old", &soon},
					{"inst-three-mid", &mid},
					{"inst-three-new", &late},
				})
				insertPendingScanUnits(env, "scan-three", "prod-three", scan.StockOut, 2)
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans/scan-three/commit",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.status", value: "committed"},
					{path: "$.unitCount", value: float64(2)},
					{path: "$.removedCount", value: float64(2)},
					{path: "$.requestedCount", value: float64(2)},
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
					path:           "/api/inventory/item-three/instances",
					expectedStatus: http.StatusOK,
					assertions: []assertion{
						{path: "$[0].id", value: "inst-three-new"},
						{path: "$[1]", absent: true},
					},
				},
			),
		},
		{
			name: "one on hand and unit count 3 reports how many left",
			setup: func(env testEnv) {
				mustProduct(env, "prod-partial", "Partial Oats")
				stockShelf(env, "item-partial", "user-1", "prod-partial", []shelfUnit{
					{"inst-partial", &soon},
				})
				insertPendingScanUnits(env, "scan-partial", "prod-partial", scan.StockOut, 3)
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans/scan-partial/commit",
				expectedStatus: http.StatusOK,
			},
			afterRequest: func(env testEnv) {
				raw, err := io.ReadAll(env.Res.Body)
				if err != nil {
					env.T.Fatalf("read commit: %v", err)
				}
				if env.Res.StatusCode != partial.Status {
					env.T.Fatalf("status %d, contract %d", env.Res.StatusCode, partial.Status)
				}
				assertContractBody(env.T, raw, partial)
				exchanges(httpExchange{
					method:         "GET",
					path:           "/api/inventory",
					expectedStatus: http.StatusOK,
					assertions:     []assertion{{path: "$[0].instanceCount", value: float64(0)}},
				})(env)
			},
		},
		{
			name: "nothing on hand stays a conflict",
			setup: func(env testEnv) {
				mustProduct(env, "prod-empty-out", "Empty Oats")
				stockShelf(env, "item-empty-out", "user-1", "prod-empty-out", nil)
				insertPendingScanUnits(env, "scan-empty-out", "prod-empty-out", scan.StockOut, 2)
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans/scan-empty-out/commit",
				expectedStatus: http.StatusConflict,
				assertions: []assertion{
					{path: "$.error", value: "Nothing on hand to take out."},
					{path: "$.code", value: "none_on_hand"},
					{path: "$.removedCount", absent: true},
					{path: "$.requestedCount", absent: true},
				},
			},
			afterRequest: exchanges(
				httpExchange{
					method:         "GET",
					path:           "/api/scans",
					query:          map[string]string{"userId": "user-1", "status": "pending"},
					expectedStatus: http.StatusOK,
					assertions: []assertion{
						{path: "$[0].id", value: "scan-empty-out"},
						{path: "$[0].status", value: "pending"},
						{path: "$[0].unitCount", value: float64(2)},
					},
				},
				httpExchange{
					method:         "GET",
					path:           "/api/inventory",
					expectedStatus: http.StatusOK,
					assertions:     []assertion{{path: "$[0].instanceCount", value: float64(0)}},
				},
			),
		},
		{
			name: "a named unit goes first and the oldest other unit follows",
			setup: func(env testEnv) {
				mustProduct(env, "prod-named", "Named Oats")
				stockShelf(env, "item-named", "user-1", "prod-named", []shelfUnit{
					{"inst-named-old", &soon},
					{"inst-named-mid", &mid},
					{"inst-named-pick", &late},
				})
				insertPendingScanUnits(env, "scan-named", "prod-named", scan.StockOut, 2)
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans/scan-named/commit",
				body:           `{"instanceId":"inst-named-pick"}`,
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.removedCount", value: float64(2)},
					{path: "$.requestedCount", value: float64(2)},
				},
			},
			afterRequest: exchanges(httpExchange{
				method:         "GET",
				path:           "/api/inventory/item-named/instances",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$[0].id", value: "inst-named-mid"},
					{path: "$[1]", absent: true},
				},
			}),
		},
		{
			name: "undoing the commit puts every removed unit back",
			setup: func(env testEnv) {
				mustProduct(env, "prod-undo", "Undo Oats")
				stockShelf(env, "item-undo", "user-1", "prod-undo", []shelfUnit{
					{"inst-undo-old", &soon},
					{"inst-undo-mid", &mid},
					{"inst-undo-new", &late},
				})
				insertPendingScanUnits(env, "scan-undo", "prod-undo", scan.StockOut, 2)
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans/scan-undo/commit",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.removedCount", value: float64(2)},
					{path: "$.requestedCount", value: float64(2)},
				},
			},
			afterRequest: func(env testEnv) {
				h := handlerFromEnv(env)
				if got := onHand(env, h); got != 1 {
					env.T.Fatalf("on hand after commit = %v", got)
				}
				view := productHistory(env, h, "item-undo")
				var move string
				for _, row := range view.Moves {
					if row.Direction == "out" && row.Quantity == 2 {
						move = row.ID
					}
				}
				if move == "" {
					env.T.Fatalf("no two-unit stock-out in %+v", view.Moves)
				}
				undoMove(env, h, move)
				if got := onHand(env, h); got != 3 {
					env.T.Fatalf("on hand after undo = %v", got)
				}
				exchanges(httpExchange{
					method:         "GET",
					path:           "/api/inventory/item-undo/instances",
					expectedStatus: http.StatusOK,
					bodyContains:   []string{"inst-undo-old", "inst-undo-mid", "inst-undo-new"},
				})(env)
			},
		},
		{
			name: "rescanning an open stock-out raises the count and waits to commit",
			setup: func(env testEnv) {
				setupProductWithBarcode("prod-rescan", "Rescan Oats", "Pantry", "5901234111287")(env)
				stockShelf(env, "item-rescan", "user-1", "prod-rescan", []shelfUnit{
					{"inst-rescan-old", &soon},
					{"inst-rescan-mid", &mid},
					{"inst-rescan-new", &late},
				})
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans",
				body:           `{"barcode":"5901234111287","userId":"user-1","direction":"stock_out"}`,
				expectedStatus: http.StatusCreated,
				assertions: []assertion{
					{path: "$.unitCount", value: float64(1)},
					{path: "$.status", value: "pending"},
				},
			},
			afterRequest: func(env testEnv) {
				h := handlerFromEnv(env)
				if got := onHand(env, h); got != 3 {
					env.T.Fatalf("on hand after the first scan = %v", got)
				}
				again := exchangeJSON(env.T, h, http.MethodPost, "/api/scans",
					`{"barcode":"5901234111287","userId":"user-1","direction":"stock_out"}`, http.StatusCreated)
				var merged struct {
					ID        string `json:"id"`
					UnitCount int    `json:"unitCount"`
					Status    string `json:"status"`
				}
				if err := json.Unmarshal(again, &merged); err != nil {
					env.T.Fatalf("rescan: %v", err)
				}
				if merged.UnitCount != 2 || merged.Status != "pending" || merged.ID == "" {
					env.T.Fatalf("rescan = %+v", merged)
				}
				if got := onHand(env, h); got != 3 {
					env.T.Fatalf("on hand before commit = %v", got)
				}
				committed := exchangeJSON(env.T, h, http.MethodPost, "/api/scans/"+merged.ID+"/commit", "", http.StatusOK)
				if !jsonContainsCounts(committed, 2, 2) {
					env.T.Fatalf("commit = %s", committed)
				}
				if got := onHand(env, h); got != 1 {
					env.T.Fatalf("on hand after commit = %v", got)
				}
				exchanges(httpExchange{
					method:         "GET",
					path:           "/api/inventory/item-rescan/instances",
					expectedStatus: http.StatusOK,
					assertions: []assertion{
						{path: "$[0].id", value: "inst-rescan-new"},
						{path: "$[1]", absent: true},
					},
				})(env)
			},
		},
		{
			name: "a named unit that is also the earliest is taken once",
			setup: func(env testEnv) {
				mustProduct(env, "prod-named-old", "Named Oldest")
				stockShelf(env, "item-named-old", "user-1", "prod-named-old", []shelfUnit{
					{"inst-named-oldest", &soon},
					{"inst-named-next", &mid},
					{"inst-named-rest", &late},
				})
				insertPendingScanUnits(env, "scan-named-old", "prod-named-old", scan.StockOut, 2)
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans/scan-named-old/commit",
				body:           `{"instanceId":"inst-named-oldest"}`,
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.removedCount", value: float64(2)},
					{path: "$.requestedCount", value: float64(2)},
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
					path:           "/api/inventory/item-named-old/instances",
					expectedStatus: http.StatusOK,
					assertions: []assertion{
						{path: "$[0].id", value: "inst-named-rest"},
						{path: "$[1]", absent: true},
					},
				},
			),
		},
		{
			name: "each removed unit has its own consumption row",
			setup: func(env testEnv) {
				finishOpening(env)
				mustProduct(env, "prod-usage", "Usage Oats")
				stockShelf(env, "item-usage", "user-1", "prod-usage", []shelfUnit{
					{"inst-usage-old", &soon},
					{"inst-usage-new", &late},
				})
				insertPendingScanUnits(env, "scan-usage", "prod-usage", scan.StockOut, 2)
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans/scan-usage/commit",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.removedCount", value: float64(2)},
					{path: "$.requestedCount", value: float64(2)},
				},
			},
			afterRequest: func(env testEnv) {
				rows, err := env.DB.QueryContext(context.Background(), `
					SELECT COALESCE(scan_entry_id, ''), COALESCE(stock_move_id, '')
					FROM consumption_events
					WHERE item_id = 'item-usage'
					ORDER BY id`)
				if err != nil {
					env.T.Fatalf("consumption rows: %v", err)
				}
				defer rows.Close()
				var moveID string
				count := 0
				for rows.Next() {
					var scanID, linked string
					if err := rows.Scan(&scanID, &linked); err != nil {
						env.T.Fatalf("scan consumption: %v", err)
					}
					if scanID != "scan-usage" || linked == "" {
						env.T.Fatalf("consumption row scan=%q move=%q", scanID, linked)
					}
					if moveID == "" {
						moveID = linked
					} else if linked != moveID {
						env.T.Fatalf("consumption rows point at %s and %s", moveID, linked)
					}
					count++
				}
				if err := rows.Err(); err != nil {
					env.T.Fatalf("consumption rows: %v", err)
				}
				if count != 2 {
					env.T.Fatalf("consumption rows = %d, want 2", count)
				}
				var quantity, moves int
				if err := env.DB.QueryRowContext(context.Background(), `
					SELECT quantity FROM stock_moves WHERE id = ?`, moveID).Scan(&quantity); err != nil {
					env.T.Fatalf("stock move: %v", err)
				}
				if err := env.DB.QueryRowContext(context.Background(), `
					SELECT COUNT(*) FROM stock_moves WHERE item_id = 'item-usage'`).Scan(&moves); err != nil {
					env.T.Fatalf("count moves: %v", err)
				}
				if quantity != 2 || moves != 1 {
					env.T.Fatalf("move quantity %d, moves %d", quantity, moves)
				}
			},
		},
		{
			name: "the same expiry takes the oldest stocked unit and leaves an undated one",
			setup: func(env testEnv) {
				mustProduct(env, "prod-tie-stock", "Tie Stock")
				when := soon
				// The earlier-acquired dated unit has the larger id, and the
				// later-acquired one is inserted first. Dropping the
				// acquired-at sort would remove inst-dated-a-late by id.
				stockShelf(env, "item-tie-stock", "user-1", "prod-tie-stock", []shelfUnit{
					{"inst-undated-early", nil},
					{"inst-dated-a-late", &when},
					{"inst-dated-z-early", &when},
				})
				setStockedAt(env, "inst-undated-early", time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC))
				setStockedAt(env, "inst-dated-a-late", time.Date(2026, 5, 3, 0, 0, 0, 0, time.UTC))
				setStockedAt(env, "inst-dated-z-early", time.Date(2026, 5, 2, 0, 0, 0, 0, time.UTC))
				insertPendingScanUnits(env, "scan-tie-stock", "prod-tie-stock", scan.StockOut, 1)
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans/scan-tie-stock/commit",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.removedCount", value: float64(1)},
					{path: "$.requestedCount", value: float64(1)},
				},
			},
			afterRequest: exchanges(httpExchange{
				method:         "GET",
				path:           "/api/inventory/item-tie-stock/instances",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$[0].id", value: "inst-dated-a-late"},
					{path: "$[1].id", value: "inst-undated-early"},
					{path: "$[2]", absent: true},
				},
			}),
		},
		{
			name: "undated units go last and the earliest acquired undated goes first",
			setup: func(env testEnv) {
				mustProduct(env, "prod-undated", "Undated Oats")
				when := soon
				// Latest acquired is inserted first, so row order is not acquisition order.
				stockShelf(env, "item-undated", "user-1", "prod-undated", []shelfUnit{
					{"inst-undated-late", nil},
					{"inst-undated-mid", nil},
					{"inst-dated-last-stocked", &when},
					{"inst-undated-early", nil},
				})
				setStockedAt(env, "inst-undated-late", time.Date(2026, 5, 5, 0, 0, 0, 0, time.UTC))
				setStockedAt(env, "inst-undated-mid", time.Date(2026, 5, 4, 0, 0, 0, 0, time.UTC))
				setStockedAt(env, "inst-dated-last-stocked", time.Date(2026, 5, 10, 0, 0, 0, 0, time.UTC))
				setStockedAt(env, "inst-undated-early", time.Date(2026, 5, 3, 0, 0, 0, 0, time.UTC))
				insertPendingScanUnits(env, "scan-undated", "prod-undated", scan.StockOut, 3)
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans/scan-undated/commit",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.removedCount", value: float64(3)},
					{path: "$.requestedCount", value: float64(3)},
				},
			},
			afterRequest: exchanges(httpExchange{
				method:         "GET",
				path:           "/api/inventory/item-undated/instances",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$[0].id", value: "inst-undated-late"},
					{path: "$[1]", absent: true},
				},
				bodyExcludes: []string{"inst-undated-early", "inst-undated-mid", "inst-dated-last-stocked"},
			}),
		},
		{
			name: "the same stock-in time breaks by id",
			setup: func(env testEnv) {
				mustProduct(env, "prod-tie-id", "Tie Id")
				when := soon
				stockShelf(env, "item-tie-id", "user-1", "prod-tie-id", []shelfUnit{
					{"inst-z-later", &when},
					{"inst-a-first", &when},
				})
				insertPendingScanUnits(env, "scan-tie-id", "prod-tie-id", scan.StockOut, 1)
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans/scan-tie-id/commit",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.removedCount", value: float64(1)},
					{path: "$.requestedCount", value: float64(1)},
				},
			},
			afterRequest: exchanges(httpExchange{
				method:         "GET",
				path:           "/api/inventory/item-tie-id/instances",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$[0].id", value: "inst-z-later"},
					{path: "$[1]", absent: true},
				},
			}),
		},
	})
}

func setStockedAt(env testEnv, id string, at time.Time) {
	env.T.Helper()
	if _, err := env.DB.ExecContext(context.Background(),
		`UPDATE item_instances SET stock_in_at = ? WHERE id = ?`, at, id); err != nil {
		env.T.Fatalf("stock_in_at %s: %v", id, err)
	}
}

func jsonContainsCounts(body []byte, removed, requested int) bool {
	var payload struct {
		RemovedCount   int `json:"removedCount"`
		RequestedCount int `json:"requestedCount"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return false
	}
	return payload.RemovedCount == removed && payload.RequestedCount == requested
}
