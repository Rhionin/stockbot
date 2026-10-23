package server

import (
	"go/ast"
	"go/token"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/cart"
	"github.com/Rhionin/stockbot/internal/cart/connection"
	"github.com/Rhionin/stockbot/internal/cart/kroger"
	"github.com/Rhionin/stockbot/internal/product"
	"github.com/go-json-experiment/json"
)

// appErrorExemptions are the responses that may omit code. The sweep fails
// when a JSON error outside this list has an empty code, and when one of
// these responses starts carrying a code.
//
//   - POST /api/sentry-tunnel: passthrough. The page does not branch on these bodies.
//   - POST /api/deploy-hook: not called by the web or mobile app.
//   - Web shell on a non-/api path: plain text the browser treats as a document.
//   - GET /brand/logo.png, GET /terms, GET /privacy: file responses. A wrong method stays the router's plain text.
//   - GET /health after boot, and a wrong method on /health: not an app route. While migrations run, /health is updating, which is not this exemption.
//   - GET /api/build during migration: 200 build JSON. TestBootGateServesMigratingThenReady covers that response and the updating body on every other route.
//   - A 502, 503, 504, or 530 this server did not produce: not generated here, so the client can still treat it as offline.
//   - GET /api/session and POST /api/login while sign-in is not configured: 503 with no code. auth_not_configured belongs to #393. The live login request is still the middleware 401; ServeLogin's 503 is what #393 will replace.
var appErrorExemptions = []string{
	"POST /api/sentry-tunnel",
	"POST /api/deploy-hook",
	"web shell on a non-/api path",
	"GET /brand/logo.png, GET /terms, GET /privacy",
	"GET /health after boot, and a wrong method on /health",
	"GET /api/build during migration",
	"a 502, 503, 504, or 530 this server did not produce",
	"unconfigured sign-in 503 (GET /api/session and POST /api/login)",
}

