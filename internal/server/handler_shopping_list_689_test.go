package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/Rhionin/stockbot/internal/cart/connection"
	"github.com/Rhionin/stockbot/internal/shopping"
	"github.com/Rhionin/stockbot/internal/tenant"
)

func setup689WithAuth(t *testing.T) (http.Handler, testEnv) {
	t.Helper()
	db := setupTestDB(t)
	svc := auth.NewService(db, t.TempDir())
	handler, env := setupOnDB(t, db, nil, WithStage1(svc, stage1Site(t)))
	return handler, env
}

func h689SeedShortKrogerLine(t *testing.T, handler http.Handler, env testEnv) (hid, lineKey, itemID string) {
	t.Helper()
	registerProductionShapeKroger(env)
	scope := primaryScope(t, env.DB)
	hid = scope.HouseholdID()
	itemID = "item-h689-line"
	const prodID = "prod-h689-line"
	mustExecTest(t, env.DB, `INSERT INTO stores (id, household_id, name, name_key, kind, linked_provider, location_id, location_chain, position, created_at, updated_at)
		VALUES ('store-h689', ?, 'H689 Store', 'h689store', 'physical', 'kroger', '01400376', 'KROGER', 0, datetime('now'), datetime('now'))`, hid)
	mustExecTest(t, env.DB, `DELETE FROM provider_connections WHERE household_id = ? AND provider_id = 'kroger'`, hid)
	createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", prodID, "H689", itemID)
	beginUsing(t, env.DB, time.Now())
	exchangeJSON(t, handler, http.MethodPut, "/api/restock/items/"+itemID, `{"kind":"amount","value":2}`, http.StatusOK)
	exchangeJSON(t, handler, http.MethodPost, "/api/shopping-list/fill", ``, http.StatusOK)
	lineKey = shopping.ItemLineKey(itemID)
	return hid, lineKey, itemID
}

func assertConnFix689(t *testing.T, na map[string]any, allowed bool) {
	t.Helper()
	fix, _ := na["fix"].(map[string]any)
	if fix == nil {
		t.Fatal("missing fix")
	}
	if fix["code"] != "connect_store" || fix["allowed"] != allowed {
		t.Fatalf("fix %#v want connect_store allowed=%v", fix, allowed)
	}
	target, _ := fix["target"].(map[string]any)
	if target["providerId"] != "kroger" {
		t.Fatalf("target provider %#v", target)
	}
	if _, has := na["detail"]; has {
		t.Fatalf("connection reason must not carry detail %#v", na)
	}
	others, _ := na["others"].([]any)
	if len(others) != 0 {
		t.Fatalf("others %#v", others)
	}
	if allowed {
		if fix["who"] != nil {
			t.Fatalf("owner who %#v", fix["who"])
		}
		return
	}
	who, _ := fix["who"].(map[string]any)
	if who["role"] != "owner" {
		t.Fatalf("who %#v", who)
	}
}

func assertConnReason689(t *testing.T, doc map[string]any, lineKey, reason string) map[string]any {
	t.Helper()
	na := naForLine686(t, doc, lineKey)
	if na["reason"] != reason {
		t.Fatalf("reason %#v want %s", na, reason)
	}
	assertConnFix689(t, na, true)
	groups := statusAttentionGroups686(doc)
	if len(groups) != 1 || groups[0]["reason"] != reason || groups[0]["count"] != float64(1) {
		t.Fatalf("attentionGroups %#v", groups)
	}
	return na
}

func writeKrogerConn689(t *testing.T, env testEnv, state connection.ConnectionState, access string) {
	t.Helper()
	scope := primaryScope(t, env.DB)
	expires := time.Now().UTC().Add(time.Hour)
	conn := &connection.Connection{
		Provider: "kroger", State: state, AccessToken: access, RefreshToken: "r", ExpiresAt: &expires,
	}
	if err := connection.NewDirectory(env.DB).Write(context.Background(), scope, conn); err != nil {
		t.Fatal(err)
	}
}

