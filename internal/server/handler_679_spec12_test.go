package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/shopping"
	"github.com/Rhionin/stockbot/internal/trip"
)

// TestSpec12_09_HTTP_ActiveZeroAfterAdvanceInItems — spec §12 test 9 GET contract.
func TestSpec12_09_HTTP_ActiveZeroAfterAdvanceInItems(t *testing.T) {
	handler, env := setupTestWithDB(t)
	registerOpenAndKroger(env)
	const itemID = "item-http9"
	const lineID = "line-http9"
	const lineKey = "item:" + itemID
	createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", "prod-http9", "Http9", itemID)
	beginUsing(t, env.DB, time.Now())
	exchangeJSON(t, handler, http.MethodPut, "/api/restock/items/"+itemID, `{"kind":"amount","value":2}`, http.StatusOK)
	exchangeJSON(t, handler, http.MethodPost, "/api/shopping-list/fill", ``, http.StatusOK)
	mustExecTest(t, env.DB, `UPDATE shopping_list_items SET id = ? WHERE household_id = ? AND item_id = ?`,
		lineID, primaryScope(t, env.DB).HouseholdID(), itemID)
	exchangeJSON(t, handler, http.MethodPut, "/api/shopping-list/items/"+lineID+"/adjustment?provider=kroger",
		`{"adjustment":0}`, http.StatusOK)
	exchangeJSON(t, handler, http.MethodPost, "/api/providers/"+openProvider+"/ledger/reset", ``, http.StatusCreated)
	body := exchangeJSON(t, handler, http.MethodGet, "/api/shopping-list?status=1&provider=kroger", "", http.StatusOK)
	var doc struct {
		Items   []map[string]any `json:"items"`
		OffList []map[string]any `json:"offList"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	var inItems bool
	for _, row := range doc.Items {
		if row["lineKey"] == lineKey {
			inItems = true
			lc, ok := row["lastChange"].(map[string]any)
			if !ok {
				t.Fatalf("missing lastChange %#v", row)
			}
			if lc["effect"] != "added" || lc["cause"] != "new_trip" {
				t.Fatalf("lastChange %#v", lc)
			}
		}
		if row["lineKey"] == lineKey && row["offListReason"] != nil {
			t.Fatalf("item should not carry offListReason")
		}
	}
	if !inItems {
		t.Fatal("line not in items after lapse")
	}
	for _, row := range doc.OffList {
		if row["id"] == lineKey {
			t.Fatalf("line still in offList: %#v", row)
		}
	}
}

// TestSpec12_07_HTTP_LegacyZeroNullZeroForTrip — spec §12 test 7 GET legacy nulls.
func TestSpec12_07_HTTP_LegacyZeroNullZeroForTrip(t *testing.T) {
	handler, env := setupTestWithDB(t)
	registerProductionShapeKroger(env)
	scope := primaryScope(t, env.DB)
	hid := scope.HouseholdID()
	const itemID = "item-leg-http"
	const lineID = "line-leg-http"
	lineKey := shopping.ItemLineKey(itemID)
	createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", "prod-leg-http", "Legacy", itemID)
	beginUsing(t, env.DB, time.Now())
	mustExecTest(t, env.DB, `INSERT INTO shopping_list_items (id, household_id, user_id, item_id, quantity, source, created_at)
		VALUES (?, ?, ?, ?, 1, 'auto', datetime('now'))`, lineID, hid, hid, itemID)
	mustExecTest(t, env.DB, `INSERT INTO shopping_list_entry_adjustments (household_id, entry_id, provider_id, quantity)
		VALUES (?, ?, 'kroger', 0)`, hid, lineID)
	body := exchangeJSON(t, handler, http.MethodGet, "/api/shopping-list?status=1&provider=kroger", "", http.StatusOK)
	var doc struct {
		OffList []map[string]any `json:"offList"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	var row map[string]any
	for _, r := range doc.OffList {
		if r["id"] == lineKey {
			row = r
			break
		}
	}
	if row == nil {
		t.Fatal("missing legacy offList row")
	}
	if row["offListReason"] != "zero_for_trip" {
		t.Fatalf("reason %#v", row["offListReason"])
	}
	zft, ok := row["zeroForTrip"].(map[string]any)
	if !ok {
		t.Fatal("zeroForTrip missing")
	}
	for _, key := range []string{"actorUserId", "actorDisplayName", "setAt"} {
		v, exists := zft[key]
		if !exists {
			t.Fatalf("zeroForTrip missing key %q", key)
		}
		if v != nil {
			t.Fatalf("legacy %q = %#v want null", key, v)
		}
	}
}

// TestSpec12_12_MassStockInFaultRollback — spec §12 test 12 put_away trigger.
func TestSpec12_12_MassStockInFaultRollback(t *testing.T) {
	handler, env := setupTestWithDB(t)
	registerOpenAndKroger(env)
	scope := primaryScope(t, env.DB)
	hid := scope.HouseholdID()
	const itemID = "item-fault-pa"
	const lineID = "line-fault-pa"
	const lineKey = "item:" + itemID
	createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", "prod-fault-pa", "FaultPA", itemID)
	beginUsing(t, env.DB, time.Now())
	exchangeJSON(t, handler, http.MethodPut, "/api/restock/items/"+itemID, `{"kind":"amount","value":2}`, http.StatusOK)
	exchangeJSON(t, handler, http.MethodPost, "/api/shopping-list/fill", ``, http.StatusOK)
	mustExecTest(t, env.DB, `UPDATE shopping_list_items SET id = ? WHERE household_id = ? AND item_id = ?`,
		lineID, hid, itemID)
	exchangeJSON(t, handler, http.MethodPut, "/api/shopping-list/items/"+lineID+"/adjustment?provider=kroger",
		`{"adjustment":0}`, http.StatusOK)
	base := time.Date(2026, 1, 10, 10, 0, 0, 0, time.UTC)
	mustExecTest(t, env.DB, `INSERT INTO staged_cart_skips (household_id, user_id, item_id, expires_trip, removed_at)
		VALUES (?, ?, 'item-pa-skip', 9, ?)`, hid, hid, base.Add(20*time.Minute).Format(time.RFC3339Nano))
	var tripBefore, adjBefore, changesBefore int
	if err := env.DB.QueryRow(`SELECT trip_seq FROM households WHERE id = ?`, hid).Scan(&tripBefore); err != nil {
		t.Fatal(err)
	}
	if err := env.DB.QueryRow(`SELECT COUNT(*) FROM shopping_list_entry_adjustments WHERE entry_id = ?`, lineID).Scan(&adjBefore); err != nil {
		t.Fatal(err)
	}
	if err := env.DB.QueryRow(`SELECT COUNT(*) FROM list_line_changes WHERE household_id = ?`, hid).Scan(&changesBefore); err != nil {
		t.Fatal(err)
	}
	adjSnap := spec12SnapAdj(t, env.DB, hid, lineID)

	restore := trip.SetFaultAfterAdvanceSeqForTest(func() error {
		return errors.New("injected fault after advance seq (put_away)")
	})
	t.Cleanup(restore)

	for i := 0; i < 5; i++ {
		at := base.Add(time.Duration(i) * time.Minute).Format(time.RFC3339Nano)
		pid := fmt.Sprintf("prod-pa-f-%d", i)
		item := fmt.Sprintf("item-pa-f-%d", i)
		mustExecTest(t, env.DB, `INSERT INTO products (id, name, category) VALUES (?, ?, 't')`, pid, pid)
		mustExecTest(t, env.DB, `INSERT INTO items (id, user_id, household_id, product_id) VALUES (?, ?, ?, ?)`, item, hid, hid, pid)
		mustExecTest(t, env.DB, `INSERT INTO stock_moves (id, household_id, item_id, product_id, direction, quantity, at, source, tracked, created_at)
			VALUES (?, ?, ?, ?, 'in', 1, ?, 'scan', 1, ?)`, fmt.Sprintf("mv-pa-f-%d", i), hid, item, pid, at, at)
	}
	tx, err := env.DB.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	closeAt := base.Add(9 * time.Minute)
	if err := trip.MaybeCloseMassStockIn(context.Background(), scope, tx, closeAt); err == nil {
		t.Fatal("expected fault from mass stock-in")
	}
	_ = tx.Rollback()

	var tripAfter, adjAfter, changesAfter int
	if err := env.DB.QueryRow(`SELECT trip_seq FROM households WHERE id = ?`, hid).Scan(&tripAfter); err != nil {
		t.Fatal(err)
	}
	if err := env.DB.QueryRow(`SELECT COUNT(*) FROM shopping_list_entry_adjustments WHERE entry_id = ?`, lineID).Scan(&adjAfter); err != nil {
		t.Fatal(err)
	}
	if err := env.DB.QueryRow(`SELECT COUNT(*) FROM list_line_changes WHERE household_id = ?`, hid).Scan(&changesAfter); err != nil {
		t.Fatal(err)
	}
	if tripAfter != tripBefore || adjAfter != adjBefore || changesAfter != changesBefore {
		t.Fatalf("rollback trip %d->%d adj %d->%d changes %d->%d", tripBefore, tripAfter, adjBefore, adjAfter, changesBefore, changesAfter)
	}
	if spec12SnapAdj(t, env.DB, hid, lineID) != adjSnap {
		t.Fatal("adjustment row changed on fault rollback")
	}
}

func spec12SnapAdj(t *testing.T, db *sql.DB, hid, lineID string) string {
	t.Helper()
	var qty int
	var exp sql.NullInt64
	var actor, setAt sql.NullString
	if err := db.QueryRow(`SELECT quantity, expires_trip, actor_user_id, set_at FROM shopping_list_entry_adjustments
		WHERE household_id = ? AND entry_id = ?`, hid, lineID).Scan(&qty, &exp, &actor, &setAt); err != nil {
		return ""
	}
	return fmt.Sprintf("q=%d exp=%v actor=%v set=%v", qty, exp, actor, setAt)
}