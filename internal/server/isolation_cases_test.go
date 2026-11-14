package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Rhionin/stockbot/internal/cart"
	"github.com/Rhionin/stockbot/internal/cart/carttest"
	"github.com/Rhionin/stockbot/internal/product"
	"github.com/Rhionin/stockbot/internal/tenant"
	"github.com/google/uuid"
)

// maxQuarantined is the number of routes still waiting on per-household
// authorization (#377). The coverage test fails when the quarantine list
// is longer than this, and when it is shorter until this constant is
// lowered to match. When a route leaves quarantine, lower this constant
// to the new length in the same change.
//
// Raising maxQuarantined requires explicit reviewer sign-off. Raising it
// is not an acceptable way to add a route.
const maxQuarantined = 99

const waitsOnHouseholdScope = "waits #377"

type isoCategory string

const (
	catPublic     isoCategory = "public"
	catOps        isoCategory = "ops"
	catIsolation  isoCategory = "isolation"
	catQuarantine isoCategory = "quarantine"
)

// isoKind is the cross-household check a route needs once it leaves
// quarantine. The kinds are the case shapes from the route inventory.
type isoKind string

const (
	kindForeignID        isoKind = "foreign-id"
	kindListLeak         isoKind = "list-leak"
	kindWriteScope       isoKind = "write-scope"
	kindScopedCollection isoKind = "scoped-collection"
)

// allowEntry is one public or ops route that is intentionally outside
// household isolation, with the reason it stays there.
type allowEntry struct {
	pattern string
	reason  string
}

// isolationCase is a quarantine entry or a real isolation case.
// check is nil for quarantine. A real case sets check; the suite runs it.
type isolationCase struct {
	kind   isoKind
	reason string
	check  func(*testing.T, *twoHouseholds)
}

// The case table is keyed by the pattern string the router registers.
// Method routes use "METHOD /path". /api/sentry-tunnel is registered with
// no method, so its key is that pattern.

// publicAllowlist must match isPublicPath. The coverage
// test fails when either side gains or loses a path.
var publicAllowlist = []allowEntry{
	{pattern: "GET /api/build", reason: "build identity; isPublicPath leaves it open on the public entry"},
	{pattern: "GET /api/telemetry", reason: "process-wide counters; isPublicPath, and the body must not carry household ids or names"},
	{pattern: "POST /api/telemetry/client", reason: "browser report; isPublicPath leaves it open on the public entry"},
	{pattern: "POST /api/deploy-hook", reason: "deploy hook; isPublicPath, and the handler checks the HMAC itself"},
	{pattern: "/api/sentry-tunnel", reason: "signed-out failure reports; isPublicPath, registered with no method"},
	{pattern: "GET /brand/logo.png", reason: "mark a grocery login stores by URL; isPublicPath"},
	{pattern: "GET /terms", reason: "legal page a grocery login stores by URL; isPublicPath"},
	{pattern: "GET /privacy", reason: "legal page a grocery login stores by URL; isPublicPath"},
}

// opsAllowlist is process liveness. These routes are not household data.
// GET /health still asks for the household password on the public entry.
var opsAllowlist = []allowEntry{
	{pattern: "GET /health", reason: "process liveness with no household data"},
}

