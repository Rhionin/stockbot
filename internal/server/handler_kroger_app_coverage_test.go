package server

import (
	"database/sql"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/Rhionin/stockbot/internal/cart"
	"github.com/Rhionin/stockbot/internal/cart/kroger"
)

func TestKrogerAppGetDerivesConnectionStatuses(t *testing.T) {
	caller, db, primary, _ := setupKrogerAppOperator(t)
	caller.house = primary
	save := caller.authed(http.MethodPut, "/api/households/"+primary+"/kroger-app", map[string]string{
		"clientId": "house-app", "clientSecret": "house-secret", "modality": "PICKUP",
	})
	if save.Code != http.StatusOK {
		t.Fatal(save.Code)
	}
	ready := caller.authed(http.MethodGet, "/api/households/"+primary+"/kroger-app", nil)
	if ready.Code != http.StatusOK || !strings.Contains(ready.Body.String(), `"status":"ready_to_connect"`) {
		t.Fatalf("ready %d %s", ready.Code, ready.Body.String())
	}
	when := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := db.Exec(`
		INSERT INTO provider_connections (id, user_id, household_id, provider_id, state, updated_at)
		VALUES ('st-connected', 'user-1', ?, 'kroger', 'connected', ?)`, primary, when); err != nil {
		t.Fatal(err)
	}
	connected := caller.authed(http.MethodGet, "/api/households/"+primary+"/kroger-app", nil)
	if connected.Code != http.StatusOK || !strings.Contains(connected.Body.String(), `"status":"connected"`) {
		t.Fatalf("connected %d %s", connected.Code, connected.Body.String())
	}
	if _, err := db.Exec(`UPDATE provider_connections SET state = 'reauth_required' WHERE id = 'st-connected'`); err != nil {
		t.Fatal(err)
	}
	reconnect := caller.authed(http.MethodGet, "/api/households/"+primary+"/kroger-app", nil)
	if reconnect.Code != http.StatusOK || !strings.Contains(reconnect.Body.String(), `"status":"needs_reconnect"`) {
		t.Fatalf("needs_reconnect %d %s", reconnect.Code, reconnect.Body.String())
	}
}

func TestKrogerAppDisabledDeleteAndTestReject(t *testing.T) {
	setupDir := t.TempDir()
	db := openMigratedDB(t, filepath.Join(t.TempDir(), "kroger-disabled-del.db"))
	svc := auth.NewService(db, setupDir)
	site := stage1Site(t)
	registry := cart.NewRegistry()
	if err := registry.Register(kroger.NewUnconfigured()); err != nil {
		t.Fatal(err)
	}
	handler, _ := setupOnDB(t, db, nil,
		WithStage1(svc, site),
		WithCartRegistry(registry, cart.NewLedger(db)),
		WithProviderEnv(ProviderEnv{Disabled: true}),
	)
	caller := &stage1Caller{t: t, h: handler}
	if rec := caller.call(http.MethodGet, "/setup", nil, app.EntryPublic, nil); rec.Code != http.StatusOK {
		t.Fatal(rec.Code)
	}
	code, err := auth.ReadSetupCode(setupDir)
	if err != nil {
		t.Fatal(err)
	}
	caller.call(http.MethodPost, "/setup", map[string]string{
		"setupCode": code, "name": "Op", "email": "op-del@test", "password": "correct horse",
	}, app.EntryPublic, nil)
	login := caller.call(http.MethodPost, "/api/auth/login", map[string]string{
		"email": "op-del@test", "password": "correct horse",
	}, app.EntryPublic, nil)
	caller.cookie = cookieFrom(t, login, site.SessionCookie().Name)
	scope, err := auth.PrimaryScope(t.Context(), db)
	if err != nil {
		t.Fatal(err)
	}
	house := scope.HouseholdID()
	caller.house = house
	del := caller.authed(http.MethodDelete, "/api/households/"+house+"/kroger-app", nil)
	if del.Code != http.StatusConflict || !strings.Contains(del.Body.String(), `"code":"store_disabled"`) {
		t.Fatalf("DELETE disabled %d %s", del.Code, del.Body.String())
	}
	testPost := caller.authed(http.MethodPost, "/api/households/"+house+"/kroger-app/test", nil)
	if testPost.Code != http.StatusConflict || !strings.Contains(testPost.Body.String(), `"code":"store_disabled"`) {
		t.Fatalf("POST test disabled %d %s", testPost.Code, testPost.Body.String())
	}
}

