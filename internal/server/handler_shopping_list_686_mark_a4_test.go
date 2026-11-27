package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/attention"
	"github.com/Rhionin/stockbot/internal/shopping"
)

func TestStatusAttentionMarkOnlyNeverFlagsHTTP(t *testing.T) {
	t.Run("on_list", func(t *testing.T) {
		handler, env := setupTestWithDB(t)
		registerProductionShapeKroger(env)
		scope, lineKey := shortItemBaseline686(t, handler, env, "item-h686-a1", "prod-h686-a1", "A1")
		hid := scope.HouseholdID()
		var tripSeq int
		if err := env.DB.QueryRow(`SELECT trip_seq FROM households WHERE id = ?`, hid).Scan(&tripSeq); err != nil {
			t.Fatal(err)
		}
		mustExecTest(t, env.DB, `INSERT INTO attention_marks (household_id, line_key, flagged_trip) VALUES (?, ?, ?)`,
			hid, lineKey, tripSeq)
		doc := statusGET686(t, handler, hid, h686Provider)
		var found bool
		for _, row := range statusItems686(doc) {
			if row["lineKey"] == lineKey {
				found = true
				if row["needsAttention"] != nil {
					t.Fatalf("mark-only on list must be null %#v", row["needsAttention"])
				}
			}
		}
		if !found {
			t.Fatalf("missing on-list row %q", lineKey)
		}
		sumBody := exchangeJSON(t, handler, http.MethodGet, "/api/shopping-list/summary", "", http.StatusOK)
		var sum struct {
			NeedsAttention int `json:"needsAttention"`
		}
		if err := json.Unmarshal(sumBody, &sum); err != nil {
			t.Fatal(err)
		}
		if sum.NeedsAttention != 0 {
			t.Fatalf("summary needsAttention %d want 0", sum.NeedsAttention)
		}
	})
	t.Run("removed", func(t *testing.T) {
		handler, env := setupTestWithDB(t)
		registerProductionShapeKroger(env)
		scope := primaryScope(t, env.DB)
		hid := scope.HouseholdID()
		const itemID = "item-h686-a1-r"
		lineKey := shopping.ItemLineKey(itemID)
		createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", "prod-h686-a1-r", "A1R", itemID)
		beginUsing(t, env.DB, time.Now())
		var tripSeq int
		if err := env.DB.QueryRow(`SELECT trip_seq FROM households WHERE id = ?`, hid).Scan(&tripSeq); err != nil {
			t.Fatal(err)
		}
		mustExecTest(t, env.DB, `INSERT INTO attention_marks (household_id, line_key, flagged_trip) VALUES (?, ?, ?)`,
			hid, lineKey, tripSeq)
		mustExecTest(t, env.DB, `INSERT INTO staged_cart_skips (household_id, user_id, item_id, expires_trip) VALUES (?, ?, ?, ?)`,
			hid, hid, itemID, tripSeq+1)
		doc := statusGET686(t, handler, hid, h686Provider)
		var found bool
		for _, row := range statusOffList686(doc) {
			if row["id"] == lineKey {
				found = true
				if row["offListReason"] != "removed" {
					t.Fatalf("offListReason %#v want removed", row["offListReason"])
				}
				if row["needsAttention"] != nil {
					t.Fatalf("mark-only removed must be null %#v", row["needsAttention"])
				}
			}
		}
		if !found {
			t.Fatalf("missing removed offList row %q", lineKey)
		}
		sumBody := exchangeJSON(t, handler, http.MethodGet, "/api/shopping-list/summary", "", http.StatusOK)
		var sum struct {
			NeedsAttention int `json:"needsAttention"`
			NotOnList      int `json:"notOnList"`
		}
		if err := json.Unmarshal(sumBody, &sum); err != nil {
			t.Fatal(err)
		}
		if sum.NeedsAttention != 0 || sum.NotOnList != 1 {
			t.Fatalf("summary needsAttention=%d notOnList=%d want 0/1", sum.NeedsAttention, sum.NotOnList)
		}
	})
	t.Run("opening", func(t *testing.T) {
		handler, env := setupTestWithDB(t)
		registerProductionShapeKroger(env)
		scope := primaryScope(t, env.DB)
		hid := scope.HouseholdID()
		const itemID = "item-h686-a1-o"
		lineKey := shopping.ItemLineKey(itemID)
		createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", "prod-h686-a1-o", "A1O", itemID)
		exchangeJSON(t, handler, http.MethodPut, "/api/restock/household", `{"kind":"months","value":3}`, http.StatusOK)
		setSupplyQuantity(t, env.DB, "prod-h686-a1-o", 3)
		var tripSeq int
		if err := env.DB.QueryRow(`SELECT trip_seq FROM households WHERE id = ?`, hid).Scan(&tripSeq); err != nil {
			t.Fatal(err)
		}
		mustExecTest(t, env.DB, `INSERT INTO attention_marks (household_id, line_key, flagged_trip) VALUES (?, ?, ?)`,
			hid, lineKey, tripSeq)
		doc := statusGET686(t, handler, hid, h686Provider)
		var found bool
		for _, row := range statusOffList686(doc) {
			if row["id"] == lineKey {
				found = true
				if row["offListReason"] != "opening" {
					t.Fatalf("offListReason %#v want opening", row["offListReason"])
				}
				if row["needsAttention"] != nil {
					t.Fatalf("mark during opening must be null %#v", row["needsAttention"])
				}
			}
		}
		if !found {
			t.Fatalf("missing opening offList row %q", lineKey)
		}
		sumBody := exchangeJSON(t, handler, http.MethodGet, "/api/shopping-list/summary", "", http.StatusOK)
		var sum struct {
			NeedsAttention int `json:"needsAttention"`
			NotOnList      int `json:"notOnList"`
		}
		if err := json.Unmarshal(sumBody, &sum); err != nil {
			t.Fatal(err)
		}
		if sum.NeedsAttention != 0 || sum.NotOnList != 1 {
			t.Fatalf("summary needsAttention=%d notOnList=%d want 0/1", sum.NeedsAttention, sum.NotOnList)
		}
	})
}

