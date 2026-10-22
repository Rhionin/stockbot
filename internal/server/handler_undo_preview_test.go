package server

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/clientmsg"
	"github.com/Rhionin/stockbot/internal/history"
	"github.com/Rhionin/stockbot/internal/scan"
)

func TestUndoPreviewMatchesTheUndo(t *testing.T) {
	at := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name      string
		reshelves bool
		onHand    float64
		setup     func(t *testing.T, handler http.Handler, env testEnv) (moveID, itemID, productID, name string)
	}{
		{
			name:      "never shelved and empty",
			reshelves: false,
			onHand:    0,
			setup: func(t *testing.T, handler http.Handler, env testEnv) (string, string, string, string) {
				seedShelfItem(env, "prod-plain", "Plain oats", "item-plain")
				return commitBackfill(t, handler, env, "prod-plain", "scan-plain", at), "item-plain", "prod-plain", "Plain oats"
			},
		},
		{
			name:      "backfill brought a wiped item back and nothing remains",
			reshelves: true,
			onHand:    0,
			setup: func(t *testing.T, handler http.Handler, env testEnv) (string, string, string, string) {
				seedShelfItem(env, "prod-wiped-preview", "Wiped oats", "item-wiped-preview")
				if _, err := env.DB.Exec(`UPDATE items SET shelved = 1 WHERE id = 'item-wiped-preview'`); err != nil {
					t.Fatal(err)
				}
				return commitBackfill(t, handler, env, "prod-wiped-preview", "scan-wiped-preview", at), "item-wiped-preview", "prod-wiped-preview", "Wiped oats"
			},
		},
		{
			name:      "a live unit stays on the shelf",
			reshelves: false,
			onHand:    1,
			setup: func(t *testing.T, handler http.Handler, env testEnv) (string, string, string, string) {
				seedShelfItem(env, "prod-live-preview", "Live oats", "item-live-preview")
				if _, err := env.DB.Exec(`UPDATE items SET shelved = 1 WHERE id = 'item-live-preview'`); err != nil {
					t.Fatal(err)
				}
				moveID := commitBackfill(t, handler, env, "prod-live-preview", "scan-live-preview", at)
				seedScanAt(env, "scan-live-preview-in", "prod-live-preview", scan.StockIn, 1, at.Add(time.Hour))
				exchangeJSON(t, handler, http.MethodPost, "/api/scans/scan-live-preview-in/commit", "", http.StatusOK)
				return moveID, "item-live-preview", "prod-live-preview", "Live oats"
			},
		},
		{
			name:      "a later unit that was used up still re-shelves",
			reshelves: true,
			onHand:    0,
			setup: func(t *testing.T, handler http.Handler, env testEnv) (string, string, string, string) {
				seedShelfItem(env, "prod-used-preview", "Used oats", "item-used-preview")
				if _, err := env.DB.Exec(`UPDATE items SET shelved = 1 WHERE id = 'item-used-preview'`); err != nil {
					t.Fatal(err)
				}
				moveID := commitBackfill(t, handler, env, "prod-used-preview", "scan-used-preview", at)
				seedScanAt(env, "scan-used-preview-in", "prod-used-preview", scan.StockIn, 1, at.Add(time.Hour))
				exchangeJSON(t, handler, http.MethodPost, "/api/scans/scan-used-preview-in/commit", "", http.StatusOK)
				seedScanAt(env, "scan-used-preview-out", "prod-used-preview", scan.StockOut, 1, at.Add(2*time.Hour))
				exchangeJSON(t, handler, http.MethodPost, "/api/scans/scan-used-preview-out/commit", "", http.StatusOK)
				return moveID, "item-used-preview", "prod-used-preview", "Used oats"
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler, env := setupTestWithDB(t)
			finishOpening(env)
			moveID, itemID, productID, name := tt.setup(t, handler, env)
			before := domainSnapshot(t, env.DB)
			body := exchangeJSON(t, handler, http.MethodGet, "/api/moves/"+moveID+"/preview", "", http.StatusOK)
			if got := domainSnapshot(t, env.DB); got != before {
				t.Fatalf("preview wrote rows:\n%s", got)
			}
			var preview struct {
				Effects []struct {
					ItemID          string `json:"itemId"`
					ProductID       string `json:"productId"`
					Name            string `json:"name"`
					StockInMoveID   string `json:"stockInMoveId"`
					StockOutMoveID  string `json:"stockOutMoveId"`
					OnHand          int    `json:"onHand"`
					ReshelvesOnUndo bool   `json:"reshelvesOnUndo"`
				} `json:"effects"`
			}
			if err := json.Unmarshal(body, &preview); err != nil {
				t.Fatal(err)
			}
			if len(preview.Effects) != 1 {
				t.Fatalf("effects = %+v", preview.Effects)
			}
			effect := preview.Effects[0]
			if effect.ItemID != itemID || effect.ProductID != productID || effect.Name != name ||
				effect.StockInMoveID != moveID || effect.StockOutMoveID == "" || effect.StockOutMoveID == moveID ||
				effect.OnHand != int(tt.onHand) || effect.ReshelvesOnUndo != tt.reshelves {
				t.Fatalf("preview = %+v", effect)
			}

			undone := exchangeJSON(t, handler, http.MethodDelete, "/api/moves/"+moveID, "", http.StatusOK)
			var after struct {
				OnHand float64 `json:"onHand"`
			}
			if err := json.Unmarshal(undone, &after); err != nil {
				t.Fatal(err)
			}
			if after.OnHand != tt.onHand {
				t.Fatalf("undo on hand = %v, preview %d", after.OnHand, effect.OnHand)
			}
			var shelved int
			if err := env.DB.QueryRow(`SELECT shelved FROM items WHERE id = ?`, itemID).Scan(&shelved); err != nil {
				t.Fatal(err)
			}
			if (shelved == 1) != tt.reshelves {
				t.Fatalf("shelved = %d, preview reshelves %v", shelved, effect.ReshelvesOnUndo)
			}
		})
	}
}