func TestKrogerAppPutRequiresBothCredentials(t *testing.T) {
	caller, _, primary, _ := setupKrogerAppOperator(t)
	caller.house = primary
	put := caller.authed(http.MethodPut, "/api/households/"+primary+"/kroger-app", map[string]string{
		"clientId": "only-id", "modality": "PICKUP",
	})
	if put.Code != http.StatusBadRequest || !strings.Contains(put.Body.String(), `"code":"validation"`) {
		t.Fatalf("half creds %d %s", put.Code, put.Body.String())
	}
}

func TestKrogerAppPostTestExplicitKeysReturnsPlainCodes(t *testing.T) {
	caller, _, primary, adapter := setupKrogerAppOperator(t)
	caller.house = primary
	adapter.WithTransport(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if strings.Contains(req.URL.Path, "/oauth2/token") {
			return jsonResp(http.StatusUnauthorized, `{"error":"invalid_client"}`), nil
		}
		return nil, io.ErrUnexpectedEOF
	}))
	post := caller.authed(http.MethodPost, "/api/households/"+primary+"/kroger-app/test", map[string]string{
		"clientId": "bad", "clientSecret": "bad",
	})
	if post.Code != http.StatusOK || !strings.Contains(post.Body.String(), `"ok":false`) ||
		!strings.Contains(post.Body.String(), `"code":"wrong_keys"`) {
		t.Fatalf("POST test bad keys %d %s", post.Code, post.Body.String())
	}
}

func TestKrogerAppReplaceClientClearsCheckRows(t *testing.T) {
	caller, db, primary, _ := setupKrogerAppOperator(t)
	caller.house = primary
	if rec := caller.authed(http.MethodPut, "/api/households/"+primary+"/kroger-app", map[string]string{
		"clientId": "first-app", "clientSecret": "first-secret", "modality": "PICKUP",
	}); rec.Code != http.StatusOK {
		t.Fatal(rec)
	}
	when := "2026-01-01T00:00:00Z"
	if _, err := db.Exec(`INSERT INTO stores (id, household_id, name, name_key, kind, position, linked_provider, location_id, location_chain, created_at, updated_at)
		VALUES ('store-chk', ?, 'Kroger', 'kroger', 'online', 0, 'kroger', '12345678', 'KROGER', ?, ?)`, primary, when, when); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO store_availability (household_id, store_id, product_id, location_id, status, checked_at, status_expires_at)
		VALUES (?, 'store-chk', 'prod-1', '12345678', 'carried', ?, ?)`, primary, when, when); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO store_checks (household_id, store_id, state, trigger, started_at, lease_until, checked_count, found_count, total_count)
		VALUES (?, 'store-chk', 'done', 'manual', ?, ?, 1, 1, 1)`, primary, when, when); err != nil {
		t.Fatal(err)
	}
	change := caller.authed(http.MethodPut, "/api/households/"+primary+"/kroger-app", map[string]string{
		"clientId": "second-app", "clientSecret": "second-secret", "modality": "PICKUP",
	})
	if change.Code != http.StatusOK {
		t.Fatalf("replace %d %s", change.Code, change.Body.String())
	}
	for _, q := range []string{
		`SELECT COUNT(*) FROM store_availability WHERE household_id = ?`,
		`SELECT COUNT(*) FROM store_checks WHERE household_id = ?`,
	} {
		var n int
		if err := db.QueryRow(q, primary).Scan(&n); err != nil || n != 0 {
			t.Fatalf("%s count=%d err=%v", q, n, err)
		}
	}
}

func TestProviderCredentialDeleteKrogerRetired(t *testing.T) {
	h := &ProviderCredentialDeleteHandler{ProvidersHandler: ProvidersHandler{Auth: auth.NewService(openMigratedDB(t, ":memory:"), t.TempDir())}}
	ctx := auth.WithPrincipal(t.Context(), auth.Principal{Kind: "account", UserID: "op-1", Operator: true})
	_, err := h.Handle(Request[struct{}, providerCredentialParams]{
		Context:    ctx,
		PathParams: providerCredentialParams{ProviderID: "kroger"},
	})
	var httpErr *HTTPError
	if err == nil || !errorsAsHTTP(err, &httpErr) || httpErr.Code != http.StatusNotFound {
		t.Fatalf("err = %v", err)
	}
}

