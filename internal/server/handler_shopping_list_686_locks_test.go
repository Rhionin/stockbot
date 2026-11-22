package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/attention"
	"github.com/Rhionin/stockbot/internal/cart"
	"github.com/Rhionin/stockbot/internal/cart/carttest"
	"github.com/Rhionin/stockbot/internal/cart/connection"
	"github.com/Rhionin/stockbot/internal/shopping"
	"github.com/Rhionin/stockbot/internal/tenant"
)

const h686Provider = "kroger"

var reservedAttentionReasons686 = []string{"probable_miss", "attention"}

func assertNoReservedReasons(t *testing.T, body map[string]any) {
	t.Helper()
	check := func(where, reason string) {
		t.Helper()
		for _, r := range reservedAttentionReasons686 {
			if reason == r {
				t.Fatalf("%s has reserved reason %q", where, reason)
			}
		}
	}
	for _, row := range statusItems686(body) {
		lk, _ := row["lineKey"].(string)
		if na, ok := row["needsAttention"].(map[string]any); ok && na != nil {
			r, _ := na["reason"].(string)
			check("items["+lk+"]", r)
			for i, o := range na["others"].([]any) {
				check(fmt.Sprintf("items[%s].others[%d]", lk, i), fmt.Sprintf("%v", o))
			}
		}
	}
	for _, row := range statusOffList686(body) {
		id, _ := row["id"].(string)
		if na, ok := row["needsAttention"].(map[string]any); ok && na != nil {
			r, _ := na["reason"].(string)
			check("offList["+id+"]", r)
			for i, o := range na["others"].([]any) {
				check(fmt.Sprintf("offList[%s].others[%d]", id, i), fmt.Sprintf("%v", o))
			}
		}
	}
	for i, g := range statusAttentionGroups686(body) {
		r, _ := g["reason"].(string)
		check(fmt.Sprintf("attentionGroups[%d]", i), r)
	}
}