func TestAPIJSONErrorsCarryACode(t *testing.T) {
	if len(appErrorExemptions) != 8 {
		t.Fatalf("exemptions = %d, the contract lists 8", len(appErrorExemptions))
	}
	handler, _ := setupTestWithContributor(t, nil)
	routes, err := enumerateRoutes()
	if err != nil {
		t.Fatal(err)
	}
	seenAPI := 0
	for _, route := range routes {
		method, path, err := concretePath(route.pattern)
		if err != nil || !strings.HasPrefix(path, "/api") {
			continue
		}
		if path == sentryTunnelPath {
			continue
		}
		seenAPI++
		rec := callAPI(handler, http.MethodOptions, path, "")
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("OPTIONS %s (%s %s) status = %d, body = %s", path, method, route.pattern, rec.Code, rec.Body.String())
			continue
		}
		assertCodedJSON(t, "OPTIONS "+path, rec, codeMethodNotAllowed)
	}
	if seenAPI < 10 {
		t.Fatalf("walked %d /api routes, the inventory is missing", seenAPI)
	}

	unknown := callAPI(handler, http.MethodGet, "/api/not-a-route", "")
	assertCodedJSON(t, "GET /api/not-a-route", unknown, codeNotFound)
	root := callAPI(handler, http.MethodGet, "/api", "")
	assertCodedJSON(t, "GET /api", root, codeNotFound)

	malformed := callAPI(handler, http.MethodPost, "/api/products", "{")
	assertCodedJSON(t, "POST /api/products malformed", malformed, codeValidation)
	assertErrorPrefix(t, malformed, "invalid request body: ")

	unset := callAPI(handler, http.MethodGet, "/api/sync/snapshot", "")
	assertCodedJSON(t, "GET /api/sync/snapshot unset", unset, codeNotFound)
	if unset.Body.String() != `{"error":"Not found.","code":"not_found"}` {
		t.Fatalf("unset snapshot = %s", unset.Body.String())
	}

	const token = "sync-token-value-ok"
	syncHandler, _ := setupTestWithContributor(t, nil, WithSyncToken(token))
	badToken := callAPI(syncHandler, http.MethodGet, "/api/sync/snapshot", "", header("Authorization", "Bearer nope"))
	assertCodedJSON(t, "GET /api/sync/snapshot bad token", badToken, codeUnauthorized)
	if badToken.Body.String() != `{"error":"The sync token is not valid.","code":"unauthorized"}` {
		t.Fatalf("bad token = %s", badToken.Body.String())
	}
	readOnly := callAPI(syncHandler, http.MethodPost, "/api/products", `{"name":"Nope"}`, header("Authorization", "Bearer "+token))
	assertCodedJSON(t, "POST /api/products with sync token", readOnly, codeSyncReadOnly)

	gate := testHousehold(t, "pantry", "correct-horse-battery")
	authed, _ := setupTestWithContributor(t, nil, WithHouseholdAuth(gate))
	open := callAPI(authed, http.MethodGet, "/api/inventory", "", publicHeaders())
	assertCodedJSON(t, "GET /api/inventory unsigned", open, codeUnauthorized)

	closed, err := LoadHouseholdAuth(filepath.Join(t.TempDir(), "missing"), "")
	if err == nil {
		t.Fatal("missing password file returned no error")
	}
	closedHandler := publicHandler(closed)
	session := callAPI(closedHandler, http.MethodGet, "/api/session", "", publicHeaders())
	if session.Code != http.StatusServiceUnavailable {
		t.Fatalf("unconfigured session = %d %s", session.Code, session.Body.String())
	}
	assertCodelessJSON(t, session, "Sign-in is unavailable right now.")
	loginDirect := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(`{"username":"pantry","password":"x"}`))
	withPublicEntry(loginDirect)
	loginRec := httptest.NewRecorder()
	closed.ServeLogin(loginRec, loginDirect)
	if loginRec.Code != http.StatusServiceUnavailable {
		t.Fatalf("ServeLogin unconfigured = %d %s", loginRec.Code, loginRec.Body.String())
	}
	assertCodelessJSON(t, loginRec, "Sign-in is unavailable right now.")

	sentry := callAPI(handler, http.MethodPost, sentryTunnelPath, "")
	if sentry.Code != http.StatusNotFound {
		t.Fatalf("sentry = %d %s", sentry.Code, sentry.Body.String())
	}
	assertCodelessJSON(t, sentry, "Not found.")

	hook := callAPI(handler, http.MethodPost, "/api/deploy-hook", "")
	if hook.Code != http.StatusServiceUnavailable {
		t.Fatalf("deploy hook = %d %s", hook.Code, hook.Body.String())
	}
	assertCodelessJSON(t, hook, "deploy hook is not configured")

	health := callAPI(handler, http.MethodPost, "/health", "")
	assertPlainText(t, "POST /health", health, http.StatusMethodNotAllowed)
	page := callAPI(handler, http.MethodPost, "/inventory", "")
	assertPlainText(t, "POST /inventory", page, http.StatusMethodNotAllowed)
	for _, path := range []string{"/brand/logo.png", "/terms", "/privacy"} {
		wrong := callAPI(handler, http.MethodPost, path, "")
		assertPlainText(t, "POST "+path, wrong, http.StatusMethodNotAllowed)
	}
	readyHealth := callAPI(handler, http.MethodGet, "/health", "")
	if readyHealth.Code != http.StatusOK {
		t.Fatalf("GET /health = %d %s", readyHealth.Code, readyHealth.Body.String())
	}
}