func TestStatusAttentionActiveZeroA4HTTP(t *testing.T) {
	t.Run("cannot_send_active_zero", func(t *testing.T) {
		handler, env := setupTestWithDB(t)
		registerProductionShapeKroger(env)
		scope := primaryScope(t, env.DB)
		hid := scope.HouseholdID()
		const itemID = "item-h686-a4"
		const lineID = "line-h686-a4"
		lineKey := shopping.ItemLineKey(itemID)
		createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", "prod-h686-a4", "A4", itemID)
		beginUsing(t, env.DB, time.Now())
		exchangeJSON(t, handler, http.MethodPut, "/api/restock/items/"+itemID, `{"kind":"amount","value":2}`, http.StatusOK)
		exchangeJSON(t, handler, http.MethodPost, "/api/shopping-list/fill", ``, http.StatusOK)
		mustExecTest(t, env.DB, `UPDATE shopping_list_items SET id = ? WHERE household_id = ? AND item_id = ?`, lineID, hid, itemID)
		exchangeJSON(t, handler, http.MethodPut, "/api/shopping-list/items/"+lineID+"/adjustment?provider=kroger", `{"adjustment":0}`, http.StatusOK)
		seedCartLineFailedSend(t, env.DB, hid, itemID, "api_error")
		doc := statusGET686(t, handler, hid, h686Provider)
		for _, row := range statusItems686(doc) {
			if row["lineKey"] == lineKey {
				can, _ := row["canSendToCart"].(bool)
				if can {
					t.Fatal("fixture must be cannot send")
				}
				if row["needsAttention"] != nil {
					t.Fatalf("active zero must exempt flag %#v", row["needsAttention"])
				}
			}
		}
	})
	// U2 lock: classify ExemptKeys uses GetAdjustment (any unpurchased entry row);
	// LoadActiveZero joins unpurchased staged lines only. A purchased line can sit in
	// ExemptKeys but not LoadActiveZero — union must not exempt it from send facts.
	t.Run("u2_classify_exempt_not_load_active_zero", func(t *testing.T) {
		handler, env := setupTestWithDB(t)
		registerProductionShapeKroger(env)
		scope := primaryScope(t, env.DB)
		hid := scope.HouseholdID()
		const itemID = "item-h686-u2"
		const lineID = "line-h686-u2"
		lineKey := shopping.ItemLineKey(itemID)
		createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", "prod-h686-u2", "U2", itemID)
		beginUsing(t, env.DB, time.Now())
		exchangeJSON(t, handler, http.MethodPut, "/api/restock/items/"+itemID, `{"kind":"amount","value":2}`, http.StatusOK)
		exchangeJSON(t, handler, http.MethodPost, "/api/shopping-list/fill", ``, http.StatusOK)
		mustExecTest(t, env.DB, `UPDATE shopping_list_items SET id = ? WHERE household_id = ? AND item_id = ?`, lineID, hid, itemID)
		exchangeJSON(t, handler, http.MethodPut, "/api/shopping-list/items/"+lineID+"/adjustment?provider=kroger", `{"adjustment":0}`, http.StatusOK)
		mustExecTest(t, env.DB, `UPDATE shopping_list_items SET purchased_at = datetime('now') WHERE household_id = ? AND id = ?`, hid, lineID)
		activeZero, err := attention.LoadActiveZero(context.Background(), env.DB, scope)
		if err != nil {
			t.Fatal(err)
		}
		if _, in := activeZero[lineKey]; in {
			t.Fatalf("fixture: %q must not be in LoadActiveZero (purchased)", lineKey)
		}
		seedCartLineFailedSend(t, env.DB, hid, itemID, "api_error")
		doc := statusGET686(t, handler, hid, h686Provider)
		na := naForLine686(t, doc, lineKey)
		if na["reason"] != "send_failed" {
			t.Fatalf("U2: send fact must flag purchased zero-adj line %#v", na)
		}
	})
	t.Run("removed_active_zero", func(t *testing.T) {
		handler, env := setupTestWithDB(t)
		registerProductionShapeKroger(env)
		scope := primaryScope(t, env.DB)
		hid := scope.HouseholdID()
		const itemID = "item-h686-a4-r"
		const lineID = "line-h686-a4-r"
		lineKey := shopping.ItemLineKey(itemID)
		createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", "prod-h686-a4-r", "A4R", itemID)
		beginUsing(t, env.DB, time.Now())
		var tripSeq int
		if err := env.DB.QueryRow(`SELECT trip_seq FROM households WHERE id = ?`, hid).Scan(&tripSeq); err != nil {
			t.Fatal(err)
		}
		exchangeJSON(t, handler, http.MethodPost, "/api/shopping-list/fill", ``, http.StatusOK)
		mustExecTest(t, env.DB, `UPDATE shopping_list_items SET id = ? WHERE household_id = ? AND item_id = ?`, lineID, hid, itemID)
		mustExecTest(t, env.DB, `INSERT INTO staged_cart_skips (household_id, user_id, item_id, expires_trip) VALUES (?, ?, ?, ?)`,
			hid, hid, itemID, tripSeq+1)
		mustExecTest(t, env.DB, `INSERT INTO shopping_list_entry_adjustments (household_id, entry_id, provider_id, quantity, expires_trip)
			VALUES (?, ?, 'kroger', 0, NULL)`, hid, lineID)
		seedCartLineFailedSend(t, env.DB, hid, itemID, "api_error")
		doc := statusGET686(t, handler, hid, h686Provider)
		for _, row := range statusOffList686(doc) {
			if row["id"] == lineKey && row["needsAttention"] != nil {
				t.Fatalf("removed + active zero must not flag %#v", row["needsAttention"])
			}
		}
	})
}