func statusGET686(t *testing.T, handler http.Handler, hid, provider string) map[string]any {
	t.Helper()
	path := "/api/shopping-list?status=1"
	if provider != "" {
		path += "&provider=" + provider
	}
	body := exchangeJSON(t, handler, http.MethodGet, path, "", http.StatusOK)
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

func statusItems686(doc map[string]any) []map[string]any {
	raw, _ := doc["items"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, x := range raw {
		out = append(out, x.(map[string]any))
	}
	return out
}

func statusOffList686(doc map[string]any) []map[string]any {
	raw, _ := doc["offList"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, x := range raw {
		out = append(out, x.(map[string]any))
	}
	return out
}

func statusAttentionGroups686(doc map[string]any) []map[string]any {
	raw, _ := doc["attentionGroups"].([]any)
	if raw == nil {
		return nil
	}
	out := make([]map[string]any, 0, len(raw))
	for _, x := range raw {
		out = append(out, x.(map[string]any))
	}
	return out
}

func findAttention686(t *testing.T, doc map[string]any, ok func(map[string]any) bool) map[string]any {
	t.Helper()
	for _, row := range statusItems686(doc) {
		if na, yes := row["needsAttention"].(map[string]any); yes && na != nil && ok(na) {
			return na
		}
	}
	for _, row := range statusOffList686(doc) {
		if na, yes := row["needsAttention"].(map[string]any); yes && na != nil && ok(na) {
			return na
		}
	}
	return nil
}

func naForLine686(t *testing.T, doc map[string]any, lineKey string) map[string]any {
	t.Helper()
	for _, row := range statusItems686(doc) {
		if row["lineKey"] == lineKey {
			if na, ok := row["needsAttention"].(map[string]any); ok && na != nil {
				return na
			}
		}
	}
	for _, row := range statusOffList686(doc) {
		if row["id"] == lineKey {
			if na, ok := row["needsAttention"].(map[string]any); ok && na != nil {
				return na
			}
		}
	}
	t.Fatalf("needsAttention missing for %q", lineKey)
	return nil
}

func seedCartLineResultAt(t *testing.T, env testEnv, hid, providerID, itemID, status, reason, attemptedAt string) {
	t.Helper()
	mustExecTest(t, env.DB, `DELETE FROM cart_line_results WHERE household_id = ? AND provider_id = ? AND item_id = ?`,
		hid, providerID, itemID)
	mustExecTest(t, env.DB, `INSERT INTO cart_line_results (household_id, provider_id, item_id, status, reason, quantity, attempted_at)
		VALUES (?, ?, ?, ?, ?, 1, ?)`, hid, providerID, itemID, status, reason, attemptedAt)
}

func shortItemBaseline686(t *testing.T, handler http.Handler, env testEnv, itemID, prodID, prodName string) (tenant.Scope, string) {
	t.Helper()
	scope := primaryScope(t, env.DB)
	createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", prodID, prodName, itemID)
	beginUsing(t, env.DB, time.Now())
	exchangeJSON(t, handler, http.MethodPut, "/api/restock/items/"+itemID, `{"kind":"amount","value":2}`, http.StatusOK)
	exchangeJSON(t, handler, http.MethodPost, "/api/shopping-list/fill", ``, http.StatusOK)
	return scope, shopping.ItemLineKey(itemID)
}

// TestStatusAttentionSendKeyGroup (H1) — member failed send flags group line, not a solo item off-list row.
func TestStatusAttentionSendKeyGroup(t *testing.T) {
	handler, env := setupTestWithDB(t)
	registerProductionShapeKroger(env)
	scope := primaryScope(t, env.DB)
	hid := scope.HouseholdID()
	const itemA = "item-h686-h1-a"
	const itemB = "item-h686-h1-b"
	const prodA = "prod-h686-h1-a"
	const prodB = "prod-h686-h1-b"
	groupID := "grp-h686-h1"
	groupKey := shopping.GroupLineKey(groupID)
	createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", prodA, "H1A", itemA)
	createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", prodB, "H1B", itemB)
	mustExecTest(t, env.DB, `INSERT INTO product_groups (id, household_id, user_id, name, name_key, rule, rule_confirmed) VALUES (?, ?, ?, 'H1 Group', 'h1g', 'favorite', 1)`,
		groupID, hid, hid)
	mustExecTest(t, env.DB, `INSERT INTO product_group_members (household_id, group_id, product_id) VALUES (?, ?, ?), (?, ?, ?)`,
		hid, groupID, prodA, hid, groupID, prodB)
	beginUsing(t, env.DB, time.Now())
	exchangeJSON(t, handler, http.MethodPut, "/api/restock/items/"+itemA, `{"kind":"amount","value":2}`, http.StatusOK)
	exchangeJSON(t, handler, http.MethodPut, "/api/restock/items/"+itemB, `{"kind":"amount","value":2}`, http.StatusOK)
	exchangeJSON(t, handler, http.MethodPost, "/api/shopping-list/fill", ``, http.StatusOK)
	mustExecTest(t, env.DB, `UPDATE shopping_list_items SET group_id = ? WHERE household_id = ? AND item_id = ?`,
		groupID, hid, itemA)
	seedCartLineFailedSend(t, env.DB, hid, itemA, "api_error")
	doc := statusGET686(t, handler, hid, h686Provider)
	na := naForLine686(t, doc, groupKey)
	if na["reason"] != "send_failed" || na["detail"] != "api_error" {
		t.Fatalf("group needsAttention %#v", na)
	}
	itemKey := shopping.ItemLineKey(itemA)
	for _, row := range statusItems686(doc) {
		if row["lineKey"] == itemKey {
			t.Fatalf("item line must not appear in items when grouped: %#v", row)
		}
	}
	for _, row := range statusOffList686(doc) {
		if row["id"] == itemKey {
			t.Fatalf("item line must not appear in offList when grouped")
		}
	}
	assertNoReservedReasons(t, doc)
}

// TestStatusAttentionSendKeyItem (H2) — staged row without group_id keeps attention on item:X.
func TestStatusAttentionSendKeyItem(t *testing.T) {
	handler, env := setupTestWithDB(t)
	registerProductionShapeKroger(env)
	scope, lineKey := shortItemBaseline686(t, handler, env, "item-h686-h2", "prod-h686-h2", "H2 Solo")
	seedCartLineFailedSend(t, env.DB, scope.HouseholdID(), "item-h686-h2", "batch_rejected")
	doc := statusGET686(t, handler, scope.HouseholdID(), h686Provider)
	na := naForLine686(t, doc, lineKey)
	if na["reason"] != "send_failed" || na["detail"] != "batch_rejected" {
		t.Fatalf("item needsAttention %#v", na)
	}
	assertNoReservedReasons(t, doc)
}

// TestStatusAttentionProviderFilter (H2b) — failed row for another provider does not flag under kroger.
func TestStatusAttentionProviderFilter(t *testing.T) {
	handler, env := setupTestWithDB(t)
	registerProductionShapeKroger(env)
	scope, lineKey := shortItemBaseline686(t, handler, env, "item-h686-h2b", "prod-h686-h2b", "H2B")
	hid := scope.HouseholdID()
	seedCartLineResultAt(t, env, hid, openProvider, "item-h686-h2b", "failed", "api_error", time.Now().UTC().Format(time.RFC3339))
	doc := statusGET686(t, handler, hid, h686Provider)
	for _, row := range statusItems686(doc) {
		if row["lineKey"] == lineKey {
			if row["needsAttention"] != nil {
				t.Fatalf("other-provider failure must not flag kroger row %#v", row)
			}
		}
	}
	for _, g := range statusAttentionGroups686(doc) {
		for _, k := range g["rowKeys"].([]any) {
			if k == lineKey {
				t.Fatalf("attentionGroups must not list %q", lineKey)
			}
		}
	}
}

// TestStatusAttentionSendRetentionWallClock (H2c-3) — inside retention flagged; outside needsAttention null.
func TestStatusAttentionSendRetentionWallClock(t *testing.T) {
	handler, env := setupTestWithDB(t)
	registerProductionShapeKroger(env)
	scope, lineKey := shortItemBaseline686(t, handler, env, "item-h686-h2c-in", "prod-h686-h2c-in", "H2C In")
	hid := scope.HouseholdID()
	now := time.Now().UTC()
	inside := now.Add(-cart.SendResultRetention + time.Hour).Format(time.RFC3339)
	outside := now.Add(-cart.SendResultRetention - time.Hour).Format(time.RFC3339)
	seedCartLineResultAt(t, env, hid, h686Provider, "item-h686-h2c-in", "failed", "api_error", inside)
	doc := statusGET686(t, handler, hid, h686Provider)
	if naForLine686(t, doc, lineKey)["reason"] != "send_failed" {
		t.Fatalf("inside retention want send_failed got %#v", naForLine686(t, doc, lineKey))
	}
	const itemOut = "item-h686-h2c-out"
	_, _ = shortItemBaseline686(t, handler, env, itemOut, "prod-h686-h2c-out", "H2C Out")
	outKey := shopping.ItemLineKey(itemOut)
	mustExecTest(t, env.DB, `DELETE FROM cart_line_results WHERE household_id = ? AND item_id = ?`, hid, itemOut)
	seedCartLineResultAt(t, env, hid, h686Provider, itemOut, "failed", "api_error", outside)
	doc = statusGET686(t, handler, hid, h686Provider)
	for _, row := range statusItems686(doc) {
		if row["lineKey"] == outKey {
			if _, ok := row["needsAttention"]; !ok {
				t.Fatal("needsAttention key must be present")
			}
			if row["needsAttention"] != nil {
				t.Fatalf("outside retention want null got %#v", row["needsAttention"])
			}
		}
	}
	for _, g := range statusAttentionGroups686(doc) {
		for _, k := range g["rowKeys"].([]any) {
			if k == outKey {
				t.Fatalf("outside retention must not be in attentionGroups")
			}
		}
	}
}

// TestStatusAttentionStoredReasonTable (H3) — §6.3 listed rows via WireFromStored expectations.
func TestStatusAttentionStoredReasonTable(t *testing.T) {
	registerOnce := func(t *testing.T, env testEnv) {
		t.Helper()
		registerProductionShapeKroger(env)
	}
	cases := []struct {
		name                   string
		status, reason         string
		wantReason, wantDetail string
		wantDetailAbsent       bool
		wantNoAttention        bool
	}{
		{"failed_batch_rejected", "failed", "batch_rejected", "send_failed", "batch_rejected", false, false},
		{"failed_invalid_line", "failed", "invalid_line", "send_failed", "invalid_line", false, false},
		{"failed_api_error", "failed", "api_error", "send_failed", "api_error", false, false},
		{"failed_request_incomplete", "failed", "request_incomplete", "send_failed", "request_incomplete", false, false},
		{"failed_store_sign_in", "failed", "store_sign_in", "send_failed", "store_sign_in", false, false},
		{"failed_reconnect_required", "failed", "reconnect_required", "send_failed", "reconnect_required", false, false},
		{"failed_no_per_item_result", "failed", "no_per_item_result", "send_failed", "no_per_item_result", false, false},
		{"failed_provider_unavailable", "failed", "provider_unavailable", "send_failed", "provider_unavailable", false, false},
		{"skipped_unavailable", "skipped", "unavailable", "send_failed", "unavailable", false, false},
		{"skipped_no_upc", "skipped", "no_upc", "no_barcode", "no_upc", false, false},
		{"skipped_no_match", "skipped", "no_match", "no_store_match", "no_match", false, false},
		{"unknown_request_incomplete", "unknown", "request_incomplete", "send_unsure", "request_incomplete", false, false},
		{"unknown_no_per_item_result", "unknown", "no_per_item_result", "send_unsure", "no_per_item_result", false, false},
		{"unknown_provider_unavailable", "unknown", "provider_unavailable", "send_unsure", "provider_unavailable", false, false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			handler, env := setupTestWithDB(t)
			registerOnce(t, env)
			scope, lineKey := shortItemBaseline686(t, handler, env, "item-h686-h3-"+tc.name, "prod-h686-h3-"+tc.name, tc.name)
			hid := scope.HouseholdID()
			itemID := "item-h686-h3-" + tc.name
			mustExecTest(t, env.DB, `DELETE FROM cart_line_results WHERE household_id = ? AND item_id = ?`, hid, itemID)
			seedCartLineResultAt(t, env, hid, h686Provider, itemID, tc.status, tc.reason, time.Now().UTC().Format(time.RFC3339))
			doc := statusGET686(t, handler, hid, h686Provider)
			if tc.wantNoAttention {
				if na := naForLine686(t, doc, lineKey); na != nil {
					t.Fatalf("unexpected needsAttention %#v", na)
				}
				return
			}
			na := naForLine686(t, doc, lineKey)
			if na["reason"] != tc.wantReason {
				t.Fatalf("reason %#v want %q", na["reason"], tc.wantReason)
			}
			if tc.wantDetailAbsent {
				if _, ok := na["detail"]; ok {
					t.Fatalf("detail must be absent got %#v", na["detail"])
				}
			} else if na["detail"] != tc.wantDetail {
				t.Fatalf("detail %#v want %q", na["detail"], tc.wantDetail)
			}
			if na["reason"] == tc.reason {
				t.Fatal("stored reason must not appear on wire reason")
			}
			assertNoReservedReasons(t, doc)
		})
	}
}

// TestStatusAttentionOffListUnnamed (H4) — flagged unnamed product stays on wire with name "".
func TestStatusAttentionOffListUnnamed(t *testing.T) {
	handler, env := setupTestWithDB(t)
	registerProductionShapeKroger(env)
	scope := primaryScope(t, env.DB)
	const itemID = "item-h686-h4"
	lineKey := shopping.ItemLineKey(itemID)
	if _, err := env.DB.Exec(`INSERT INTO products (id, name, category) VALUES ('prod-h686-h4', '', 'c')`); err != nil {
		t.Fatal(err)
	}
	if _, err := env.DB.Exec(`INSERT INTO items (id, user_id, household_id, product_id) VALUES (?, ?, ?, 'prod-h686-h4')`,
		itemID, scope.HouseholdID(), scope.HouseholdID()); err != nil {
		t.Fatal(err)
	}
	beginUsing(t, env.DB, time.Now())
	mustExecTest(t, env.DB, `INSERT INTO shopping_list_items (id, household_id, user_id, item_id, quantity, source, created_at)
		VALUES ('line-h686-h4', ?, ?, ?, 1, 'manual', datetime('now'))`, scope.HouseholdID(), scope.HouseholdID(), itemID)
	seedCartLineFailedSend(t, env.DB, scope.HouseholdID(), itemID, "api_error")
	doc := statusGET686(t, handler, scope.HouseholdID(), h686Provider)
	var ghost map[string]any
	for _, row := range statusItems686(doc) {
		if row["lineKey"] == lineKey {
			ghost = row
		}
	}
	if ghost == nil {
		for _, row := range statusOffList686(doc) {
			if row["id"] == lineKey {
				ghost = row
			}
		}
	}
	if ghost == nil {
		t.Fatal("unnamed flagged row must appear on items or offList")
	}
	if ghost["name"] != "" {
		t.Fatalf("name %#v want empty string", ghost["name"])
	}
	if ghost["needsAttention"] == nil {
		t.Fatal("needsAttention required")
	}
	var groupCount int
	for _, g := range statusAttentionGroups686(doc) {
		for _, k := range g["rowKeys"].([]any) {
			if k == lineKey {
				groupCount++
			}
		}
	}
	if groupCount != 1 {
		t.Fatalf("attentionGroups rowKeys for %q = %d want 1", lineKey, groupCount)
	}
}

// TestStatusAttentionGroupName (H5 partial) — group line uses product_groups.name on status=1.
func TestStatusAttentionGroupName(t *testing.T) {
	handler, env := setupTestWithDB(t)
	registerProductionShapeKroger(env)
	scope := primaryScope(t, env.DB)
	hid := scope.HouseholdID()
	const itemA = "item-h686-h5-a"
	const prodA = "prod-h686-h5-a"
	const prodB = "prod-h686-h5-b"
	groupID := "grp-h686-h5"
	groupKey := shopping.GroupLineKey(groupID)
	const groupName = "H5 Group Display"
	createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", prodA, "A", itemA)
	createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", prodB, "B", "item-h686-h5-b")
	mustExecTest(t, env.DB, `INSERT INTO product_groups (id, household_id, user_id, name, name_key, rule, rule_confirmed) VALUES (?, ?, ?, ?, 'h5g', 'favorite', 1)`,
		groupID, hid, hid, groupName)
	mustExecTest(t, env.DB, `INSERT INTO product_group_members (household_id, group_id, product_id) VALUES (?, ?, ?), (?, ?, ?)`,
		hid, groupID, prodA, hid, groupID, prodB)
	beginUsing(t, env.DB, time.Now())
	exchangeJSON(t, handler, http.MethodPut, "/api/restock/items/"+itemA, `{"kind":"amount","value":2}`, http.StatusOK)
	exchangeJSON(t, handler, http.MethodPost, "/api/shopping-list/fill", ``, http.StatusOK)
	mustExecTest(t, env.DB, `UPDATE shopping_list_items SET group_id = ? WHERE household_id = ? AND item_id = ?`, groupID, hid, itemA)
	doc := statusGET686(t, handler, hid, h686Provider)
	for _, row := range statusItems686(doc) {
		if row["lineKey"] == groupKey {
			if row["name"] != groupName {
				t.Fatalf("group name %#v want %q", row["name"], groupName)
			}
			return
		}
	}
	t.Fatalf("missing group row %q", groupKey)
}

// TestStatusAttentionShape (H6) — others [], full fix.target keys, needsAttention key always present.
func TestStatusAttentionShape(t *testing.T) {
	handler, env := setupTestWithDB(t)
	registerProductionShapeKroger(env)
	scope, cleanKey := shortItemBaseline686(t, handler, env, "item-h686-h6-clean", "prod-h686-h6-clean", "H6 Clean")
	flaggedItem := "item-h686-h6-flag"
	_, flaggedKey := shortItemBaseline686(t, handler, env, flaggedItem, "prod-h686-h6-flag", "H6 Flag")
	seedCartLineFailedSend(t, env.DB, scope.HouseholdID(), flaggedItem, "store_sign_in")
	doc := statusGET686(t, handler, scope.HouseholdID(), h686Provider)
	var cleanRow map[string]any
	for _, row := range statusItems686(doc) {
		if row["lineKey"] == cleanKey {
			cleanRow = row
		}
	}
	if cleanRow == nil {
		t.Fatalf("missing clean row %q", cleanKey)
	}
	if _, ok := cleanRow["needsAttention"]; !ok {
		t.Fatal("unflagged row must include needsAttention key")
	}
	if cleanRow["needsAttention"] != nil {
		t.Fatalf("unflagged needsAttention want null got %#v", cleanRow["needsAttention"])
	}
	na := naForLine686(t, doc, flaggedKey)
	others, _ := na["others"].([]any)
	if others == nil {
		t.Fatal("others must be non-null array")
	}
	if len(others) != 0 {
		t.Fatalf("others %#v want []", others)
	}
	fix, ok := na["fix"].(map[string]any)
	if !ok {
		t.Fatal("fix object missing")
	}
	target, ok := fix["target"].(map[string]any)
	if !ok {
		t.Fatal("fix.target missing")
	}
	for _, k := range []string{"entryId", "itemId", "groupId", "productId", "providerId"} {
		if _, ok := target[k]; !ok {
			t.Fatalf("fix.target missing key %q", k)
		}
	}
}

// TestStatusAttentionLoaderFaults (H7) — each attention loader fault → 500 without partial rows.
func TestStatusAttentionLoaderFaults(t *testing.T) {
	handler, env := setupTestWithDB(t)
	registerProductionShapeKroger(env)
	scope := primaryScope(t, env.DB)
	hid := scope.HouseholdID()
	createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", "prod-h686-h7", "H7", "item-h686-h7")
	beginUsing(t, env.DB, time.Now())
	exchangeJSON(t, handler, http.MethodPut, "/api/restock/items/item-h686-h7", `{"kind":"amount","value":2}`, http.StatusOK)
	exchangeJSON(t, handler, http.MethodPost, "/api/shopping-list/fill", ``, http.StatusOK)
	faults := []struct {
		name    string
		install func() func()
	}{
		{"LoadMarks", func() func() {
			return attention.SetFaultAfterLoadMarksForTest(func() error { return errors.New("marks fault") })
		}},
		{"LoadActiveZero", func() func() {
			return attention.SetFaultAfterLoadActiveZeroForTest(func() error { return errors.New("active zero fault") })
		}},
		{"LoadSendResults", func() func() {
			return attention.SetFaultAfterLoadSendResultsForTest(func() error { return errors.New("send results fault") })
		}},
		{"LoadItemProductGroups", func() func() {
			return attention.SetFaultAfterLoadItemProductGroupsForTest(func() error { return errors.New("item groups fault") })
		}},
		{"householdTripMeta", func() func() {
			return SetFaultAfterHouseholdTripMetaForTest(func() error { return errors.New("trip meta fault") })
		}},
		{"batchActiveSkips", func() func() {
			return SetFaultAfterBatchActiveSkipsForTest(func() error { return errors.New("skips fault") })
		}},
		{"LoadConnFact", func() func() {
			return attention.SetFaultAfterLoadConnFactForTest(func() error { return errors.New("connection fault") })
		}},
	}
	for _, fc := range faults {
		fc := fc
		t.Run(fc.name, func(t *testing.T) {
			restore := fc.install()
			t.Cleanup(restore)
			req := httptest.NewRequest(http.MethodGet, "/api/shopping-list?status=1&provider="+h686Provider, nil)
			req.Header.Set("X-Household-Id", hid)
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)
			if rr.Code != http.StatusInternalServerError {
				t.Fatalf("status %d want 500 body %s", rr.Code, rr.Body.String())
			}
			var doc map[string]any
			if err := json.Unmarshal(rr.Body.Bytes(), &doc); err == nil {
				if items, ok := doc["items"].([]any); ok && len(items) > 0 {
					t.Fatalf("partial items on loader fault: %#v", items)
				}
			}
		})
	}
}

// TestStatusAttentionNoProductPicked (H8) — invalid group rule → not_planned / no_product_picked.
func TestStatusAttentionNoProductPicked(t *testing.T) {
	handler, env := setupTestWithDB(t)
	registerProductionShapeKroger(env)
	scope := primaryScope(t, env.DB)
	hid := scope.HouseholdID()
	const itemA = "item-h686-h8-a"
	const prodA = "prod-h686-h8-a"
	const prodB = "prod-h686-h8-b"
	groupID := "grp-h686-h8"
	groupKey := shopping.GroupLineKey(groupID)
	createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", prodA, "H8A", itemA)
	createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", prodB, "H8B", "item-h686-h8-b")
	mustExecTest(t, env.DB, `PRAGMA ignore_check_constraints=1`)
	mustExecTest(t, env.DB, `INSERT INTO product_groups (id, household_id, user_id, name, name_key, rule, rule_confirmed, window_months) VALUES (?, ?, ?, 'H8G', 'h8g', 'not_a_rule', 1, 3)`,
		groupID, hid, hid)
	mustExecTest(t, env.DB, `INSERT INTO product_group_members (household_id, group_id, product_id) VALUES (?, ?, ?), (?, ?, ?)`,
		hid, groupID, prodA, hid, groupID, prodB)
	var storedRule string
	if err := env.DB.QueryRow(`SELECT rule FROM product_groups WHERE id = ?`, groupID).Scan(&storedRule); err != nil {
		t.Fatal(err)
	}
	if storedRule != "not_a_rule" {
		t.Fatalf("fixture rule %q want not_a_rule", storedRule)
	}
	beginUsing(t, env.DB, time.Now().Add(-30*24*time.Hour))
	exchangeJSON(t, handler, http.MethodPut, "/api/restock/household", `{"kind":"months","value":3}`, http.StatusOK)
	mustExecTest(t, env.DB, `DELETE FROM item_targets WHERE item_id IN (?, ?)`, itemA, "item-h686-h8-b")
	exchangeJSON(t, handler, http.MethodPost, "/api/shopping-list/fill", ``, http.StatusOK)
	doc := statusGET686(t, handler, hid, h686Provider)
	na := findAttention686(t, doc, func(na map[string]any) bool {
		return na["reason"] == "not_planned" && na["detail"] == "no_product_picked"
	})
	if na == nil {
		t.Fatalf("missing not_planned/no_product_picked on %q", groupKey)
	}
	assertNoReservedReasons(t, doc)
}

// TestSendStore502IsUnsure (H9) — pre-cart token failure records failed/store_sign_in → send_failed.
func TestSendStore502IsUnsure(t *testing.T) {
	handler, env := setupTestWithDB(t)
	createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", "prod-h686-h9", "H9", "item-h686-h9")
	insertManualShoppingItem(t, env.DB, "sli-h686-h9", "user-1", "item-h686-h9", 2)
	if err := env.ProductStore.UpsertBarcodeMapping(context.Background(), "999", "prod-h686-h9", "global", ""); err != nil {
		t.Fatal(err)
	}
	script := carttest.NewScript().
		WithTokenExchangeError(&cart.OAuthTokenError{Err: errors.New("connection reset")}).
		WithIdentityLookup("999", cart.ProductIdentity("SKU-H9"))
	provider := carttest.NewFake(cart.Capabilities{
		Auth:         cart.AuthOAuth2,
		Delivery:     cart.DeliveryServerPush,
		Confirmation: cart.ConfirmPerRequest,
		Mutation:     cart.MutateAddOnly,
		Identity:     cart.IdentityDerived,
	}, script)
	if err := env.Registry.Register(provider, cart.WithCredentialsConfigured(true)); err != nil {
		t.Fatal(err)
	}
	seedDefaultExportApp(t, env.DB, string(provider.ID()))
	scope := primaryScope(t, env.DB)
	expired := time.Now().UTC().Add(-time.Hour)
	if err := connection.NewDirectory(env.DB).Write(context.Background(), scope, &connection.Connection{
		Provider:     string(provider.ID()),
		State:        connection.StateConnected,
		AccessToken:  "stale-h9",
		RefreshToken: "refresh-h9",
		ExpiresAt:    &expired,
	}); err != nil {
		t.Fatal(err)
	}
	beginUsing(t, env.DB, time.Now())
	exchangeJSON(t, handler, http.MethodPost, "/api/shopping-list/export",
		`{"provider":"test-oauth2_authorization_code-server_push"}`, http.StatusBadGateway)
	var status, reason string
	if err := env.DB.QueryRow(`SELECT status, COALESCE(reason,'') FROM cart_line_results
		WHERE household_id = ? AND item_id = 'item-h686-h9'`,
		scope.HouseholdID()).Scan(&status, &reason); err != nil {
		t.Fatalf("cart_line_results: %v", err)
	}
	if status != "failed" || reason != "store_sign_in" {
		t.Fatalf("engine wrote status=%q reason=%q", status, reason)
	}
	lineKey := shopping.ItemLineKey("item-h686-h9")
	const exportProvider = "test-oauth2_authorization_code-server_push"
	doc := statusGET686(t, handler, scope.HouseholdID(), exportProvider)
	na := naForLine686(t, doc, lineKey)
	if na["reason"] != "send_failed" || na["detail"] != "store_sign_in" {
		t.Fatalf("send_failed/store_sign_in %#v", na)
	}
	assertNoReservedReasons(t, doc)
}

// TestStatusAttentionSendKeyGroupViaPlan (H10) — step 3: send on member without its own staged row maps to group line.
func TestStatusAttentionSendKeyGroupViaPlan(t *testing.T) {
	handler, env := setupTestWithDB(t)
	registerProductionShapeKroger(env)
	scope := primaryScope(t, env.DB)
	hid := scope.HouseholdID()
	const itemA = "item-h686-h10-a"
	const itemB = "item-h686-h10-b"
	const prodA = "prod-h686-h10-a"
	const prodB = "prod-h686-h10-b"
	groupID := "grp-h686-h10"
	groupKey := shopping.GroupLineKey(groupID)
	itemKey := shopping.ItemLineKey(itemB)
	createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", prodA, "H10A", itemA)
	createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", prodB, "H10B", itemB)
	mustExecTest(t, env.DB, `INSERT INTO product_groups (id, household_id, user_id, name, name_key, rule, rule_confirmed) VALUES (?, ?, ?, 'H10G', 'h10g', 'favorite', 1)`,
		groupID, hid, hid)
	mustExecTest(t, env.DB, `INSERT INTO product_group_members (household_id, group_id, product_id) VALUES (?, ?, ?), (?, ?, ?)`,
		hid, groupID, prodA, hid, groupID, prodB)
	beginUsing(t, env.DB, time.Now())
	exchangeJSON(t, handler, http.MethodPut, "/api/restock/items/"+itemA, `{"kind":"amount","value":2}`, http.StatusOK)
	exchangeJSON(t, handler, http.MethodPost, "/api/shopping-list/fill", ``, http.StatusOK)
	mustExecTest(t, env.DB, `UPDATE shopping_list_items SET group_id = ? WHERE household_id = ? AND item_id = ?`, groupID, hid, itemA)
	seedCartLineFailedSend(t, env.DB, hid, itemB, "invalid_line")
	doc := statusGET686(t, handler, hid, h686Provider)
	na := naForLine686(t, doc, groupKey)
	if na["reason"] != "send_failed" {
		t.Fatalf("group flag %#v", na)
	}
	for _, row := range statusItems686(doc) {
		if row["lineKey"] == itemKey {
			t.Fatalf("item row must not appear %#v", row)
		}
	}
	for _, row := range statusOffList686(doc) {
		if row["id"] == itemKey {
			t.Fatalf("item offList row must not appear")
		}
	}
}

// TestStatusAttentionSendKeyFallbackItem (H11) — step 4 fallback to item:X.
func TestStatusAttentionSendKeyFallbackItem(t *testing.T) {
	t.Run("no_group_no_plan", func(t *testing.T) {
		handler, env := setupTestWithDB(t)
		registerProductionShapeKroger(env)
		scope := primaryScope(t, env.DB)
		const itemID = "item-h686-h11a"
		lineKey := shopping.ItemLineKey(itemID)
		if _, err := env.DB.Exec(`INSERT INTO products (id, name, category) VALUES ('prod-h686-h11a', 'H11A', 'c')`); err != nil {
			t.Fatal(err)
		}
		if _, err := env.DB.Exec(`INSERT INTO items (id, user_id, household_id, product_id) VALUES (?, ?, ?, 'prod-h686-h11a')`,
			itemID, scope.HouseholdID(), scope.HouseholdID()); err != nil {
			t.Fatal(err)
		}
		beginUsing(t, env.DB, time.Now())
		mustExecTest(t, env.DB, `INSERT INTO shopping_list_items (id, household_id, user_id, item_id, quantity, source, created_at)
			VALUES ('line-h686-h11a', ?, ?, ?, 1, 'manual', datetime('now'))`, scope.HouseholdID(), scope.HouseholdID(), itemID)
		seedCartLineFailedSend(t, env.DB, scope.HouseholdID(), itemID, "api_error")
		doc := statusGET686(t, handler, scope.HouseholdID(), h686Provider)
		na := naForLine686(t, doc, lineKey)
		if na["reason"] != "send_failed" {
			t.Fatalf("offList item flag %#v", na)
		}
	})
	t.Run("group_not_in_plan", func(t *testing.T) {
		handler, env := setupTestWithDB(t)
		registerProductionShapeKroger(env)
		scope := primaryScope(t, env.DB)
		hid := scope.HouseholdID()
		const itemID = "item-h686-h11b"
		lineKey := shopping.ItemLineKey(itemID)
		groupID := "grp-h686-h11b"
		createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", "prod-h686-h11b", "H11B", itemID)
		mustExecTest(t, env.DB, `INSERT INTO product_groups (id, household_id, user_id, name, name_key, rule, rule_confirmed) VALUES (?, ?, ?, 'H11BG', 'h11bg', 'favorite', 1)`,
			groupID, hid, hid)
		mustExecTest(t, env.DB, `INSERT INTO product_group_members (household_id, group_id, product_id) VALUES (?, ?, 'prod-h686-h11b')`,
			hid, groupID)
		beginUsing(t, env.DB, time.Now())
		mustExecTest(t, env.DB, `INSERT INTO shopping_list_items (id, household_id, user_id, item_id, quantity, source, created_at)
			VALUES ('line-h686-h11b', ?, ?, ?, 1, 'manual', datetime('now'))`, hid, hid, itemID)
		seedCartLineFailedSend(t, env.DB, hid, itemID, "request_incomplete")
		doc := statusGET686(t, handler, hid, h686Provider)
		na := naForLine686(t, doc, lineKey)
		if na["reason"] != "send_failed" {
			t.Fatalf("item flag %#v", na)
		}
		groupKey := shopping.GroupLineKey(groupID)
		for _, row := range statusItems686(doc) {
			if row["lineKey"] == groupKey && row["needsAttention"] != nil {
				t.Fatalf("group line must not be flagged %#v", row)
			}
		}
		for _, row := range statusOffList686(doc) {
			if row["id"] == groupKey && row["needsAttention"] != nil {
				t.Fatalf("group offList must not be flagged")
			}
		}
	})
	t.Run("group_not_in_plan_with_manual_staged_item_line", func(t *testing.T) {
		handler, env := setupTestWithDB(t)
		registerProductionShapeKroger(env)
		scope := primaryScope(t, env.DB)
		hid := scope.HouseholdID()
		const itemID = "item-h686-h11c"
		lineKey := shopping.ItemLineKey(itemID)
		groupID := "grp-h686-h11c"
		createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", "prod-h686-h11c", "H11C", itemID)
		mustExecTest(t, env.DB, `INSERT INTO product_groups (id, household_id, user_id, name, name_key, rule, rule_confirmed) VALUES (?, ?, ?, 'H11CG', 'h11cg', 'favorite', 1)`,
			groupID, hid, hid)
		mustExecTest(t, env.DB, `INSERT INTO product_group_members (household_id, group_id, product_id) VALUES (?, ?, 'prod-h686-h11c')`,
			hid, groupID)
		beginUsing(t, env.DB, time.Now())
		mustExecTest(t, env.DB, `INSERT INTO shopping_list_items (id, household_id, user_id, item_id, quantity, source, created_at)
			VALUES ('line-h686-h11c', ?, ?, ?, 1, 'manual', datetime('now'))`, hid, hid, itemID)
		seedCartLineFailedSend(t, env.DB, hid, itemID, "api_error")
		doc := statusGET686(t, handler, hid, h686Provider)
		na := naForLine686(t, doc, lineKey)
		if na["reason"] != "send_failed" {
			t.Fatalf("manual staged item line %#v", na)
		}
		groupKey := shopping.GroupLineKey(groupID)
		for _, row := range append(statusItems686(doc), statusOffList686(doc)...) {
			id, _ := row["lineKey"].(string)
			if id == "" {
				id, _ = row["id"].(string)
			}
			if id == groupKey && row["needsAttention"] != nil {
				t.Fatalf("group line must not be flagged %#v", row)
			}
		}
	})
	t.Run("h11b_zero_staged_send_flags_item_not_group", func(t *testing.T) {
		handler, env := setupTestWithDB(t)
		registerProductionShapeKroger(env)
		scope := primaryScope(t, env.DB)
		hid := scope.HouseholdID()
		const itemID = "item-h686-h11d"
		lineKey := shopping.ItemLineKey(itemID)
		groupID := "grp-h686-h11d"
		groupKey := shopping.GroupLineKey(groupID)
		createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", "prod-h686-h11d", "H11D", itemID)
		mustExecTest(t, env.DB, `INSERT INTO product_groups (id, household_id, user_id, name, name_key, rule, rule_confirmed) VALUES (?, ?, ?, 'H11DG', 'h11dg', 'favorite', 1)`,
			groupID, hid, hid)
		mustExecTest(t, env.DB, `INSERT INTO product_group_members (household_id, group_id, product_id) VALUES (?, ?, 'prod-h686-h11d')`,
			hid, groupID)
		beginUsing(t, env.DB, time.Now())
		exchangeJSON(t, handler, http.MethodPut, "/api/restock/items/"+itemID, `{"kind":"amount","value":2}`, http.StatusOK)
		mustExecTest(t, env.DB, `DELETE FROM shopping_list_items WHERE household_id = ? AND item_id = ? AND purchased_at IS NULL`, hid, itemID)
		seedCartLineFailedSend(t, env.DB, hid, itemID, "api_error")
		var n int
		if err := env.DB.QueryRow(`SELECT COUNT(*) FROM shopping_list_items WHERE household_id = ? AND item_id = ? AND purchased_at IS NULL AND quantity >= 1`,
			hid, itemID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Fatalf("fixture wants zero staged unpurchased qty for %s, got %d rows", itemID, n)
		}
		doc := statusGET686(t, handler, hid, h686Provider)
		na := naForLine686(t, doc, lineKey)
		if na["reason"] != "send_failed" {
			t.Fatalf("H11(b) HTTP want send_failed on %s %#v", lineKey, na)
		}
		for _, ag := range statusAttentionGroups686(doc) {
			rowKeys, _ := ag["rowKeys"].([]any)
			for _, rk := range rowKeys {
				if rk == groupKey {
					t.Fatalf("attentionGroups must not cite group line %#v", ag)
				}
			}
		}
		for _, row := range append(statusItems686(doc), statusOffList686(doc)...) {
			id, _ := row["lineKey"].(string)
			if id == "" {
				id, _ = row["id"].(string)
			}
			if id == groupKey {
				if row["needsAttention"] != nil {
					t.Fatalf("send must not flag group line %#v", row["needsAttention"])
				}
			}
		}
	})
	t.Run("h11b_shelved_member_stale_staged_group_not_flagged", func(t *testing.T) {
		handler, env := setupTestWithDB(t)
		registerProductionShapeKroger(env)
		scope := primaryScope(t, env.DB)
		hid := scope.HouseholdID()
		const itemX = "item-h686-h11s-x"
		const itemY = "item-h686-h11s-y"
		const prodX = "prod-h686-h11s-x"
		const prodY = "prod-h686-h11s-y"
		const lineY = "line-h686-h11s-y"
		groupID := "grp-h686-h11s"
		createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", prodX, "SX", itemX)
		createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", prodY, "SY", itemY)
		mustExecTest(t, env.DB, `INSERT INTO product_groups (id, household_id, user_id, name, name_key, rule, rule_confirmed) VALUES (?, ?, ?, 'H11S', 'h11s', 'favorite', 1)`,
			groupID, hid, hid)
		mustExecTest(t, env.DB, `INSERT INTO product_group_members (household_id, group_id, product_id) VALUES (?, ?, ?)`,
			hid, groupID, prodX)
		mustExecTest(t, env.DB, `UPDATE items SET shelved = 1 WHERE id = ?`, itemX)
		beginUsing(t, env.DB, time.Now())
		mustExecTest(t, env.DB, `INSERT INTO shopping_list_items (id, household_id, user_id, item_id, group_id, quantity, source, created_at)
			VALUES (?, ?, ?, ?, ?, 1, 'manual', datetime('now'))`, lineY, hid, hid, itemY, groupID)
		seedCartLineFailedSend(t, env.DB, hid, itemX, "api_error")
		doc := statusGET686(t, handler, hid, h686Provider)
		for _, ag := range statusAttentionGroups686(doc) {
			if ag["reason"] == "send_failed" {
				t.Fatalf("stale send on shelved member must not produce send_failed attentionGroups %#v", ag)
			}
		}
		for _, row := range append(statusItems686(doc), statusOffList686(doc)...) {
			if row["needsAttention"] != nil {
				id, _ := row["lineKey"].(string)
				if id == "" {
					id, _ = row["id"].(string)
				}
				t.Fatalf("row %q must not be flagged %#v", id, row["needsAttention"])
			}
		}
		var yFound bool
		for _, row := range statusItems686(doc) {
			if row["id"] == lineY {
				yFound = true
				if row["quantity"] != float64(1) {
					t.Fatalf("Y list row quantity %#v want 1", row["quantity"])
				}
				break
			}
		}
		if !yFound {
			t.Fatalf("staged row %q missing from items[]", lineY)
		}
		var yQty int
		if err := env.DB.QueryRow(`SELECT quantity FROM shopping_list_items WHERE id = ?`, lineY).Scan(&yQty); err != nil {
			t.Fatal(err)
		}
		if yQty != 1 {
			t.Fatalf("Y staged row quantity = %d, want 1", yQty)
		}
	})
	t.Run("h11_step3_group_in_planviews_send_on_member", func(t *testing.T) {
		handler, env := setupTestWithDB(t)
		registerProductionShapeKroger(env)
		scope := primaryScope(t, env.DB)
		hid := scope.HouseholdID()
		const itemA = "item-h686-h11e-a"
		const itemB = "item-h686-h11e-b"
		groupID := "grp-h686-h11e"
		groupKey := shopping.GroupLineKey(groupID)
		itemKey := shopping.ItemLineKey(itemB)
		createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", "prod-h686-h11e-a", "E", itemA)
		createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", "prod-h686-h11e-b", "E", itemB)
		mustExecTest(t, env.DB, `INSERT INTO product_groups (id, household_id, user_id, name, name_key, rule, rule_confirmed) VALUES (?, ?, ?, 'H11E', 'h11e', 'favorite', 1)`,
			groupID, hid, hid)
		mustExecTest(t, env.DB, `INSERT INTO product_group_members (household_id, group_id, product_id) VALUES (?, ?, 'prod-h686-h11e-a'), (?, ?, 'prod-h686-h11e-b')`,
			hid, groupID, hid, groupID)
		beginUsing(t, env.DB, time.Now())
		exchangeJSON(t, handler, http.MethodPut, "/api/restock/items/"+itemA, `{"kind":"amount","value":2}`, http.StatusOK)
		exchangeJSON(t, handler, http.MethodPost, "/api/shopping-list/fill", ``, http.StatusOK)
		mustExecTest(t, env.DB, `UPDATE shopping_list_items SET group_id = ? WHERE household_id = ? AND item_id = ?`, groupID, hid, itemA)
		seedCartLineFailedSend(t, env.DB, hid, itemB, "api_error")
		doc := statusGET686(t, handler, hid, h686Provider)
		na := naForLine686(t, doc, groupKey)
		if na["reason"] != "send_failed" {
			t.Fatalf("step 3 group line %#v", na)
		}
		for _, row := range append(statusItems686(doc), statusOffList686(doc)...) {
			id, _ := row["lineKey"].(string)
			if id == "" {
				id, _ = row["id"].(string)
			}
			if id == itemKey && row["needsAttention"] != nil {
				t.Fatalf("member without staged row must not carry send %#v", row)
			}
		}
	})
}

// TestStatusAttentionGroupDetailTieBreak (H12) — end-to-end §6.2 (i) and (iii).
func TestStatusAttentionGroupDetailTieBreak(t *testing.T) {
	now := time.Now().UTC()
	t0 := now.Add(-48 * time.Hour).Format(time.RFC3339)
	t1 := now.Add(-48*time.Hour + time.Second).Format(time.RFC3339)
	cases := []struct {
		name                                   string
		aAt, bAt, aReason, bReason, wantDetail string
	}{
		{"i_latest_time", t0, t1, "api_error", "batch_rejected", "batch_rejected"},
		{"iii_item_id_tie", t1, t1, "api_error", "batch_rejected", "api_error"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			handler, env := setupTestWithDB(t)
			registerProductionShapeKroger(env)
			scope := primaryScope(t, env.DB)
			hid := scope.HouseholdID()
			itemA := "item-h686-h12a-" + tc.name
			itemB := "item-h686-h12b-" + tc.name
			prodA := "prod-h686-h12a-" + tc.name
			prodB := "prod-h686-h12b-" + tc.name
			groupID := "grp-h686-h12-" + tc.name
			groupKey := shopping.GroupLineKey(groupID)
			createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", prodA, "A", itemA)
			createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", prodB, "B", itemB)
			mustExecTest(t, env.DB, `INSERT INTO product_groups (id, household_id, user_id, name, name_key, rule, rule_confirmed) VALUES (?, ?, ?, 'H12', 'h12', 'favorite', 1)`,
				groupID, hid, hid)
			mustExecTest(t, env.DB, `INSERT INTO product_group_members (household_id, group_id, product_id) VALUES (?, ?, ?), (?, ?, ?)`,
				hid, groupID, prodA, hid, groupID, prodB)
			beginUsing(t, env.DB, time.Now())
			exchangeJSON(t, handler, http.MethodPut, "/api/restock/items/"+itemA, `{"kind":"amount","value":2}`, http.StatusOK)
			exchangeJSON(t, handler, http.MethodPost, "/api/shopping-list/fill", ``, http.StatusOK)
			mustExecTest(t, env.DB, `UPDATE shopping_list_items SET group_id = ? WHERE household_id = ? AND item_id = ?`, groupID, hid, itemA)
			seedCartLineResultAt(t, env, hid, h686Provider, itemA, "failed", tc.aReason, tc.aAt)
			seedCartLineResultAt(t, env, hid, h686Provider, itemB, "failed", tc.bReason, tc.bAt)
			doc := statusGET686(t, handler, hid, h686Provider)
			na := naForLine686(t, doc, groupKey)
			if na["detail"] != tc.wantDetail {
				t.Fatalf("detail %#v want %q", na["detail"], tc.wantDetail)
			}
			groups := statusAttentionGroups686(doc)
			if len(groups) != 1 {
				t.Fatalf("attentionGroups %#v", groups)
			}
			rowKeys, _ := groups[0]["rowKeys"].([]any)
			if len(rowKeys) != 1 || rowKeys[0] != groupKey {
				t.Fatalf("rowKeys %#v want %q", rowKeys, groupKey)
			}
		})
	}
}

