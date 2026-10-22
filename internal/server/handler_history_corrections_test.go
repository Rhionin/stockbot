package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/scan"
)

func TestHistoryCorrectionsReadBack(t *testing.T) {
	const oz = 28.349523125
	runHandlerTests(t, []handlerTestCase{
		{
			name: "changing and undoing moves updates the trail and the shelf",
			setup: func(env testEnv) {
				finishOpening(env)
				seedShelfItem(env, "prod-oats", "Rolled Oats", "item-oats")
				if _, err := env.DB.Exec(`
					UPDATE products
					SET unit_of_measure = 'cans', net_base_value = ?, net_dimension = 'mass'
					WHERE id = 'prod-oats'`, 14.5*oz); err != nil {
					env.T.Fatalf("size: %v", err)
				}
				seedScanAt(env, "scan-in", "prod-oats", scan.StockIn, 3, time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC))
				seedScanAt(env, "scan-out-a", "prod-oats", scan.StockOut, 1, time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC))
				seedScanAt(env, "scan-out-b", "prod-oats", scan.StockOut, 1, time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC))
				seedScanAt(env, "scan-out-c", "prod-oats", scan.StockOut, 1, time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC))
			},
			httpExchange: httpExchange{
				method: "POST", path: "/api/scans/scan-in/commit", expectedStatus: http.StatusOK,
			},
			afterRequest: func(env testEnv) {
				h := handlerFromEnv(env)
				commitScan(env, h, "scan-out-a")
				view := productHistory(env, h, "item-oats")
				if view.Detail != "14.5 oz cans" || view.OnHand != 2 {
					env.T.Fatalf("opened history = %+v", view)
				}
				outID, inID := moveID(view.Moves, "out"), moveID(view.Moves, "in")
				if moveQty(view.Moves, outID) != 1 || moveQty(view.Moves, inID) != 3 {
					env.T.Fatalf("moves = %+v", view.Moves)
				}

				patchMove(env, h, outID, 4, http.StatusConflict, "Only 2 are on the shelf, so this can't be 4.")
				if got := onHand(env, h); got != 2 {
					env.T.Fatalf("on hand after refused grow = %v", got)
				}
				patchMove(env, h, outID, 2, http.StatusOK, "")
				if got := onHand(env, h); got != 1 {
					env.T.Fatalf("on hand after growing the use = %v", got)
				}
				patchMove(env, h, outID, 1, http.StatusOK, "")
				undoMove(env, h, outID)
				if got := onHand(env, h); got != 3 {
					env.T.Fatalf("on hand after undo = %v", got)
				}
				patchMove(env, h, inID, 1000, http.StatusBadRequest, "Quantity has to be 999 or less.")
				if got := onHand(env, h); got != 3 {
					env.T.Fatalf("on hand after refused quantity = %v", got)
				}

				commitScan(env, h, "scan-out-b")
				commitScan(env, h, "scan-out-c")
				if got := onHand(env, h); got != 1 {
					env.T.Fatalf("on hand after two uses = %v", got)
				}
				patchMove(env, h, inID, 1, http.StatusConflict, "2 of these were already used. Undo those uses first.")
				undoMoveExpect(env, h, inID, http.StatusConflict, "2 of these were already used. Undo those uses first.")
				if got := onHand(env, h); got != 1 {
					env.T.Fatalf("on hand after refused undo = %v", got)
				}
				latest := productHistory(env, h, "item-oats")
				if moveQty(latest.Moves, inID) != 3 {
					env.T.Fatalf("stock in quantity changed: %+v", latest.Moves)
				}
			},
		},
		{
			name: "one used unit blocks undoing the stock in and growing the use",
			setup: func(env testEnv) {
				finishOpening(env)
				seedShelfItem(env, "prod-one", "One Can", "item-one")
				seedScanAt(env, "scan-one-in", "prod-one", scan.StockIn, 2, time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC))
				seedScanAt(env, "scan-one-out", "prod-one", scan.StockOut, 1, time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC))
			},
			httpExchange: httpExchange{
				method: "POST", path: "/api/scans/scan-one-in/commit", expectedStatus: http.StatusOK,
			},
			afterRequest: func(env testEnv) {
				h := handlerFromEnv(env)
				commitScan(env, h, "scan-one-out")
				view := productHistory(env, h, "item-one")
				undoMoveExpect(env, h, moveID(view.Moves, "in"), http.StatusConflict, "1 of these was already used. Undo that use first.")
				patchMove(env, h, moveID(view.Moves, "out"), 3, http.StatusConflict, "Only 1 is on the shelf, so this can't be 3.")
				if got := onHand(env, h); got != 1 {
					env.T.Fatalf("on hand = %v", got)
				}
			},
		},
		{
			name: "older shelf rows and a bad opening stamp are read back",
			setup: func(env testEnv) {
				seedShelfItem(env, "prod-old", "Old Beans", "item-old")
				seedShelfItem(env, "prod-bare-a", "Plain A", "item-bare-a")
				seedShelfItem(env, "prod-bare-b", "Plain B", "item-bare-b")
				t1 := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
				t2 := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
				t3 := time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC)
				mustInsertInstance(env, "old-a", "item-old", t1, nil, "")
				mustInsertInstance(env, "old-b", "item-old", t1, &t3, "manual")
				mustInsertInstance(env, "old-c", "item-old", t2, nil, "")
				if _, err := env.DB.Exec(`INSERT INTO app_settings (key, value) VALUES ('onboarding_started_at', 'yesterday')`); err != nil {
					env.T.Fatalf("stamp: %v", err)
				}
				if _, err := env.DB.Exec(`
					INSERT INTO product_groups (id, user_id, name, name_key, rule, rule_confirmed) VALUES
					('group-empty', 'user-1', 'Empty', 'empty', 'same_as_ran_out', 1),
					('group-plain', 'user-1', 'Plain', 'plain', 'same_as_ran_out', 1)`); err != nil {
					env.T.Fatalf("groups: %v", err)
				}
				if _, err := env.DB.Exec(`
					INSERT INTO product_group_members (product_id, group_id) VALUES
					('prod-bare-a', 'group-plain'), ('prod-bare-b', 'group-plain')`); err != nil {
					env.T.Fatalf("members: %v", err)
				}
			},
			httpExchange: httpExchange{
				method:         "GET",
				path:           "/api/history",
				query:          map[string]string{"direction": " in "},
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.setupAt", absent: true},
					{path: "$.moves[0].direction", value: "in"},
					{path: "$.moves[0].quantity", value: float64(1)},
					{path: "$.moves[1].direction", value: "in"},
					{path: "$.moves[1].quantity", value: float64(2)},
					{path: "$.moves[2]", absent: true},
				},
			},
			afterRequest: exchanges(
				httpExchange{
					method:         "GET",
					path:           "/api/history",
					expectedStatus: http.StatusOK,
					assertions: []assertion{
						{path: "$.moves[0].direction", value: "out"},
						{path: "$.moves[0].quantity", value: float64(1)},
						{path: "$.moves[0].source", value: "manual"},
						{path: "$.moves[1].productName", value: "Old Beans"},
						{path: "$.setupAt", absent: true},
					},
				},
				httpExchange{
					method:         "GET",
					path:           "/api/groups/group-empty/history",
					expectedStatus: http.StatusOK,
					assertions: []assertion{
						{path: "$.kind", value: "group"},
						{path: "$.onHand", value: float64(0)},
						{path: "$.detail", absent: true},
						{path: "$.moves", value: []any{}},
					},
				},
				httpExchange{
					method:         "GET",
					path:           "/api/groups/group-plain/history",
					expectedStatus: http.StatusOK,
					assertions: []assertion{
						{path: "$.detail", value: "2 products"},
						{path: "$.name", value: "Plain"},
					},
				},
			),
		},
	})
}