func TestUndoPreviewUsesTheUndoErrors(t *testing.T) {
	handler, env := setupTestWithDB(t)
	finishOpening(env)
	missing := exchangeJSON(t, handler, http.MethodGet, "/api/moves/missing/preview", "", http.StatusNotFound)
	if !strings.Contains(string(missing), "That move is no longer in the history.") {
		t.Fatalf("missing = %s", missing)
	}
	again := exchangeJSON(t, handler, http.MethodDelete, "/api/moves/missing", "", http.StatusNotFound)
	if string(missing) != string(again) {
		t.Fatalf("preview %s\nundo %s", missing, again)
	}

	at := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	seedShelfItem(env, "prod-used-in", "Used in", "item-used-in")
	seedScanAt(env, "scan-used-in", "prod-used-in", scan.StockIn, 1, at)
	exchangeJSON(t, handler, http.MethodPost, "/api/scans/scan-used-in/commit", "", http.StatusOK)
	seedScanAt(env, "scan-used-out", "prod-used-in", scan.StockOut, 1, at.Add(time.Hour))
	exchangeJSON(t, handler, http.MethodPost, "/api/scans/scan-used-out/commit", "", http.StatusOK)
	view := productHistory(env, handler, "item-used-in")
	var inID string
	for _, move := range view.Moves {
		if move.Direction == "in" {
			inID = move.ID
		}
	}
	if inID == "" {
		t.Fatalf("moves = %+v", view.Moves)
	}
	before := domainSnapshot(t, env.DB)
	preview := exchangeJSON(t, handler, http.MethodGet, "/api/moves/"+inID+"/preview", "", http.StatusConflict)
	if got := domainSnapshot(t, env.DB); got != before {
		t.Fatalf("conflict preview wrote rows:\n%s", got)
	}
	undo := exchangeJSON(t, handler, http.MethodDelete, "/api/moves/"+inID, "", http.StatusConflict)
	if string(preview) != string(undo) {
		t.Fatalf("preview %s\nundo %s", preview, undo)
	}
	if got := domainSnapshot(t, env.DB); got != before {
		t.Fatalf("conflict undo wrote rows:\n%s", got)
	}
}

