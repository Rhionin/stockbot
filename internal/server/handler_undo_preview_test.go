package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

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
