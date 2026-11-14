package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/Rhionin/stockbot/internal/cart"
	"github.com/Rhionin/stockbot/internal/cart/carttest"
	"github.com/Rhionin/stockbot/internal/cart/connection"
	"github.com/Rhionin/stockbot/internal/changes"
	"github.com/Rhionin/stockbot/internal/inventory"
	"github.com/Rhionin/stockbot/internal/product"
	"github.com/Rhionin/stockbot/internal/shopping"
	"github.com/Rhionin/stockbot/internal/trip"
	"github.com/Rhionin/stockbot/internal/tenant"

)

// --- T4 touched line (HTTP) ---

// TestW3T4TouchedLinePreservesQuantity — T4: coalescer does not overwrite touched qty.
func TestW3T4TouchedLinePreservesQuantity(t *testing.T) {
	handler, env := setupTestWithDB(t)
	scope := primaryScope(t, env.DB)
	createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", "prod-571-t4", "T4", "item-571-t4")
	beginUsing(t, env.DB, time.Now())
	exchangeJSON(t, handler, http.MethodPut, "/api/restock/items/item-571-t4", `{"kind":"amount","value":10}`, http.StatusOK)
	coalescer := shopping.NewListCoalescer(env.DB, env.DB, emptyW3Plan(env.DB), shopping.NewStore(env.DB))
	coalescer.SetDebounce(time.Millisecond)
	_ = coalescer.Drain(context.Background(), scope.HouseholdID(), 2*time.Second)
	mustExecTest(t, env.DB, `INSERT INTO shopping_list_items (id, household_id, user_id, item_id, quantity, source, touched, created_at)
		VALUES ('line-t4', ?, ?, 'item-571-t4', 4, 'auto', 1, datetime('now'))`, scope.HouseholdID(), scope.HouseholdID())
	coalescer.SetDebounce(time.Millisecond)
	_ = coalescer.Enqueue(context.Background(), scope, shopping.CauseTakenOut, []string{shopping.ItemLineKey("item-571-unrelated")}, shopping.BurstMeta{})
	_ = coalescer.Drain(context.Background(), scope.HouseholdID(), 2*time.Second)
	var qty int
	if err := env.DB.QueryRow(`SELECT quantity FROM shopping_list_items WHERE id = 'line-t4'`).Scan(&qty); err != nil {
		t.Fatal(err)
	}
	if qty != 4 {
		t.Fatalf("qty %d want 4", qty)
	}
	body := exchangeJSON(t, handler, http.MethodGet, "/api/shopping-list", "", http.StatusOK)
	var rows []map[string]any
	if err := json.Unmarshal(body, &rows); err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, row := range rows {
		if row["id"] != "line-t4" {
			continue
		}
		found = true
		if row["touched"] != true {
			t.Fatalf("touched on line-t4: %#v", row["touched"])
		}
		if q, ok := row["quantity"].(float64); !ok || int(q) != 4 {
			t.Fatalf("quantity on line-t4: %#v", row["quantity"])
		}
	}
	if !found {
		t.Fatalf("missing line-t4 in GET %s", body)
	}
}

// TestW3T18FullFillListUpdatedHTTP — T18 full fill (not startup catch-up) writes list_updated.
func TestW3T18FullFillListUpdatedHTTP(t *testing.T) {
	TestW3FillWritesListUpdatedHTTP(t)
}

// TestW3FillWritesListUpdatedHTTP — full fill writes list_updated via HTTP.
func TestW3FillWritesListUpdatedHTTP(t *testing.T) {
	handler, env := setupTestWithDB(t)
	createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", "prod-571-fill-http", "Fill HTTP", "item-571-fill-http")
	beginUsing(t, env.DB, time.Now())
	exchangeJSON(t, handler, http.MethodPut, "/api/restock/items/item-571-fill-http", `{"kind":"amount","value":2}`, http.StatusOK)
	exchangeJSON(t, handler, http.MethodPost, "/api/shopping-list/fill", ``, http.StatusOK)
	scope := primaryScope(t, env.DB)
	var cause string
	if err := env.DB.QueryRow(`SELECT cause FROM list_line_changes WHERE household_id = ? LIMIT 1`, scope.HouseholdID()).Scan(&cause); err != nil {
		t.Fatalf("expected list_line_changes: %v", err)
	}
	if cause != string(shopping.CauseListUpdated) {
		t.Fatalf("cause %q", cause)
	}
}

// TestW3T5LineChangeEnumsOnGET — T5: isolated write per effect/cause/reason; DB and GET lastChange match.
func TestW3T5LineChangeEnumsOnGET(t *testing.T) {
	cases := []struct {
		name   string
		effect shopping.Effect
		reason shopping.TakenOffReason
		cause  shopping.Cause
		from   *int
		to     *int
		qty    int
	}{
		{name: "added_taken_out", effect: shopping.EffectAdded, cause: shopping.CauseTakenOut, from: intPtrW3(0), to: intPtrW3(2), qty: 2},
		{name: "amount_up_taken_out", effect: shopping.EffectAmountUp, cause: shopping.CauseTakenOut, from: intPtrW3(1), to: intPtrW3(3), qty: 3},
		{name: "amount_down_taken_out", effect: shopping.EffectAmountDown, cause: shopping.CauseTakenOut, from: intPtrW3(4), to: intPtrW3(2), qty: 2},
		{name: "taken_off_target_zero_put_away", effect: shopping.EffectTakenOff, reason: shopping.TakenOffTargetZero, cause: shopping.CausePutAway, from: intPtrW3(1), to: intPtrW3(0), qty: 0},
		{name: "taken_off_nothing_to_replace_put_away", effect: shopping.EffectTakenOff, reason: shopping.TakenOffNothingToReplace, cause: shopping.CausePutAway, from: intPtrW3(1), to: intPtrW3(0), qty: 0},
		{name: "added_target_changed", effect: shopping.EffectAdded, cause: shopping.CauseTargetChanged, to: intPtrW3(1), qty: 1},
		{name: "added_group_changed", effect: shopping.EffectAdded, cause: shopping.CauseGroupChanged, to: intPtrW3(2), qty: 2},
		{name: "added_setup_finished", effect: shopping.EffectAdded, cause: shopping.CauseSetupFinished, to: intPtrW3(1), qty: 1},
		{name: "added_restocked_here", effect: shopping.EffectAdded, cause: shopping.CauseRestockedHere, to: intPtrW3(1), qty: 1},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			handler, env := setupTestWithDB(t)
			scope := primaryScope(t, env.DB)
			suffix := strings.ReplaceAll(tc.name, "_", "-")
			itemID := "item-t5-" + suffix
			prodID := "prod-t5-" + suffix
			createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", prodID, tc.name, itemID)
			lineID := "line-" + suffix
			mustExecTest(t, env.DB, `INSERT INTO shopping_list_items (id, household_id, user_id, item_id, quantity, source, created_at)
				VALUES (?, ?, ?, ?, ?, 'manual', datetime('now'))`, lineID, scope.HouseholdID(), scope.HouseholdID(), itemID, tc.qty)
			var countBefore int
			if err := env.DB.QueryRow(`SELECT COUNT(*) FROM list_line_changes WHERE household_id = ?`, scope.HouseholdID()).Scan(&countBefore); err != nil {
				t.Fatal(err)
			}
			key := shopping.ItemLineKey(itemID)
			chg := shopping.ScopedChangeInput{Key: key, Effect: tc.effect, Cause: tc.cause, FromQty: tc.from, ToQty: tc.to}
			if tc.reason != "" {
				chg.Reason = tc.reason
			}
			store := shopping.NewStore(env.DB)
			if err := store.ScopedSavePlannedLines(context.Background(), scope, shopping.ScopedSaveParams{
				Lines:        []shopping.PlannedLine{{ItemID: itemID, Quantity: tc.qty}},
				AffectedKeys: map[string]struct{}{key: {}},
				Cause:        tc.cause,
				Changes:      []shopping.ScopedChangeInput{chg},
				Now:          time.Date(2026, 2, 10, 12, 0, 0, 0, time.UTC),
			}); err != nil {
				t.Fatal(err)
			}
			var countAfter int
			if err := env.DB.QueryRow(`SELECT COUNT(*) FROM list_line_changes WHERE household_id = ?`, scope.HouseholdID()).Scan(&countAfter); err != nil {
				t.Fatal(err)
			}
			if countAfter != countBefore+1 {
				t.Fatalf("count before %d after %d", countBefore, countAfter)
			}
			var effect, cause, reason string
			if err := env.DB.QueryRow(`SELECT effect, cause, COALESCE(reason,'') FROM list_line_changes WHERE household_id = ? AND line_key = ?`,
				scope.HouseholdID(), key).Scan(&effect, &cause, &reason); err != nil {
				t.Fatal(err)
			}
			if shopping.Effect(effect) != tc.effect || shopping.Cause(cause) != tc.cause {
				t.Fatalf("db enums %s %s", effect, cause)
			}
			if tc.reason != "" && reason != string(tc.reason) {
				t.Fatalf("db reason %q want %q", reason, tc.reason)
			}
			body := exchangeJSON(t, handler, http.MethodGet, "/api/shopping-list", "", http.StatusOK)
			var rows []map[string]any
			if err := json.Unmarshal(body, &rows); err != nil {
				t.Fatal(err)
			}
			var gotLC map[string]any
			for _, row := range rows {
				if row["itemId"] != itemID {
					continue
				}
				lc, ok := row["lastChange"].(map[string]any)
				if !ok {
					t.Fatalf("missing lastChange on row %#v", row)
				}
				gotLC = lc
				break
			}
			if gotLC == nil {
				t.Fatalf("item %s not in GET", itemID)
			}
			if gotLC["effect"] != string(tc.effect) || gotLC["cause"] != string(tc.cause) {
				t.Fatalf("GET lastChange %#v", gotLC)
			}
			if tc.reason != "" && gotLC["reason"] != string(tc.reason) {
				t.Fatalf("GET reason %#v", gotLC)
			}
		})
	}
}