func TestUndoPreviewByStockOutAndPlainMoves(t *testing.T) {
	at := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	handler, env := setupTestWithDB(t)
	finishOpening(env)
	seedShelfItem(env, "prod-out-preview", "Out oats", "item-out-preview")
	inID, outID := commitBackfillPair(t, handler, env, "prod-out-preview", "scan-out-preview", at)

	before := domainSnapshot(t, env.DB)
	if !strings.Contains(before, "history_repair\n1\n") || !strings.Contains(before, "sqlite_sequence\n(absent)\n") {
		t.Fatalf("snapshot missing repair state:\n%s", before)
	}
	byOut := exchangeJSON(t, handler, http.MethodGet, "/api/moves/"+outID+"/preview", "", http.StatusOK)
	if got := domainSnapshot(t, env.DB); got != before {
		t.Fatalf("stock-out preview changed the shelf:\n%s", got)
	}
	byIn := exchangeJSON(t, handler, http.MethodGet, "/api/moves/"+inID+"/preview", "", http.StatusOK)
	if string(byOut) != string(byIn) {
		t.Fatalf("out %s\nin %s", byOut, byIn)
	}
	var preview struct {
		Effects []struct {
			StockInMoveID   string `json:"stockInMoveId"`
			StockOutMoveID  string `json:"stockOutMoveId"`
			OnHand          int    `json:"onHand"`
			ReshelvesOnUndo bool   `json:"reshelvesOnUndo"`
		} `json:"effects"`
	}
	if err := json.Unmarshal(byOut, &preview); err != nil {
		t.Fatal(err)
	}
	if len(preview.Effects) != 1 || preview.Effects[0].StockInMoveID != inID ||
		preview.Effects[0].StockOutMoveID != outID || preview.Effects[0].OnHand != 0 ||
		preview.Effects[0].ReshelvesOnUndo {
		t.Fatalf("preview = %+v", preview.Effects)
	}

	seedShelfItem(env, "prod-plain-preview", "Plain oats", "item-plain-preview")
	seedScanAt(env, "scan-plain-preview", "prod-plain-preview", scan.StockIn, 1, at)
	exchangeJSON(t, handler, http.MethodPost, "/api/scans/scan-plain-preview/commit", "", http.StatusOK)
	plain := moveID(productHistory(env, handler, "item-plain-preview").Moves, "in")
	if plain == "" {
		t.Fatal("missing stock-in")
	}
	before = domainSnapshot(t, env.DB)
	body := exchangeJSON(t, handler, http.MethodGet, "/api/moves/"+plain+"/preview", "", http.StatusOK)
	if got := domainSnapshot(t, env.DB); got != before {
		t.Fatalf("plain preview changed the shelf:\n%s", got)
	}
	var plainPreview struct {
		Effects []struct {
			StockInMoveID   string `json:"stockInMoveId"`
			StockOutMoveID  string `json:"stockOutMoveId"`
			OnHand          int    `json:"onHand"`
			ReshelvesOnUndo bool   `json:"reshelvesOnUndo"`
		} `json:"effects"`
	}
	if err := json.Unmarshal(body, &plainPreview); err != nil {
		t.Fatal(err)
	}
	if len(plainPreview.Effects) != 1 || plainPreview.Effects[0].StockInMoveID != plain ||
		plainPreview.Effects[0].StockOutMoveID != "" || plainPreview.Effects[0].OnHand != 0 ||
		plainPreview.Effects[0].ReshelvesOnUndo {
		t.Fatalf("plain = %+v", plainPreview.Effects)
	}
	undone := exchangeJSON(t, handler, http.MethodDelete, "/api/moves/"+plain, "", http.StatusOK)
	var after struct {
		OnHand float64 `json:"onHand"`
	}
	if err := json.Unmarshal(undone, &after); err != nil {
		t.Fatal(err)
	}
	if after.OnHand != 0 {
		t.Fatalf("undo on hand = %v", after.OnHand)
	}
}