// TestStatusAttentionReservedReasonsNotEmitted (H13-reserved) — probable_miss only.
func TestStatusAttentionReservedReasonsNotEmitted(t *testing.T) {
	handler, env := setupTestWithDB(t)
	registerProductionShapeKroger(env)
	scope := primaryScope(t, env.DB)
	hid := scope.HouseholdID()
	beginUsing(t, env.DB, time.Now())
	mustExecTest(t, env.DB, `INSERT INTO stores (id, household_id, name, name_key, kind, linked_provider, location_id, location_chain, position, created_at, updated_at)
		VALUES ('store-h686-h13', ?, 'H13 Store', 'h13store', 'physical', 'kroger', '01400376', 'KROGER', 0, datetime('now'), datetime('now'))`, hid)
	const itemSent = "item-h686-h13-sent"
	createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", "prod-h686-h13-sent", "H13 Sent", itemSent)
	exchangeJSON(t, handler, http.MethodPut, "/api/restock/items/"+itemSent, `{"kind":"amount","value":2}`, http.StatusOK)
	exchangeJSON(t, handler, http.MethodPost, "/api/shopping-list/fill", ``, http.StatusOK)
	sentAt := time.Now().UTC().Add(-2 * time.Hour).Format(time.RFC3339)
	mustExecTest(t, env.DB, `DELETE FROM cart_line_results WHERE household_id = ? AND item_id = ?`, hid, itemSent)
	seedCartLineResultAt(t, env, hid, h686Provider, itemSent, "sent", "", sentAt)
	mustExecTest(t, env.DB, `INSERT INTO stock_in_clusters (household_id, started_at, closed_at, product_count, k_used, trip_seq)
		VALUES (?, '2026-01-01T00:00:00Z', ?, 1, 1, 1)`, hid, time.Now().UTC().Format(time.RFC3339))
	doc := statusGET686(t, handler, hid, h686Provider)
	assertNoReservedReasons(t, doc)
	_ = scope
}