// isolationCases holds routes whose cross-household check runs in this
// suite. A new household route lands here with a check. It does not join
// quarantine.
var isolationCases = map[string]isolationCase{
	"GET /api/moves/{id}/preview": {
		kind:   kindForeignID,
		reason: "another household's userId or move id is the same 404 as a missing move, and the body names nothing",
		check:  checkPreviewHidesForeignMove,
	},
	"GET /api/scans/{id}/link-suggestions": {
		kind:   kindForeignID,
		reason: "another household's scan id is the same 404 as a missing scan, and the body names nothing",
		check:  checkLinkSuggestionsHideForeignScan,
	},
	"GET /api/scans/mode": {
		kind:   kindListLeak,
		reason: "scanner mode is one slot per household and device, so another household's direction is not this response",
		check:  checkScannerModeStaysInHousehold,
	},
	"POST /api/scans/mode": {
		kind:   kindWriteScope,
		reason: "setting scanner mode writes only the caller's household and device slot",
		check:  checkScannerModeStaysInHousehold,
	},
	"GET /api/products/search": {
		kind:   kindListLeak,
		reason: "another household's typed product name never appears in search results",
		check:  checkProductSearchHidesForeignTypedProducts,
	},
	"POST /api/products/{id}/item": {
		kind:   kindForeignID,
		reason: "another household's product id cannot be forced into this shelf",
		check:  checkEnsureItemRejectsForeignProduct,
	},
	"GET /api/stores":                      {kind: kindListLeak, reason: "household-scoped store list", check: checkStoresListScoped},
	"GET /api/stores/{id}/removal-preview": {kind: kindForeignID, reason: "another household's store id is 404", check: checkStoreForeignID},
	"PATCH /api/stores/{id}":               {kind: kindForeignID, reason: "another household's store id is 404", check: checkStoreForeignID},
	"DELETE /api/stores/{id}":              {kind: kindForeignID, reason: "another household's store id is 404", check: checkStoreForeignID},
	"POST /api/stores":                     {kind: kindWriteScope, reason: "store create is household-scoped", check: checkStoreCreateScoped},
	"PUT /api/stores/order":                {kind: kindWriteScope, reason: "store order is household-scoped", check: checkStoreOrderScoped},
	"PUT /api/stores/default":              {kind: kindWriteScope, reason: "default store is household-scoped", check: checkStoreDefaultScoped},
	"POST /api/shopping-list/items/{itemId}/undo-remove": {
		kind:   kindForeignID,
		reason: "another household's pantry item id is the same 404 as a missing skip, and the body names nothing",
		check:  checkUndoRemoveHidesForeignSkip,
	},
	"GET /api/shopping-list/send-results": {
		kind:   kindListLeak,
		reason: "another household's cart_line_results rows never appear in this list",
		check:  checkSendResultsHidesForeignRows,
	},
	"GET /api/shopping-list/summary": {
		kind:   kindListLeak,
		reason: "summary counts are household-scoped like the list GET",
		check:  checkShoppingListSummaryStaysInHousehold,
	},
}

func checkStoreCreateScoped(t *testing.T, fx *twoHouseholds) {
	t.Helper()
	var birchBefore int
	if err := fx.DB.QueryRow(`SELECT COUNT(*) FROM stores WHERE household_id = ?`, fx.Birch.ID).Scan(&birchBefore); err != nil {
		t.Fatal(err)
	}
	body := `{"name":"Alder Only Q7 ` + uuid.NewString()[:8] + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/stores", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	stampHousehold(t, req, fx.Alder)
	rec := httptest.NewRecorder()
	fx.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/stores status %d", rec.Code)
	}
	var birchAfter int
	if err := fx.DB.QueryRow(`SELECT COUNT(*) FROM stores WHERE household_id = ?`, fx.Birch.ID).Scan(&birchAfter); err != nil {
		t.Fatal(err)
	}
	if birchAfter != birchBefore {
		t.Fatalf("birch stores before %d after %d", birchBefore, birchAfter)
	}
}