func TestTwoBackfillsPreviewOnlyTheRequestedPair(t *testing.T) {
	at := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	handler, env := setupTestWithDB(t)
	finishOpening(env)
	seedShelfItem(env, "prod-two", "Two oats", "item-two")
	firstIn, firstOut := commitBackfillPair(t, handler, env, "prod-two", "scan-two-a", at)
	secondIn, secondOut := commitBackfillPair(t, handler, env, "prod-two", "scan-two-b", at.Add(time.Hour))

	before := domainSnapshot(t, env.DB)
	body := exchangeJSON(t, handler, http.MethodGet, "/api/moves/"+firstIn+"/preview", "", http.StatusOK)
	if got := domainSnapshot(t, env.DB); got != before {
		t.Fatalf("preview changed the shelf:\n%s", got)
	}
	var preview struct {
		Effects []struct {
			StockInMoveID  string `json:"stockInMoveId"`
			StockOutMoveID string `json:"stockOutMoveId"`
		} `json:"effects"`
	}
	if err := json.Unmarshal(body, &preview); err != nil {
		t.Fatal(err)
	}
	if len(preview.Effects) != 1 || preview.Effects[0].StockInMoveID != firstIn || preview.Effects[0].StockOutMoveID != firstOut {
		t.Fatalf("preview = %+v", preview.Effects)
	}
	if strings.Contains(string(body), secondIn) || strings.Contains(string(body), secondOut) {
		t.Fatalf("preview named the other pair: %s", body)
	}
	exchangeJSON(t, handler, http.MethodDelete, "/api/moves/"+firstIn, "", http.StatusOK)
	left := productHistory(env, handler, "item-two")
	got := map[string]bool{}
	for _, move := range left.Moves {
		got[move.ID] = true
	}
	if got[firstIn] || got[firstOut] || !got[secondIn] || !got[secondOut] {
		t.Fatalf("moves after undo = %+v", left.Moves)
	}
}

func TestUndoPreviewUnitsNoLongerRecorded(t *testing.T) {
	at := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	handler, env := setupTestWithDB(t)
	finishOpening(env)
	seedShelfItem(env, "prod-gone", "Gone oats", "item-gone")
	seedScanAt(env, "scan-gone-in", "prod-gone", scan.StockIn, 1, at)
	exchangeJSON(t, handler, http.MethodPost, "/api/scans/scan-gone-in/commit", "", http.StatusOK)
	seedScanAt(env, "scan-gone-out", "prod-gone", scan.StockOut, 1, at.Add(time.Hour))
	exchangeJSON(t, handler, http.MethodPost, "/api/scans/scan-gone-out/commit", "", http.StatusOK)
	outID := moveID(productHistory(env, handler, "item-gone").Moves, "out")
	if outID == "" {
		t.Fatal("missing stock-out")
	}
	if _, err := env.DB.Exec(`DELETE FROM item_instances WHERE item_id = 'item-gone'`); err != nil {
		t.Fatal(err)
	}
	before := domainSnapshot(t, env.DB)
	preview := exchangeJSON(t, handler, http.MethodGet, "/api/moves/"+outID+"/preview", "", http.StatusConflict)
	if got := domainSnapshot(t, env.DB); got != before {
		t.Fatalf("preview changed the shelf:\n%s", got)
	}
	undo := exchangeJSON(t, handler, http.MethodDelete, "/api/moves/"+outID, "", http.StatusConflict)
	if string(preview) != string(undo) {
		t.Fatalf("preview %s\nundo %s", preview, undo)
	}
	if !strings.Contains(string(preview), "those units are no longer recorded") {
		t.Fatalf("body = %s", preview)
	}
	if got := domainSnapshot(t, env.DB); got != before {
		t.Fatalf("undo changed the shelf:\n%s", got)
	}
}