func TestStatusAttentionSendUnsureGroupFixTargetHTTP(t *testing.T) {
	handler, env := setupTestWithDB(t)
	registerProductionShapeKroger(env)
	scope := primaryScope(t, env.DB)
	hid := scope.HouseholdID()
	const itemA = "item-h686-g3-a"
	const itemB = "item-h686-g3-b"
	const itemC = "item-h686-g3-c"
	const prodA = "prod-h686-g3-a"
	const prodB = "prod-h686-g3-b"
	const prodC = "prod-h686-g3-c"
	const groupID = "grp-h686-g3-mix"
	const entryB = "line-h686-g3-b-entry"
	groupKey := shopping.GroupLineKey(groupID)
	createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", prodA, "G3A", itemA)
	createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", prodB, "G3B", itemB)
	createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", prodC, "G3C", itemC)
	mustExecTest(t, env.DB, `INSERT INTO product_groups (id, household_id, user_id, name, name_key, rule, rule_confirmed) VALUES (?, ?, ?, 'G3Mix', 'g3mix', 'favorite', 1)`,
		groupID, hid, hid)
	mustExecTest(t, env.DB, `INSERT INTO product_group_members (household_id, group_id, product_id) VALUES (?, ?, ?), (?, ?, ?), (?, ?, ?)`,
		hid, groupID, prodA, hid, groupID, prodB, hid, groupID, prodC)
	beginUsing(t, env.DB, time.Now())
	exchangeJSON(t, handler, http.MethodPut, "/api/groups/"+groupID+"/target", `{"kind":"items","value":6}`, http.StatusOK)
	exchangeJSON(t, handler, http.MethodPost, "/api/shopping-list/fill", ``, http.StatusOK)
	mustExecTest(t, env.DB, `UPDATE shopping_list_items SET group_id = ? WHERE household_id = ? AND purchased_at IS NULL`,
		groupID, hid)
	now := time.Now().UTC()
	t0 := now.Add(-3 * time.Hour).Format(time.RFC3339)
	t1 := now.Add(-2 * time.Hour).Format(time.RFC3339)
	t2 := now.Add(-1 * time.Hour).Format(time.RFC3339)
	seedCartLineResultAt(t, testEnv{T: t, DB: env.DB}, hid, h686Provider, itemA, "unknown", "provider_unavailable", t0)
	mustExecTest(t, env.DB, `DELETE FROM cart_line_results WHERE household_id = ? AND item_id = ?`, hid, itemB)
	mustExecTest(t, env.DB, `INSERT INTO cart_line_results (household_id, provider_id, item_id, entry_id, status, reason, quantity, attempted_at)
		VALUES (?, 'kroger', ?, ?, 'unknown', 'provider_unavailable', 1, ?)`, hid, itemB, entryB, t1)
	seedCartLineResultAt(t, testEnv{T: t, DB: env.DB}, hid, h686Provider, itemC, "failed", "api_error", t2)
	doc := statusGET686(t, handler, hid, h686Provider)
	na := naForLine686(t, doc, groupKey)
	if na["reason"] != "send_unsure" {
		t.Fatalf("reason %#v want send_unsure", na["reason"])
	}
	fix, _ := na["fix"].(map[string]any)
	target, _ := fix["target"].(map[string]any)
	if target["itemId"] != itemB {
		t.Fatalf("fix.target.itemId %#v want %q", target["itemId"], itemB)
	}
	if target["entryId"] != entryB {
		t.Fatalf("fix.target.entryId %#v want %q", target["entryId"], entryB)
	}
	others, _ := na["others"].([]any)
	hasFailed := false
	for _, o := range others {
		if o == "send_failed" {
			hasFailed = true
		}
	}
	if !hasFailed {
		t.Fatalf("others %#v must include send_failed", others)
	}
	assertNoReservedReasons(t, doc)
}

