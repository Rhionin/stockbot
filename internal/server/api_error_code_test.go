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
var appErrorExemptions = []string{
	"POST /api/sentry-tunnel",
	"POST /api/deploy-hook",
	"web shell on a non-/api path",
	"GET /brand/logo.png, GET /terms, GET /privacy",
	"GET /health after boot, and a wrong method on /health",
	"GET /api/build during migration",
	"a 502, 503, 504, or 530 this server did not produce",
}

func TestAPIJSONErrorsCarryACode(t *testing.T) {
	if len(appErrorExemptions) != 7 {
		t.Fatalf("exemptions = %d, the contract lists 7", len(appErrorExemptions))
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
	closedHandler, _ := NewHandler(nil, nil, nil, nil, WithHouseholdAuth(closed))
	session := callAPI(closedHandler, http.MethodGet, "/api/session", "", publicHeaders())
	assertSignInNotConfigured(t, session)
	login := callAPI(closedHandler, http.MethodPost, "/api/login", `{"username":"pantry","password":"x"}`, publicHeaders())
	assertSignInNotConfigured(t, login)

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
		if err := connection.NewDirectory(db).GenerateAuthState(t.Context(), primaryScope(t, db), "kroger", "state-1"); err != nil {
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
		login := publicHandler(gate)
		badPassword := callAPI(login, http.MethodPost, "/api/login", `{"username":"pantry","password":"wrong-password-value"}`, publicHeaders())
		if badPassword.Code != http.StatusUnauthorized || badPassword.Body.String() != `{"error":"The username or password is incorrect.","code":"unauthorized"}` {
			t.Fatalf("login = %d %s", badPassword.Code, badPassword.Body.String())
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
		gate := testHousehold(t, "pantry", "correct-horse-battery")
		login := publicHandler(gate)
		badBody := callAPI(login, http.MethodPost, "/api/login", "{", publicHeaders())
		if badBody.Code != http.StatusBadRequest || badBody.Body.String() != `{"error":"Enter your username and password.","code":"validation"}` {
			t.Fatalf("login = %d %s", badBody.Code, badBody.Body.String())
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
		// No Content-Length: MaxBytesReader rejects the body (handler_wrapper.go).
		chunked := httptest.NewRequest(http.MethodPost, "/api/products", strings.NewReader(body))
		chunked.ContentLength = -1
		chunked.Header.Del("Content-Length")
		chunkedRec := httptest.NewRecorder()
		handler.ServeHTTP(chunkedRec, chunked)
		if chunkedRec.Code != http.StatusBadRequest || chunkedRec.Body.String() != `{"error":"request body is too large","code":"payload_too_large"}` {
			t.Fatalf("chunked = %d %s", chunkedRec.Code, chunkedRec.Body.String())
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
	for _, status := range []int{
		http.StatusForbidden,
		http.StatusGone,
		http.StatusRequestEntityTooLarge,
		http.StatusUnsupportedMediaType,
	} {
		if got := defaultClientCode(status); got != "" {
			t.Errorf("status %d default code = %q; 403, 410, 413, and 415 have no default", status, got)
		}
	}
	fset := token.NewFileSet()
	exemptCalls := map[string]int{}
	for _, dir := range []string{serverDir, filepath.Join(serverDir, "..", "..", "cmd", "server")} {
		files, err := parseNonTest(fset, dir)
		if err != nil {
			t.Fatal(err)
		}
		inspectCodelessWriters(t, fset, files, exemptCalls)
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
				continue
			}
			text, err := os.ReadFile(filepath.Join(dir, entry.Name()))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(text), `map[string]string{"error"`) && entry.Name() != "sentry_tunnel.go" {
				t.Errorf("%s builds a JSON error map with no code", entry.Name())
			}
		}
	}
	if exemptCalls["sentry_tunnel.go"] == 0 {
		t.Fatal("sentry tunnel no longer uses writeExemptError")
	}
	for name, n := range exemptCalls {
		if name != "sentry_tunnel.go" {
			t.Errorf("%s has %d writeExemptError calls; only the sentry tunnel may omit a code", name, n)
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

func inspectCodelessWriters(t *testing.T, fset *token.FileSet, files []*ast.File, exemptCalls map[string]int) {
	t.Helper()
	for _, file := range files {
		name := filepath.Base(fset.Position(file.Pos()).Filename)
		var fnName string
		var walk func(ast.Node) bool
		walk = func(n ast.Node) bool {
			if n == nil {
				return false
			}
			switch node := n.(type) {
			case *ast.FuncDecl:
				prev := fnName
				if node.Name != nil {
					fnName = node.Name.Name
				}
				if node.Body != nil {
					ast.Inspect(node.Body, walk)
				}
				fnName = prev
				return false
			case *ast.FuncLit:
				prev := fnName
				fnName = ""
				if node.Body != nil {
					ast.Inspect(node.Body, walk)
				}
				fnName = prev
				return false
			case *ast.CompositeLit:
				if typeName(node.Type) == "HTTPError" && statusWithoutDefault(compositeField(node, "Code")) && !codeArg(compositeField(node, "ErrorCode")) {
					t.Errorf("%s: HTTPError uses a 4xx with no default code and no ErrorCode", fset.Position(node.Pos()))
				}
			case *ast.CallExpr:
				ident, ok := node.Fun.(*ast.Ident)
				if !ok {
					if sel, ok := node.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Error" {
						if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "http" {
							t.Errorf("%s: http.Error on an API path has no code", fset.Position(node.Pos()))
						}
					}
					return true
				}
				switch ident.Name {
				case "writeError", "writeAuthError":
					if len(node.Args) < 4 || !codeArg(node.Args[2]) {
						if len(node.Args) >= 2 && statusWithoutDefault(node.Args[1]) {
							t.Errorf("%s: %s uses a 4xx with no default code", fset.Position(node.Pos()), ident.Name)
						} else {
							t.Errorf("%s: %s needs a code argument", fset.Position(node.Pos()), ident.Name)
						}
					}
				case "writeStatusError":
					if len(node.Args) >= 4 && statusWithoutDefault(node.Args[2]) && !exprCarriesCode(node.Args[3]) {
						t.Errorf("%s: writeStatusError uses a 4xx with no default code", fset.Position(node.Pos()))
					}
				case "writeExemptError":
					exemptCalls[name]++
				case "writeJSON":
					if len(node.Args) < 3 || fnName == "writeExemptError" {
						break
					}
					lit, ok := node.Args[2].(*ast.CompositeLit)
					if !ok || typeName(lit.Type) != "errorPayload" {
						break
					}
					if !codeFieldPresent(compositeField(lit, "Code")) {
						t.Errorf("%s: writeJSON(errorPayload) has no Code", fset.Position(node.Pos()))
					}
				}
			}
			return true
		}
		ast.Inspect(file, walk)
	}
}

func typeName(expr ast.Expr) string {
	switch v := expr.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.SelectorExpr:
		return v.Sel.Name
	case *ast.StarExpr:
		return typeName(v.X)
	default:
		return ""
	}
}

func compositeField(lit *ast.CompositeLit, name string) ast.Expr {
	if lit == nil {
		return nil
	}
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		id, ok := kv.Key.(*ast.Ident)
		if ok && id.Name == name {
			return kv.Value
		}
	}
	return nil
}

func codeFieldPresent(expr ast.Expr) bool {
	if expr == nil {
		return false
	}
	lit, ok := expr.(*ast.BasicLit)
	return !ok || (lit.Value != `""` && lit.Value != "``")
}

func statusWithoutDefault(expr ast.Expr) bool {
	switch v := expr.(type) {
	case *ast.SelectorExpr:
		pkg, ok := v.X.(*ast.Ident)
		if !ok || pkg.Name != "http" {
			return false
		}
		switch v.Sel.Name {
		case "StatusForbidden", "StatusGone", "StatusRequestEntityTooLarge", "StatusUnsupportedMediaType":
			return true
		}
	case *ast.BasicLit:
		switch v.Value {
		case "403", "410", "413", "415":
			return true
		}
	}
	return false
}

func exprCarriesCode(expr ast.Expr) bool {
	switch v := expr.(type) {
	case *ast.UnaryExpr:
		return exprCarriesCode(v.X)
	case *ast.CompositeLit:
		return codeArg(compositeField(v, "ErrorCode"))
	case *ast.CallExpr:
		ident, ok := v.Fun.(*ast.Ident)
		if !ok {
			return false
		}
		switch ident.Name {
		case "BadRequest", "NotFound", "Conflict", "ConfirmationMismatch", "ProviderUnavailable", "ConflictDetails", "ConflictSuggestions":
			return true
		}
	}
	return false
}

func codeArg(expr ast.Expr) bool {
	switch v := expr.(type) {
	case *ast.Ident:
		switch v.Name {
		case "codeUnauthorized", "codeValidation", "codeConfirmationMismatch", "codeRateLimited",
			"codePayloadTooLarge", "codeNotFound", "codeConflict", "codeMethodNotAllowed",
			"codeInternal", "codeSyncReadOnly", "codeProviderUnavailable", "authNotConfiguredCode", "CodeInternal":
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
			"provider_unavailable", "updating", "busy", "no_space", "auth_not_configured":
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