func TestAppFacingErrorCodes(t *testing.T) {
	t.Run("provider_unavailable", func(t *testing.T) {
		handler, env := setupTestWithContributor(t, nil)
		prod := product.Product{
			ID:     "ext-down",
			Name:   "Cached Name",
			Source: product.SourceExternal,
		}
		if err := env.ProductStore.CreateProduct(t.Context(), prod); err != nil {
			t.Fatal(err)
		}
		env.OpenFoodFacts.SeedError("ext-down", io.ErrUnexpectedEOF)
		rec := callAPI(handler, http.MethodPost, "/api/products/ext-down/refresh", "")
		if rec.Code != http.StatusBadGateway {
			t.Fatalf("status = %d %s", rec.Code, rec.Body.String())
		}
		const want = `{"error":"Open Food Facts isn't responding. Try again later.","code":"provider_unavailable","provider":"Open Food Facts"}`
		if rec.Body.String() != want {
			t.Fatalf("refresh = %s", rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), "UnexpectedEOF") {
			t.Fatalf("body leaked the transport error: %s", rec.Body.String())
		}

		db := setupTestDB(t)
		adapter, err := kroger.New("client", "secret", "https://pantry.example/api/providers/kroger/callback", "PICKUP")
		if err != nil {
			t.Fatal(err)
		}
		adapter.WithTransport(roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, io.ErrUnexpectedEOF
		}))
		registry := cart.NewRegistry()
		if err := registry.Register(adapter, cart.WithCredentialsConfigured(true)); err != nil {
			t.Fatal(err)
		}
		if err := connection.NewDirectory(db).GenerateAuthState(t.Context(), "kroger", "state-1"); err != nil {
			t.Fatal(err)
		}
		callbackHandler, _ := NewHandler(nil, nil, nil, db, WithCartRegistry(registry, cart.NewLedger(db)))
		callback := callAPI(callbackHandler, http.MethodGet, "/api/providers/kroger/callback?code=abc&state=state-1", "")
		if callback.Code != http.StatusBadGateway {
			t.Fatalf("callback = %d %s", callback.Code, callback.Body.String())
		}
		const callbackWant = `{"error":"Kroger isn't responding. Try again later.","code":"provider_unavailable","provider":"Kroger"}`
		if callback.Body.String() != callbackWant {
			t.Fatalf("callback = %s", callback.Body.String())
		}
	})

	t.Run("rate_limited", func(t *testing.T) {
		gate := testHousehold(t, "pantry", "correct-horse-battery")
		now := time.Date(2026, 10, 8, 15, 0, 0, 0, time.UTC)
		gate.now = func() time.Time { return now }
		handler := publicHandler(gate)
		for i := 0; i < maxAuthFailures; i++ {
			rec := callAPI(handler, http.MethodPost, "/api/login", `{"username":"pantry","password":"wrong-password-value"}`, publicHeaders())
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("attempt %d = %d %s", i+1, rec.Code, rec.Body.String())
			}
		}
		locked := callAPI(handler, http.MethodPost, "/api/login", `{"username":"pantry","password":"correct-horse-battery"}`, publicHeaders())
		assertRetryAfter(t, locked)
		if locked.Body.String() != `{"error":"Too many sign-in attempts. Try again later.","code":"rate_limited"}` {
			t.Fatalf("login = %s", locked.Body.String())
		}
		basicReq := httptest.NewRequest(http.MethodGet, "/api/scans", nil)
		for key, value := range publicHeaders() {
			basicReq.Header.Set(key, value)
		}
		basicReq.SetBasicAuth("pantry", "correct-horse-battery")
		basic := httptest.NewRecorder()
		handler.ServeHTTP(basic, basicReq)
		if basic.Code != http.StatusTooManyRequests {
			t.Fatalf("basic = %d %s", basic.Code, basic.Body.String())
		}
		assertRetryAfter(t, basic)
		if !strings.Contains(basic.Body.String(), `"code":"rate_limited"`) {
			t.Fatalf("basic = %s", basic.Body.String())
		}
	})

	t.Run("unauthorized", func(t *testing.T) {
		gate := testHousehold(t, "pantry", "correct-horse-battery")
		handler, _ := setupTestWithContributor(t, nil, WithHouseholdAuth(gate))
		rec := callAPI(handler, http.MethodGet, "/api/inventory", "", publicHeaders())
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d %s", rec.Code, rec.Body.String())
		}
		if rec.Body.String() != `{"error":"Sign in required.","code":"unauthorized"}` {
			t.Fatalf("body = %s", rec.Body.String())
		}
	})

	t.Run("validation", func(t *testing.T) {
		handler, _ := setupTestWithContributor(t, nil)
		rec := callAPI(handler, http.MethodPost, "/api/products", "{")
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d %s", rec.Code, rec.Body.String())
		}
		assertCodedJSON(t, "malformed", rec, codeValidation)
		assertErrorPrefix(t, rec, "invalid request body: ")
		if strings.Contains(rec.Body.String(), `"field"`) {
			t.Fatalf("validation body included field: %s", rec.Body.String())
		}
	})

	t.Run("payload_too_large", func(t *testing.T) {
		handler, _ := setupTestWithContributor(t, nil)
		body := `{"name":"` + strings.Repeat("a", maxJSONBodyBytes) + `"}`
		rec := callAPI(handler, http.MethodPost, "/api/products", body)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d %s", rec.Code, rec.Body.String())
		}
		if rec.Body.String() != `{"error":"request body is too large","code":"payload_too_large"}` {
			t.Fatalf("body = %s", rec.Body.String())
		}
		report := callAPI(handler, http.MethodPost, "/api/telemetry/client", `{"kind":"page_load","message":"`+strings.Repeat("a", maxClientReportBytes)+`"}`)
		if report.Code != http.StatusBadRequest {
			t.Fatalf("report status = %d %s", report.Code, report.Body.String())
		}
		if report.Body.String() != `{"error":"report is too large","code":"payload_too_large"}` {
			t.Fatalf("report = %s", report.Body.String())
		}
	})

	t.Run("confirmation_mismatch", func(t *testing.T) {
		handler, _ := setupTestWithContributor(t, nil)
		rec := callAPI(handler, http.MethodPost, "/api/inventory/wipe", `{"confirmation":"wipe inventory"}`)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d %s", rec.Code, rec.Body.String())
		}
		const want = `{"error":"Type WIPE INVENTORY to confirm wiping the inventory.","code":"confirmation_mismatch"}`
		if rec.Body.String() != want {
			t.Fatalf("body = %s", rec.Body.String())
		}
	})

	t.Run("method_not_allowed", func(t *testing.T) {
		handler, _ := setupTestWithContributor(t, nil)
		rec := callAPI(handler, http.MethodDelete, "/api/products/1", "")
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("status = %d %s", rec.Code, rec.Body.String())
		}
		if rec.Body.String() != `{"error":"Method not allowed.","code":"method_not_allowed"}` {
			t.Fatalf("body = %s", rec.Body.String())
		}
		if rec.Header().Get("Allow") == "" {
			t.Fatal("405 dropped Allow")
		}
	})

	t.Run("sync_read_only", func(t *testing.T) {
		const token = "sync-token-value-ok"
		handler, _ := setupTestWithContributor(t, nil, WithSyncToken(token))
		rec := callAPI(handler, http.MethodPost, "/api/products", `{"name":"Nope"}`, header("Authorization", "Bearer "+token))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d %s", rec.Code, rec.Body.String())
		}
		if rec.Body.String() != `{"error":"This token only reads the sync snapshot.","code":"sync_read_only"}` {
			t.Fatalf("body = %s", rec.Body.String())
		}
	})
}

