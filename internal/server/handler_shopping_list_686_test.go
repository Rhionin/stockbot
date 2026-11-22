package server

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/shopping"
)

func assertEmittableWireReason686(reason string) bool {
	switch reason {
	case "no_barcode", "no_store_match", "send_unsure", "send_failed", "not_planned":
		return true
	}
	return false
}

func TestStatusAttentionStoredReasonCatchAll(t *testing.T) {
	handler, env := setupTestWithDB(t)
	registerProductionShapeKroger(env)
	scope := primaryScope(t, env.DB)
	hid := scope.HouseholdID()
	createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", "prod-h3", "H3", "item-h3")
	beginUsing(t, env.DB, time.Now())
	exchangeJSON(t, handler, http.MethodPut, "/api/restock/items/item-h3", `{"kind":"amount","value":2}`, http.StatusOK)
	exchangeJSON(t, handler, http.MethodPost, "/api/shopping-list/fill", ``, http.StatusOK)
	lineKey := shopping.ItemLineKey("item-h3")
	cases := []struct {
		status, reason string
		wantAttn       bool
		wantReason     string
		wantDetail     bool
	}{
		{"failed", "", true, "send_failed", false},
		{"failed", "zz_future_code", true, "send_failed", false},
		{"failed", "no_upc", true, "send_failed", false},
		{"unknown", "", true, "send_unsure", false},
		{"unknown", "zz_future_code", true, "send_unsure", false},
		{"skipped", "", false, "", false},
		{"skipped", "api_error", false, "", false},
		{"skipped", "zz_future_code", false, "", false},
		{"sent", "zz_future_code", false, "", false},
		{"bogus_status", "api_error", false, "", false},
	}
	insertResult := func(tc struct {
		status, reason string
		wantAttn       bool
		wantReason     string
		wantDetail     bool
	}) {
		mustExecTest(t, env.DB, `DELETE FROM cart_line_results WHERE household_id = ? AND item_id = 'item-h3'`, hid)
		if tc.reason == "" {
			mustExecTest(t, env.DB, `INSERT INTO cart_line_results (household_id, provider_id, item_id, status, reason, quantity, attempted_at)
				VALUES (?, 'kroger', 'item-h3', ?, NULL, 1, datetime('now'))`, hid, tc.status)
			return
		}
		mustExecTest(t, env.DB, `INSERT INTO cart_line_results (household_id, provider_id, item_id, status, reason, quantity, attempted_at)
			VALUES (?, 'kroger', 'item-h3', ?, ?, 1, datetime('now'))`, hid, tc.status, tc.reason)
	}
	for _, tc := range cases {
		t.Run(tc.status+"/"+tc.reason, func(t *testing.T) {
			insertResult(tc)
			body := exchangeJSON(t, handler, http.MethodGet, "/api/shopping-list?status=1&provider=kroger", "", http.StatusOK)
			var doc map[string]any
			if err := json.Unmarshal(body, &doc); err != nil {
				t.Fatal(err)
			}
			var na map[string]any
			for _, row := range doc["items"].([]any) {
				m := row.(map[string]any)
				if m["lineKey"] == lineKey {
					if v, ok := m["needsAttention"]; ok && v != nil {
						na = v.(map[string]any)
					}
				}
			}
			if tc.wantAttn && na == nil {
				t.Fatal("expected needsAttention")
			}
			if !tc.wantAttn && na != nil {
				t.Fatalf("unexpected needsAttention %#v", na)
			}
			if na != nil {
				if na["reason"] != tc.wantReason {
					t.Fatalf("reason %#v", na["reason"])
				}
				_, hasDetail := na["detail"]
				if tc.wantDetail != hasDetail {
					t.Fatalf("detail presence %v want %v", hasDetail, tc.wantDetail)
				}
				rsn, _ := na["reason"].(string)
				if !assertEmittableWireReason686(rsn) {
					t.Fatalf("reason %q not emittable", rsn)
				}
				if tc.reason != "" && (rsn == tc.reason) {
					t.Fatal("stored reason must not appear on wire reason")
				}
				if others, ok := na["others"].([]any); ok {
					for _, o := range others {
						os, _ := o.(string)
						if !assertEmittableWireReason686(os) {
							t.Fatalf("other %q not emittable", os)
						}
						if tc.reason != "" && os == tc.reason {
							t.Fatal("stored reason must not appear on wire others")
						}
					}
				}
			}
		})
	}
}