func TestOtherHouseholdMoveLooksMissing(t *testing.T) {
	handler, env := setupTestWithDB(t)
	finishOpening(env)
	if _, err := env.DB.Exec(`
		INSERT INTO products (id, name, category, unit_of_measure) VALUES ('prod-other', 'Other oats', 'Pantry', 'unit');
		INSERT INTO items (id, user_id, product_id) VALUES ('item-other', 'user-2', 'prod-other');
		INSERT INTO stock_moves (id, item_id, product_id, direction, quantity, at, source, tracked)
		VALUES ('move-other', 'item-other', 'prod-other', 'in', 1, '2026-08-01 12:00:00', 'manual', 0);
		INSERT INTO item_instances (id, item_id, stock_in_at, stock_move_id)
		VALUES ('inst-other', 'item-other', '2026-08-01 12:00:00', 'move-other');
	`); err != nil {
		t.Fatal(err)
	}
	missing := exchangeJSON(t, handler, http.MethodGet, "/api/moves/missing/preview", "", http.StatusNotFound)
	before := domainSnapshot(t, env.DB)
	preview := exchangeJSON(t, handler, http.MethodGet, "/api/moves/move-other/preview", "", http.StatusNotFound)
	if string(preview) != string(missing) {
		t.Fatalf("preview %s\nmissing %s", preview, missing)
	}
	if strings.Contains(string(preview), "item-other") || strings.Contains(string(preview), "prod-other") ||
		strings.Contains(string(preview), "Other oats") || strings.Contains(string(preview), "onHand") {
		t.Fatalf("preview leaked %s", preview)
	}
	if got := domainSnapshot(t, env.DB); got != before {
		t.Fatalf("preview changed the other household:\n%s", got)
	}
	deleted := exchangeJSON(t, handler, http.MethodDelete, "/api/moves/move-other", "", http.StatusNotFound)
	if string(deleted) != string(missing) {
		t.Fatalf("delete %s\nmissing %s", deleted, missing)
	}
	if got := domainSnapshot(t, env.DB); got != before {
		t.Fatalf("delete changed the other household:\n%s", got)
	}
	edited := exchangeJSON(t, handler, http.MethodPatch, "/api/moves/move-other", `{"quantity":2}`, http.StatusNotFound)
	if string(edited) != string(missing) {
		t.Fatalf("edit %s\nmissing %s", edited, missing)
	}
	if got := domainSnapshot(t, env.DB); got != before {
		t.Fatalf("edit changed the other household:\n%s", got)
	}
	for _, path := range []string{
		"/api/moves/move-other/preview?userId=user-2",
		"/api/moves/move-other?userId=user-2",
	} {
		method := http.MethodGet
		body := ""
		if !strings.HasSuffix(path, "/preview?userId=user-2") {
			method = http.MethodDelete
		}
		got := exchangeJSON(t, handler, method, path, body, http.StatusNotFound)
		if string(got) != string(missing) {
			t.Fatalf("%s %s\nmissing %s", method, got, missing)
		}
	}
	edited = exchangeJSON(t, handler, http.MethodPatch, "/api/moves/move-other?userId=user-2", `{"quantity":2}`, http.StatusNotFound)
	if string(edited) != string(missing) {
		t.Fatalf("edit %s\nmissing %s", edited, missing)
	}
	if got := domainSnapshot(t, env.DB); got != before {
		t.Fatalf("foreign userId changed the other household:\n%s", got)
	}
}

func TestForeignUserIDChangesNothing(t *testing.T) {
	at := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	handler, env := setupTestWithDB(t)
	finishOpening(env)
	seedShelfItem(env, "prod-own", "Own oats", "item-own")
	seedScanAt(env, "scan-own", "prod-own", scan.StockIn, 2, at)
	exchangeJSON(t, handler, http.MethodPost, "/api/scans/scan-own/commit", "", http.StatusOK)
	id := moveID(productHistory(env, handler, "item-own").Moves, "in")
	if id == "" {
		t.Fatal("missing stock-in")
	}
	missing := exchangeJSON(t, handler, http.MethodGet, "/api/moves/missing/preview", "", http.StatusNotFound)
	before := domainSnapshot(t, env.DB)
	for _, path := range []string{
		"/api/moves/" + id + "/preview?userId=user-2",
		"/api/moves/" + id + "/preview?userId=%20user-2%20",
		"/api/moves/" + id + "?userId=user-2",
	} {
		method := http.MethodGet
		body := ""
		if !strings.Contains(path, "/preview") {
			method = http.MethodDelete
		}
		got := exchangeJSON(t, handler, method, path, body, http.StatusNotFound)
		if string(got) != string(missing) {
			t.Fatalf("%s %s %s", method, path, got)
		}
		if strings.Contains(string(got), "item-own") || strings.Contains(string(got), "Own oats") || strings.Contains(string(got), "onHand") {
			t.Fatalf("leaked %s", got)
		}
	}
	edited := exchangeJSON(t, handler, http.MethodPatch, "/api/moves/"+id+"?userId=user-2", `{"quantity":4}`, http.StatusNotFound)
	if string(edited) != string(missing) {
		t.Fatalf("edit %s", edited)
	}
	if got := domainSnapshot(t, env.DB); got != before {
		t.Fatalf("foreign userId changed the shelf:\n%s", got)
	}

	patched := exchangeJSON(t, handler, http.MethodPatch, "/api/moves/"+id+"?userId=user-1", `{"quantity":4}`, http.StatusOK)
	var raised struct {
		OnHand float64 `json:"onHand"`
	}
	if err := json.Unmarshal(patched, &raised); err != nil {
		t.Fatal(err)
	}
	if raised.OnHand != 4 {
		t.Fatalf("owner patch on hand = %v", raised.OnHand)
	}
	deleted := exchangeJSON(t, handler, http.MethodDelete, "/api/moves/"+id, "", http.StatusOK)
	var after struct {
		OnHand float64 `json:"onHand"`
	}
	if err := json.Unmarshal(deleted, &after); err != nil {
		t.Fatal(err)
	}
	if after.OnHand != 0 {
		t.Fatalf("owner delete on hand = %v", after.OnHand)
	}
}