func checkStoreOrderScoped(t *testing.T, fx *twoHouseholds) {
	t.Helper()
	birchA := seedFixtureStore(t, fx.DB, fx.Birch.ID, "Birch Order A Q7")
	birchB := seedFixtureStore(t, fx.DB, fx.Birch.ID, "Birch Order B Q7")
	birchBefore, err := storePositionsForHousehold(t, fx.DB, fx.Birch.ID)
	if err != nil {
		t.Fatal(err)
	}
	alderStore := seedFixtureStore(t, fx.DB, fx.Alder.ID, "Alder Order Q7")
	alderOther := seedFixtureStore(t, fx.DB, fx.Alder.ID, "Alder Order Other Q7")
	bodyForeign := `{"storeIds":["` + birchA + `","` + alderStore + `"]}`
	reqForeign := httptest.NewRequest(http.MethodPut, "/api/stores/order", strings.NewReader(bodyForeign))
	reqForeign.Header.Set("Content-Type", "application/json")
	stampHousehold(t, reqForeign, fx.Alder)
	recForeign := httptest.NewRecorder()
	fx.Handler.ServeHTTP(recForeign, reqForeign)
	if recForeign.Code != http.StatusNotFound {
		t.Fatalf("foreign store in order status %d body %s", recForeign.Code, recForeign.Body.String())
	}
	alderIDs, err := storeIDsForHousehold(t, fx.DB, fx.Alder.ID)
	if err != nil {
		t.Fatal(err)
	}
	order := []string{alderOther, alderStore}
	for _, id := range alderIDs {
		if id != alderStore && id != alderOther {
			order = append(order, id)
		}
	}
	body := `{"storeIds":` + mustJSON(t, order) + `}`
	req := httptest.NewRequest(http.MethodPut, "/api/stores/order", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	stampHousehold(t, req, fx.Alder)
	rec := httptest.NewRecorder()
	fx.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT order status %d body %s", rec.Code, rec.Body.String())
	}
	birchAfter, err := storePositionsForHousehold(t, fx.DB, fx.Birch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if birchBefore[birchA] != birchAfter[birchA] || birchBefore[birchB] != birchAfter[birchB] {
		t.Fatalf("birch positions moved before %+v after %+v", birchBefore, birchAfter)
	}
}

func storePositionsForHousehold(t *testing.T, db *sql.DB, householdID string) (map[string]int, error) {
	t.Helper()
	rows, err := db.Query(`SELECT id, position FROM stores WHERE household_id = ?`, householdID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var id string
		var pos int
		if err := rows.Scan(&id, &pos); err != nil {
			return nil, err
		}
		out[id] = pos
	}
	return out, rows.Err()
}

func storeIDsForHousehold(t *testing.T, db *sql.DB, householdID string) ([]string, error) {
	t.Helper()
	rows, err := db.Query(`SELECT id FROM stores WHERE household_id = ? ORDER BY position`, householdID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func checkStoreDefaultScoped(t *testing.T, fx *twoHouseholds) {
	t.Helper()
	birchStore := seedFixtureStore(t, fx.DB, fx.Birch.ID, "Birch Default Q7")
	reqForeign := httptest.NewRequest(http.MethodPut, "/api/stores/default", strings.NewReader(`{"storeId":"`+birchStore+`"}`))
	reqForeign.Header.Set("Content-Type", "application/json")
	stampHousehold(t, reqForeign, fx.Alder)
	recForeign := httptest.NewRecorder()
	fx.Handler.ServeHTTP(recForeign, reqForeign)
	if recForeign.Code != http.StatusNotFound {
		t.Fatalf("foreign default status %d body %s", recForeign.Code, recForeign.Body.String())
	}
	alderStore := seedFixtureStore(t, fx.DB, fx.Alder.ID, "Alder Default Q7")
	req := httptest.NewRequest(http.MethodPut, "/api/stores/default", strings.NewReader(`{"storeId":"`+alderStore+`"}`))
	req.Header.Set("Content-Type", "application/json")
	stampHousehold(t, req, fx.Alder)
	rec := httptest.NewRecorder()
	fx.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT default status %d", rec.Code)
	}
	var n int
	if err := fx.DB.QueryRow(`SELECT COUNT(*) FROM household_settings WHERE household_id = ? AND key = 'default_store_id'`, fx.Birch.ID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("birch default set %d err %v", n, err)
	}
}

func checkProductSearchHidesForeignTypedProducts(t *testing.T, fx *twoHouseholds) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/products/search?q=Secret", nil)
	fx.principalFor(fx.Alder).Apply(req)
	rec := httptest.NewRecorder()
	fx.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	assertNoLeak(t, rec.Body.Bytes(), fx.Birch)
}

func checkEnsureItemRejectsForeignProduct(t *testing.T, fx *twoHouseholds) {
	t.Helper()
	cedarHousehold(t, fx.DB)
	cedarScope, err := tenant.New(cedarHouseholdID)
	if err != nil {
		t.Fatal(err)
	}
	catalog := product.NewCatalog(fx.DB)
	typed, err := catalog.CreateTypedProduct(context.Background(), cedarScope, product.Product{
		Name: fx.Birch.Marker + " typed",
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/products/"+typed.ID+"/item", nil)
	fx.principalFor(fx.Alder).Apply(req)
	rec := httptest.NewRecorder()
	fx.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	assertNoLeak(t, rec.Body.Bytes(), fx.Birch)
}

func checkPreviewHidesForeignMove(t *testing.T, fx *twoHouseholds) {
	t.Helper()
	for _, path := range []string{
		"/api/moves/" + fx.Birch.ProductID + "/preview?userId=" + fx.Birch.ID,
		"/api/moves/seg/preview?userId=" + fx.Birch.ID,
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		fx.principalFor(fx.Alder).Apply(req)
		rec := httptest.NewRecorder()
		fx.Handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s status %d body %s", path, rec.Code, rec.Body.String())
		}
		assertNoLeak(t, rec.Body.Bytes(), fx.Birch)
		assertNoLeak(t, rec.Body.Bytes(), fx.Alder)
	}
}

// checkScannerModeStaysInHousehold proves a mode change on one household
// does not move the other household, including a same device id on both.
// The check resets both slots first because the suite shares one handler.
func checkScannerModeStaysInHousehold(t *testing.T, fx *twoHouseholds) {
	t.Helper()
	const alderDevice = "01901111-1111-7000-8000-000000000d11"
	const birchDevice = "01902222-2222-7000-8000-000000000d22"
	insertModeDevice(t, fx, fx.Alder, alderDevice)
	insertModeDevice(t, fx, fx.Birch, birchDevice)
	setScannerMode(t, fx, fx.Alder, "", "stock_in")
	setScannerMode(t, fx, fx.Birch, "", "stock_in")
	setScannerMode(t, fx, fx.Alder, alderDevice, "stock_in")
	setScannerMode(t, fx, fx.Birch, birchDevice, "stock_in")

	setScannerMode(t, fx, fx.Alder, "", "stock_out")
	if got := readScannerMode(t, fx, fx.Birch, ""); got != "stock_in" {
		t.Fatalf("birch default mode = %s after alder switched to stock_out", got)
	}
	if got := readScannerMode(t, fx, fx.Alder, alderDevice); got != "stock_in" {
		t.Fatalf("alder device %s mode = %s after the default slot changed", alderDevice, got)
	}
	setScannerMode(t, fx, fx.Alder, alderDevice, "stock_out")
	if got := readScannerMode(t, fx, fx.Birch, birchDevice); got != "stock_in" {
		t.Fatalf("birch device mode = %s after alder switched its own device", got)
	}
	if got := readScannerMode(t, fx, fx.Alder, ""); got != "stock_out" {
		t.Fatalf("alder default mode = %s, want stock_out", got)
	}
	if code := scannerModeStatus(t, fx, fx.Birch, alderDevice); code != http.StatusNotFound {
		t.Fatalf("birch read alder's device: %d", code)
	}
}

func insertModeDevice(t *testing.T, fx *twoHouseholds, h fixtureHousehold, id string) {
	t.Helper()
	_, err := fx.DB.Exec(`
		INSERT OR IGNORE INTO devices (id, household_id, kind, label, created_at)
		VALUES (?, ?, 'pi_hub', 'Counter', '2026-04-04T00:00:00Z')`, id, h.ID)
	if err != nil {
		t.Fatalf("device %s: %v", id, err)
	}
}

func scannerModeStatus(t *testing.T, fx *twoHouseholds, h fixtureHousehold, device string) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/scans/mode?deviceId="+device, nil)
	stampHousehold(t, req, h)
	rec := httptest.NewRecorder()
	fx.Handler.ServeHTTP(rec, req)
	return rec.Code
}

func setScannerMode(t *testing.T, fx *twoHouseholds, h fixtureHousehold, device, mode string) {
	t.Helper()
	body := `{"mode":"` + mode + `"}`
	if device != "" {
		body = `{"mode":"` + mode + `","deviceId":"` + device + `"}`
	}
	req := httptest.NewRequest(http.MethodPost, "/api/scans/mode", strings.NewReader(body))
	stampHousehold(t, req, h)
	rec := httptest.NewRecorder()
	fx.Handler.ServeHTTP(rec, req)
	raw, err := io.ReadAll(rec.Result().Body)
	if err != nil {
		t.Fatalf("read mode write: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/scans/mode %s status %d body %s", h.Name, rec.Code, raw)
	}
	assertNoLeak(t, raw, fx.Alder)
	assertNoLeak(t, raw, fx.Birch)
	var got scannerModeResponse
	if err := json.Unmarshal(raw, &got); err != nil || string(got.Mode) != mode {
		t.Fatalf("POST /api/scans/mode %s = %s (%v)", h.Name, raw, err)
	}
}

func readScannerMode(t *testing.T, fx *twoHouseholds, h fixtureHousehold, device string) string {
	t.Helper()
	path := "/api/scans/mode"
	if device != "" {
		path += "?deviceId=" + device
	}
	req := httptest.NewRequest(http.MethodGet, path, nil)
	stampHousehold(t, req, h)
	rec := httptest.NewRecorder()
	fx.Handler.ServeHTTP(rec, req)
	raw, err := io.ReadAll(rec.Result().Body)
	if err != nil {
		t.Fatalf("read mode: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/scans/mode %s status %d body %s", h.Name, rec.Code, raw)
	}
	assertNoLeak(t, raw, fx.Alder)
	assertNoLeak(t, raw, fx.Birch)
	var got scannerModeResponse
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("GET /api/scans/mode %s: %v body %s", h.Name, err, raw)
	}
	return string(got.Mode)
}