func TestStatusAttentionSendUnsureFixFromResultRowHTTP(t *testing.T) {
	handler, env := setupTestWithDB(t)
	registerProductionShapeKroger(env)
	scope := primaryScope(t, env.DB)
	hid := scope.HouseholdID()
	const itemID = "item-h686-g3"
	const lineID = "line-h686-g3-staged"
	const resultEntry = "line-h686-g3-result"
	lineKey := shopping.ItemLineKey(itemID)
	createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", "prod-h686-g3", "G3", itemID)
	beginUsing(t, env.DB, time.Now())
	exchangeJSON(t, handler, http.MethodPut, "/api/restock/items/"+itemID, `{"kind":"amount","value":2}`, http.StatusOK)
	exchangeJSON(t, handler, http.MethodPost, "/api/shopping-list/fill", ``, http.StatusOK)
	mustExecTest(t, env.DB, `UPDATE shopping_list_items SET id = ? WHERE household_id = ? AND item_id = ?`, lineID, hid, itemID)
	mustExecTest(t, env.DB, `INSERT INTO cart_line_results (household_id, provider_id, item_id, entry_id, status, reason, quantity, attempted_at)
		VALUES (?, 'kroger', ?, ?, 'unknown', 'provider_unavailable', 1, datetime('now'))`, hid, itemID, resultEntry)
	doc := statusGET686(t, handler, hid, h686Provider)
	na := naForLine686(t, doc, lineKey)
	fix, _ := na["fix"].(map[string]any)
	target, _ := fix["target"].(map[string]any)
	if target["entryId"] != resultEntry {
		t.Fatalf("fix.target.entryId %#v want %q", target["entryId"], resultEntry)
	}
	if target["itemId"] != itemID {
		t.Fatalf("fix.target.itemId %#v want %q", target["itemId"], itemID)
	}
}