func TestDefaultStatusCodeDoesNotReplaceAnExplicitCode(t *testing.T) {
	cases := []struct {
		status int
		code   string
	}{
		{http.StatusBadRequest, codeValidation},
		{http.StatusUnprocessableEntity, codeValidation},
		{http.StatusUnauthorized, codeUnauthorized},
		{http.StatusNotFound, codeNotFound},
		{http.StatusMethodNotAllowed, codeMethodNotAllowed},
		{http.StatusConflict, codeConflict},
		{http.StatusTooManyRequests, codeRateLimited},
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		writeStatusError(nil, rec, tc.status, &HTTPError{Code: tc.status, Message: "x"})
		assertCodedJSON(t, http.StatusText(tc.status), rec, tc.code)
	}
	rec := httptest.NewRecorder()
	writeStatusError(nil, rec, http.StatusBadRequest, ConfirmationMismatch("Type WIPE INVENTORY to confirm wiping the inventory."))
	assertCodedJSON(t, "confirmation", rec, codeConfirmationMismatch)
}

func TestCodelessWritersStayOnTheExemptionList(t *testing.T) {
	serverDir, _, err := sourceDirs()
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	files, err := parseNonTest(fset, serverDir)
	if err != nil {
		t.Fatal(err)
	}
	exemptCalls := map[string]int{}
	for _, file := range files {
		name := filepath.Base(fset.Position(file.Pos()).Filename)
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			ident, ok := call.Fun.(*ast.Ident)
			if !ok {
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Error" {
					if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "http" {
						t.Errorf("%s: http.Error on an API path has no code", fset.Position(call.Pos()))
					}
				}
				return true
			}
			switch ident.Name {
			case "writeError", "writeAuthError":
				if len(call.Args) < 4 || !codeArg(call.Args[2]) {
					t.Errorf("%s: %s needs a code argument", fset.Position(call.Pos()), ident.Name)
				}
			case "writeExemptError":
				exemptCalls[name]++
				if name == "household_auth.go" {
					lit, ok := call.Args[len(call.Args)-1].(*ast.BasicLit)
					if !ok || lit.Value != `"Sign-in is unavailable right now."` {
						t.Errorf("%s: writeExemptError is only for the unconfigured sign-in sentence", fset.Position(call.Pos()))
					}
				}
			}
			return true
		})
	}
	if exemptCalls["sentry_tunnel.go"] == 0 {
		t.Fatal("sentry tunnel no longer uses writeExemptError")
	}
	if exemptCalls["household_auth.go"] != 2 {
		t.Fatalf("household writeExemptError calls = %d, want the two #393 lines", exemptCalls["household_auth.go"])
	}
	for name, n := range exemptCalls {
		if name != "sentry_tunnel.go" && name != "household_auth.go" {
			t.Errorf("%s has %d writeExemptError calls; that file is not an exemption", name, n)
		}
	}

	entries, err := os.ReadDir(serverDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		text, err := os.ReadFile(filepath.Join(serverDir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(text), `map[string]string{"error"`) && entry.Name() != "sentry_tunnel.go" {
			t.Errorf("%s builds a JSON error map with no code", entry.Name())
		}
	}

	hook, err := os.ReadFile(filepath.Join(serverDir, "..", "deployhook", "hook.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, sentence := range []string{
		"method not allowed",
		"deploy hook is not configured",
		"request is too large",
		"could not read the request",
		"invalid signature",
		"could not start the update",
	} {
		if !strings.Contains(string(hook), `"`+sentence+`"`) {
			t.Errorf("deploy hook dropped the exempt sentence %q", sentence)
		}
	}
	if strings.Contains(string(hook), `"code"`) {
		t.Fatal("deploy hook gained a code field")
	}
	web, err := os.ReadFile(filepath.Join(serverDir, "..", "webui", "webui.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(web), "http.Error(") {
		t.Fatal("web shell no longer answers in plain text")
	}
	if strings.Contains(string(web), `"code"`) {
		t.Fatal("web shell gained a code field")
	}
}

func codeArg(expr ast.Expr) bool {
	switch v := expr.(type) {
	case *ast.Ident:
		switch v.Name {
		case "codeUnauthorized", "codeValidation", "codeConfirmationMismatch", "codeRateLimited",
			"codePayloadTooLarge", "codeNotFound", "codeConflict", "codeMethodNotAllowed",
			"codeInternal", "codeSyncReadOnly", "codeProviderUnavailable", "CodeInternal":
			return true
		default:
			return false
		}
	case *ast.SelectorExpr:
		return v.Sel.Name == "CodeInternal"
	case *ast.BasicLit:
		s, err := strconv.Unquote(v.Value)
		if err != nil {
			return false
		}
		switch s {
		case "unauthorized", "validation", "confirmation_mismatch", "rate_limited", "payload_too_large",
			"not_found", "conflict", "method_not_allowed", "internal", "sync_read_only",
			"provider_unavailable", "updating", "busy", "no_space":
			return true
		default:
			return false
		}
	default:
		return false
	}
}

func callAPI(handler http.Handler, method, path, body string, headers ...map[string]string) *httptest.ResponseRecorder {
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	for _, set := range headers {
		for key, value := range set {
			req.Header.Set(key, value)
		}
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func header(key, value string) map[string]string {
	return map[string]string{key: value}
}

func publicHeaders() map[string]string {
	return map[string]string{
		entryHeader:         entryPublic,
		"X-Forwarded-Proto": "https",
		clientIPHeader:      "203.0.113.8",
	}
}

func assertCodedJSON(t *testing.T, name string, rec *httptest.ResponseRecorder, want string) {
	t.Helper()
	if rec.Code < 400 {
		t.Fatalf("%s status = %d, want an error; body = %s", name, rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("%s content type = %q, body = %s", name, rec.Header().Get("Content-Type"), rec.Body.String())
	}
	var payload struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("%s body %s: %v", name, rec.Body.String(), err)
	}
	if payload.Code == "" {
		t.Fatalf("%s JSON error has no code: %s", name, rec.Body.String())
	}
	if want != "" && payload.Code != want {
		t.Fatalf("%s code = %q, want %q; body = %s", name, payload.Code, want, rec.Body.String())
	}
}

func assertErrorPrefix(t *testing.T, rec *httptest.ResponseRecorder, prefix string) {
	t.Helper()
	var payload struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("body %s: %v", rec.Body.String(), err)
	}
	if !strings.HasPrefix(payload.Error, prefix) || payload.Error == prefix {
		t.Fatalf("error = %q, want prefix %q", payload.Error, prefix)
	}
}

func assertCodelessJSON(t *testing.T, rec *httptest.ResponseRecorder, sentence string) {
	t.Helper()
	var payload struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("body %s: %v", rec.Body.String(), err)
	}
	if payload.Code != "" {
		t.Fatalf("exempt body gained code %q: %s", payload.Code, rec.Body.String())
	}
	if payload.Error != sentence {
		t.Fatalf("error = %q, want %q", payload.Error, sentence)
	}
}

func assertPlainText(t *testing.T, name string, rec *httptest.ResponseRecorder, status int) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("%s status = %d, body = %s", name, rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Header().Get("Content-Type"), "application/json") || strings.HasPrefix(strings.TrimSpace(rec.Body.String()), "{") {
		t.Fatalf("%s became JSON: %s", name, rec.Body.String())
	}
}

func assertRetryAfter(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d %s", rec.Code, rec.Body.String())
	}
	secs, err := strconv.Atoi(rec.Header().Get("Retry-After"))
	if err != nil || secs < 1 {
		t.Fatalf("Retry-After = %q", rec.Header().Get("Retry-After"))
	}
}