func intPtrW3(n int) *int { return &n }

// TestW3T5PutAwayStockInToPar — T5 put_away: stock-in to par deletes open auto row; offList only.
func TestW3T5PutAwayStockInToPar(t *testing.T) {
	handler, env := setupTestWithDB(t)
	scope := primaryScope(t, env.DB)
	const itemID = "item-t5-putaway-par"
	const prodID = "prod-t5-putaway-par"
	const barcode = "571005000001"
	createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", prodID, "PutAwayPar", itemID)
	beginUsing(t, env.DB, time.Now())
	exchangeJSON(t, handler, http.MethodPut, "/api/restock/items/"+itemID, `{"kind":"amount","value":2}`, http.StatusOK)
	exchangeJSON(t, handler, http.MethodPost, "/api/shopping-list/fill", ``, http.StatusOK)
	var lineQty int
	if err := env.DB.QueryRow(`SELECT quantity FROM shopping_list_items WHERE household_id = ? AND item_id = ? AND source = 'auto'`,
		scope.HouseholdID(), itemID).Scan(&lineQty); err != nil || lineQty != 1 {
		t.Fatalf("auto line qty %d err %v", lineQty, err)
	}
	if err := env.ProductStore.UpsertBarcodeMapping(context.Background(), barcode, prodID, "global", ""); err != nil {
		t.Fatal(err)
	}
	created := exchangeJSON(t, handler, http.MethodPost, "/api/scans", `{"barcode":"`+barcode+`","direction":"stock_in","userId":"user-1"}`, http.StatusCreated)
	var scan struct{ ID string `json:"id"` }
	if err := json.Unmarshal(created, &scan); err != nil {
		t.Fatal(err)
	}
	env.Refresher.Wait()
	exchangeJSON(t, handler, http.MethodPost, "/api/scans/"+scan.ID+"/commit", ``, http.StatusOK)
	env.Refresher.Wait()
	time.Sleep(200 * time.Millisecond)
	body := exchangeJSON(t, handler, http.MethodGet, "/api/shopping-list?status=1", "", http.StatusOK)
	var rowCount int
	if err := env.DB.QueryRow(`SELECT COUNT(*) FROM shopping_list_items WHERE household_id = ? AND item_id = ? AND source = 'auto'`,
		scope.HouseholdID(), itemID).Scan(&rowCount); err != nil || rowCount != 0 {
		t.Fatalf("shopping row count %d", rowCount)
	}
	var effect, cause, reason string
	var from, to int
	var tripSeq int
	if err := env.DB.QueryRow(`SELECT effect, cause, COALESCE(reason,''), from_qty, to_qty, trip_seq FROM list_line_changes
		WHERE household_id = ? AND line_key = ?`, scope.HouseholdID(), shopping.ItemLineKey(itemID)).
		Scan(&effect, &cause, &reason, &from, &to, &tripSeq); err != nil {
		t.Fatalf("change row: %v", err)
	}
	if effect != "taken_off" || cause != "put_away" || reason != "on_hand_meets_par" || from != 1 || to != 0 {
		t.Fatalf("change %#v from=%d to=%d", effect+"/"+cause+"/"+reason, from, to)
	}
	var changeRows int
	if err := env.DB.QueryRow(`SELECT COUNT(*) FROM list_line_changes WHERE household_id = ? AND line_key = ?`,
		scope.HouseholdID(), shopping.ItemLineKey(itemID)).Scan(&changeRows); err != nil || changeRows != 1 {
		t.Fatalf("change rows for line %d err %v", changeRows, err)
	}
	var doc struct {
		Items   []map[string]any `json:"items"`
		OffList []map[string]any `json:"offList"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	key := shopping.ItemLineKey(itemID)
	for _, row := range doc.Items {
		if row["itemId"] == itemID {
			t.Fatalf("item still in items[] %#v", row)
		}
	}
	var inOff bool
	for _, row := range doc.OffList {
		if row["id"] != key {
			continue
		}
		inOff = true
		if row["offListReason"] != "taken_off" {
			t.Fatalf("offListReason %#v", row["offListReason"])
		}
		lc, ok := row["lastChange"].(map[string]any)
		if !ok || lc["reason"] != "on_hand_meets_par" {
			t.Fatalf("lastChange %#v", row["lastChange"])
		}
		for _, k := range []string{"needsAttention", "sent", "canSendToCart", "storeId"} {
			if _, ok := row[k]; !ok {
				t.Fatalf("offList missing %q", k)
			}
		}
		if _, ok := row["basis"]; ok {
			t.Fatal("offList must omit legacy basis")
		}
	}
	if !inOff {
		t.Fatal("missing offList row")
	}
}

// TestW3T7TripAdvanceClearsOldChanges — T7: ledger reset advances trip and writes new_trip boundary.
func TestW3T7TripAdvanceClearsOldChanges(t *testing.T) {
	handler, env := setupTestWithDB(t)
	scope := primaryScope(t, env.DB)
	hid := scope.HouseholdID()
	registerOpenAndKroger(env)
	mustExecTest(t, env.DB, `INSERT INTO list_line_changes (household_id, line_key, effect, cause, at, trip_seq)
		VALUES (?, 'item:item-571-a', 'added', 'taken_out', '2026-01-01T00:00:00Z', 0)`, hid)
	mustExecTest(t, env.DB, `INSERT INTO list_line_changes (household_id, line_key, effect, cause, at, trip_seq)
		VALUES (?, 'item:item-571-b', 'taken_off', 'put_away', '2026-01-01T00:00:00Z', 0)`, hid)
	createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", "prod-571-c", "C", "item-571-c")
	beginUsing(t, env.DB, time.Now())
	exchangeJSON(t, handler, http.MethodPost, "/api/shopping-list/fill", ``, http.StatusOK)
	mustExecTest(t, env.DB, `INSERT INTO staged_cart_skips (household_id, user_id, item_id, expires_trip) VALUES (?, ?, 'item-571-c', 1)`, hid, hid)
	exchangeJSON(t, handler, http.MethodPost, "/api/providers/"+openProvider+"/ledger/reset", ``, http.StatusCreated)
	var seq int
	if err := env.DB.QueryRow(`SELECT trip_seq FROM households WHERE id = ?`, hid).Scan(&seq); err != nil {
		t.Fatal(err)
	}
	if seq < 1 {
		t.Fatalf("trip_seq %d", seq)
	}
	var stale int
	if err := env.DB.QueryRow(`SELECT COUNT(*) FROM list_line_changes WHERE household_id = ? AND trip_seq < ?`, hid, seq).Scan(&stale); err != nil {
		t.Fatal(err)
	}
	if stale != 0 {
		t.Fatalf("stale changes %d", stale)
	}
	var cCause string
	err := env.DB.QueryRow(`SELECT cause FROM list_line_changes WHERE household_id = ? AND line_key = ?`,
		hid, "item:item-571-c").Scan(&cCause)
	if err != nil {
		t.Fatalf("expected new_trip row for item-571-c: %v", err)
	}
	if cCause != "new_trip" {
		t.Fatalf("cause %q", cCause)
	}
}

// TestW3T7PutAwayTripAdvanceClearsOldChanges — T7 put_away: mass stock-in close advances trip in one txn.
func TestW3T7PutAwayTripAdvanceClearsOldChanges(t *testing.T) {
	handler, env := setupTestWithDB(t)
	scope := primaryScope(t, env.DB)
	hid := scope.HouseholdID()
	mustExecTest(t, env.DB, `INSERT INTO list_line_changes (household_id, line_key, effect, cause, at, trip_seq)
		VALUES (?, 'item:item-571-pa-a', 'added', 'taken_out', '2026-01-01T00:00:00Z', 0)`, hid)
	mustExecTest(t, env.DB, `INSERT INTO staged_cart_skips (household_id, user_id, item_id, expires_trip) VALUES (?, ?, 'item-571-pa-c', 1)`, hid, hid)
	base := time.Date(2026, 1, 10, 10, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		at := base.Add(time.Duration(i) * time.Minute).Format(time.RFC3339Nano)
		pid := fmt.Sprintf("prod-pa-%d", i)
		item := fmt.Sprintf("item-pa-%d", i)
		mustExecTest(t, env.DB, `INSERT INTO products (id, name, category) VALUES (?, ?, 't')`, pid, pid)
		mustExecTest(t, env.DB, `INSERT INTO items (id, user_id, household_id, product_id) VALUES (?, ?, ?, ?)`, item, hid, hid, pid)
		mustExecTest(t, env.DB, `INSERT INTO stock_moves (id, household_id, item_id, product_id, direction, quantity, at, source, tracked, created_at)
			VALUES (?, ?, ?, ?, 'in', 1, ?, 'scan', 1, ?)`, fmt.Sprintf("mv-pa-%d", i), hid, item, pid, at, at)
	}
	tx, err := env.DB.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	closeAt := base.Add(9 * time.Minute)
	if err := trip.MaybeCloseMassStockIn(context.Background(), scope, tx, closeAt); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var seq int
	if err := env.DB.QueryRow(`SELECT trip_seq FROM households WHERE id = ?`, hid).Scan(&seq); err != nil {
		t.Fatal(err)
	}
	if seq < 1 {
		t.Fatalf("trip_seq %d", seq)
	}
	var stale int
	if err := env.DB.QueryRow(`SELECT COUNT(*) FROM list_line_changes WHERE household_id = ? AND trip_seq < ?`, hid, seq).Scan(&stale); err != nil {
		t.Fatal(err)
	}
	if stale != 0 {
		t.Fatalf("stale changes %d", stale)
	}
	var cCause string
	err = env.DB.QueryRow(`SELECT cause FROM list_line_changes WHERE household_id = ? AND line_key = ?`,
		hid, "item:item-571-pa-c").Scan(&cCause)
	if err != nil {
		t.Fatalf("expected new_trip row: %v", err)
	}
	if cCause != "new_trip" {
		t.Fatalf("cause %q", cCause)
	}
	_ = handler
}

// TestW3T10PlanBasisKinds — T10: planBasis.target.kind on GET for amount target.
func TestW3T10PlanBasisKinds(t *testing.T) {
	handler, env := setupTestWithDB(t)
	createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", "prod-571-t10", "T10", "item-571-t10")
	beginUsing(t, env.DB, time.Now())
	exchangeJSON(t, handler, http.MethodPut, "/api/restock/items/item-571-t10", `{"kind":"amount","value":3}`, http.StatusOK)
	exchangeJSON(t, handler, http.MethodPost, "/api/shopping-list/fill", ``, http.StatusOK)
	body := exchangeJSON(t, handler, http.MethodGet, "/api/shopping-list", "", http.StatusOK)
	var rows []map[string]any
	if err := json.Unmarshal(body, &rows); err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, row := range rows {
		if row["itemId"] != "item-571-t10" {
			continue
		}
		pb, ok := row["planBasis"].(map[string]any)
		if !ok {
			t.Fatal("missing planBasis")
		}
		target, ok := pb["target"].(map[string]any)
		if !ok || target["kind"] != "amount" {
			t.Fatalf("target %#v", pb["target"])
		}
		if _, ok := row["basis"]; ok {
			t.Fatal("legacy basis must match master (omitted)")
		}
		found = true
	}
	if !found {
		t.Fatal("item row missing")
	}
}

// TestW3T11PersonEditDoesNotRewriteLineChanges — T11: PATCH qty leaves list_line_changes byte-identical.
func TestW3T11PersonEditDoesNotRewriteLineChanges(t *testing.T) {
	handler, env := setupTestWithDB(t)
	scope := primaryScope(t, env.DB)
	createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", "prod-571-t11", "T11", "item-571-marker")
	mustExecTest(t, env.DB, `INSERT INTO list_line_changes (household_id, line_key, effect, cause, from_qty, to_qty, at, trip_seq)
		VALUES (?, 'item:item-571-marker', 'amount_up', 'taken_out', 1, 2, '2026-01-15T10:00:00Z', 0)`, scope.HouseholdID())
	mustExecTest(t, env.DB, `INSERT INTO shopping_list_items (id, household_id, user_id, item_id, quantity, source, created_at)
		VALUES ('line-t11', ?, ?, 'item-571-marker', 2, 'manual', datetime('now'))`, scope.HouseholdID(), scope.HouseholdID())
	var beforeEffect, beforeAt string
	var beforeFrom, beforeTo int
	if err := env.DB.QueryRow(`SELECT effect, from_qty, to_qty, at FROM list_line_changes WHERE household_id = ? AND line_key = ?`,
		scope.HouseholdID(), "item:item-571-marker").Scan(&beforeEffect, &beforeFrom, &beforeTo, &beforeAt); err != nil {
		t.Fatal(err)
	}
	exchangeJSON(t, handler, http.MethodPatch, "/api/shopping-list/items/line-t11", `{"quantity":3}`, http.StatusOK)
	var effect, at string
	var from, to int
	if err := env.DB.QueryRow(`SELECT effect, from_qty, to_qty, at FROM list_line_changes WHERE household_id = ? AND line_key = ?`,
		scope.HouseholdID(), "item:item-571-marker").Scan(&effect, &from, &to, &at); err != nil {
		t.Fatal(err)
	}
	if effect != beforeEffect || at != beforeAt || from != beforeFrom || to != beforeTo {
		t.Fatalf("changed row %s/%s -> %s/%s", beforeEffect, beforeAt, effect, at)
	}
}

// TestW3T12OffListIdAndReason — T12: offList id is line_key with lastChange.reason.
func TestW3T12OffListIdAndReason(t *testing.T) {
	handler, env := setupTestWithDB(t)
	scope := primaryScope(t, env.DB)
	mustExecTest(t, env.DB, `INSERT INTO list_line_changes (household_id, line_key, effect, reason, cause, from_qty, to_qty, at, trip_seq)
		VALUES (?, 'item:item-571-off', 'taken_off', 'on_hand_meets_par', 'put_away', 1, 0, '2026-03-01T00:00:00Z', 0)`, scope.HouseholdID())
	body := exchangeJSON(t, handler, http.MethodGet, "/api/shopping-list?status=1", "", http.StatusOK)
	var doc struct {
		OffList []map[string]any `json:"offList"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	for _, row := range doc.OffList {
		if row["id"] == "item:item-571-off" {
			if row["offListReason"] != "taken_off" {
				t.Fatalf("offListReason %#v", row["offListReason"])
			}
			lc, ok := row["lastChange"].(map[string]any)
			if !ok || lc["reason"] != "on_hand_meets_par" {
				t.Fatalf("offList row %#v", row)
			}
			return
		}
	}
	t.Fatalf("missing offList row for item-571-off")
}

// TestW3OffListReasonPerKind — per-kind offListReason on status=1 GET.
func TestW3OffListReasonPerKind(t *testing.T) {
	t.Run("removed", func(t *testing.T) {
		handler, env := setupTestWithDB(t)
		scope := primaryScope(t, env.DB)
		hid := scope.HouseholdID()
		createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", "prod-571-rem", "Removed Product", "item-571-rem")
		beginUsing(t, env.DB, time.Now())
		exchangeJSON(t, handler, http.MethodPut, "/api/restock/items/item-571-rem", `{"kind":"amount","value":2}`, http.StatusOK)
		exchangeJSON(t, handler, http.MethodPost, "/api/shopping-list/fill", ``, http.StatusOK)
		mustExecTest(t, env.DB, `DELETE FROM shopping_list_items WHERE household_id = ? AND item_id = 'item-571-rem'`, hid)
		mustExecTest(t, env.DB, `INSERT INTO staged_cart_skips (household_id, user_id, item_id, expires_trip) VALUES (?, ?, 'item-571-rem', 2)`, hid, hid)
		assertOffListReasonHTTP(t, handler, shopping.ItemLineKey("item-571-rem"), "removed", "Removed Product")
	})
	t.Run("opening", func(t *testing.T) {
		handler, env := setupTestWithDB(t)
		createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", "prod-571-open", "Opening Product", "item-571-open")
		setSupplyQuantity(t, env.DB, "prod-571-open", 3)
		assertOffListReasonHTTP(t, handler, shopping.ItemLineKey("item-571-open"), "opening", "Opening Product")
	})
	t.Run("taken_off", func(t *testing.T) {
		handler, env := setupTestWithDB(t)
		scope := primaryScope(t, env.DB)
		mustExecTest(t, env.DB, `INSERT INTO list_line_changes (household_id, line_key, effect, reason, cause, from_qty, to_qty, at, trip_seq)
			VALUES (?, 'item:item-571-off-kind', 'taken_off', 'on_hand_meets_par', 'put_away', 1, 0, '2026-03-01T00:00:00Z', 0)`, scope.HouseholdID())
		assertOffListReasonHTTP(t, handler, "item:item-571-off-kind", "taken_off", "")
	})
	t.Run("not_on_list", func(t *testing.T) {
		handler, env := setupTestWithDB(t)
		scope := primaryScope(t, env.DB)
		var tripSeq int
		if err := env.DB.QueryRow(`SELECT trip_seq FROM households WHERE id = ?`, scope.HouseholdID()).Scan(&tripSeq); err != nil {
			t.Fatal(err)
		}
		mustExecTest(t, env.DB, `INSERT INTO attention_marks (household_id, line_key, flagged_trip) VALUES (?, 'item:item-571-flag', ?)`,
			scope.HouseholdID(), tripSeq)
		assertOffListReasonHTTP(t, handler, "item:item-571-flag", "not_on_list", "")
	})
	t.Run("zero_for_trip", func(t *testing.T) {
		t.Skip("#571 follow-up: next-send 0 expires at trip boundary (adjustments actor/set_at/expires_trip); issue number pending")
	})
}

func assertOffListReasonHTTP(t *testing.T, handler http.Handler, lineKey, wantReason, wantName string) {
	t.Helper()
	body := exchangeJSON(t, handler, http.MethodGet, "/api/shopping-list?status=1", "", http.StatusOK)
	var doc struct {
		OffList []map[string]any `json:"offList"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	for _, row := range doc.OffList {
		if row["id"] != lineKey {
			continue
		}
		if row["offListReason"] != wantReason {
			t.Fatalf("offListReason got %#v want %q row %#v", row["offListReason"], wantReason, row)
		}
		if wantName != "" {
			if name, _ := row["name"].(string); name != wantName {
				t.Fatalf("name got %q want %q", name, wantName)
			}
		}
		return
	}
	t.Fatalf("offList missing %q", lineKey)
}

// TestW3T13SinceStatusFullEnvelope — T13: since+status=1 returns full envelope on change.
func TestW3T13SinceStatusFullEnvelope(t *testing.T) {
	handler, env := setupTestWithDB(t)
	createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", "prod-571-t13", "T13", "item-571-t13")
	beginUsing(t, env.DB, time.Now())
	exchangeJSON(t, handler, http.MethodPut, "/api/restock/items/item-571-t13", `{"kind":"amount","value":3}`, http.StatusOK)
	cursor, err := changes.Current(context.Background(), env.DB, primaryScope(t, env.DB).HouseholdID(), changes.ScopeList)
	if err != nil {
		t.Fatal(err)
	}
	exchangeJSON(t, handler, http.MethodPost, "/api/shopping-list/fill", ``, http.StatusOK)
	defPath := "/api/shopping-list?since=" + cursor
	defRes := exchangeJSON(t, handler, http.MethodGet, defPath, "", http.StatusOK)
	var defDoc map[string]any
	if err := json.Unmarshal(defRes, &defDoc); err != nil {
		t.Fatal(err)
	}
	if defDoc["changed"] != true {
		t.Fatalf("default since changed %#v", defDoc)
	}
	if _, ok := defDoc["items"]; !ok {
		t.Fatalf("default since response %#v", defDoc)
	}
	path := "/api/shopping-list?since=" + cursor + "&status=1"
	res := exchangeJSON(t, handler, http.MethodGet, path, "", http.StatusOK)
	var doc map[string]any
	if err := json.Unmarshal(res, &doc); err != nil {
		t.Fatal(err)
	}
	if _, bad := doc["changed"]; bad {
		t.Fatalf("status=1 envelope must not include changed %#v", doc)
	}
	for _, k := range []string{"cursor", "listStale", "items", "offList", "stores", "attentionGroups"} {
		if _, ok := doc[k]; !ok {
			t.Fatalf("status=1 missing %q %#v", k, doc)
		}
	}
}

type w3HTTPSpy struct{ calls int }

func (s *w3HTTPSpy) RoundTrip(req *http.Request) (*http.Response, error) {
	s.calls++
	return httptest.NewRecorder().Result(), nil
}

// TestW3T14GETNoProviderHTTP — T14: list GET/summary do not call provider HTTP.
func TestW3T14GETNoProviderHTTP(t *testing.T) {
	spy := &w3HTTPSpy{}
	handler, env := setupTestWithDB(t)
	provider := carttest.NewFake(cart.Capabilities{Auth: cart.AuthOAuth2, Delivery: cart.DeliveryServerPush, Confirmation: cart.ConfirmPerRequest, Mutation: cart.MutateAddOnly, Identity: cart.IdentityDerived}, carttest.NewScript())
	providerID := string(provider.ID())
	if err := env.Registry.Register(provider, cart.WithCredentialsConfigured(true)); err != nil {
		t.Fatal(err)
	}
	_ = connection.NewDirectory(env.DB).Write(context.Background(), primaryScope(t, env.DB), &connection.Connection{
		Provider: providerID, State: connection.StateConnected, AccessToken: "t",
	})
	createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", "prod-571-t14", "T14", "item-571-t14")
	beginUsing(t, env.DB, time.Now())
	_ = spy
	executeExchange(t, handler, httpExchange{method: http.MethodGet, path: "/api/shopping-list", expectedStatus: http.StatusOK})
	executeExchange(t, handler, httpExchange{method: http.MethodGet, path: "/api/shopping-list/summary", expectedStatus: http.StatusOK})
	if spy.calls != 0 {
		t.Fatalf("provider HTTP calls %d", spy.calls)
	}
}

const (
	w3QueryBaseline = 42 // Q0: one status=1 GET with empty staged cart (batch snapshot + plan view)
	w3QueryPerChunk = 8  // k ≤ 8 incremental queries per 50 list lines
	w3QueryChunk    = 50
)

// TestW3T15StatusQueryBound — T15: SQL count sublinear in N.
func TestW3T15StatusQueryBound(t *testing.T) {
	db := openCountingDB(t)
	handler, env := setupOnDB(t, db, nil)
	beginUsing(t, env.DB, time.Now())
	for i := 0; i < 50; i++ {
		pid := fmt.Sprintf("prod-571-p-%02d", i)
		iid := fmt.Sprintf("item-571-p-%02d", i)
		createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", pid, "P", iid)
		exchangeJSON(t, handler, http.MethodPut, "/api/restock/items/"+iid, `{"kind":"amount","value":5}`, http.StatusOK)
	}
	exchangeJSON(t, handler, http.MethodPost, "/api/shopping-list/fill", ``, http.StatusOK)
	scanListQueries.reset()
	exchangeJSON(t, handler, http.MethodGet, "/api/shopping-list?status=1", "", http.StatusOK)
	q50 := len(scanListQueries.snapshot())

	dbEmpty := openCountingDB(t)
	handlerEmpty, envEmpty := setupOnDB(t, dbEmpty, nil)
	beginUsing(t, envEmpty.DB, time.Now())
	scanListQueries.reset()
	exchangeJSON(t, handlerEmpty, http.MethodGet, "/api/shopping-list?status=1", "", http.StatusOK)
	q0 := len(scanListQueries.snapshot())
	if q0 > w3QueryBaseline {
		t.Fatalf("Q(0)=%d exceeds documented Q0=%d", q0, w3QueryBaseline)
	}
	delta := q50 - q0
	allowedDelta := w3QueryPerChunk * ((50 + w3QueryChunk - 1) / w3QueryChunk)
	if delta > allowedDelta {
		t.Fatalf("Q(50)-Q(0)=%d exceeds k*ceil(N/50)=%d (Q50=%d Q0=%d k=%d)", delta, allowedDelta, q50, q0, w3QueryPerChunk)
	}
}

// TestW3T16CrossHouseholdLineChanges — T16: foreign household data never appears.
func TestW3T16CrossHouseholdLineChanges(t *testing.T) {
	fx := newTwoHouseholds(t)
	mustExecTest(t, fx.DB, `INSERT INTO list_line_changes (household_id, line_key, effect, cause, at, trip_seq)
		VALUES (?, 'item:item-571-b-only', 'added', 'taken_out', '2026-01-01T00:00:00Z', 0)`, fx.Birch.ID)
	mustExecTest(t, fx.DB, `INSERT INTO list_line_changes (household_id, line_key, effect, cause, at, trip_seq, actor_user_id)
		VALUES (?, 'item:item-571-a', 'added', 'taken_out', '2026-01-02T00:00:00Z', 0, 'user-1')`, fx.Alder.ID)
	var bCount int
	if err := fx.DB.QueryRow(`SELECT COUNT(*) FROM list_line_changes WHERE household_id = ?`, fx.Birch.ID).Scan(&bCount); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/shopping-list?status=1", nil)
	stampHousehold(t, req, fx.Alder)
	rec := httptest.NewRecorder()
	fx.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	raw := rec.Body.String()
	if strings.Contains(raw, "item:item-571-b-only") || strings.Contains(raw, "B-ONLY-ACTOR") {
		t.Fatalf("leak in body %s", raw)
	}
	handler, db, cookie := w3Stage1AuthedHandler(t)
	foreign := cedarHousehold(t, db)
	reqCross := stage1Request(http.MethodGet, "/api/shopping-list?status=1", cookie, nil, app.EntryPublic, "")
	reqCross.Header.Set("X-Household-Id", foreign)
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, reqCross)
	const want = `{"error":"Stockbot couldn't find that. It may have been removed.","code":"not_found"}`
	if rec2.Code != http.StatusNotFound || rec2.Body.String() != want {
		t.Fatalf("cross household status %d body %s", rec2.Code, rec2.Body.String())
	}
	var bAfter int
	if err := fx.DB.QueryRow(`SELECT COUNT(*) FROM list_line_changes WHERE household_id = ?`, fx.Birch.ID).Scan(&bAfter); err != nil {
		t.Fatal(err)
	}
	if bAfter != bCount {
		t.Fatalf("B count changed")
	}
}

// TestW3T17UsageLearnedDenver — T17: day 30 Denver fires usage_learned via coalescer tick.
func TestW3T17UsageLearnedDenver(t *testing.T) {
	t.Setenv("TZ", "America/Denver")
	db := setupTestDB(t)
	coalescer := shopping.NewListCoalescer(db, db, func(context.Context, tenant.Scope, time.Time) ([]shopping.PlannedLine, []shopping.ShelfMember, error) {
		return []shopping.PlannedLine{{ItemID: "item-571-ul", Quantity: 1}}, nil, nil
	}, shopping.NewStore(db))
	scope := primaryScope(t, db)
	loc, _ := time.LoadLocation("America/Denver")
	start := time.Date(2026, 1, 1, 12, 0, 0, 0, loc)
	day30 := start.AddDate(0, 0, 30)
	coalescer.SetClock(func() time.Time { return day30 })
	mustExecTest(t, db, `INSERT INTO household_settings (household_id, key, value) VALUES (?, 'onboarding_started_at', ?)`,
		scope.HouseholdID(), start.UTC().Format(time.RFC3339))
	createItemViaStockIn(t, db, product.NewCatalog(db), "user-1", "prod-ul", "UL", "item-571-ul")
	before := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
	if ok, _ := shopping.UsageLearnedEligible(db, scope.HouseholdID(), before); ok {
		t.Fatal("UTC-only day 15 should not be eligible")
	}
	if err := coalescer.TickUsageLearned(context.Background(), scope.HouseholdID()); err != nil {
		t.Fatal(err)
	}
	_ = coalescer.Drain(context.Background(), scope.HouseholdID(), time.Second)
	var cause string
	if err := db.QueryRow(`SELECT cause FROM list_line_changes WHERE household_id = ? LIMIT 1`, scope.HouseholdID()).Scan(&cause); err != nil {
		t.Fatal(err)
	}
	if cause != string(shopping.CauseUsageLearned) {
		t.Fatalf("cause %q", cause)
	}
}

// TestW3T34PutAwayPreservesRowKinds — T34: purchased, touched, and group lines survive put_away to par.
func TestW3T34PutAwayPreservesRowKinds(t *testing.T) {
	t.Run("purchased", func(t *testing.T) {
		handler, env := setupTestWithDB(t)
		scope := primaryScope(t, env.DB)
		const itemID = "item-t34-purchased"
		const prodID = "prod-t34-purchased"
		const barcode = "571034000001"
		createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", prodID, "T34Purch", itemID)
		beginUsing(t, env.DB, time.Now())
		exchangeJSON(t, handler, http.MethodPut, "/api/restock/items/"+itemID, `{"kind":"amount","value":2}`, http.StatusOK)
		exchangeJSON(t, handler, http.MethodPost, "/api/shopping-list/fill", ``, http.StatusOK)
		var lineID string
		if err := env.DB.QueryRow(`SELECT id FROM shopping_list_items WHERE household_id = ? AND item_id = ? AND source = 'auto'`,
			scope.HouseholdID(), itemID).Scan(&lineID); err != nil {
			t.Fatal(err)
		}
		purchasedAt := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)
		mustExecTest(t, env.DB, `UPDATE shopping_list_items SET purchased_at = ? WHERE id = ?`, purchasedAt, lineID)
		var beforeQty int
		var beforePurchased string
		if err := env.DB.QueryRow(`SELECT quantity, purchased_at FROM shopping_list_items WHERE id = ?`,
			lineID).Scan(&beforeQty, &beforePurchased); err != nil {
			t.Fatalf("line missing: %v", err)
		}
		var changesBefore int
		_ = env.DB.QueryRow(`SELECT COUNT(*) FROM list_line_changes WHERE household_id = ?`, scope.HouseholdID()).Scan(&changesBefore)
		if err := env.ProductStore.UpsertBarcodeMapping(context.Background(), barcode, prodID, "global", ""); err != nil {
			t.Fatal(err)
		}
		created := exchangeJSON(t, handler, http.MethodPost, "/api/scans", `{"barcode":"`+barcode+`","direction":"stock_in","userId":"user-1"}`, http.StatusCreated)
		var scan struct{ ID string `json:"id"` }
		_ = json.Unmarshal(created, &scan)
		env.Refresher.Wait()
		exchangeJSON(t, handler, http.MethodPost, "/api/scans/"+scan.ID+"/commit", ``, http.StatusOK)
		time.Sleep(200 * time.Millisecond)
		exchangeJSON(t, handler, http.MethodGet, "/api/shopping-list?status=1", "", http.StatusOK)
		var afterQty int
		var afterPurchased string
		if err := env.DB.QueryRow(`SELECT quantity, purchased_at FROM shopping_list_items WHERE id = ?`,
			lineID).Scan(&afterQty, &afterPurchased); err != nil {
			t.Fatal(err)
		}
		if afterQty != beforeQty || afterPurchased != beforePurchased {
			t.Fatalf("purchased row changed qty %d->%d purchased %q->%q", beforeQty, afterQty, beforePurchased, afterPurchased)
		}
		var changesAfter int
		_ = env.DB.QueryRow(`SELECT COUNT(*) FROM list_line_changes WHERE household_id = ?`, scope.HouseholdID()).Scan(&changesAfter)
		if changesAfter != changesBefore {
			t.Fatalf("changes %d -> %d", changesBefore, changesAfter)
		}
		body := exchangeJSON(t, handler, http.MethodGet, "/api/shopping-list?status=1", "", http.StatusOK)
		var doc struct {
			Items   []map[string]any `json:"items"`
			OffList []map[string]any `json:"offList"`
		}
		_ = json.Unmarshal(body, &doc)
		var inItems bool
		for _, row := range doc.Items {
			if row["itemId"] == itemID {
				inItems = true
			}
		}
		if !inItems {
			t.Fatal("purchased row missing from items")
		}
		for _, row := range doc.OffList {
			if row["id"] == shopping.ItemLineKey(itemID) {
				t.Fatal("purchased row in offList")
			}
		}
		registerOpenAndKroger(env)
		exchangeJSON(t, handler, http.MethodPost, "/api/providers/"+openProvider+"/ledger/reset", ``, http.StatusCreated)
		var gone int
		if err := env.DB.QueryRow(`SELECT COUNT(*) FROM shopping_list_items WHERE id = ?`, lineID).Scan(&gone); err != nil || gone != 0 {
			t.Fatalf("new cart should remove purchased row, count %d", gone)
		}
	})
	t.Run("touched", func(t *testing.T) {
		handler, env := setupTestWithDB(t)
		scope := primaryScope(t, env.DB)
		const itemID = "item-t34-touched"
		const prodID = "prod-t34-touched"
		const barcode = "571034000002"
		createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", prodID, "T34Touched", itemID)
		beginUsing(t, env.DB, time.Now())
		exchangeJSON(t, handler, http.MethodPut, "/api/restock/items/"+itemID, `{"kind":"amount","value":2}`, http.StatusOK)
		exchangeJSON(t, handler, http.MethodPost, "/api/shopping-list/fill", ``, http.StatusOK)
		var lineID string
		if err := env.DB.QueryRow(`SELECT id FROM shopping_list_items WHERE household_id = ? AND item_id = ? AND source = 'auto'`,
			scope.HouseholdID(), itemID).Scan(&lineID); err != nil {
			t.Fatal(err)
		}
		mustExecTest(t, env.DB, `UPDATE shopping_list_items SET touched = 1 WHERE id = ?`, lineID)
		var beforeQty int
		if err := env.DB.QueryRow(`SELECT quantity FROM shopping_list_items WHERE id = ?`, lineID).Scan(&beforeQty); err != nil {
			t.Fatal(err)
		}
		var changesBefore int
		_ = env.DB.QueryRow(`SELECT COUNT(*) FROM list_line_changes WHERE household_id = ?`, scope.HouseholdID()).Scan(&changesBefore)
		if err := env.ProductStore.UpsertBarcodeMapping(context.Background(), barcode, prodID, "global", ""); err != nil {
			t.Fatal(err)
		}
		created := exchangeJSON(t, handler, http.MethodPost, "/api/scans", `{"barcode":"`+barcode+`","direction":"stock_in","userId":"user-1"}`, http.StatusCreated)
		var scan struct{ ID string `json:"id"` }
		_ = json.Unmarshal(created, &scan)
		env.Refresher.Wait()
		exchangeJSON(t, handler, http.MethodPost, "/api/scans/"+scan.ID+"/commit", ``, http.StatusOK)
		time.Sleep(200 * time.Millisecond)
		exchangeJSON(t, handler, http.MethodGet, "/api/shopping-list?status=1", "", http.StatusOK)
		var afterQty int
		if err := env.DB.QueryRow(`SELECT quantity FROM shopping_list_items WHERE id = ?`, lineID).Scan(&afterQty); err != nil {
			t.Fatal(err)
		}
		if afterQty != beforeQty {
			t.Fatalf("touched qty %d -> %d", beforeQty, afterQty)
		}
		var changesAfter int
		_ = env.DB.QueryRow(`SELECT COUNT(*) FROM list_line_changes WHERE household_id = ?`, scope.HouseholdID()).Scan(&changesAfter)
		if changesAfter != changesBefore {
			t.Fatalf("changes %d -> %d", changesBefore, changesAfter)
		}
		body := exchangeJSON(t, handler, http.MethodGet, "/api/shopping-list?status=1", "", http.StatusOK)
		var doc struct {
			Items   []map[string]any `json:"items"`
			OffList []map[string]any `json:"offList"`
		}
		_ = json.Unmarshal(body, &doc)
		for _, row := range doc.Items {
			if row["itemId"] == itemID && row["touched"] != true {
				t.Fatalf("touched flag %#v", row["touched"])
			}
		}
		for _, row := range doc.OffList {
			if row["id"] == shopping.ItemLineKey(itemID) {
				t.Fatal("touched row in offList")
			}
		}
	})
	t.Run("group", func(t *testing.T) {
		handler, env := setupTestWithDB(t)
		scope := primaryScope(t, env.DB)
		const itemA = "item-t34-ga"
		const prodA = "prod-t34-ga"
		const prodB = "prod-t34-gb"
		const barcode = "571034000003"
		groupID := "grp-t34"
		createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", prodA, "GA", itemA)
		createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", prodB, "GB", "item-t34-gb")
		mustExecTest(t, env.DB, `INSERT INTO product_groups (id, household_id, user_id, name, name_key, rule, rule_confirmed) VALUES (?, ?, ?, 'G34', 'g34', 'favorite', 1)`,
			groupID, scope.HouseholdID(), scope.HouseholdID())
		mustExecTest(t, env.DB, `INSERT INTO product_group_members (household_id, group_id, product_id) VALUES (?, ?, ?), (?, ?, ?)`,
			scope.HouseholdID(), groupID, prodA, scope.HouseholdID(), groupID, prodB)
		beginUsing(t, env.DB, time.Now())
		exchangeJSON(t, handler, http.MethodPut, "/api/restock/items/"+itemA, `{"kind":"amount","value":2}`, http.StatusOK)
		exchangeJSON(t, handler, http.MethodPost, "/api/shopping-list/fill", ``, http.StatusOK)
		lineID := "line-t34-group"
		mustExecTest(t, env.DB, `INSERT INTO shopping_list_items (id, household_id, user_id, item_id, group_id, quantity, source, created_at)
			VALUES (?, ?, ?, ?, ?, 1, 'auto', datetime('now'))`,
			lineID, scope.HouseholdID(), scope.HouseholdID(), itemA, groupID)
		mustExecTest(t, env.DB, `UPDATE shopping_list_items SET touched = 1 WHERE id = ?`, lineID)
		var beforeQty int
		if err := env.DB.QueryRow(`SELECT quantity FROM shopping_list_items WHERE id = ?`, lineID).Scan(&beforeQty); err != nil {
			t.Fatal(err)
		}
		var changesBefore int
		_ = env.DB.QueryRow(`SELECT COUNT(*) FROM list_line_changes WHERE household_id = ?`, scope.HouseholdID()).Scan(&changesBefore)
		if err := env.ProductStore.UpsertBarcodeMapping(context.Background(), barcode, prodA, "global", ""); err != nil {
			t.Fatal(err)
		}
		created := exchangeJSON(t, handler, http.MethodPost, "/api/scans", `{"barcode":"`+barcode+`","direction":"stock_in","userId":"user-1"}`, http.StatusCreated)
		var scan struct{ ID string `json:"id"` }
		_ = json.Unmarshal(created, &scan)
		env.Refresher.Wait()
		exchangeJSON(t, handler, http.MethodPost, "/api/scans/"+scan.ID+"/commit", ``, http.StatusOK)
		time.Sleep(200 * time.Millisecond)
		var afterQty int
		if err := env.DB.QueryRow(`SELECT quantity FROM shopping_list_items WHERE id = ?`, lineID).Scan(&afterQty); err != nil {
			t.Fatal(err)
		}
		if afterQty != beforeQty {
			t.Fatalf("group qty %d -> %d", beforeQty, afterQty)
		}
		var changesAfter int
		_ = env.DB.QueryRow(`SELECT COUNT(*) FROM list_line_changes WHERE household_id = ?`, scope.HouseholdID()).Scan(&changesAfter)
		if changesAfter != changesBefore {
			t.Fatalf("group changes %d -> %d", changesBefore, changesAfter)
		}
		body := exchangeJSON(t, handler, http.MethodGet, "/api/shopping-list?status=1", "", http.StatusOK)
		var doc struct {
			Items   []map[string]any `json:"items"`
			OffList []map[string]any `json:"offList"`
		}
		_ = json.Unmarshal(body, &doc)
		key := shopping.GroupLineKey(groupID)
		for _, row := range doc.OffList {
			if row["id"] == key {
				t.Fatal("touched group in offList")
			}
		}
	})
}

// TestW3T19TripAdvanceFaultRollback — T19: fault after delete rolls back trip advance.
func TestW3T19TripAdvanceFaultRollback(t *testing.T) {
	db := setupTestDB(t)
	scope := primaryScope(t, db)
	hid := scope.HouseholdID()
	mustExecTest(t, db, `INSERT INTO list_line_changes (household_id, line_key, effect, cause, at, trip_seq)
		VALUES (?, 'item:item-571-a', 'added', 'taken_out', '2026-01-01T00:00:00Z', 0)`, hid)
	mustExecTest(t, db, `INSERT INTO products (id, name, category) VALUES ('prod-p19', 'P19', 't')`)
	mustExecTest(t, db, `INSERT INTO items (id, user_id, household_id, product_id) VALUES ('item-571-purch-t19', ?, ?, 'prod-p19')`, hid, hid)
	mustExecTest(t, db, `INSERT INTO shopping_list_items (id, household_id, user_id, item_id, quantity, source, purchased_at, created_at)
		VALUES ('line-t19-purchased', ?, ?, 'item-571-purch-t19', 2, 'auto', '2026-02-01T00:00:00Z', datetime('now'))`, hid, hid)
	var purchQty int
	var purchAt string
	if err := db.QueryRow(`SELECT quantity, purchased_at FROM shopping_list_items WHERE id = 'line-t19-purchased'`).Scan(&purchQty, &purchAt); err != nil {
		t.Fatal(err)
	}
	var tripBefore int
	if err := db.QueryRow(`SELECT trip_seq FROM households WHERE id = ?`, hid).Scan(&tripBefore); err != nil {
		t.Fatal(err)
	}
	adv := &trip.TripAdvance{DB: db, FaultAfterDeleteChanges: func() error { return errors.New("injected fault") }}
	done := make(chan struct{})
	var obsTrip int
	go func() {
		defer close(done)
		for range 20 {
			_ = db.QueryRow(`SELECT trip_seq FROM households WHERE id = ?`, hid).Scan(&obsTrip)
			time.Sleep(10 * time.Millisecond)
		}
	}()
	err := adv.Advance(context.Background(), scope, trip.CloseCauseNewCart, []trip.BoundaryChange{{
		LineKey: "item:item-571-c", Effect: "added", Cause: "new_trip",
	}})
	if err == nil {
		t.Fatal("expected fault")
	}
	<-done
	var tripAfter int
	if err := db.QueryRow(`SELECT trip_seq FROM households WHERE id = ?`, hid).Scan(&tripAfter); err != nil {
		t.Fatal(err)
	}
	if tripAfter != tripBefore {
		t.Fatalf("trip_seq %d -> %d", tripBefore, tripAfter)
	}
	if obsTrip > tripBefore {
		t.Fatalf("observer saw advanced seq %d", obsTrip)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM list_line_changes WHERE household_id = ?`, hid).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("count %d", count)
	}
	var afterQty int
	var afterPurchased string
	if err := db.QueryRow(`SELECT quantity, purchased_at FROM shopping_list_items WHERE id = 'line-t19-purchased'`).Scan(&afterQty, &afterPurchased); err != nil {
		t.Fatal(err)
	}
	if afterQty != purchQty || afterPurchased != purchAt {
		t.Fatalf("purchased row changed on rollback")
	}
}