// TestStatusAttentionSkipExpiryEdges (H14) — A3a through HTTP.
func TestStatusAttentionSkipExpiryEdges(t *testing.T) {
	handler, env := setupTestWithDB(t)
	registerProductionShapeKroger(env)
	scope := primaryScope(t, env.DB)
	hid := scope.HouseholdID()
	var tripSeq int
	if err := env.DB.QueryRow(`SELECT trip_seq FROM households WHERE id = ?`, hid).Scan(&tripSeq); err != nil {
		t.Fatal(err)
	}
	const itemT = "item-h686-h14-t"
	const itemTP = "item-h686-h14-tp"
	beginUsing(t, env.DB, time.Now())
	createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", "prod-h686-h14-t", "H14T", itemT)
	createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", "prod-h686-h14-tp", "H14TP", itemTP)
	exchangeJSON(t, handler, http.MethodPut, "/api/restock/items/"+itemT, `{"kind":"amount","value":2}`, http.StatusOK)
	exchangeJSON(t, handler, http.MethodPut, "/api/restock/items/"+itemTP, `{"kind":"amount","value":2}`, http.StatusOK)
	exchangeJSON(t, handler, http.MethodPost, "/api/shopping-list/fill", ``, http.StatusOK)
	keyT := shopping.ItemLineKey(itemT)
	keyTP := shopping.ItemLineKey(itemTP)
	mustExecTest(t, env.DB, `INSERT INTO staged_cart_skips (household_id, user_id, item_id, expires_trip) VALUES (?, ?, ?, ?)`,
		hid, hid, itemT, tripSeq)
	mustExecTest(t, env.DB, `INSERT INTO staged_cart_skips (household_id, user_id, item_id, expires_trip) VALUES (?, ?, ?, ?)`,
		hid, hid, itemTP, tripSeq+1)
	seedCartLineFailedSend(t, env.DB, hid, itemT, "api_error")
	seedCartLineFailedSend(t, env.DB, hid, itemTP, "api_error")
	doc := statusGET686(t, handler, hid, h686Provider)
	for _, row := range append(statusItems686(doc), statusOffList686(doc)...) {
		id, _ := row["lineKey"].(string)
		if id == "" {
			id, _ = row["id"].(string)
		}
		if id == keyTP {
			if row["needsAttention"] != nil {
				t.Fatalf("expires_trip T+1 must not flag %#v", row["needsAttention"])
			}
		}
	}
	naT := naForLine686(t, doc, keyT)
	if naT["reason"] != "send_failed" {
		t.Fatalf("expires_trip T must flag send_failed %#v", naT)
	}
}