func TestStatusAttentionPurchasedNotShortHTTP(t *testing.T) {
	handler, env := setupTestWithDB(t)
	registerProductionShapeKroger(env)
	scope := primaryScope(t, env.DB)
	hid := scope.HouseholdID()
	const itemID = "item-h686-sh1"
	lineKey := shopping.ItemLineKey(itemID)
	createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", "prod-h686-sh1", "SH1", itemID)
	beginUsing(t, env.DB, time.Now())
	exchangeJSON(t, handler, http.MethodPut, "/api/restock/items/"+itemID, `{"kind":"amount","value":0}`, http.StatusOK)
	mustExecTest(t, env.DB, `INSERT INTO shopping_list_items (id, household_id, user_id, item_id, quantity, source, purchased_at, created_at)
		VALUES ('line-h686-sh1', ?, ?, ?, 1, 'auto', datetime('now'), datetime('now'))`, hid, hid, itemID)
	seedCartLineFailedSend(t, env.DB, hid, itemID, "api_error")
	doc := statusGET686(t, handler, hid, h686Provider)
	for _, row := range statusItems686(doc) {
		if row["lineKey"] == lineKey && row["needsAttention"] != nil {
			t.Fatalf("purchased row must not be flagged (SH1) %#v", row["needsAttention"])
		}
	}
	for _, row := range statusOffList686(doc) {
		if row["id"] == lineKey && row["needsAttention"] != nil {
			t.Fatalf("purchased off-list must not be flagged %#v", row["needsAttention"])
		}
	}
}

func TestStatusAttentionSupplyErrorsHTTP(t *testing.T) {
	t.Run("settings_opening", func(t *testing.T) {
		handler, env := setupTestWithDB(t)
		registerProductionShapeKroger(env)
		restore := SetFaultOnSupplySettingsSuccessForTest(func() error {
			return errors.New("settings fault")
		})
		defer restore()
		exchangeJSON(t, handler, http.MethodGet, "/api/shopping-list?status=1&provider=kroger", "", http.StatusInternalServerError)
	})
	t.Run("plan_view", func(t *testing.T) {
		handler, env := setupTestWithDB(t)
		registerProductionShapeKroger(env)
		restore := SetFaultOnSupplyPlanViewSuccessForTest(func() error {
			return errors.New("plan view fault")
		})
		defer restore()
		_ = env
		exchangeJSON(t, handler, http.MethodGet, "/api/shopping-list?status=1&provider=kroger", "", http.StatusInternalServerError)
	})
}
