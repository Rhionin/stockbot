package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/product"
	"github.com/Rhionin/stockbot/internal/scan"
)

func TestBackfillRecordsThePairAtScannedAt(t *testing.T) {
	handler, env := setupTestWithDB(t)
	finishOpening(env)
	seedShelfItem(env, "prod-cream", "Sour cream", "item-cream")
	if _, err := env.DB.Exec(`UPDATE products SET pack_count = 6 WHERE id = 'prod-cream'`); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 5, 2, 15, 4, 5, 0, time.UTC)
	seedScanAt(env, "scan-cream", "prod-cream", scan.StockOut, 2, at)

	body := exchangeJSON(t, handler, http.MethodPost, "/api/scans/scan-cream/commit", `{"backfill":true}`, http.StatusOK)
	var got struct {
		Removed    int    `json:"removed"`
		Backfilled int    `json:"backfilled"`
		ItemID     string `json:"itemId"`
		Backfill   struct {
			StockInMoveID  string `json:"stockInMoveId"`
			StockOutMoveID string `json:"stockOutMoveId"`
		} `json:"backfill"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if got.Removed != 2 || got.Backfilled != 2 || got.ItemID != "item-cream" {
		t.Fatalf("backfill = %+v", got)
	}

	view := productHistory(env, handler, "item-cream")
	if view.OnHand != 0 || len(view.Moves) != 2 {
		t.Fatalf("history = %+v", view)
	}
	wantAt := at.Format(time.RFC3339)
	byDirection := map[string]historyMove{}
	for _, move := range view.Moves {
		byDirection[move.Direction] = move
	}
	in, out := byDirection["in"], byDirection["out"]
	if in.Quantity != 2 || in.Source != "backfill" || in.At != wantAt {
		t.Fatalf("stock-in = %+v, want qty 2 source backfill at %s", in, wantAt)
	}
	if out.Quantity != 2 || out.Source != "scan" || out.At != wantAt {
		t.Fatalf("stock-out = %+v, want qty 2 source scan at %s", out, wantAt)
	}
	if got.Backfill.StockInMoveID != in.ID || got.Backfill.StockOutMoveID != out.ID || in.ID == out.ID {
		t.Fatalf("backfill ids = %+v, history in %s out %s", got.Backfill, in.ID, out.ID)
	}
	if n := mustCount(t, env.DB, `SELECT COUNT(*) FROM stock_in_events WHERE product_id = 'prod-cream'`); n != 0 {
		t.Fatalf("stock_in_events = %d, want 0", n)
	}
	if n := mustCount(t, env.DB, `SELECT COUNT(*) FROM item_instances WHERE item_id = 'item-cream'`); n != 2 {
		t.Fatalf("instances = %d, want 2 (pack count is not applied)", n)
	}
	if n := mustCount(t, env.DB, `SELECT COUNT(*) FROM item_instances WHERE item_id = 'item-cream' AND removed_at IS NULL`); n != 0 {
		t.Fatalf("on-hand instances = %d", n)
	}
	if n := mustCount(t, env.DB, `SELECT COUNT(*) FROM consumption_events WHERE item_id = 'item-cream'`); n != 2 {
		t.Fatalf("consumption_events = %d, want 2", n)
	}
	pending := exchangeJSON(t, handler, http.MethodGet, "/api/scans?userId=user-1&status=pending", "", http.StatusOK)
	if strings.Contains(string(pending), "scan-cream") {
		t.Fatalf("scan still pending: %s", pending)
	}
}

func TestBackfillLeavesPaceReplenishAndBuyFrequency(t *testing.T) {
	handler, env := setupTestWithDB(t)
	now := time.Now().UTC()
	started := now.Add(-200 * 24 * time.Hour)
	beginUsing(t, env.DB, started)

	createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", "prod-rate", "Milk", "item-rate")
	if _, err := env.DB.Exec(`DELETE FROM item_instances WHERE item_id = 'item-rate'`); err != nil {
		t.Fatal(err)
	}
	firstIn := started.Add(24 * time.Hour)
	insertStockIn(t, env.DB, "prod-rate", firstIn)
	for i, id := range []string{"ce-rate-1", "ce-rate-2", "ce-rate-3", "ce-rate-4"} {
		insertConsumptionEvent(t, env.DB, id, "item-rate", firstIn.Add(time.Duration(i+1)*30*24*time.Hour))
	}

	seedShelfItem(env, "prod-repl", "Oats", "item-repl")
	insertStockIn(t, env.DB, "prod-repl", now.Add(-15*24*time.Hour))
	insertConsumptionEvent(t, env.DB, "ce-repl-old", "item-repl", now.Add(-20*24*time.Hour))
	insertConsumptionEvent(t, env.DB, "ce-repl-new", "item-repl", now.Add(-8*24*time.Hour))

	beforePace := paceOf(t, handler, "item-repl")
	if !beforePace.Known || beforePace.DaysBetweenUses != 10 {
		t.Fatalf("pace before backfill = %+v, want known with 10 days between uses", beforePace)
	}
	lines := fillShopping(t, handler)
	repl := shopLineByItem(lines, "item-repl")
	rate := shopLineByItem(lines, "item-rate")
	if repl == nil || repl.Quantity != 1 || repl.Note != "replacing 1 you used" {
		t.Fatalf("replenish before backfill = %+v", repl)
	}
	if rate == nil || rate.Quantity != 3 || rate.Note != "about 1 a month, so 3 for 3 months." {
		t.Fatalf("buy-frequency before backfill = %+v", rate)
	}
	rateIns := mustCount(t, env.DB, `SELECT COUNT(*) FROM stock_in_events WHERE product_id = 'prod-rate'`)
	replIns := mustCount(t, env.DB, `SELECT COUNT(*) FROM stock_in_events WHERE product_id = 'prod-repl'`)
	rateUses := mustCount(t, env.DB, `SELECT COUNT(*) FROM consumption_events WHERE item_id = 'item-rate'`)
	replUses := mustCount(t, env.DB, `SELECT COUNT(*) FROM consumption_events WHERE item_id = 'item-repl'`)

	// Inside the pace window and after the latest stock-in. A real stock-in
	// here would move the replenish boundary and drop the earlier use. The
	// backfill stock-in does not, so the extra use is what changes the note.
	seedScanAt(env, "scan-rate", "prod-rate", scan.StockOut, 1, now.Add(-10*24*time.Hour))
	seedScanAt(env, "scan-repl", "prod-repl", scan.StockOut, 1, now.Add(-5*24*time.Hour))
	exchangeJSON(t, handler, http.MethodPost, "/api/scans/scan-rate/commit", `{"backfill":true}`, http.StatusOK)
	exchangeJSON(t, handler, http.MethodPost, "/api/scans/scan-repl/commit", `{"backfill":true}`, http.StatusOK)

	afterPace := paceOf(t, handler, "item-repl")
	if !afterPace.Known || afterPace.DaysBetweenUses != 6.7 {
		t.Fatalf("pace after the in-window use = %+v, want known with 6.7 days between uses", afterPace)
	}
	again := fillShopping(t, handler)
	if got := shopLineByItem(again, "item-repl"); got == nil || got.Quantity != 2 || got.Note != "replacing 2 you used" {
		t.Fatalf("replenish after the in-window use = %+v, want replacing 2", got)
	}
	if got := shopLineByItem(again, "item-rate"); got == nil || got.Quantity != rate.Quantity || got.Note != rate.Note {
		t.Fatalf("buy-frequency after backfill = %+v, want %+v", got, rate)
	}
	if n := mustCount(t, env.DB, `SELECT COUNT(*) FROM stock_in_events WHERE product_id = 'prod-rate'`); n != rateIns {
		t.Fatalf("rate stock-ins = %d, want %d", n, rateIns)
	}
	if n := mustCount(t, env.DB, `SELECT COUNT(*) FROM stock_in_events WHERE product_id = 'prod-repl'`); n != replIns {
		t.Fatalf("replenish stock-ins = %d, want %d", n, replIns)
	}
	if n := mustCount(t, env.DB, `SELECT COUNT(*) FROM consumption_events WHERE item_id = 'item-rate'`); n != rateUses+1 {
		t.Fatalf("rate usage = %d, want %d", n, rateUses+1)
	}
	if n := mustCount(t, env.DB, `SELECT COUNT(*) FROM consumption_events WHERE item_id = 'item-repl'`); n != replUses+1 {
		t.Fatalf("replenish usage = %d, want %d", n, replUses+1)
	}
}

func TestBackfillUndoRemovesBothMoves(t *testing.T) {
	handler, env := setupTestWithDB(t)
	finishOpening(env)
	seedShelfItem(env, "prod-undo", "Yogurt", "item-undo")
	if _, err := env.DB.Exec(`
		INSERT INTO barcodes (barcode, product_id, source, user_id)
		VALUES ('scan-undo', 'prod-undo', 'user_override', 'user-1')`); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 5, 3, 8, 0, 0, 0, time.UTC)
	seedScanAt(env, "scan-undo", "prod-undo", scan.StockOut, 1, at)
	exchangeJSON(t, handler, http.MethodPost, "/api/scans/scan-undo/commit", `{"backfill":true}`, http.StatusOK)

	view := productHistory(env, handler, "item-undo")
	var inID, outID string
	for _, move := range view.Moves {
		if move.Direction == "in" {
			inID = move.ID
		}
		if move.Direction == "out" {
			outID = move.ID
		}
	}
	if inID == "" || outID == "" {
		t.Fatalf("moves = %+v", view.Moves)
	}

	t.Run("undo the stock-in", func(t *testing.T) {
		undoBackfillPair(t, handler, env, inID, "item-undo", "prod-undo", "scan-undo")
	})

	seedScanAt(env, "scan-undo-2", "prod-undo", scan.StockOut, 1, at.Add(time.Hour))
	exchangeJSON(t, handler, http.MethodPost, "/api/scans/scan-undo-2/commit", `{"backfill":true}`, http.StatusOK)
	view = productHistory(env, handler, "item-undo")
	outID = ""
	for _, move := range view.Moves {
		if move.Direction == "out" && move.Source == "scan" {
			outID = move.ID
		}
	}
	if outID == "" {
		t.Fatalf("second pair = %+v", view.Moves)
	}
	t.Run("undo the stock-out", func(t *testing.T) {
		undoBackfillPair(t, handler, env, outID, "item-undo", "prod-undo", "scan-undo-2")
	})
}

func TestUndoBackfillKeepsALaterUnit(t *testing.T) {
	handler, env := setupTestWithDB(t)
	finishOpening(env)
	seedShelfItem(env, "prod-later", "Later oats", "item-later")
	if _, err := env.DB.Exec(`UPDATE items SET shelved = 1 WHERE id = 'item-later'`); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)
	seedScanAt(env, "scan-later", "prod-later", scan.StockOut, 1, at)
	body := exchangeJSON(t, handler, http.MethodPost, "/api/scans/scan-later/commit", `{"backfill":true}`, http.StatusOK)
	var committed struct {
		Backfill struct {
			StockInMoveID string `json:"stockInMoveId"`
		} `json:"backfill"`
	}
	if err := json.Unmarshal(body, &committed); err != nil || committed.Backfill.StockInMoveID == "" {
		t.Fatalf("backfill = %s (%v)", body, err)
	}
	var shelved int
	if err := env.DB.QueryRow(`SELECT shelved FROM items WHERE id = 'item-later'`).Scan(&shelved); err != nil {
		t.Fatal(err)
	}
	if shelved != 0 {
		t.Fatal("backfill left the item shelved")
	}

	seedScanAt(env, "scan-later-in", "prod-later", scan.StockIn, 1, at.Add(time.Hour))
	exchangeJSON(t, handler, http.MethodPost, "/api/scans/scan-later-in/commit", "", http.StatusOK)
	exchangeJSON(t, handler, http.MethodDelete, "/api/moves/"+committed.Backfill.StockInMoveID, "", http.StatusOK)

	if err := env.DB.QueryRow(`SELECT shelved FROM items WHERE id = 'item-later'`).Scan(&shelved); err != nil {
		t.Fatal(err)
	}
	if shelved != 0 {
		t.Fatal("undo shelved an item that still has a unit")
	}
	inventory := exchangeJSON(t, handler, http.MethodGet, "/api/inventory", "", http.StatusOK)
	var rows []struct {
		Item struct {
			ID string `json:"id"`
		} `json:"item"`
		InstanceCount float64 `json:"instanceCount"`
	}
	if err := json.Unmarshal(inventory, &rows); err != nil {
		t.Fatal(err)
	}
	var count float64
	var found bool
	for _, row := range rows {
		if row.Item.ID == "item-later" {
			found = true
			count = row.InstanceCount
		}
	}
	if !found || count != 1 {
		t.Fatalf("inventory row for item-later = found %v count %v in %s", found, count, inventory)
	}
	if !productIDs(t, handler, "/api/products")["prod-later"] {
		t.Fatal("product dropped out of the picker")
	}
	view := productHistory(env, handler, "item-later")
	if view.OnHand != 1 {
		t.Fatalf("history on hand = %v, want 1", view.OnHand)
	}
}

func TestBackfillRejectsUnlinkedAndUnitsOnHand(t *testing.T) {
	handler, env := setupTestWithDB(t)
	finishOpening(env)
	at := time.Date(2026, 5, 4, 9, 0, 0, 0, time.UTC)

	t.Run("no product", func(t *testing.T) {
		seedLooseScan(env, "scan-none", "011110000099", scan.StockOut, 1, nil, scan.Pending, at)
		body := exchangeJSON(t, handler, http.MethodPost, "/api/scans/scan-none/commit", `{"backfill":true}`, http.StatusBadRequest)
		assertCode(t, body, "product_unresolved", "Choose which item this is first.")
	})

	t.Run("product with no household item", func(t *testing.T) {
		seedShelfItem(env, "prod-missing-item", "Missing", "item-other-household")
		if _, err := env.DB.Exec(`DELETE FROM items WHERE id = 'item-other-household'`); err != nil {
			t.Fatal(err)
		}
		productID := "prod-missing-item"
		seedLooseScan(env, "scan-no-item", "011110000088", scan.StockOut, 1, &productID, scan.Pending, at)
		body := exchangeJSON(t, handler, http.MethodPost, "/api/scans/scan-no-item/commit", `{"backfill":true}`, http.StatusBadRequest)
		assertCode(t, body, "product_unresolved", "Choose which item this is first.")
		if strings.Contains(string(body), "no_item") {
			t.Fatalf("unlinked backfill returned no_item: %s", body)
		}
	})

	t.Run("units already on hand", func(t *testing.T) {
		seedShelfItem(env, "prod-full", "Full Oats", "item-full")
		for _, id := range []string{"full-1", "full-2", "full-3"} {
			if _, err := env.DB.Exec(`
				INSERT INTO item_instances (id, item_id, stock_in_at) VALUES (?, 'item-full', ?)`,
				id, at); err != nil {
				t.Fatal(err)
			}
		}
		seedScanAt(env, "scan-full", "prod-full", scan.StockOut, 1, at)
		body := exchangeJSON(t, handler, http.MethodPost, "/api/scans/scan-full/commit", `{"backfill":true}`, http.StatusBadRequest)
		var got struct {
			Error  string  `json:"error"`
			Code   string  `json:"code"`
			ItemID string  `json:"itemId"`
			OnHand float64 `json:"onHand"`
		}
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatal(err)
		}
		if got.Code != "units_on_hand" || got.Error != "You already have some on hand, so there's nothing to add." ||
			got.ItemID != "item-full" || got.OnHand != 3 {
			t.Fatalf("units on hand = %+v", got)
		}
		if n := mustCount(t, env.DB, `SELECT COUNT(*) FROM stock_moves WHERE item_id = 'item-full'`); n != 0 {
			t.Fatalf("stock moves = %d", n)
		}
		if n := mustCount(t, env.DB, `SELECT COUNT(*) FROM item_instances WHERE item_id = 'item-full' AND removed_at IS NULL`); n != 3 {
			t.Fatalf("on hand = %d", n)
		}
		pending := exchangeJSON(t, handler, http.MethodGet, "/api/scans?userId=user-1&status=pending", "", http.StatusOK)
		if !strings.Contains(string(pending), "scan-full") {
			t.Fatalf("scan was not left pending: %s", pending)
		}
	})

	t.Run("stock-in", func(t *testing.T) {
		seedShelfItem(env, "prod-in", "Inbound", "item-in-scan")
		seedScanAt(env, "scan-in-backfill", "prod-in", scan.StockIn, 1, at)
		body := exchangeJSON(t, handler, http.MethodPost, "/api/scans/scan-in-backfill/commit", `{"backfill":true}`, http.StatusBadRequest)
		assertCode(t, body, "not_stock_out", "This only works when taking something out.")
		if n := mustCount(t, env.DB, `SELECT COUNT(*) FROM stock_moves WHERE item_id = 'item-in-scan'`); n != 0 {
			t.Fatalf("stock moves = %d", n)
		}
	})

	t.Run("shelved item comes back at zero and undo hides it again", func(t *testing.T) {
		seedShelfItem(env, "prod-wiped", "Wiped", "item-wiped")
		if _, err := env.DB.Exec(`UPDATE items SET shelved = 1 WHERE id = 'item-wiped'`); err != nil {
			t.Fatal(err)
		}
		seedScanAt(env, "scan-wiped", "prod-wiped", scan.StockOut, 1, at)
		plain := exchangeJSON(t, handler, http.MethodPost, "/api/scans/scan-wiped/commit", "", http.StatusConflict)
		assertCode(t, plain, "none_on_hand", "Nothing on hand to take out.")
		var shelved int
		if err := env.DB.QueryRow(`SELECT shelved FROM items WHERE id = 'item-wiped'`).Scan(&shelved); err != nil {
			t.Fatal(err)
		}
		if shelved != 1 {
			t.Fatal("stock-out unshelved the item")
		}

		body := exchangeJSON(t, handler, http.MethodPost, "/api/scans/scan-wiped/commit", `{"backfill":true}`, http.StatusOK)
		var got struct {
			Removed  int    `json:"removed"`
			ItemID   string `json:"itemId"`
			Backfill struct {
				StockInMoveID  string `json:"stockInMoveId"`
				StockOutMoveID string `json:"stockOutMoveId"`
			} `json:"backfill"`
		}
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatal(err)
		}
		if got.Removed != 1 || got.ItemID != "item-wiped" || got.Backfill.StockInMoveID == "" || got.Backfill.StockOutMoveID == "" {
			t.Fatalf("backfill = %+v", got)
		}
		if err := env.DB.QueryRow(`SELECT shelved FROM items WHERE id = 'item-wiped'`).Scan(&shelved); err != nil {
			t.Fatal(err)
		}
		if shelved != 0 {
			t.Fatal("backfill left the item hidden")
		}
		view := productHistory(env, handler, "item-wiped")
		if view.OnHand != 0 || len(view.Moves) != 2 {
			t.Fatalf("history = %+v", view)
		}
		inventory := exchangeJSON(t, handler, http.MethodGet, "/api/inventory", "", http.StatusOK)
		if !strings.Contains(string(inventory), "item-wiped") {
			t.Fatalf("unshelved item is missing from inventory: %s", inventory)
		}
		var moveID string
		for _, move := range view.Moves {
			if move.Source == "backfill" && move.Direction == "in" {
				moveID = move.ID
			}
		}
		if moveID == "" || moveID != got.Backfill.StockInMoveID {
			t.Fatalf("moves = %+v, commit stock-in %s", view.Moves, got.Backfill.StockInMoveID)
		}
		undone := exchangeJSON(t, handler, http.MethodDelete, "/api/moves/"+got.Backfill.StockInMoveID, "", http.StatusOK)
		var after struct {
			OnHand float64       `json:"onHand"`
			Moves  []historyMove `json:"moves"`
		}
		if err := json.Unmarshal(undone, &after); err != nil {
			t.Fatal(err)
		}
		if after.OnHand != 0 || len(after.Moves) != 0 {
			t.Fatalf("history after undo = %+v", after)
		}
		if n := mustCount(t, env.DB, `SELECT COUNT(*) FROM stock_moves WHERE item_id = 'item-wiped'`); n != 0 {
			t.Fatalf("moves left = %d", n)
		}
		if err := env.DB.QueryRow(`SELECT shelved FROM items WHERE id = 'item-wiped'`).Scan(&shelved); err != nil {
			t.Fatal(err)
		}
		if shelved != 1 {
			t.Fatal("undo left the wiped item visible")
		}
		hidden := exchangeJSON(t, handler, http.MethodGet, "/api/inventory", "", http.StatusOK)
		if strings.Contains(string(hidden), "item-wiped") {
			t.Fatalf("re-shelved item is on the inventory list: %s", hidden)
		}
		exchangeJSON(t, handler, http.MethodGet, "/api/products/prod-wiped", "", http.StatusOK)
	})

	t.Run("shelved leftovers are not on hand", func(t *testing.T) {
		seedShelfItem(env, "prod-left", "Leftover", "item-left")
		for _, id := range []string{"left-1", "left-2"} {
			if _, err := env.DB.Exec(`INSERT INTO item_instances (id, item_id, stock_in_at) VALUES (?, 'item-left', ?)`, id, at); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := env.DB.Exec(`UPDATE items SET shelved = 1 WHERE id = 'item-left'`); err != nil {
			t.Fatal(err)
		}
		seedScanAt(env, "scan-left", "prod-left", scan.StockOut, 1, at)
		exchangeJSON(t, handler, http.MethodPost, "/api/scans/scan-left/commit", `{"backfill":true}`, http.StatusOK)
		var shelved int
		if err := env.DB.QueryRow(`SELECT shelved FROM items WHERE id = 'item-left'`).Scan(&shelved); err != nil {
			t.Fatal(err)
		}
		if shelved != 0 {
			t.Fatal("backfill left the leftovers hidden")
		}
		if n := mustCount(t, env.DB, `SELECT COUNT(*) FROM item_instances WHERE item_id = 'item-left' AND removed_at IS NULL`); n != 2 {
			t.Fatalf("leftover on hand = %d, want 2", n)
		}
		if n := mustCount(t, env.DB, `SELECT COUNT(*) FROM stock_moves WHERE item_id = 'item-left'`); n != 2 {
			t.Fatalf("moves = %d, want the backfill pair", n)
		}
		view := productHistory(env, handler, "item-left")
		if view.OnHand != 2 {
			t.Fatalf("history on hand = %v, want 2", view.OnHand)
		}
	})
}

func TestStockOutCreateAndLinkWritesNoStockEvents(t *testing.T) {
	handler, env := setupTestWithDB(t)
	finishOpening(env)
	at := time.Date(2026, 5, 5, 10, 0, 0, 0, time.UTC)
	seedLooseScan(env, "scan-new", "011110000000", scan.StockOut, 1, nil, scan.Pending, at)

	created := exchangeJSON(t, handler, http.MethodPost, "/api/products",
		`{"name":"Sour cream","category":"Dairy","unitOfMeasure":"tub"}`, http.StatusCreated)
	var product struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(created, &product); err != nil || product.ID == "" {
		t.Fatalf("product = %s (%v)", created, err)
	}
	linked := exchangeJSON(t, handler, http.MethodPatch, "/api/scans/scan-new",
		`{"productId":"`+product.ID+`","status":"pending"}`, http.StatusOK)
	var scanBody struct {
		Status    string  `json:"status"`
		ProductID string  `json:"productId"`
		OnHand    float64 `json:"onHand"`
	}
	if err := json.Unmarshal(linked, &scanBody); err != nil {
		t.Fatal(err)
	}
	if scanBody.Status != "pending" || scanBody.ProductID != product.ID || scanBody.OnHand != 0 {
		t.Fatalf("link = %+v", scanBody)
	}
	var itemID string
	if err := env.DB.QueryRow(`SELECT id FROM items WHERE user_id = 'user-1' AND product_id = ?`, product.ID).Scan(&itemID); err != nil {
		t.Fatalf("item: %v", err)
	}
	assertNoStockEvents(t, env.DB, itemID, product.ID)
	var override string
	if err := env.DB.QueryRow(`
		SELECT product_id FROM barcodes WHERE barcode = '011110000000' AND source = 'user_override' AND user_id = 'user-1'`).Scan(&override); err != nil {
		t.Fatal(err)
	}
	if override != product.ID {
		t.Fatalf("override product = %s", override)
	}
	pending := exchangeJSON(t, handler, http.MethodGet, "/api/scans?userId=user-1&status=pending", "", http.StatusOK)
	if !strings.Contains(string(pending), "scan-new") || strings.Contains(string(pending), `"onHand"`) {
		t.Fatalf("pending list = %s", pending)
	}

	t.Run("existing units are reported and left in place", func(t *testing.T) {
		seedShelfItem(env, "prod-have", "Have Some", "item-have")
		for _, id := range []string{"have-1", "have-2"} {
			if _, err := env.DB.Exec(`INSERT INTO item_instances (id, item_id, stock_in_at) VALUES (?, 'item-have', ?)`, id, at); err != nil {
				t.Fatal(err)
			}
		}
		seedLooseScan(env, "scan-have", "022220000000", scan.StockOut, 1, nil, scan.Pending, at)
		body := exchangeJSON(t, handler, http.MethodPatch, "/api/scans/scan-have",
			`{"productId":"prod-have","status":"pending"}`, http.StatusOK)
		var got struct {
			OnHand float64 `json:"onHand"`
			Status string  `json:"status"`
		}
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatal(err)
		}
		if got.OnHand != 2 || got.Status != "pending" {
			t.Fatalf("link = %+v", got)
		}
		if n := mustCount(t, env.DB, `SELECT COUNT(*) FROM item_instances WHERE item_id = 'item-have'`); n != 2 {
			t.Fatalf("instances = %d", n)
		}
		if n := mustCount(t, env.DB, `SELECT COUNT(*) FROM stock_moves WHERE item_id = 'item-have'`); n != 0 {
			t.Fatalf("moves = %d", n)
		}
	})

	t.Run("flagged stock-out also creates the item", func(t *testing.T) {
		seedLooseScan(env, "scan-flagged-out", "033330000000", scan.StockOut, 1, nil, scan.Flagged, at)
		body := exchangeJSON(t, handler, http.MethodPatch, "/api/scans/scan-flagged-out",
			`{"productId":"`+product.ID+`","unitCount":3,"status":"pending"}`, http.StatusOK)
		var got struct {
			Status    string  `json:"status"`
			UnitCount int     `json:"unitCount"`
			OnHand    float64 `json:"onHand"`
		}
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatal(err)
		}
		if got.Status != "pending" || got.OnHand != 0 || got.UnitCount != 3 {
			t.Fatalf("flagged stock-out link = %+v", got)
		}
		var overrideUser string
		if err := env.DB.QueryRow(`
			SELECT user_id FROM barcodes
			WHERE barcode = '033330000000' AND source = 'user_override' AND product_id = ?`, product.ID).Scan(&overrideUser); err != nil {
			t.Fatal(err)
		}
		if overrideUser != "user-1" {
			t.Fatalf("override user = %s", overrideUser)
		}
	})

	t.Run("flagged stock-in does not create an item", func(t *testing.T) {
		seedLooseScan(env, "scan-flagged-in", "044440000000", scan.StockIn, 1, nil, scan.Flagged, at)
		fresh := exchangeJSON(t, handler, http.MethodPost, "/api/products", `{"name":"Inbound only"}`, http.StatusCreated)
		var inbound struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(fresh, &inbound); err != nil || inbound.ID == "" {
			t.Fatal(err)
		}
		body := exchangeJSON(t, handler, http.MethodPatch, "/api/scans/scan-flagged-in",
			`{"productId":"`+inbound.ID+`","status":"pending"}`, http.StatusOK)
		if strings.Contains(string(body), `"onHand"`) {
			t.Fatalf("stock-in resolve carried onHand: %s", body)
		}
		if n := mustCount(t, env.DB, `SELECT COUNT(*) FROM items WHERE product_id = ?`, inbound.ID); n != 0 {
			t.Fatalf("stock-in resolve created %d items", n)
		}
	})
}

func TestBatchCommitIgnoresBackfill(t *testing.T) {
	handler, env := setupTestWithDB(t)
	finishOpening(env)
	at := time.Date(2026, 5, 6, 11, 0, 0, 0, time.UTC)

	t.Run("empty shelf still fails and stocks nothing in", func(t *testing.T) {
		seedShelfItem(env, "prod-batch-empty", "Empty", "item-batch-empty")
		seedScanAt(env, "scan-batch-empty", "prod-batch-empty", scan.StockOut, 2, at)
		body := exchangeJSON(t, handler, http.MethodPost, "/api/scans/batch-commit",
			`{"scanEntryIds":["scan-batch-empty"],"commit":true,"backfill":true}`, http.StatusOK)
		var got struct {
			CommittedIDs []string `json:"committedIds"`
			FailedIDs    []string `json:"failedIds"`
			FailedCodes  []string `json:"failedCodes"`
		}
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatal(err)
		}
		if len(got.CommittedIDs) != 0 || len(got.FailedIDs) != 1 || got.FailedIDs[0] != "scan-batch-empty" ||
			len(got.FailedCodes) != 1 || got.FailedCodes[0] != "none_on_hand" {
			t.Fatalf("batch = %+v", got)
		}
		if n := mustCount(t, env.DB, `SELECT COUNT(*) FROM stock_moves WHERE item_id = 'item-batch-empty'`); n != 0 {
			t.Fatalf("moves = %d", n)
		}
		if n := mustCount(t, env.DB, `SELECT COUNT(*) FROM stock_in_events WHERE product_id = 'prod-batch-empty'`); n != 0 {
			t.Fatalf("stock-ins = %d", n)
		}
	})

	t.Run("a stocked item is a normal stock-out", func(t *testing.T) {
		seedShelfItem(env, "prod-batch-full", "Full", "item-batch-full")
		for _, id := range []string{"batch-1", "batch-2", "batch-3"} {
			if _, err := env.DB.Exec(`INSERT INTO item_instances (id, item_id, stock_in_at) VALUES (?, 'item-batch-full', ?)`, id, at); err != nil {
				t.Fatal(err)
			}
		}
		seedScanAt(env, "scan-batch-full", "prod-batch-full", scan.StockOut, 2, at)
		exchangeJSON(t, handler, http.MethodPost, "/api/scans/batch-commit",
			`{"scanEntryIds":["scan-batch-full"],"commit":true,"backfill":true}`, http.StatusOK)
		view := productHistory(env, handler, "item-batch-full")
		var outs, backfills int
		for _, move := range view.Moves {
			if move.Source == "backfill" {
				backfills++
			}
			if move.Direction == "out" && move.Source == "scan" && move.Quantity == 1 {
				outs++
			}
		}
		if view.OnHand != 2 || outs != 1 || backfills != 0 {
			t.Fatalf("batch history = %+v", view)
		}
	})
}

func TestBackfillIsHouseholdScoped(t *testing.T) {
	handler, env := setupTestWithDB(t)
	finishOpening(env)
	at := time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)
	seedShelfItem(env, "prod-shared", "Shared", "item-user-1")
	if _, err := env.DB.Exec(`INSERT INTO items (id, user_id, product_id) VALUES ('item-user-2', 'user-2', 'prod-shared')`); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"u2-1", "u2-2"} {
		if _, err := env.DB.Exec(`INSERT INTO item_instances (id, item_id, stock_in_at) VALUES (?, 'item-user-2', ?)`, id, at); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := env.DB.Exec(`
		INSERT INTO barcodes (barcode, product_id, source, user_id)
		VALUES ('user-2-code', 'prod-shared', 'user_override', 'user-2')`); err != nil {
		t.Fatal(err)
	}
	seedScanAt(env, "scan-shared", "prod-shared", scan.StockOut, 1, at)
	exchangeJSON(t, handler, http.MethodPost, "/api/scans/scan-shared/commit", `{"backfill":true}`, http.StatusOK)

	if n := mustCount(t, env.DB, `SELECT COUNT(*) FROM item_instances WHERE item_id = 'item-user-2' AND removed_at IS NULL`); n != 2 {
		t.Fatalf("user-2 on hand = %d", n)
	}
	if n := mustCount(t, env.DB, `SELECT COUNT(*) FROM stock_moves WHERE item_id = 'item-user-2'`); n != 0 {
		t.Fatalf("user-2 moves = %d", n)
	}
	var linked string
	if err := env.DB.QueryRow(`
		SELECT product_id FROM barcodes WHERE barcode = 'user-2-code' AND user_id = 'user-2'`).Scan(&linked); err != nil {
		t.Fatal(err)
	}
	if linked != "prod-shared" {
		t.Fatalf("user-2 barcode = %s", linked)
	}

	t.Run("another household's item is not this household's item", func(t *testing.T) {
		if err := env.ProductStore.CreateProduct(context.Background(), product.Product{
			ID: "prod-only-2", Name: "Theirs", Category: "Test", UnitOfMeasure: "unit",
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := env.DB.Exec(`INSERT INTO items (id, user_id, product_id) VALUES ('item-only-2', 'user-2', 'prod-only-2')`); err != nil {
			t.Fatal(err)
		}
		if _, err := env.DB.Exec(`INSERT INTO item_instances (id, item_id, stock_in_at) VALUES ('only-2', 'item-only-2', ?)`, at); err != nil {
			t.Fatal(err)
		}
		seedScanAt(env, "scan-only-other", "prod-only-2", scan.StockOut, 1, at.Add(time.Hour))
		body := exchangeJSON(t, handler, http.MethodPost, "/api/scans/scan-only-other/commit", `{"backfill":true}`, http.StatusBadRequest)
		assertCode(t, body, "product_unresolved", "Choose which item this is first.")
		if n := mustCount(t, env.DB, `SELECT COUNT(*) FROM item_instances WHERE item_id = 'item-only-2' AND removed_at IS NULL`); n != 1 {
			t.Fatalf("user-2 on hand = %d", n)
		}
	})
}

func undoBackfillPair(t *testing.T, handler http.Handler, env testEnv, moveID, itemID, productID, scanID string) {
	t.Helper()
	body := exchangeJSON(t, handler, http.MethodDelete, "/api/moves/"+moveID, "", http.StatusOK)
	var view struct {
		OnHand float64       `json:"onHand"`
		Moves  []historyMove `json:"moves"`
	}
	if err := json.Unmarshal(body, &view); err != nil {
		t.Fatal(err)
	}
	if view.OnHand != 0 || len(view.Moves) != 0 {
		t.Fatalf("history after undo = %+v", view)
	}
	if n := mustCount(t, env.DB, `SELECT COUNT(*) FROM stock_moves WHERE item_id = ?`, itemID); n != 0 {
		t.Fatalf("moves left = %d", n)
	}
	if n := mustCount(t, env.DB, `SELECT COUNT(*) FROM item_instances WHERE item_id = ?`, itemID); n != 0 {
		t.Fatalf("instances left = %d", n)
	}
	if n := mustCount(t, env.DB, `SELECT COUNT(*) FROM consumption_events WHERE item_id = ?`, itemID); n != 0 {
		t.Fatalf("usage left = %d", n)
	}
	exchangeJSON(t, handler, http.MethodGet, "/api/products/"+productID, "", http.StatusOK)
	rows := exchangeJSON(t, handler, http.MethodGet, "/api/inventory", "", http.StatusOK)
	if !strings.Contains(string(rows), itemID) {
		t.Fatalf("item was deleted: %s", rows)
	}
	pending := exchangeJSON(t, handler, http.MethodGet, "/api/scans?userId=user-1&status=pending", "", http.StatusOK)
	if strings.Contains(string(pending), scanID) {
		t.Fatalf("undo reopened %s: %s", scanID, pending)
	}
}

func seedLooseScan(env testEnv, id, barcode string, direction scan.ScanDirection, units int, productID *string, status scan.ScanStatus, at time.Time) {
	env.T.Helper()
	if _, err := scan.NewQueue(env.DB).CreateScanEntry(context.Background(), scan.ScanEntry{
		ID: id, UserID: "user-1", Barcode: barcode, ScannedAt: at,
		Direction: &direction, UnitCount: units, ProductID: productID, Status: status,
	}); err != nil {
		env.T.Fatalf("scan %s: %v", id, err)
	}
}

func assertNoStockEvents(t *testing.T, db *sql.DB, itemID, productID string) {
	t.Helper()
	if n := mustCount(t, db, `SELECT COUNT(*) FROM item_instances WHERE item_id = ?`, itemID); n != 0 {
		t.Fatalf("instances = %d", n)
	}
	if n := mustCount(t, db, `SELECT COUNT(*) FROM stock_moves WHERE item_id = ?`, itemID); n != 0 {
		t.Fatalf("moves = %d", n)
	}
	if n := mustCount(t, db, `SELECT COUNT(*) FROM stock_in_events WHERE product_id = ?`, productID); n != 0 {
		t.Fatalf("stock-ins = %d", n)
	}
	if n := mustCount(t, db, `SELECT COUNT(*) FROM consumption_events WHERE item_id = ?`, itemID); n != 0 {
		t.Fatalf("usage = %d", n)
	}
}

func assertCode(t *testing.T, body []byte, code, message string) {
	t.Helper()
	var got struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("body %s: %v", body, err)
	}
	if got.Code != code || got.Error != message {
		t.Fatalf("got %s %q, want %s %q", got.Code, got.Error, code, message)
	}
}

func mustCount(t *testing.T, db *sql.DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

type paceSnapshot struct {
	Known           bool    `json:"known"`
	DaysBetweenUses float64 `json:"daysBetweenUses"`
	DaysLeft        int     `json:"daysLeft"`
	DaysLeftKnown   bool    `json:"daysLeftKnown"`
	Trend           string  `json:"trend"`
}

func paceOf(t *testing.T, handler http.Handler, itemID string) paceSnapshot {
	t.Helper()
	body := exchangeJSON(t, handler, http.MethodGet, "/api/items/"+itemID+"/history", "", http.StatusOK)
	var view struct {
		Pace paceSnapshot `json:"pace"`
	}
	if err := json.Unmarshal(body, &view); err != nil {
		t.Fatal(err)
	}
	return view.Pace
}