func TestKrogerAppPutNewSecretSameClientIDKeepsConnection(t *testing.T) {
	caller, db, primary, _ := setupKrogerAppOperator(t)
	caller.house = primary
	save := caller.authed(http.MethodPut, "/api/households/"+primary+"/kroger-app", map[string]string{
		"clientId": "same-client", "clientSecret": "secret-one", "modality": "PICKUP",
	})
	if save.Code != http.StatusOK {
		t.Fatal(save)
	}
	when := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := db.Exec(`
		INSERT INTO provider_connections (id, user_id, household_id, provider_id, state, updated_at)
		VALUES ('keep-conn', 'user-1', ?, 'kroger', 'connected', ?)`, primary, when); err != nil {
		t.Fatal(err)
	}
	rotate := caller.authed(http.MethodPut, "/api/households/"+primary+"/kroger-app", map[string]string{
		"clientId": "same-client", "clientSecret": "secret-two", "modality": "PICKUP",
	})
	if rotate.Code != http.StatusOK {
		t.Fatalf("rotate %d %s", rotate.Code, rotate.Body.String())
	}
	var state string
	if err := db.QueryRow(`SELECT state FROM provider_connections WHERE id = 'keep-conn'`).Scan(&state); err != nil || state != "connected" {
		t.Fatalf("state=%q err=%v", state, err)
	}
}

func TestKrogerAppPostTestRetestUpdatesProductAccess(t *testing.T) {
	caller, db, primary, adapter := setupKrogerAppOperator(t)
	caller.house = primary
	if rec := caller.authed(http.MethodPut, "/api/households/"+primary+"/kroger-app", map[string]string{
		"clientId": "house-app", "clientSecret": "house-secret", "modality": "PICKUP",
	}); rec.Code != http.StatusOK {
		t.Fatal(rec)
	}
	adapter.WithTransport(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if strings.Contains(req.URL.Path, "/oauth2/token") {
			return jsonResp(http.StatusForbidden, `{"error":"invalid_scope"}`), nil
		}
		return nil, io.ErrUnexpectedEOF
	}))
	post := caller.authed(http.MethodPost, "/api/households/"+primary+"/kroger-app/test", nil)
	if post.Code != http.StatusOK || !strings.Contains(post.Body.String(), `"code":"no_product_access"`) {
		t.Fatalf("retest %d %s", post.Code, post.Body.String())
	}
	var pa sql.NullInt64
	if err := db.QueryRow(`SELECT product_access FROM provider_app_credentials WHERE household_id = ? AND provider_id = 'kroger'`, primary).Scan(&pa); err != nil || !pa.Valid || pa.Int64 != 0 {
		t.Fatalf("product_access = %v err=%v", pa, err)
	}
}

func TestKrogerAppDeleteClearsCredentialsConnectionsAndChecks(t *testing.T) {
	caller, db, primary, _ := setupKrogerAppOperator(t)
	caller.house = primary
	when := time.Now().UTC().Format(time.RFC3339Nano)
	if rec := caller.authed(http.MethodPut, "/api/households/"+primary+"/kroger-app", map[string]string{
		"clientId": "delete-me", "clientSecret": "delete-secret", "modality": "PICKUP",
	}); rec.Code != http.StatusOK {
		t.Fatal(rec)
	}
	if _, err := db.Exec(`
		INSERT INTO provider_connections (id, user_id, household_id, provider_id, state, updated_at)
		VALUES ('del-conn', 'user-1', ?, 'kroger', 'connected', ?)`, primary, when); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO stores (id, household_id, name, name_key, kind, position, linked_provider, location_id, location_chain, created_at, updated_at)
		VALUES ('store-del', ?, 'Kroger', 'kroger', 'online', 0, 'kroger', '12345678', 'KROGER', ?, ?)`, primary, when, when); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO store_checks (household_id, store_id, state, trigger, started_at, lease_until, checked_count, found_count, total_count)
		VALUES (?, 'store-del', 'done', 'manual', ?, ?, 1, 1, 1)`, primary, when, when); err != nil {
		t.Fatal(err)
	}
	del := caller.authed(http.MethodDelete, "/api/households/"+primary+"/kroger-app", nil)
	if del.Code != http.StatusOK || !strings.Contains(del.Body.String(), `"status":"not_set_up"`) {
		t.Fatalf("delete %d %s", del.Code, del.Body.String())
	}
	for _, q := range []string{
		`SELECT COUNT(*) FROM provider_app_credentials WHERE household_id = ? AND provider_id = 'kroger'`,
		`SELECT COUNT(*) FROM provider_connections WHERE household_id = ? AND provider_id = 'kroger'`,
		`SELECT COUNT(*) FROM store_checks WHERE household_id = ?`,
	} {
		var n int
		if err := db.QueryRow(q, primary).Scan(&n); err != nil || n != 0 {
			t.Fatalf("%s count=%d err=%v", q, n, err)
		}
	}
}