func TestStatusAttentionConnectionReasons(t *testing.T) {
	t.Run("no_row", func(t *testing.T) {
		handler, env := setupTestWithDB(t)
		hid, lineKey, _ := h689SeedShortKrogerLine(t, handler, env)
		doc := statusGET686(t, handler, hid, h686Provider)
		assertConnReason689(t, doc, lineKey, "not_connected")
	})
	t.Run("generate_auth_state", func(t *testing.T) {
		handler, env := setupTestWithDB(t)
		hid, lineKey, _ := h689SeedShortKrogerLine(t, handler, env)
		scope, _ := tenantScope689(t, env, hid)
		if err := connection.NewDirectory(env.DB).GenerateAuthState(context.Background(), scope, "kroger", "state-h689", "app", "shopping"); err != nil {
			t.Fatal(err)
		}
		doc := statusGET686(t, handler, hid, h686Provider)
		assertConnReason689(t, doc, lineKey, "not_connected")
	})
	t.Run("disconnect_after_connected", func(t *testing.T) {
		handler, env := setupTestWithDB(t)
		hid, lineKey, _ := h689SeedShortKrogerLine(t, handler, env)
		writeKrogerConn689(t, env, connection.StateConnected, "live")
		scope, _ := tenantScope689(t, env, hid)
		if err := connection.NewDirectory(env.DB).Disconnect(context.Background(), scope, "kroger"); err != nil {
			t.Fatal(err)
		}
		doc := statusGET686(t, handler, hid, h686Provider)
		assertConnReason689(t, doc, lineKey, "not_connected")
	})
	t.Run("reauth_mark", func(t *testing.T) {
		handler, env := setupTestWithDB(t)
		hid, lineKey, _ := h689SeedShortKrogerLine(t, handler, env)
		writeKrogerConn689(t, env, connection.StateConnected, "live")
		mustExecTest(t, env.DB, `UPDATE provider_connections SET state='reauth_required' WHERE household_id=? AND provider_id='kroger'`, hid)
		doc := statusGET686(t, handler, hid, h686Provider)
		assertConnReason689(t, doc, lineKey, "reconnect_required")
	})
	t.Run("reauth_app_client_change", func(t *testing.T) {
		handler, env := setupTestWithDB(t)
		hid, lineKey, _ := h689SeedShortKrogerLine(t, handler, env)
		writeKrogerConn689(t, env, connection.StateConnected, "live")
		mustExecTest(t, env.DB, `UPDATE provider_app_credentials SET client_id='client-b' WHERE household_id=? AND provider_id='kroger'`, hid)
		mustExecTest(t, env.DB, `UPDATE provider_connections SET state='reauth_required', access_token=NULL, refresh_token=NULL, access_token_expires_at=NULL WHERE household_id=? AND provider_id='kroger'`, hid)
		doc := statusGET686(t, handler, hid, h686Provider)
		assertConnReason689(t, doc, lineKey, "reconnect_required")
	})
	t.Run("raw_empty_state", func(t *testing.T) {
		handler, env := setupTestWithDB(t)
		hid, lineKey, _ := h689SeedShortKrogerLine(t, handler, env)
		mustExecTest(t, env.DB, `INSERT INTO provider_connections (id, user_id, household_id, provider_id, state, updated_at)
			VALUES (?, ?, ?, 'kroger', '', datetime('now'))`, hid+":kroger", hid, hid)
		doc := statusGET686(t, handler, hid, h686Provider)
		assertConnReason689(t, doc, lineKey, "not_connected")
	})
	t.Run("raw_unknown_state", func(t *testing.T) {
		handler, env := setupTestWithDB(t)
		hid, lineKey, _ := h689SeedShortKrogerLine(t, handler, env)
		mustExecTest(t, env.DB, `INSERT INTO provider_connections (id, user_id, household_id, provider_id, state, updated_at)
			VALUES (?, ?, ?, 'kroger', 'zz_future', datetime('now'))`, hid+":kroger2", hid, hid)
		doc := statusGET686(t, handler, hid, h686Provider)
		assertConnReason689(t, doc, lineKey, "not_connected")
	})
	t.Run("connected", func(t *testing.T) {
		handler, env := setupTestWithDB(t)
		hid, lineKey, _ := h689SeedShortKrogerLine(t, handler, env)
		writeKrogerConn689(t, env, connection.StateConnected, "live")
		doc := statusGET686(t, handler, hid, h686Provider)
		for _, row := range statusItems686(doc) {
			if row["lineKey"] == lineKey {
				if na, ok := row["needsAttention"]; ok && na != nil {
					t.Fatalf("connected should not flag %#v", na)
				}
			}
		}
	})
}

func tenantScope689(t *testing.T, _ testEnv, hid string) (tenant.Scope, error) {
	t.Helper()
	return tenant.New(hid)
}

