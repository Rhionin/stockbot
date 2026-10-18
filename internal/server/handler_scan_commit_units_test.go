package server

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/scan"
)

// commitContract is the shared stock-out commit example frontend tests can read.
// body is the stable part of a 200. The handler also returns timestamps and the
// product summary; the test checks that every fixture field matches those bytes.
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

func (c commitContract) assertions() []assertion {
	out := make([]assertion, 0, len(c.Body))
	for key, value := range c.Body {
		out = append(out, assertion{path: "$." + key, value: value})
	}
	return out
}

func contractString(t *testing.T, body map[string]any, key string) string {
	t.Helper()
	value, ok := body[key].(string)
	if !ok || value == "" {
		t.Fatalf("contract %s = %#v", key, body[key])
	}
	return value
}

func contractCount(t *testing.T, body map[string]any, key string) int {
	t.Helper()
	value, ok := body[key].(float64)
	if !ok || value < 1 {
		t.Fatalf("contract %s = %#v", key, body[key])
	}
	return int(value)
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
				productID := contractString(env.T, partial.Body, "productId")
				mustProduct(env, productID, "Partial Oats")
				stockShelf(env, "item-partial", contractString(env.T, partial.Body, "userId"), productID, []shelfUnit{
					{"inst-partial", &soon},
				})
				direction := scan.StockOut
				product := productID
				if _, err := scan.NewQueue(env.DB).CreateScanEntry(context.Background(), scan.ScanEntry{
					ID:        contractString(env.T, partial.Body, "id"),
					UserID:    contractString(env.T, partial.Body, "userId"),
					Barcode:   contractString(env.T, partial.Body, "barcode"),
					ScannedAt: time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
					Direction: &direction,
					UnitCount: contractCount(env.T, partial.Body, "unitCount"),
					ProductID: &product,
					Status:    scan.Pending,
				}); err != nil {
					env.T.Fatalf("CreateScanEntry: %v", err)
				}
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans/" + contractString(t, partial.Body, "id") + "/commit",
				expectedStatus: partial.Status,
				assertions:     partial.assertions(),
			},
			afterRequest: exchanges(httpExchange{
				method:         "GET",
				path:           "/api/inventory",
				expectedStatus: http.StatusOK,
				assertions:     []assertion{{path: "$[0].instanceCount", value: float64(0)}},
			}),
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
	})
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