func stampHousehold(t *testing.T, req *http.Request, h fixtureHousehold) {
	t.Helper()
	scope, err := tenant.New(h.ID)
	if err != nil {
		t.Fatalf("scope %s: %v", h.Name, err)
	}
	user1Principal{household: h}.Apply(req)
	cloned := req.WithContext(tenant.WithScope(req.Context(), scope))
	*req = *cloned
}

func checkUndoRemoveHidesForeignSkip(t *testing.T, fx *twoHouseholds) {
	t.Helper()
	const itemID = "item-alder-undo-iso"
	if _, err := fx.DB.Exec(`
		INSERT INTO items (id, user_id, product_id, household_id)
		VALUES (?, ?, ?, ?)`,
		itemID, fx.Alder.ID, fx.Alder.ProductID, fx.Alder.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := fx.DB.Exec(`
		INSERT INTO staged_cart_skips (household_id, user_id, item_id, expires_trip)
		VALUES (?, ?, ?, 2)`, fx.Alder.ID, fx.Alder.ID, itemID); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/shopping-list/items/"+itemID+"/undo-remove", nil)
	stampHousehold(t, req, fx.Birch)
	rec := httptest.NewRecorder()
	fx.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	assertNoLeak(t, rec.Body.Bytes(), fx.Alder)
	var n int
	if err := fx.DB.QueryRow(`SELECT COUNT(*) FROM staged_cart_skips WHERE household_id = ? AND item_id = ?`,
		fx.Alder.ID, itemID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("alder skip count %d", n)
	}
}

func checkShoppingListSummaryStaysInHousehold(t *testing.T, fx *twoHouseholds) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/shopping-list/summary", nil)
	stampHousehold(t, req, fx.Birch)
	rec := httptest.NewRecorder()
	fx.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	assertNoLeak(t, rec.Body.Bytes(), fx.Alder)
}