func TestStatusAttentionConnectionOutranksSendReconnect(t *testing.T) {
	handler, env := setupTestWithDB(t)
	hid, lineKey, itemID := h689SeedShortKrogerLine(t, handler, env)
	writeKrogerConn689(t, env, connection.StateConnected, "live")
	seedCartLineFailedSend(t, env.DB, hid, itemID, "reconnect_required")
	doc := statusGET686(t, handler, hid, h686Provider)
	na := naForLine686(t, doc, lineKey)
	if na["reason"] != "send_failed" || na["detail"] != "reconnect_required" {
		t.Fatalf("connected only send %#v", na)
	}
	mustExecTest(t, env.DB, `UPDATE provider_connections SET state='reauth_required' WHERE household_id=? AND provider_id='kroger'`, hid)
	doc = statusGET686(t, handler, hid, h686Provider)
	na = naForLine686(t, doc, lineKey)
	if na["reason"] != "reconnect_required" {
		t.Fatalf("reauth rank %#v", na)
	}
	if others, _ := na["others"].([]any); len(others) != 1 || others[0] != "send_failed" {
		t.Fatalf("others %#v", na["others"])
	}
	if _, has := na["detail"]; has {
		t.Fatal("connection primary must omit detail")
	}
	mustExecTest(t, env.DB, `DELETE FROM provider_connections WHERE household_id=? AND provider_id='kroger'`, hid)
	seedCartLineResultAt(t, env, hid, h686Provider, itemID, "skipped", "no_upc", time.Now().UTC().Format(time.RFC3339))
	doc = statusGET686(t, handler, hid, h686Provider)
	na = naForLine686(t, doc, lineKey)
	if na["reason"] != "not_connected" {
		t.Fatalf("not_connected rank %#v", na)
	}
	if others, _ := na["others"].([]any); len(others) != 1 || others[0] != "no_barcode" {
		t.Fatalf("others %#v", na["others"])
	}
}

func TestStatusAttentionConnectionGates(t *testing.T) {
	handler, env := setupTestWithDB(t)
	hid, lineKey, itemID := h689SeedShortKrogerLine(t, handler, env)
	writeKrogerConn689(t, env, connection.StateConnected, "live")
	var tripSeq int
	if err := env.DB.QueryRow(`SELECT trip_seq FROM households WHERE id = ?`, hid).Scan(&tripSeq); err != nil {
		t.Fatal(err)
	}
	mustExecTest(t, env.DB, `INSERT INTO staged_cart_skips (household_id, user_id, item_id, expires_trip) VALUES (?, ?, ?, ?)`, hid, hid, itemID, tripSeq+1)
	doc := statusGET686(t, handler, hid, h686Provider)
	for _, row := range statusItems686(doc) {
		if row["lineKey"] == lineKey && row["needsAttention"] != nil {
			t.Fatalf("removed exempt want null got %#v", row["needsAttention"])
		}
	}
	mustExecTest(t, env.DB, `DELETE FROM staged_cart_skips WHERE household_id=? AND item_id=?`, hid, itemID)
	var entryID string
	if err := env.DB.QueryRow(`SELECT id FROM shopping_list_items WHERE household_id=? AND item_id=? LIMIT 1`, hid, itemID).Scan(&entryID); err != nil {
		t.Fatal(err)
	}
	mustExecTest(t, env.DB, `INSERT INTO shopping_list_entry_adjustments (household_id, entry_id, provider_id, quantity, expires_trip)
		VALUES (?, ?, 'kroger', 0, ?)`, hid, entryID, tripSeq+1)
	doc = statusGET686(t, handler, hid, h686Provider)
	for _, row := range statusItems686(doc) {
		if row["lineKey"] == lineKey && row["needsAttention"] != nil {
			t.Fatalf("active zero exempt %#v", row["needsAttention"])
		}
	}
	mustExecTest(t, env.DB, `DELETE FROM shopping_list_entry_adjustments WHERE household_id=?`, hid)
	mustExecTest(t, env.DB, `DELETE FROM stores WHERE id='store-h689'`)
	mustExecTest(t, env.DB, `UPDATE provider_connections SET state='reauth_required' WHERE household_id=? AND provider_id='kroger'`, hid)
	doc = statusGET686(t, handler, hid, h686Provider)
	if na := naForLine686(t, doc, lineKey); na["reason"] != "reconnect_required" {
		t.Fatalf("zero-store flagged %#v", na)
	}
	mustExecTest(t, env.DB, `INSERT INTO stores (id, household_id, name, name_key, kind, linked_provider, location_id, location_chain, position, created_at, updated_at)
		VALUES ('store-h689', ?, 'H689 Store', 'h689store', 'physical', 'kroger', '01400376', 'KROGER', 0, datetime('now'), datetime('now'))`, hid)
	writeKrogerConn689(t, env, connection.StateConnected, "live")
	inpersonItem := "item-h689-inperson"
	createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", "prod-h689-inperson", "InPerson", inpersonItem)
	mustExecTest(t, env.DB, `INSERT INTO stores (id, household_id, name, name_key, kind, position, created_at, updated_at)
		VALUES ('store-h689-inperson', ?, 'Walk-in', 'walkin', 'physical', 1, datetime('now'), datetime('now'))`, hid)
	mustExecTest(t, env.DB, `UPDATE shopping_list_items SET run_store_id='store-h689-inperson' WHERE item_id=?`, inpersonItem)
	exchangeJSON(t, handler, http.MethodPut, "/api/restock/items/"+inpersonItem, `{"kind":"amount","value":2}`, http.StatusOK)
	doc = statusGET686(t, handler, hid, h686Provider)
	inKey := shopping.ItemLineKey(inpersonItem)
	for _, row := range statusItems686(doc) {
		if row["lineKey"] == inKey && row["needsAttention"] != nil {
			t.Fatalf("in-person line flagged %#v", row["needsAttention"])
		}
	}
	mustExecTest(t, env.DB, `DELETE FROM provider_app_credentials WHERE household_id=?`, hid)
	doc = statusGET686(t, handler, hid, "")
	for _, row := range statusItems686(doc) {
		if row["lineKey"] == lineKey && row["needsAttention"] != nil {
			t.Fatalf("no app want null got %#v", row["needsAttention"])
		}
	}
}