// TestStatusAttentionZeroExpiryEdges (H15) — A5 through HTTP.
func TestStatusAttentionZeroExpiryEdges(t *testing.T) {
	handler, env := setupTestWithDB(t)
	registerProductionShapeKroger(env)
	scope := primaryScope(t, env.DB)
	hid := scope.HouseholdID()
	const tripSeq = 5
	mustExecTest(t, env.DB, `UPDATE households SET trip_seq = ? WHERE id = ?`, tripSeq, hid)
	type fixture struct {
		item, line, exp string
	}
	fixtures := []fixture{
		{"item-h686-h15-t", "line-h686-h15-t", "5"},
		{"item-h686-h15-tp", "line-h686-h15-tp", "6"},
		{"item-h686-h15-null", "line-h686-h15-null", "NULL"},
	}
	keys := map[string]string{}
	for _, fx := range fixtures {
		prod := "prod-" + fx.item
		createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", prod, fx.item, fx.item)
		beginUsing(t, env.DB, time.Now())
		exchangeJSON(t, handler, http.MethodPut, "/api/restock/items/"+fx.item, `{"kind":"amount","value":2}`, http.StatusOK)
		exchangeJSON(t, handler, http.MethodPost, "/api/shopping-list/fill", ``, http.StatusOK)
		mustExecTest(t, env.DB, `UPDATE shopping_list_items SET id = ? WHERE household_id = ? AND item_id = ?`, fx.line, hid, fx.item)
		exchangeJSON(t, handler, http.MethodPut, "/api/shopping-list/items/"+fx.line+"/adjustment?provider=kroger", `{"adjustment":0}`, http.StatusOK)
		if fx.exp == "NULL" {
			mustExecTest(t, env.DB, `UPDATE shopping_list_entry_adjustments SET expires_trip = NULL WHERE household_id = ? AND entry_id = ?`, hid, fx.line)
		} else {
			mustExecTest(t, env.DB, `UPDATE shopping_list_entry_adjustments SET expires_trip = ? WHERE household_id = ? AND entry_id = ?`, fx.exp, hid, fx.line)
		}
		keys[fx.item] = shopping.ItemLineKey(fx.item)
		seedCartLineFailedSend(t, env.DB, hid, fx.item, "api_error")
	}
	_ = scope
	doc := statusGET686(t, handler, hid, h686Provider)
	keyT := keys["item-h686-h15-t"]
	naT := naForLine686(t, doc, keyT)
	if naT["reason"] != "send_failed" {
		t.Fatalf("expires_trip T want send_failed %#v", naT)
	}
	for _, item := range []string{"item-h686-h15-tp", "item-h686-h15-null"} {
		key := keys[item]
		for _, row := range append(statusItems686(doc), statusOffList686(doc)...) {
			id, _ := row["lineKey"].(string)
			if id == "" {
				id, _ = row["id"].(string)
			}
			if id == key && row["needsAttention"] != nil {
				t.Fatalf("%s must not flag %#v", item, row["needsAttention"])
			}
		}
	}
}