func TestMoveUserIDIsAnExactHouseholdOrOmitted(t *testing.T) {
	at := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	handler, env := setupTestWithDB(t)
	finishOpening(env)
	seedShelfItem(env, "prod-exact", "Exact oats", "item-exact")
	seedScanAt(env, "scan-exact", "prod-exact", scan.StockIn, 2, at)
	exchangeJSON(t, handler, http.MethodPost, "/api/scans/scan-exact/commit", "", http.StatusOK)
	id := moveID(productHistory(env, handler, "item-exact").Moves, "in")
	if id == "" {
		t.Fatal("missing stock-in")
	}
	missing := exchangeJSON(t, handler, http.MethodGet, "/api/moves/missing/preview", "", http.StatusNotFound)
	for _, path := range []string{
		"/api/moves/" + id + "/preview",
		"/api/moves/" + id + "/preview?userId=user-1",
	} {
		body := exchangeJSON(t, handler, http.MethodGet, path, "", http.StatusOK)
		if !strings.Contains(string(body), `"onHand"`) {
			t.Fatalf("%s body %s", path, body)
		}
	}
	before := domainSnapshot(t, env.DB)
	rejected := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, "/api/moves/" + id + "/preview?userId=", ""},
		{http.MethodGet, "/api/moves/" + id + "/preview?userId=User-1", ""},
		{http.MethodGet, "/api/moves/" + id + "/preview?userId=user-1%20", ""},
		{http.MethodGet, "/api/moves/" + id + "/preview?userId=%20user-1", ""},
		{http.MethodGet, "/api/moves/" + id + "/preview?userId=user-1&userId=user-1", ""},
		{http.MethodGet, "/api/moves/" + id + "/preview?userId=user-1&userId=user-2", ""},
		{http.MethodGet, "/api/moves/" + id + "/preview?userId=user-2;x", ""},
		{http.MethodGet, "/api/moves/" + id + "/preview?userId=%ZZ", ""},
		{http.MethodDelete, "/api/moves/" + id + "?userId=user-2;x", ""},
		{http.MethodPatch, "/api/moves/" + id + "?userId=%ZZ", `{"quantity":4}`},
		{http.MethodDelete, "/api/moves/" + id + "?userId=", ""},
		{http.MethodDelete, "/api/moves/" + id + "?userId=User-1", ""},
		{http.MethodDelete, "/api/moves/" + id + "?userId=user-1&userId=user-1", ""},
		{http.MethodPatch, "/api/moves/" + id + "?userId=", `{"quantity":4}`},
		{http.MethodPatch, "/api/moves/" + id + "?userId=user-1&userId=user-1", `{"quantity":4}`},
		{http.MethodPatch, "/api/moves/" + id + "?userId=USER-1", `{"quantity":4}`},
	}
	for _, req := range rejected {
		got := exchangeJSON(t, handler, req.method, req.path, req.body, http.StatusNotFound)
		if string(got) != string(missing) {
			t.Fatalf("%s %s %s", req.method, req.path, got)
		}
	}
	if got := domainSnapshot(t, env.DB); got != before {
		t.Fatalf("rejected userId changed the shelf:\n%s", got)
	}
	patched := exchangeJSON(t, handler, http.MethodPatch, "/api/moves/"+id+"?userId=user-1", `{"quantity":4}`, http.StatusOK)
	var raised struct {
		OnHand float64 `json:"onHand"`
	}
	if err := json.Unmarshal(patched, &raised); err != nil {
		t.Fatal(err)
	}
	if raised.OnHand != 4 {
		t.Fatalf("exact userId on hand = %v", raised.OnHand)
	}
	deleted := exchangeJSON(t, handler, http.MethodDelete, "/api/moves/"+id, "", http.StatusOK)
	var after struct {
		OnHand float64 `json:"onHand"`
	}
	if err := json.Unmarshal(deleted, &after); err != nil {
		t.Fatal(err)
	}
	if after.OnHand != 0 {
		t.Fatalf("omitted userId on hand = %v", after.OnHand)
	}
}

func TestPreviewHoldsNoWriteLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pantry.db")
	db, err := app.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	var mode string
	if err := db.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if mode != "delete" {
		t.Fatalf("journal_mode = %s", mode)
	}
	var busy int
	if err := db.QueryRow(`PRAGMA busy_timeout`).Scan(&busy); err != nil {
		t.Fatal(err)
	}
	if busy != 0 || db.Stats().MaxOpenConnections != 1 {
		t.Fatalf("busy_timeout %d pool %d", busy, db.Stats().MaxOpenConnections)
	}
	if err := app.RunMigrations(db); err != nil {
		db.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	handler, env := setupOnDB(t, db, nil)
	finishOpening(env)
	at := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	seedShelfItem(env, "prod-lock", "Lock oats", "item-lock")
	inID, _ := commitBackfillPair(t, handler, env, "prod-lock", "scan-lock", at)
	before := domainSnapshot(t, db)

	holder, err := app.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	holder.SetMaxOpenConns(1)
	defer holder.Close()
	if _, err := holder.ExecContext(context.Background(), "BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	body := exchangeJSON(t, handler, http.MethodGet, "/api/moves/"+inID+"/preview", "", http.StatusOK)
	if _, err := holder.ExecContext(context.Background(), "ROLLBACK"); err != nil {
		t.Fatal(err)
	}
	if got := domainSnapshot(t, db); got != before {
		t.Fatalf("preview changed the shelf while a writer held the lock:\n%s", got)
	}
	var preview struct {
		Effects []struct {
			OnHand          int  `json:"onHand"`
			ReshelvesOnUndo bool `json:"reshelvesOnUndo"`
		} `json:"effects"`
	}
	if err := json.Unmarshal(body, &preview); err != nil {
		t.Fatal(err)
	}
	undone := exchangeJSON(t, handler, http.MethodDelete, "/api/moves/"+inID, "", http.StatusOK)
	var after struct {
		OnHand float64 `json:"onHand"`
	}
	if err := json.Unmarshal(undone, &after); err != nil {
		t.Fatal(err)
	}
	if len(preview.Effects) != 1 || float64(preview.Effects[0].OnHand) != after.OnHand {
		t.Fatalf("preview %+v undo on hand %v", preview.Effects, after.OnHand)
	}
}

func TestPreviewDiskFullIsUnavailable(t *testing.T) {
	handler, env := setupTestWithDB(t)
	finishOpening(env)
	before := domainSnapshot(t, env.DB)
	restore := history.SetPreviewBackupForTest(func(context.Context, *sql.DB, string) error {
		return errors.New("database or disk is full")
	})
	t.Cleanup(restore)
	var buf bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(previous) })

	body := exchangeJSON(t, handler, http.MethodGet, "/api/moves/anything/preview", "", http.StatusServiceUnavailable)
	var payload struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Error != clientmsg.PreviewNoSpace {
		t.Fatalf("body = %s", body)
	}
	if strings.Contains(string(body), "disk is full") || strings.Contains(string(body), "SQLITE") {
		t.Fatalf("body leaked the disk error: %s", body)
	}
	if !strings.Contains(buf.String(), "request failed") || !strings.Contains(buf.String(), "disk is full") {
		t.Fatalf("log = %s", buf.String())
	}
	if got := domainSnapshot(t, env.DB); got != before {
		t.Fatalf("full disk changed the shelf:\n%s", got)
	}
}

