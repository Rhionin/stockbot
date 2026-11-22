package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/shopping"
	"github.com/Rhionin/stockbot/internal/supply"
	"github.com/Rhionin/stockbot/internal/tenant"
)

func TestW3ZeroForTripLineDisplayNameHTTP(t *testing.T) {
	handler, env := setupTestWithDB(t)
	registerProductionShapeKroger(env)
	const itemID = "item-679-line-display"
	const lineID = "line-679-line-display"
	lineKey := shopping.ItemLineKey(itemID)
	createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", "prod-679-line-display", "Hand Added", itemID)
	beginUsing(t, env.DB, time.Now())
	exchangeJSON(t, handler, http.MethodPut, "/api/restock/items/"+itemID, `{"kind":"amount","value":2}`, http.StatusOK)
	exchangeJSON(t, handler, http.MethodPost, "/api/shopping-list/fill", ``, http.StatusOK)
	mustExecTest(t, env.DB, `UPDATE shopping_list_items SET id = ? WHERE household_id = ? AND item_id = ?`,
		lineID, primaryScope(t, env.DB).HouseholdID(), itemID)
	exchangeJSON(t, handler, http.MethodPut, "/api/shopping-list/items/"+lineID+"/adjustment?provider=kroger",
		`{"adjustment":0}`, http.StatusOK)
	batchOffListProductNamesHook = func(context.Context, *sql.DB, tenant.Scope, map[string]supply.LinePlanView) (map[string]string, error) {
		return map[string]string{}, nil
	}
	t.Cleanup(func() { batchOffListProductNamesHook = nil })
	assertOffListReasonHTTP(t, handler, lineKey, "zero_for_trip", "Hand Added")
}

func TestW3ApplyZeroForTripErrorNoPartialBodyHTTP(t *testing.T) {
	handler, env := setupTestWithDB(t)
	registerProductionShapeKroger(env)
	const itemID = "item-679-g2"
	const lineID = "line-679-g2"
	createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", "prod-679-g2", "G2 Product", itemID)
	beginUsing(t, env.DB, time.Now())
	exchangeJSON(t, handler, http.MethodPut, "/api/restock/items/"+itemID, `{"kind":"amount","value":2}`, http.StatusOK)
	exchangeJSON(t, handler, http.MethodPost, "/api/shopping-list/fill", ``, http.StatusOK)
	mustExecTest(t, env.DB, `UPDATE shopping_list_items SET id = ? WHERE household_id = ? AND item_id = ?`,
		lineID, primaryScope(t, env.DB).HouseholdID(), itemID)
	exchangeJSON(t, handler, http.MethodPut, "/api/shopping-list/items/"+lineID+"/adjustment?provider=kroger",
		`{"adjustment":0}`, http.StatusOK)
	applyZeroForTripTestErr = fmt.Errorf("injected applyZeroForTrip fault (G2)")
	t.Cleanup(func() { applyZeroForTripTestErr = nil })
	scope := primaryScope(t, env.DB)
	req := httptest.NewRequest(http.MethodGet, "/api/shopping-list?status=1&provider=kroger", nil)
	req.Header.Set("X-Household-Id", scope.HouseholdID())
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status %d want 500 (G2 mutant returns 200)", rr.Code)
	}
	var doc struct {
		OffList []map[string]any `json:"offList"`
		Items   []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &doc); err == nil {
		for _, row := range doc.OffList {
			if row["offListReason"] == "zero_for_trip" {
				t.Fatalf("partial zero_for_trip offList row on error: %#v", row)
			}
		}
	}
}