func checkSendResultsHidesForeignRows(t *testing.T, fx *twoHouseholds) {
	t.Helper()
	const itemID = "item-alder-send-res"
	provider := carttest.NewFake(cart.Capabilities{Auth: cart.AuthNone, Delivery: cart.DeliveryServerPush, Confirmation: cart.ConfirmPerRequest, Mutation: cart.MutateAddOnly, Identity: cart.IdentityDerived}, carttest.NewScript())
	providerID := string(provider.ID())
	if err := fx.Registry.Register(provider); err != nil {
		t.Fatal(err)
	}
	if _, err := fx.DB.Exec(`
		INSERT INTO cart_line_results (household_id, provider_id, item_id, status, attempted_at)
		VALUES (?, ?, ?, 'sent', '2026-11-11T14:00:00Z')`,
		fx.Alder.ID, providerID, itemID); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/shopping-list/send-results?provider="+providerID+"&itemId="+itemID, nil)
	stampHousehold(t, req, fx.Birch)
	rec := httptest.NewRecorder()
	fx.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	raw := rec.Body.Bytes()
	assertNoLeak(t, raw, fx.Alder)
	var body struct {
		Results []map[string]any `json:"results"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("json: %v", err)
	}
	if len(body.Results) != 0 {
		t.Fatalf("results %#v", body.Results)
	}
}

func checkLinkSuggestionsHideForeignScan(t *testing.T, fx *twoHouseholds) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/scans/"+fx.Birch.ProductID+"/link-suggestions", nil)
	fx.principalFor(fx.Alder).Apply(req)
	rec := httptest.NewRecorder()
	fx.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	assertNoLeak(t, rec.Body.Bytes(), fx.Birch)
	assertNoLeak(t, rec.Body.Bytes(), fx.Alder)
}

// quarantine is every household route that cannot be asserted until
// per-household authorization exists (#377). The list may shrink. It must
// not grow. GET /api/events and GET /api/sync/snapshot stay here: they
// carry household data and are not on an allowlist.
var quarantine = map[string]isolationCase{
	"GET /api/events":         {kind: kindListLeak, reason: waitsOnHouseholdScope},
	"POST /api/scanner/mode":  {kind: kindWriteScope, reason: waitsOnHouseholdScope},
	"GET /api/scanner/config": {kind: kindListLeak, reason: waitsOnHouseholdScope},

	"GET /api/products/lookup":             {kind: kindListLeak, reason: waitsOnHouseholdScope},
	"GET /api/products":                    {kind: kindListLeak, reason: waitsOnHouseholdScope},
	"GET /api/products/{id}":               {kind: kindForeignID, reason: waitsOnHouseholdScope},
	"POST /api/products":                   {kind: kindWriteScope, reason: waitsOnHouseholdScope},
	"PUT /api/products/{id}":               {kind: kindForeignID, reason: waitsOnHouseholdScope},
	"POST /api/products/overrides":         {kind: kindWriteScope, reason: waitsOnHouseholdScope},
	"POST /api/products/{id}/refresh":      {kind: kindForeignID, reason: waitsOnHouseholdScope},
	"GET /api/products/{id}/contributions": {kind: kindForeignID, reason: waitsOnHouseholdScope},
	"GET /api/contributions":               {kind: kindListLeak, reason: waitsOnHouseholdScope},
	"GET /api/settings/contribution":       {kind: kindListLeak, reason: waitsOnHouseholdScope},
	"PUT /api/settings/contribution":       {kind: kindWriteScope, reason: waitsOnHouseholdScope},

	"POST /api/scans":              {kind: kindWriteScope, reason: waitsOnHouseholdScope},
	"GET /api/scans":               {kind: kindListLeak, reason: waitsOnHouseholdScope},
	"GET /api/scans/history":       {kind: kindListLeak, reason: waitsOnHouseholdScope},
	"PATCH /api/scans/{id}":        {kind: kindForeignID, reason: waitsOnHouseholdScope},
	"POST /api/scans/{id}/commit":  {kind: kindForeignID, reason: waitsOnHouseholdScope},
	"POST /api/scans/batch-commit": {kind: kindWriteScope, reason: waitsOnHouseholdScope},

	"GET /api/inventory":                           {kind: kindListLeak, reason: waitsOnHouseholdScope},
	"GET /api/inventory/{itemId}/instances":        {kind: kindForeignID, reason: waitsOnHouseholdScope},
	"POST /api/inventory/{itemId}/instances":       {kind: kindForeignID, reason: waitsOnHouseholdScope},
	"POST /api/inventory/{itemId}/stock-out":       {kind: kindForeignID, reason: waitsOnHouseholdScope},
	"DELETE /api/inventory/instances/{instanceId}": {kind: kindForeignID, reason: waitsOnHouseholdScope},
	"POST /api/inventory/wipe":                     {kind: kindWriteScope, reason: waitsOnHouseholdScope},
	"GET /api/sync/snapshot":                       {kind: kindListLeak, reason: waitsOnHouseholdScope},
	"GET /api/history":                             {kind: kindListLeak, reason: waitsOnHouseholdScope},
	"GET /api/items/{itemId}/history":              {kind: kindForeignID, reason: waitsOnHouseholdScope},
	"GET /api/groups/{groupId}/history":            {kind: kindForeignID, reason: waitsOnHouseholdScope},
	"PATCH /api/moves/{id}":                        {kind: kindForeignID, reason: waitsOnHouseholdScope},
	"DELETE /api/moves/{id}":                       {kind: kindForeignID, reason: waitsOnHouseholdScope},
	"GET /api/settings/supply":                     {kind: kindListLeak, reason: waitsOnHouseholdScope},
	"PUT /api/settings/supply":                     {kind: kindWriteScope, reason: waitsOnHouseholdScope},
	"GET /api/products/{id}/supply-override":       {kind: kindForeignID, reason: waitsOnHouseholdScope},
	"PUT /api/products/{id}/supply-override":       {kind: kindForeignID, reason: waitsOnHouseholdScope},
	"POST /api/onboarding/complete":                {kind: kindWriteScope, reason: waitsOnHouseholdScope},

	"GET /api/restock":                                           {kind: kindListLeak, reason: waitsOnHouseholdScope},
	"PUT /api/restock/household":                                 {kind: kindWriteScope, reason: waitsOnHouseholdScope},
	"PUT /api/restock/items/{itemId}":                            {kind: kindForeignID, reason: waitsOnHouseholdScope},
	"DELETE /api/restock/items/{itemId}":                         {kind: kindForeignID, reason: waitsOnHouseholdScope},
	"POST /api/restock/items/{itemId}/dont-restock":              {kind: kindForeignID, reason: waitsOnHouseholdScope},
	"POST /api/restock/items/{itemId}/categories":                {kind: kindForeignID, reason: waitsOnHouseholdScope},
	"DELETE /api/restock/items/{itemId}/categories/{categoryId}": {kind: kindForeignID, reason: waitsOnHouseholdScope},
	"GET /api/restock/categories":                                {kind: kindListLeak, reason: waitsOnHouseholdScope},
	"POST /api/restock/categories":                               {kind: kindWriteScope, reason: waitsOnHouseholdScope},
	"PUT /api/restock/categories/{id}/target":                    {kind: kindForeignID, reason: waitsOnHouseholdScope},
	"POST /api/restock/categories/{id}/preview":                  {kind: kindForeignID, reason: waitsOnHouseholdScope},
	"PATCH /api/restock/categories/{id}":                         {kind: kindForeignID, reason: waitsOnHouseholdScope},
	"POST /api/restock/categories/{id}/merge":                    {kind: kindForeignID, reason: waitsOnHouseholdScope},
	"DELETE /api/restock/categories/{id}":                        {kind: kindForeignID, reason: waitsOnHouseholdScope},
	"POST /api/restock/groups/{id}/preview":                      {kind: kindForeignID, reason: waitsOnHouseholdScope},
	"GET /api/restock/suggestions":                               {kind: kindListLeak, reason: waitsOnHouseholdScope},
	"POST /api/restock/suggestions/apply":                        {kind: kindWriteScope, reason: waitsOnHouseholdScope},
	"POST /api/restock/suggestions/dismiss":                      {kind: kindWriteScope, reason: waitsOnHouseholdScope},

	"GET /api/groups":                                  {kind: kindListLeak, reason: waitsOnHouseholdScope},
	"POST /api/groups":                                 {kind: kindWriteScope, reason: waitsOnHouseholdScope},
	"POST /api/groups/preview":                         {kind: kindWriteScope, reason: waitsOnHouseholdScope},
	"GET /api/groups/{id}":                             {kind: kindForeignID, reason: waitsOnHouseholdScope},
	"PATCH /api/groups/{id}":                           {kind: kindForeignID, reason: waitsOnHouseholdScope},
	"DELETE /api/groups/{id}":                          {kind: kindForeignID, reason: waitsOnHouseholdScope},
	"POST /api/groups/{id}/members":                    {kind: kindForeignID, reason: waitsOnHouseholdScope},
	"DELETE /api/groups/{id}/members/{productId}":      {kind: kindForeignID, reason: waitsOnHouseholdScope},
	"PUT /api/groups/{id}/members/{productId}/restock": {kind: kindForeignID, reason: waitsOnHouseholdScope},
	"PUT /api/groups/{id}/rule":                        {kind: kindForeignID, reason: waitsOnHouseholdScope},
	"PUT /api/groups/{id}/target":                      {kind: kindForeignID, reason: waitsOnHouseholdScope},
	"GET /api/group-suggestions":                       {kind: kindListLeak, reason: waitsOnHouseholdScope},
	"POST /api/group-suggestions/rescan":               {kind: kindWriteScope, reason: waitsOnHouseholdScope},
	"POST /api/group-suggestions/from-scan":            {kind: kindWriteScope, reason: waitsOnHouseholdScope},
	"POST /api/group-suggestions/{id}/accept":          {kind: kindForeignID, reason: waitsOnHouseholdScope},
	"POST /api/group-suggestions/{id}/dismiss":         {kind: kindForeignID, reason: waitsOnHouseholdScope},
	"POST /api/group-suggestions/{id}/skip":            {kind: kindForeignID, reason: waitsOnHouseholdScope},
	"GET /api/settings/group-rule":                     {kind: kindListLeak, reason: waitsOnHouseholdScope},
	"PUT /api/settings/group-rule":                     {kind: kindWriteScope, reason: waitsOnHouseholdScope},
	"POST /api/items/{itemId}/target-quantity":         {kind: kindForeignID, reason: waitsOnHouseholdScope},

	"GET /api/shopping-list":                   {kind: kindListLeak, reason: waitsOnHouseholdScope},
	"POST /api/shopping-list/fill":             {kind: kindWriteScope, reason: waitsOnHouseholdScope},
	"POST /api/shopping-list/items":            {kind: kindWriteScope, reason: waitsOnHouseholdScope},
	"DELETE /api/shopping-list/items/{id}":     {kind: kindForeignID, reason: waitsOnHouseholdScope},
	"PATCH /api/shopping-list/items/{id}":      {kind: kindForeignID, reason: waitsOnHouseholdScope},
	"POST /api/shopping-list/items/{id}/swap":  {kind: kindForeignID, reason: waitsOnHouseholdScope},
	"PUT /api/shopping-list/deals":             {kind: kindWriteScope, reason: waitsOnHouseholdScope},
	"DELETE /api/shopping-list/deals/{itemId}": {kind: kindForeignID, reason: waitsOnHouseholdScope},
	"POST /api/shopping-list/export":           {kind: kindWriteScope, reason: waitsOnHouseholdScope},

	"GET /api/providers":                              {kind: kindScopedCollection, reason: waitsOnHouseholdScope},
	"GET /api/providers/{providerId}/authorize":       {kind: kindScopedCollection, reason: waitsOnHouseholdScope},
	"GET /api/providers/{providerId}/callback":        {kind: kindScopedCollection, reason: waitsOnHouseholdScope},
	"DELETE /api/providers/{providerId}/connection":   {kind: kindScopedCollection, reason: waitsOnHouseholdScope},
	"PUT /api/providers/{providerId}/credentials":     {kind: kindScopedCollection, reason: "operator-only server credential write (#489); household scope waits #377"},
	"DELETE /api/providers/{providerId}/credentials":  {kind: kindScopedCollection, reason: "operator-only server credential write (#489); household scope waits #377"},
	"GET /api/providers/{providerId}/ledger":          {kind: kindScopedCollection, reason: waitsOnHouseholdScope},
	"POST /api/providers/{providerId}/ledger/reset":   {kind: kindScopedCollection, reason: waitsOnHouseholdScope},
	"POST /api/items/{itemId}/replenishment-mode":     {kind: kindForeignID, reason: waitsOnHouseholdScope},
	"PUT /api/shopping-list/items/{id}/adjustment":    {kind: kindForeignID, reason: waitsOnHouseholdScope},
	"DELETE /api/shopping-list/items/{id}/adjustment": {kind: kindForeignID, reason: waitsOnHouseholdScope},

	"GET /api/push/vapid-public-key":                        {kind: kindListLeak, reason: waitsOnHouseholdScope},
	"POST /api/push/subscriptions":                          {kind: kindWriteScope, reason: waitsOnHouseholdScope},
	"DELETE /api/push/subscriptions":                        {kind: kindWriteScope, reason: waitsOnHouseholdScope},
	"POST /api/shopping-list/items/{id}/unknown-resolution": {kind: kindForeignID, reason: waitsOnHouseholdScope},
}