func seedScanAt(env testEnv, id, productID string, direction scan.ScanDirection, units int, at time.Time) {
	env.T.Helper()
	if _, err := scan.NewQueue(env.DB).CreateScanEntry(env.T.Context(), scan.ScanEntry{
		ID: id, UserID: "user-1", Barcode: id, ScannedAt: at,
		Direction: &direction, UnitCount: units, ProductID: &productID, Status: scan.Pending,
	}); err != nil {
		env.T.Fatalf("scan %s: %v", id, err)
	}
}

func mustInsertInstance(env testEnv, id, itemID string, at time.Time, removed *time.Time, reason string) {
	env.T.Helper()
	var removedAt, removal any
	if removed != nil {
		removedAt = *removed
		removal = reason
	}
	if _, err := env.DB.Exec(`
		INSERT INTO item_instances (id, item_id, stock_in_at, removed_at, removal_reason)
		VALUES (?, ?, ?, ?, ?)`, id, itemID, at, removedAt, removal); err != nil {
		env.T.Fatalf("instance %s: %v", id, err)
	}
}

type historyView struct {
	Detail string        `json:"detail"`
	OnHand float64       `json:"onHand"`
	Moves  []historyMove `json:"moves"`
}

type historyMove struct {
	ID        string  `json:"id"`
	Direction string  `json:"direction"`
	Quantity  float64 `json:"quantity"`
}