func TestW3ZeroForTripUnnamedOnListNotFlaggedHTTP(t *testing.T) {
	handler, env := setupTestWithDB(t)
	registerProductionShapeKroger(env)
	scope := primaryScope(t, env.DB)
	hid := scope.HouseholdID()
	const itemID = "item-679-zft-attn"
	const lineID = "line-679-zft-attn"
	lineKey := shopping.ItemLineKey(itemID)
	if _, err := env.DB.Exec(`INSERT INTO products (id, name, category) VALUES ('prod-679-noname', '', 'c')`); err != nil {
		t.Fatal(err)
	}
	if _, err := env.DB.Exec(`INSERT INTO items (id, user_id, household_id, product_id) VALUES (?, ?, ?, 'prod-679-noname')`, itemID, hid, hid); err != nil {
		t.Fatal(err)
	}
	if _, err := env.DB.Exec(`INSERT INTO shopping_list_items (id, household_id, user_id, item_id, quantity, source, created_at)
		VALUES (?, ?, ?, ?, 2, 'manual', datetime('now'))`, lineID, hid, hid, itemID); err != nil {
		t.Fatal(err)
	}
	beginUsing(t, env.DB, time.Now())
	var tripSeq int
	if err := env.DB.QueryRow(`SELECT trip_seq FROM households WHERE id = ?`, hid).Scan(&tripSeq); err != nil {
		t.Fatal(err)
	}
	seedCartLineFailedSend(t, env.DB, hid, itemID, "api_error")
	exchangeJSON(t, handler, http.MethodPut, "/api/shopping-list/items/"+lineID+"/adjustment?provider=kroger",
		`{"adjustment":0}`, http.StatusOK)
	body := exchangeJSON(t, handler, http.MethodGet, "/api/shopping-list?status=1&provider=kroger", "", http.StatusOK)
	var doc struct {
		Items           []map[string]any `json:"items"`
		OffList         []map[string]any `json:"offList"`
		AttentionGroups []map[string]any `json:"attentionGroups"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	var onList bool
	for _, row := range doc.Items {
		if row["lineKey"] == lineKey {
			onList = true
			if na, ok := row["needsAttention"]; ok && na != nil {
				t.Fatalf("unnamed active zero on list must not be flagged (W3 §5a Q14); needsAttention %#v", na)
			}
		}
	}
	if !onList {
		t.Fatal("unnamed skipped line stays on items")
	}
	for _, row := range doc.OffList {
		if row["id"] == lineKey {
			t.Fatal("unnamed line must not be offList")
		}
	}
	for _, g := range doc.AttentionGroups {
		for _, k := range g["rowKeys"].([]any) {
			if k == lineKey {
				t.Fatalf("unnamed zero_for_trip key must not appear in attentionGroups %#v", g)
			}
		}
	}
	sumBody := exchangeJSON(t, handler, http.MethodGet, "/api/shopping-list/summary", "", http.StatusOK)
	var sum struct {
		NeedsAttention int `json:"needsAttention"`
	}
	if err := json.Unmarshal(sumBody, &sum); err != nil {
		t.Fatal(err)
	}
	if sum.NeedsAttention != 0 {
		t.Fatalf("needsAttention summary %d want 0 (mutant re-flag)", sum.NeedsAttention)
	}
}

func TestW3SkippedActiveZeroOnListExemptFromAttentionHTTP(t *testing.T) {
	handler, env := setupTestWithDB(t)
	registerProductionShapeKroger(env)
	scope := primaryScope(t, env.DB)
	hid := scope.HouseholdID()
	const itemID = "item-679-zft-skip-0"
	const lineID = "line-679-zft-skip-0"
	lineKey := shopping.ItemLineKey(itemID)
	createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", "prod-679-skip-0", "Skip Zero", itemID)
	beginUsing(t, env.DB, time.Now())
	exchangeJSON(t, handler, http.MethodPut, "/api/restock/items/"+itemID, `{"kind":"amount","value":2}`, http.StatusOK)
	exchangeJSON(t, handler, http.MethodPost, "/api/shopping-list/fill", ``, http.StatusOK)
	mustExecTest(t, env.DB, `UPDATE shopping_list_items SET id = ? WHERE household_id = ? AND item_id = ?`,
		lineID, hid, itemID)
	var tripSeq int
	if err := env.DB.QueryRow(`SELECT trip_seq FROM households WHERE id = ?`, hid).Scan(&tripSeq); err != nil {
		t.Fatal(err)
	}
	mustExecTest(t, env.DB, `INSERT INTO staged_cart_skips (household_id, user_id, item_id, expires_trip) VALUES (?, ?, ?, ?)`,
		hid, hid, itemID, tripSeq+1)
	seedCartLineFailedSend(t, env.DB, hid, itemID, "api_error")
	exchangeJSON(t, handler, http.MethodPut, "/api/shopping-list/items/"+lineID+"/adjustment?provider=kroger",
		`{"adjustment":0}`, http.StatusOK)
	body := exchangeJSON(t, handler, http.MethodGet, "/api/shopping-list?status=1&provider=kroger", "", http.StatusOK)
	var doc struct {
		Items           []map[string]any `json:"items"`
		AttentionGroups []map[string]any `json:"attentionGroups"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	var onList bool
	for _, row := range doc.Items {
		if row["lineKey"] == lineKey {
			onList = true
			if na, ok := row["needsAttention"]; ok && na != nil {
				t.Fatalf("skipped active zero on list must stay exempt (M1a); needsAttention %#v", na)
			}
		}
	}
	if !onList {
		t.Fatal("removed skip + active zero must stay on items[]")
	}
	for _, g := range doc.AttentionGroups {
		for _, k := range g["rowKeys"].([]any) {
			if k == lineKey {
				t.Fatalf("M1a: key must not be in attentionGroups %#v", g)
			}
		}
	}
	sumBody := exchangeJSON(t, handler, http.MethodGet, "/api/shopping-list/summary", "", http.StatusOK)
	var sum struct {
		NeedsAttention int `json:"needsAttention"`
	}
	if err := json.Unmarshal(sumBody, &sum); err != nil {
		t.Fatal(err)
	}
	if sum.NeedsAttention != 0 {
		t.Fatalf("summary needsAttention %d want 0 (M1a mutant)", sum.NeedsAttention)
	}
}

func TestW3ActiveZeroOffListExemptFromAttentionHTTP(t *testing.T) {
	handler, env := setupTestWithDB(t)
	registerProductionShapeKroger(env)
	scope := primaryScope(t, env.DB)
	hid := scope.HouseholdID()
	const itemID = "item-679-zft-exempt"
	const lineID = "line-679-zft-exempt"
	lineKey := shopping.ItemLineKey(itemID)
	createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", "prod-679-exempt", "Exempt Trip", itemID)
	beginUsing(t, env.DB, time.Now())
	exchangeJSON(t, handler, http.MethodPut, "/api/restock/items/"+itemID, `{"kind":"amount","value":2}`, http.StatusOK)
	exchangeJSON(t, handler, http.MethodPost, "/api/shopping-list/fill", ``, http.StatusOK)
	mustExecTest(t, env.DB, `UPDATE shopping_list_items SET id = ? WHERE household_id = ? AND item_id = ?`,
		lineID, hid, itemID)
	var tripSeq int
	if err := env.DB.QueryRow(`SELECT trip_seq FROM households WHERE id = ?`, hid).Scan(&tripSeq); err != nil {
		t.Fatal(err)
	}
	seedCartLineFailedSend(t, env.DB, hid, itemID, "api_error")
	exchangeJSON(t, handler, http.MethodPut, "/api/shopping-list/items/"+lineID+"/adjustment?provider=kroger",
		`{"adjustment":0}`, http.StatusOK)
	body := exchangeJSON(t, handler, http.MethodGet, "/api/shopping-list?status=1&provider=kroger", "", http.StatusOK)
	var doc struct {
		Items           []map[string]any `json:"items"`
		OffList         []map[string]any `json:"offList"`
		AttentionGroups []map[string]any `json:"attentionGroups"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	for _, row := range doc.Items {
		if row["lineKey"] == lineKey {
			t.Fatalf("active zero line must leave items[] before attention read; still %#v", row)
		}
	}
	var offRow map[string]any
	for _, row := range doc.OffList {
		if row["id"] == lineKey {
			offRow = row
			break
		}
	}
	if offRow == nil || offRow["offListReason"] != "zero_for_trip" {
		t.Fatalf("missing zero_for_trip offList %#v", offRow)
	}
	if na, ok := offRow["needsAttention"]; ok && na != nil {
		t.Fatalf("off-list active zero must not be flagged (§8); needsAttention %#v", na)
	}
	for _, g := range doc.AttentionGroups {
		for _, k := range g["rowKeys"].([]any) {
			if k == lineKey {
				t.Fatalf("active zero must be absent from attentionGroups %#v", g)
			}
		}
	}
	sumBody := exchangeJSON(t, handler, http.MethodGet, "/api/shopping-list/summary", "", http.StatusOK)
	var sum struct {
		NeedsAttention int `json:"needsAttention"`
	}
	if err := json.Unmarshal(sumBody, &sum); err != nil {
		t.Fatal(err)
	}
	if sum.NeedsAttention != 0 {
		t.Fatalf("summary needsAttention %d want 0 (mutant: off-list-only exemption)", sum.NeedsAttention)
	}
}

func TestW3NotOnListSkipsFlaggedRemovedHTTP(t *testing.T) {
	handler, env := setupTestWithDB(t)
	scope := primaryScope(t, env.DB)
	hid := scope.HouseholdID()
	const itemID = "item-677-flag-removed"
	lineKey := shopping.ItemLineKey(itemID)
	createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", "prod-677-fr", "FR Product", itemID)
	beginUsing(t, env.DB, time.Now())
	var tripSeq int
	if err := env.DB.QueryRow(`SELECT trip_seq FROM households WHERE id = ?`, hid).Scan(&tripSeq); err != nil {
		t.Fatal(err)
	}
	mustExecTest(t, env.DB, `INSERT INTO attention_marks (household_id, line_key, flagged_trip) VALUES (?, ?, ?)`,
		hid, lineKey, tripSeq)
	mustExecTest(t, env.DB, `INSERT INTO staged_cart_skips (household_id, user_id, item_id, expires_trip) VALUES (?, ?, ?, ?)`,
		hid, hid, itemID, tripSeq+1)
	body := exchangeJSON(t, handler, http.MethodGet, "/api/shopping-list?status=1", "", http.StatusOK)
	var doc struct {
		OffList         []map[string]any `json:"offList"`
		AttentionGroups []map[string]any `json:"attentionGroups"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	var removedRow map[string]any
	for _, row := range doc.OffList {
		if row["id"] == lineKey && row["offListReason"] == "removed" {
			removedRow = row
			break
		}
	}
	if removedRow == nil {
		t.Fatal("missing removed offList row")
	}
	if na, ok := removedRow["needsAttention"]; ok && na != nil {
		t.Fatalf("mark-only removed row must not be flagged (D1); needsAttention %#v", na)
	}
	for _, g := range doc.AttentionGroups {
		for _, k := range g["rowKeys"].([]any) {
			if k == lineKey {
				t.Fatalf("removed mark-only key must not appear in attentionGroups %#v", g)
			}
		}
	}
	sumBody := exchangeJSON(t, handler, http.MethodGet, "/api/shopping-list/summary", "", http.StatusOK)
	var sum struct {
		NotOnList      int `json:"notOnList"`
		NeedsAttention int `json:"needsAttention"`
	}
	if err := json.Unmarshal(sumBody, &sum); err != nil {
		t.Fatal(err)
	}
	if sum.NotOnList != 1 {
		t.Fatalf("notOnList %d want 1", sum.NotOnList)
	}
	if sum.NeedsAttention != 0 {
		t.Fatalf("needsAttention %d want 0", sum.NeedsAttention)
	}
}

func TestW3FlaggedTakenOffExcludedFromNotOnListHTTP(t *testing.T) {
	handler, env := setupTestWithDB(t)
	registerProductionShapeKroger(env)
	scope := primaryScope(t, env.DB)
	hid := scope.HouseholdID()
	const itemID = "item-679-takenoff-flag"
	lineKey := shopping.ItemLineKey(itemID)
	createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", "prod-679-tof", "Taken Off", itemID)
	beginUsing(t, env.DB, time.Now())
	exchangeJSON(t, handler, http.MethodPut, "/api/restock/items/"+itemID, `{"kind":"amount","value":2}`, http.StatusOK)
	exchangeJSON(t, handler, http.MethodPost, "/api/shopping-list/fill", ``, http.StatusOK)
	var tripSeq int
	if err := env.DB.QueryRow(`SELECT trip_seq FROM households WHERE id = ?`, hid).Scan(&tripSeq); err != nil {
		t.Fatal(err)
	}
	mustExecTest(t, env.DB, `DELETE FROM shopping_list_items WHERE household_id = ? AND item_id = ?`, hid, itemID)
	mustExecTest(t, env.DB, `DELETE FROM list_line_changes WHERE household_id = ? AND line_key = ?`, hid, lineKey)
	mustExecTest(t, env.DB, `INSERT INTO list_line_changes (household_id, line_key, effect, reason, cause, from_qty, to_qty, at, trip_seq)
		VALUES (?, ?, 'taken_off', 'on_hand_meets_par', 'put_away', 1, 0, datetime('now'), ?)`, hid, lineKey, tripSeq)
	seedCartLineFailedSend(t, env.DB, hid, itemID, "api_error")
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
	if row == nil || row["offListReason"] != "taken_off" {
		t.Fatalf("missing taken_off row %#v", row)
	}
	if row["needsAttention"] == nil {
		t.Fatal("send_failed fact must flag taken_off row")
	}
	sumBody := exchangeJSON(t, handler, http.MethodGet, "/api/shopping-list/summary", "", http.StatusOK)
	var sum struct {
		NotOnList      int `json:"notOnList"`
		NeedsAttention int `json:"needsAttention"`
	}
	if err := json.Unmarshal(sumBody, &sum); err != nil {
		t.Fatal(err)
	}
	if sum.NotOnList != 0 {
		t.Fatalf("flagged taken_off must not count in notOnList; got %d", sum.NotOnList)
	}
	if sum.NeedsAttention != 1 {
		t.Fatalf("needsAttention %d want 1", sum.NeedsAttention)
	}
}