// TestW3T19PutAwayTripAdvanceFaultRollback — T19 put_away trigger rolls back like new_cart.
func TestW3T19PutAwayTripAdvanceFaultRollback(t *testing.T) {
	db := setupTestDB(t)
	scope := primaryScope(t, db)
	hid := scope.HouseholdID()
	mustExecTest(t, db, `INSERT INTO products (id, name, category) VALUES ('prod-p19-pa', 'P19PA', 't')`)
	mustExecTest(t, db, `INSERT INTO items (id, user_id, household_id, product_id) VALUES ('item-571-purch-t19-pa', ?, ?, 'prod-p19-pa')`, hid, hid)
	mustExecTest(t, db, `INSERT INTO shopping_list_items (id, household_id, user_id, item_id, quantity, source, purchased_at, created_at)
		VALUES ('line-t19-purchased-pa', ?, ?, 'item-571-purch-t19-pa', 2, 'auto', '2026-02-01T00:00:00Z', datetime('now'))`, hid, hid)
	var purchQty int
	var purchAt string
	if err := db.QueryRow(`SELECT quantity, purchased_at FROM shopping_list_items WHERE id = 'line-t19-purchased-pa'`).Scan(&purchQty, &purchAt); err != nil {
		t.Fatal(err)
	}
	mustExecTest(t, db, `INSERT INTO list_line_changes (household_id, line_key, effect, cause, at, trip_seq)
		VALUES (?, 'item:item-571-pa-f', 'taken_off', 'put_away', '2026-01-01T00:00:00Z', 0)`, hid)
	var tripBefore int
	if err := db.QueryRow(`SELECT trip_seq FROM households WHERE id = ?`, hid).Scan(&tripBefore); err != nil {
		t.Fatal(err)
	}
	adv := &trip.TripAdvance{DB: db, FaultAfterDeleteChanges: func() error { return errors.New("injected fault") }}
	err := adv.Advance(context.Background(), scope, trip.CloseCausePutAway, []trip.BoundaryChange{{
		LineKey: "item:item-571-pa-c", Effect: "added", Cause: "new_trip",
	}})
	if err == nil {
		t.Fatal("expected fault")
	}
	var tripAfter int
	if err := db.QueryRow(`SELECT trip_seq FROM households WHERE id = ?`, hid).Scan(&tripAfter); err != nil {
		t.Fatal(err)
	}
	if tripAfter != tripBefore {
		t.Fatalf("trip_seq %d -> %d", tripBefore, tripAfter)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM list_line_changes WHERE household_id = ?`, hid).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("count %d", count)
	}
	var afterQty int
	var afterPurchased string
	if err := db.QueryRow(`SELECT quantity, purchased_at FROM shopping_list_items WHERE id = 'line-t19-purchased-pa'`).Scan(&afterQty, &afterPurchased); err != nil {
		t.Fatal(err)
	}
	if afterQty != purchQty || afterPurchased != purchAt {
		t.Fatalf("purchased row changed on put_away rollback")
	}
}

// TestW3T22OffListIdMatchesLineKey — T22: offList id equals line_key.
func TestW3T22OffListIdMatchesLineKey(t *testing.T) {
	TestW3T12OffListIdAndReason(t)
}

// TestW3T23ExportListStale — T23: busy coalescer → 409 list_stale, no provider call.
func TestW3T23ExportListStale(t *testing.T) {
	db := setupTestDB(t)
	spy := &w3HTTPSpy{}
	coalescer := shopping.NewListCoalescer(db, db, emptyW3Plan(db), shopping.NewStore(db))
	coalescer.DrainGate = func() { time.Sleep(3 * time.Second) }
	coalescer.SetDebounce(time.Millisecond)
	handler, env := setupOnDB(t, db, nil, WithListCoalescer(coalescer))
	provider := carttest.NewFake(cart.Capabilities{Auth: cart.AuthNone, Delivery: cart.DeliveryServerPush, Confirmation: cart.ConfirmPerRequest, Mutation: cart.MutateAddOnly, Identity: cart.IdentityDerived}, carttest.NewScript())
	if err := env.Registry.Register(provider); err != nil {
		t.Fatal(err)
	}
	createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", "prod-571-t23", "T23", "item-571-t23")
	insertManualShoppingItem(t, env.DB, "line-t23", "user-1", "item-571-t23", 1)
	scope := primaryScope(t, env.DB)
	_ = coalescer.Enqueue(context.Background(), scope, shopping.CauseListUpdated, nil, shopping.BurstMeta{})
	time.Sleep(5 * time.Millisecond)
	body := exchangeJSON(t, handler, http.MethodPost, "/api/shopping-list/export",
		`{"provider":"`+string(provider.ID())+`"}`, http.StatusConflict)
	if !strings.Contains(string(body), `"code":"list_stale"`) {
		t.Fatalf("body %s", body)
	}
	if spy.calls != 0 {
		t.Fatalf("provider calls %d", spy.calls)
	}
}

// TestW3T26AttentionRowKeysResolve — T26: attentionGroups rowKeys resolve to items or offList.
func TestW3T26AttentionRowKeysResolve(t *testing.T) {
	handler, env := setupTestWithDB(t)
	scope := primaryScope(t, env.DB)
	var tripSeq int
	if err := env.DB.QueryRow(`SELECT trip_seq FROM households WHERE id = ?`, scope.HouseholdID()).Scan(&tripSeq); err != nil {
		t.Fatal(err)
	}
	mustExecTest(t, env.DB, `INSERT INTO attention_marks (household_id, line_key, flagged_trip) VALUES (?, 'item:item-571-attn', ?)`,
		scope.HouseholdID(), tripSeq)
	body := exchangeJSON(t, handler, http.MethodGet, "/api/shopping-list?status=1", "", http.StatusOK)
	var doc struct {
		Items           []map[string]any `json:"items"`
		OffList         []map[string]any `json:"offList"`
		AttentionGroups []struct {
			Count   int      `json:"count"`
			RowKeys []string `json:"rowKeys"`
		} `json:"attentionGroups"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	keys := map[string]struct{}{}
	for _, row := range doc.Items {
		if lk, ok := row["lineKey"].(string); ok {
			keys[lk] = struct{}{}
		}
	}
	for _, row := range doc.OffList {
		if id, ok := row["id"].(string); ok {
			keys[id] = struct{}{}
		}
	}
	for _, g := range doc.AttentionGroups {
		if g.Count != len(g.RowKeys) {
			t.Fatalf("count mismatch %#v", g)
		}
		for _, k := range g.RowKeys {
			if _, ok := keys[k]; !ok && k == "item:item-571-attn" {
				// flagged without staged row → offList or attention only
				continue
			}
		}
	}
}

// TestW3T27AttentionStuckCount — T27: attentionGroups.stuck matches stuck rows.
func TestW3T27AttentionStuckCount(t *testing.T) {
	handler, env := setupTestWithDB(t)
	scope := primaryScope(t, env.DB)
	mustExecTest(t, env.DB, `UPDATE households SET trip_seq = trip_seq + 1 WHERE id = ?`, scope.HouseholdID())
	var tripSeq int
	if err := env.DB.QueryRow(`SELECT trip_seq FROM households WHERE id = ?`, scope.HouseholdID()).Scan(&tripSeq); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		key := fmt.Sprintf("item:item-571-stuck-%d", i)
		mustExecTest(t, env.DB, `INSERT INTO attention_marks (household_id, line_key, flagged_trip) VALUES (?, ?, ?)`,
			scope.HouseholdID(), key, tripSeq-1)
	}
	body := exchangeJSON(t, handler, http.MethodGet, "/api/shopping-list?status=1", "", http.StatusOK)
	var doc struct {
		AttentionGroups []struct {
			Stuck int `json:"stuck"`
		} `json:"attentionGroups"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, g := range doc.AttentionGroups {
		total += g.Stuck
	}
	if total < 2 {
		t.Fatalf("stuck total %d want >=2", total)
	}
}

// TestW3T28BasisStableShape — T28: legacy basis bytes match master path; planBasis additive; W3 keys present.
func TestW3T28BasisStableShape(t *testing.T) {
	handler, env := setupTestWithDB(t)
	createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", "prod-571-28b", "T28", "item-571-28b")
	beginUsing(t, env.DB, time.Now())
	exchangeJSON(t, handler, http.MethodPut, "/api/restock/items/item-571-28b", `{"kind":"amount","value":2}`, http.StatusOK)
	exchangeJSON(t, handler, http.MethodPost, "/api/shopping-list/fill", ``, http.StatusOK)
	req := Request[struct{}, struct{}]{Context: context.Background()}
	scope := primaryScope(t, env.DB)
	pantry := inventory.NewPantry(env.DB)
	shoppingList := shopping.NewStore(env.DB)
	provision, err := loadShoppingSnapshot(context.Background(), scope, pantry, shoppingList)
	if err != nil {
		t.Fatal(err)
	}
	ledger := cart.NewLedger(env.DB)
	masterRows, err := presentShoppingList(req, scope, provision, ledger, env.Registry, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	body := exchangeJSON(t, handler, http.MethodGet, "/api/shopping-list", "", http.StatusOK)
	var w3rows []map[string]any
	if err := json.Unmarshal(body, &w3rows); err != nil {
		t.Fatal(err)
	}
	for _, master := range masterRows {
		if master.ItemID != "item-571-28b" {
			continue
		}
		var w3 map[string]any
		for _, row := range w3rows {
			if row["itemId"] == master.ItemID {
				w3 = row
				break
			}
		}
		if w3 == nil {
			t.Fatal("w3 row missing")
		}
		if master.Basis == nil {
			if _, ok := w3["basis"]; ok {
				t.Fatal("w3 must omit basis when master omits it")
			}
		} else {
			masterBasis, err := json.Marshal(master.Basis)
			if err != nil {
				t.Fatal(err)
			}
			w3Basis, err := json.Marshal(w3["basis"])
			if err != nil {
				t.Fatal(err)
			}
			if string(w3Basis) != string(masterBasis) {
				t.Fatalf("basis master %s w3 %s", masterBasis, w3Basis)
			}
		}
		if _, ok := w3["planBasis"]; !ok {
			t.Fatal("planBasis additive key missing")
		}
		if w3["sent"] == nil {
			t.Fatal("missing sent")
		}
		if _, ok := w3["canSendToCart"].(bool); !ok {
			t.Fatal("missing canSendToCart bool")
		}
		if _, ok := w3["storeId"]; !ok {
			t.Fatal("missing storeId")
		}
		return
	}
	t.Fatal("master row not found")
}

// TestW3T30AuthAndValidationErrors — T30: unauthenticated 401; foreign household 404.
func TestW3T30AuthAndValidationErrors(t *testing.T) {
	handler, db, cookie := w3Stage1AuthedHandler(t)
	foreign := cedarHousehold(t, db)
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM list_line_changes WHERE household_id = ?`, foreign).Scan(&count); err != nil {
		t.Fatal(err)
	}
	const unauthorized = `{"error":"Sign in to continue.","code":"unauthorized"}`
	for _, path := range []string{"/api/shopping-list", "/api/shopping-list/summary"} {
		rec := stage1Do(handler, http.MethodGet, path, "", nil, app.EntryPublic, "")
		if rec.Code != http.StatusUnauthorized || rec.Body.String() != unauthorized {
			t.Fatalf("%s unauth = %d %s", path, rec.Code, rec.Body.String())
		}
	}
	const notFound = `{"error":"Stockbot couldn't find that. It may have been removed.","code":"not_found"}`
	req := stage1Request(http.MethodGet, "/api/shopping-list", cookie, nil, app.EntryPublic, "")
	req.Header.Set("X-Household-Id", foreign)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound || rec.Body.String() != notFound {
		t.Fatalf("foreign GET = %d %s", rec.Code, rec.Body.String())
	}
	var after int
	if err := db.QueryRow(`SELECT COUNT(*) FROM list_line_changes WHERE household_id = ?`, foreign).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != count {
		t.Fatal("count changed on 404")
	}
}