func TestStatusAttentionConnectionStoreDisabled(t *testing.T) {
	handler, env := setupTestWithContributor(t, nil, WithProviderEnv(ProviderEnv{Disabled: true}))
	registerProductionShapeKroger(env)
	hid, lineKey, itemID := h689SeedShortKrogerLine(t, handler, env)
	writeKrogerConn689(t, env, connection.StateReauthRequired, "stale")
	failItem := itemID + "-fail"
	createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", "prod-h689-fail", "Fail", failItem)
	exchangeJSON(t, handler, http.MethodPut, "/api/restock/items/"+failItem, `{"kind":"amount","value":2}`, http.StatusOK)
	exchangeJSON(t, handler, http.MethodPost, "/api/shopping-list/fill", ``, http.StatusOK)
	seedCartLineFailedSend(t, env.DB, hid, failItem, "api_error")
	doc := statusGET686(t, handler, hid, h686Provider)
	for _, row := range append(statusItems686(doc), statusOffList686(doc)...) {
		if na, ok := row["needsAttention"].(map[string]any); ok && na != nil {
			if na["reason"] == "not_connected" || na["reason"] == "reconnect_required" {
				t.Fatalf("connection reason while disabled %#v", na)
			}
		}
	}
	if sd, _ := doc["storeDisabled"].(map[string]any); sd == nil || sd["providerId"] != "kroger" {
		t.Fatalf("storeDisabled %#v", doc["storeDisabled"])
	}
	failKey := shopping.ItemLineKey(failItem)
	na := naForLine686(t, doc, failKey)
	if na["reason"] != "send_failed" {
		t.Fatalf("send still loads %#v", na)
	}
	for _, row := range statusItems686(doc) {
		if row["lineKey"] == lineKey && row["needsAttention"] != nil {
			t.Fatalf("reauth line suppressed %#v", row["needsAttention"])
		}
	}
	body := exchangeJSON(t, handler, http.MethodGet, "/api/shopping-list/summary", "", http.StatusOK)
	var summary map[string]any
	if err := json.Unmarshal(body, &summary); err != nil {
		t.Fatal(err)
	}
	if sd, _ := summary["storeDisabled"].(map[string]any); sd == nil || sd["providerId"] != "kroger" {
		t.Fatalf("summary storeDisabled %#v", summary["storeDisabled"])
	}
}

func statusGET689WithUser(t *testing.T, handler http.Handler, hid, provider, userID, role string) map[string]any {
	t.Helper()
	ctx := auth.WithMembershipHook(context.Background(), func(_ context.Context, uid, _ string) (string, string, bool, error) {
		if uid != userID {
			return "owner", "", true, nil
		}
		return role, "", true, nil
	})
	ctx = auth.WithPrincipal(ctx, auth.Principal{Kind: "account", UserID: userID, Role: role})
	path := "/api/shopping-list?status=1"
	if provider != "" {
		path += "&provider=" + provider
	}
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("X-Household-Id", hid)
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d %s", rr.Code, rr.Body.String())
	}
	var doc map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

func TestStatusAttentionConnectionFixRole(t *testing.T) {
	handler, env := setup689WithAuth(t)
	hid, lineKey, _ := h689SeedShortKrogerLine(t, handler, env)
	const ownerID = "user-1"
	doc := statusGET689WithUser(t, handler, hid, h686Provider, ownerID, "owner")
	na := assertConnReason689(t, doc, lineKey, "not_connected")
	assertConnFix689(t, na, true)
	const memberID = "user-member-689"
	if _, err := env.DB.Exec(`INSERT INTO memberships (household_id, user_id, role, created_at) VALUES (?, ?, 'member', datetime('now'))`, hid, memberID); err != nil {
		t.Fatal(err)
	}
	memberDoc := statusGET689WithUser(t, handler, hid, h686Provider, memberID, "member")
	mna := naForLine686(t, memberDoc, lineKey)
	assertConnFix689(t, mna, false)
}