func TestCanceledPreviewIsLoggedAsACancel(t *testing.T) {
	handler, _ := setupTestWithDB(t)
	started := make(chan struct{})
	release := make(chan struct{})
	restore := history.SetPreviewBackupForTest(func(ctx context.Context, src *sql.DB, dest string) error {
		select {
		case <-started:
		default:
			close(started)
		}
		select {
		case <-release:
		case <-ctx.Done():
			return ctx.Err()
		}
		return nil
	})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
		restore()
	})

	firstDone := make(chan struct{})
	go func() {
		req := httptest.NewRequest(http.MethodGet, "/api/moves/anything/preview", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		close(firstDone)
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("preview did not start")
	}

	var buf bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(previous)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/moves/anything/preview", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	res := rec.Result()
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK || len(body) != 0 {
		t.Fatalf("status %d body %q", res.StatusCode, body)
	}
	if strings.Contains(buf.String(), "request failed") || !strings.Contains(buf.String(), "request canceled") {
		t.Fatalf("log = %q", buf.String())
	}
	close(release)
	select {
	case <-firstDone:
	case <-time.After(5 * time.Second):
		t.Fatal("first preview did not finish")
	}
}

func TestDeadlineOnALiveRequestIsAServerError(t *testing.T) {
	handler, _ := setupTestWithDB(t)
	restore := history.SetPreviewBackupForTest(func(context.Context, *sql.DB, string) error {
		return fmt.Errorf("preview: %w", context.DeadlineExceeded)
	})
	t.Cleanup(restore)
	tr := &recordTransport{}
	useSentryTransport(t, tr)
	if err := InitSentry(SentrySettings{DSN: fakeSentryDSN, Environment: "test", IngestHost: "o1.ingest.example"}); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(previous) })

	req := httptest.NewRequest(http.MethodGet, "/api/moves/anything/preview", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	FlushSentry()
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Error != "Something went wrong. Please try again." {
		t.Fatalf("body = %s", rec.Body.String())
	}
	if strings.Contains(buf.String(), "request canceled") || !strings.Contains(buf.String(), "request failed") {
		t.Fatalf("log = %q", buf.String())
	}
	events := tr.errors()
	if len(events) != 1 {
		t.Fatalf("reported events = %d", len(events))
	}
	var text string
	for _, ex := range events[0].Exception {
		text += ex.Value
	}
	if !strings.Contains(strings.ToLower(text), "deadline") {
		t.Fatalf("event = %#v", events[0].Exception)
	}
}

func commitBackfill(t *testing.T, handler http.Handler, env testEnv, productID, scanID string, at time.Time) string {
	t.Helper()
	seedScanAt(env, scanID, productID, scan.StockOut, 1, at)
	body := exchangeJSON(t, handler, http.MethodPost, "/api/scans/"+scanID+"/commit", `{"backfill":true}`, http.StatusOK)
	var got struct {
		Backfill struct {
			StockInMoveID string `json:"stockInMoveId"`
		} `json:"backfill"`
	}
	if err := json.Unmarshal(body, &got); err != nil || got.Backfill.StockInMoveID == "" {
		t.Fatalf("backfill %s: %v", body, err)
	}
	return got.Backfill.StockInMoveID
}

func commitBackfillPair(t *testing.T, handler http.Handler, env testEnv, productID, scanID string, at time.Time) (string, string) {
	t.Helper()
	seedScanAt(env, scanID, productID, scan.StockOut, 1, at)
	body := exchangeJSON(t, handler, http.MethodPost, "/api/scans/"+scanID+"/commit", `{"backfill":true}`, http.StatusOK)
	var got struct {
		Backfill struct {
			StockInMoveID  string `json:"stockInMoveId"`
			StockOutMoveID string `json:"stockOutMoveId"`
		} `json:"backfill"`
	}
	if err := json.Unmarshal(body, &got); err != nil || got.Backfill.StockInMoveID == "" || got.Backfill.StockOutMoveID == "" {
		t.Fatalf("backfill %s: %v", body, err)
	}
	return got.Backfill.StockInMoveID, got.Backfill.StockOutMoveID
}