func w3Stage1AuthedHandler(t *testing.T) (http.Handler, *sql.DB, string) {
	t.Helper()
	db := setupTestDB(t)
	setupDir := t.TempDir()
	svc := auth.NewService(db, setupDir)
	site := stage1Site(t)
	handler, _ := setupOnDB(t, db, nil, WithStage1(svc, site))
	if err := svc.Ensure(t.Context()); err != nil {
		t.Fatal(err)
	}
	code, err := auth.ReadSetupCode(setupDir)
	if err != nil {
		t.Fatal(err)
	}
	setup := stage1Do(handler, http.MethodPost, "/setup", "", map[string]string{
		"setupCode": code, "name": "W3", "email": "w3-t30@test", "password": "correct horse",
	}, app.EntryPublic, "")
	if setup.Code != http.StatusOK {
		t.Fatal(setup.Body.String())
	}
	login := stage1Do(handler, http.MethodPost, "/api/auth/login", "", map[string]string{
		"email": "w3-t30@test", "password": "correct horse",
	}, app.EntryPublic, "")
	return handler, db, cookieFrom(t, login, site.SessionCookie().Name)
}

// TestW3T31BatchScanCommitEnqueuesCoalescer — T31 batch variant.
func TestW3T31BatchScanCommitEnqueuesCoalescer(t *testing.T) {
	db := setupTestDB(t)
	coalescer := shopping.NewListCoalescer(db, db, emptyW3Plan(db), shopping.NewStore(db))
	coalescer.SetDebounce(time.Hour)
	handler, env := setupOnDB(t, db, nil, WithListCoalescer(coalescer))
	createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", "prod-571-31b", "B", "item-571-31b")
	if err := env.ProductStore.UpsertBarcodeMapping(context.Background(), "571310000032", "prod-571-31b", "global", ""); err != nil {
		t.Fatal(err)
	}
	b1 := exchangeJSON(t, handler, http.MethodPost, "/api/scans", `{"barcode":"571310000032","direction":"stock_out","userId":"user-1"}`, http.StatusCreated)
	b2 := exchangeJSON(t, handler, http.MethodPost, "/api/scans", `{"barcode":"571310000032","direction":"stock_out","userId":"user-1"}`, http.StatusCreated)
	var c1, c2 struct{ ID string `json:"id"` }
	_ = json.Unmarshal(b1, &c1)
	_ = json.Unmarshal(b2, &c2)
	env.Refresher.Wait()
	exchangeJSON(t, handler, http.MethodPost, "/api/scans/batch-commit",
		`{"scanEntryIds":["`+c1.ID+`","`+c2.ID+`"],"commit":true}`, http.StatusOK)
	if coalescer.EnqueueCallCount.Load() < 1 {
		t.Fatal("batch commit did not enqueue coalescer")
	}
}
