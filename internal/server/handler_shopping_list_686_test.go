package server

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/attention"
	"github.com/Rhionin/stockbot/internal/shopping"
)

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
		{"skipped", "api_error", false, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.status+"/"+tc.reason, func(t *testing.T) {
			mustExecTest(t, env.DB, `DELETE FROM cart_line_results WHERE household_id = ? AND item_id = 'item-h3'`, hid)
			reason := tc.reason
			if reason == "" {
				mustExecTest(t, env.DB, `INSERT INTO cart_line_results (household_id, provider_id, item_id, status, reason, quantity, attempted_at)
					VALUES (?, 'kroger', 'item-h3', ?, NULL, 1, datetime('now'))`, hid, tc.status)
			} else {
				mustExecTest(t, env.DB, `INSERT INTO cart_line_results (household_id, provider_id, item_id, status, reason, quantity, attempted_at)
					VALUES (?, 'kroger', 'item-h3', ?, ?, 1, datetime('now'))`, hid, tc.status, reason)
			}
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
				if na["reason"] == reason {
					t.Fatal("stored reason must not appear on wire reason")
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

func TestReasonsParityHostedContract(t *testing.T) {
	got := make([]string, len(attention.Reasons))
	for i, r := range attention.Reasons {
		got[i] = string(r)
	}
	want := []string{
		"not_connected", "reconnect_required", "no_barcode", "no_store_match",
		"send_unsure", "send_failed", "probable_miss", "not_planned",
	}
	if len(got) != len(want) {
		t.Fatalf("len %d want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("reason[%d] %q want %q", i, got[i], want[i])
		}
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