func productHistory(env testEnv, h http.Handler, itemID string) historyView {
	env.T.Helper()
	body := exchangeJSON(env.T, h, http.MethodGet, "/api/items/"+itemID+"/history", "", http.StatusOK)
	var view historyView
	if err := json.Unmarshal(body, &view); err != nil {
		env.T.Fatalf("history: %v", err)
	}
	return view
}

func onHand(env testEnv, h http.Handler) float64 {
	env.T.Helper()
	body := exchangeJSON(env.T, h, http.MethodGet, "/api/inventory", "", http.StatusOK)
	var rows []struct {
		InstanceCount float64 `json:"instanceCount"`
	}
	if err := json.Unmarshal(body, &rows); err != nil || len(rows) != 1 {
		env.T.Fatalf("inventory %s: %v", body, err)
	}
	return rows[0].InstanceCount
}

func moveID(moves []historyMove, direction string) string {
	for _, move := range moves {
		if move.Direction == direction {
			return move.ID
		}
	}
	return ""
}

func moveQty(moves []historyMove, id string) float64 {
	for _, move := range moves {
		if move.ID == id {
			return move.Quantity
		}
	}
	return -1
}

func commitScan(env testEnv, h http.Handler, id string) {
	env.T.Helper()
	exchangeJSON(env.T, h, http.MethodPost, "/api/scans/"+id+"/commit", "", http.StatusOK)
}

func patchMove(env testEnv, h http.Handler, id string, quantity int, status int, wantErr string) {
	env.T.Helper()
	body := exchangeJSON(env.T, h, http.MethodPatch, "/api/moves/"+id,
		`{"quantity":`+strconv.Itoa(quantity)+`}`, status)
	if wantErr != "" && !strings.Contains(string(body), wantErr) {
		env.T.Fatalf("PATCH %s = %s", id, body)
	}
}

func undoMove(env testEnv, h http.Handler, id string) {
	env.T.Helper()
	undoMoveExpect(env, h, id, http.StatusOK, "")
}

func undoMoveExpect(env testEnv, h http.Handler, id string, status int, wantErr string) {
	env.T.Helper()
	body := exchangeJSON(env.T, h, http.MethodDelete, "/api/moves/"+id, "", status)
	if wantErr != "" && !strings.Contains(string(body), wantErr) {
		env.T.Fatalf("DELETE %s = %s", id, body)
	}
}