// When DISABLE_KROGER is set, sending is off but the household's target provider
// stays resolved for read paths; prior send problems still surface (spec §D.1; not §6.1).
func TestStatusAttentionSendFailedWhenKrogerDisabled(t *testing.T) {
	handler, env := setupTestWithContributor(t, nil, WithProviderEnv(ProviderEnv{Disabled: true}))
	registerProductionShapeKroger(env)
	scope := primaryScope(t, env.DB)
	hid := scope.HouseholdID()
	createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", "prod-h2d", "H2D", "item-h2d")
	beginUsing(t, env.DB, time.Now())
	exchangeJSON(t, handler, http.MethodPut, "/api/restock/items/item-h2d", `{"kind":"amount","value":2}`, http.StatusOK)
	exchangeJSON(t, handler, http.MethodPost, "/api/shopping-list/fill", ``, http.StatusOK)
	lineKey := shopping.ItemLineKey("item-h2d")
	mustExecTest(t, env.DB, `INSERT INTO cart_line_results (household_id, provider_id, item_id, status, reason, quantity, attempted_at)
		VALUES (?, 'kroger', 'item-h2d', 'failed', NULL, 1, datetime('now'))`, hid)
	body := exchangeJSON(t, handler, http.MethodGet, "/api/shopping-list?status=1", "", http.StatusOK)
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	var na map[string]any
	for _, row := range doc["items"].([]any) {
		m := row.(map[string]any)
		if m["lineKey"] == lineKey {
			if v, ok := m["needsAttention"]; ok && v != nil {
				na = v.(map[string]any)
			}
		}
	}
	if na == nil {
		t.Fatal("expected send_failed needsAttention with DISABLE_KROGER")
	}
	if na["reason"] != "send_failed" {
		t.Fatalf("reason %#v", na["reason"])
	}
}

func TestStatusEntryHasNameAndNeedsAttentionKey(t *testing.T) {
	handler, env := setupTestWithDB(t)
	createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", "prod-h5", "Renamed", "item-h5")
	beginUsing(t, env.DB, time.Now())
	mustExecTest(t, env.DB, `INSERT INTO household_product_overrides (household_id, product_id, display_name, updated_at) VALUES (?, 'prod-h5', 'House Name', datetime('now'))`,
		primaryScope(t, env.DB).HouseholdID())
	exchangeJSON(t, handler, http.MethodPut, "/api/restock/items/item-h5", `{"kind":"amount","value":2}`, http.StatusOK)
	exchangeJSON(t, handler, http.MethodPost, "/api/shopping-list/fill", ``, http.StatusOK)
	body := exchangeJSON(t, handler, http.MethodGet, "/api/shopping-list?status=1", "", http.StatusOK)
	var doc struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Items) == 0 {
		t.Fatal("no items")
	}
	if _, ok := doc.Items[0]["name"]; !ok {
		t.Fatal("status item missing name")
	}
	if doc.Items[0]["name"] != "House Name" {
		t.Fatalf("name %#v", doc.Items[0]["name"])
	}
	if _, ok := doc.Items[0]["needsAttention"]; !ok {
		t.Fatal("missing needsAttention key")
	}
	defaultBody := exchangeJSON(t, handler, http.MethodGet, "/api/shopping-list", "", http.StatusOK)
	var defaultRows []map[string]any
	if err := json.Unmarshal(defaultBody, &defaultRows); err != nil {
		t.Fatal(err)
	}
	if _, ok := defaultRows[0]["name"]; ok {
		t.Fatal("default GET must not include name")
	}
}

func containsLiteral(s, lit string) bool {
	return len(s) >= len(lit) && (func() bool {
		for i := 0; i+len(lit) <= len(s); i++ {
			if s[i:i+len(lit)] == lit {
				return true
			}
		}
		return false
	})()
}