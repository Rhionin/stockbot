package server

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/buildinfo"
	"github.com/Rhionin/stockbot/internal/cart"
	"github.com/Rhionin/stockbot/internal/product"
	"github.com/Rhionin/stockbot/internal/supply"
	"github.com/go-json-experiment/json"
	"pgregory.net/rapid"
)

// isSPADocument reports whether body is the SPA/placeholder document served by
// the web UI fallback. The tracked placeholder names itself with
// placeholderMarker, so its presence is a reliable signal that SPA_Fallback
// fired — which Properties 4 and 5 assert must never happen for API or health
// paths.
func isSPADocument(body []byte) bool {
	return bytes.Contains(body, []byte(placeholderMarker))
}

// TestAPIPathsNeverFallBack implements
// Feature: pi-release-build, Property 4: API paths never fall back to the SPA
// **Validates: Requirements 3.5**
//
// For any request path beginning with /api/ that matches no registered API
// route, the response status is 404 and the body is not the index.html
// document.
func TestAPIPathsNeverFallBack(t *testing.T) {
	handler, _ := setupTestWithDB(t)

	rapid.Check(t, func(t *rapid.T) {
		path := generateUnregisteredAPIPath(t)

		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		res := w.Result()
		body, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatalf("read body for GET %s: %v", path, err)
		}

		if res.StatusCode != http.StatusNotFound {
			t.Fatalf("expected status 404 for GET %s, got %d", path, res.StatusCode)
		}
		if isSPADocument(body) {
			t.Fatalf("GET %s fell back to the SPA document", path)
		}
	})
}

// generateUnregisteredAPIPath draws paths under /api/ that match no registered
// route, including segments that shadow real prefixes like
// /api/products/x/y/z. Any path that happens to match a registered route is
// skipped, since Property 4 is about unmatched paths only.
func generateUnregisteredAPIPath(t *rapid.T) string {
	seg := rapid.StringMatching(`[a-z][a-z0-9-]{0,10}`)

	var path string
	switch rapid.IntRange(0, 2).Draw(t, "apiPathShape") {
	case 0:
		// Random multi-segment path under /api/.
		n := rapid.IntRange(1, 5).Draw(t, "apiSegments")
		parts := make([]string, 0, n)
		for i := 0; i < n; i++ {
			parts = append(parts, seg.Draw(t, "apiSeg"))
		}
		path = "/api/" + strings.Join(parts, "/")
	case 1:
		// Over-deep path that shadows a real prefix, e.g. /api/products/x/y/z.
		prefix := rapid.SampledFrom([]string{
			"products", "scans", "inventory", "suggestions", "items", "shopping-list",
		}).Draw(t, "apiPrefix")
		n := rapid.IntRange(2, 4).Draw(t, "apiExtraSegments")
		parts := []string{prefix}
		for i := 0; i < n; i++ {
			parts = append(parts, seg.Draw(t, "apiExtraSeg"))
		}
		path = "/api/" + strings.Join(parts, "/")
	default:
		// A single nonsense segment.
		path = "/api/" + seg.Draw(t, "apiSingleSeg")
	}

	if isRegisteredGetPath(path) {
		t.Skip("generated a registered route; Property 4 is about unmatched paths")
	}
	return path
}

// isRegisteredGetPath reports whether path matches one of the GET routes
// registered on apiMux. It is a conservative allow-list used only to skip
// generated paths that would legitimately not be 404s.
func isRegisteredGetPath(path string) bool {
	switch path {
	case "/api/products", "/api/products/lookup", "/api/scans", "/api/scans/history",
		"/api/inventory", "/api/shopping-list", "/api/events", "/api/build",
		"/api/scanner/config", "/api/telemetry",
		// POST-only. GET is 405 from the method pattern, not an unmatched path.
		"/api/deploy-hook",
		"/api/contributions", "/api/settings/contribution", "/api/settings/supply",
		"/api/groups", "/api/group-suggestions", "/api/settings/group-rule",
		"/api/history":
		return true
	}
	// Wildcard GET routes: /api/inventory/{itemId}/instances, /api/products/{id},
	// /api/products/{id}/contributions, /api/products/{id}/supply-override.
	if regexp.MustCompile(`^/api/products/[^/]+/supply-override$`).MatchString(path) {
		return true
	}
	if regexp.MustCompile(`^/api/inventory/[^/]+/instances$`).MatchString(path) {
		return true
	}
	if regexp.MustCompile(`^/api/products/[^/]+$`).MatchString(path) {
		return true
	}
	if regexp.MustCompile(`^/api/products/[^/]+/contributions$`).MatchString(path) {
		return true
	}
	if regexp.MustCompile(`^/api/groups/[^/]+$`).MatchString(path) {
		return true
	}
	return false
}

// TestHealthPathsNeverFallBack implements
// Feature: pi-release-build, Property 5: Health paths never fall back to the SPA
// **Validates: Requirements 3.6**
//
// For any request whose path is /health or begins with /health/ and that
// matches no registered route — for any method — the response status is never
// 200 and the body is never the SPA document. The registered-and-valid cases
// (GET /health, and HEAD /health which ServeMux serves from the GET pattern)
// are excluded, since they correctly return 200.
func TestHealthPathsNeverFallBack(t *testing.T) {
	handler, _ := setupTestWithDB(t)

	rapid.Check(t, func(t *rapid.T) {
		method := rapid.SampledFrom([]string{
			http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut,
			http.MethodPatch, http.MethodDelete, http.MethodOptions,
		}).Draw(t, "healthMethod")
		path := generateHealthPath(t)

		// Exclude the registered-and-valid case: GET /health returns 200, and
		// Go 1.22+ ServeMux serves HEAD from a GET pattern, so HEAD /health also
		// returns 200. Both are correct behavior for the exact /health path, so
		// neither should be asserted non-200. Every other method on /health, and
		// every /health/... subpath under any method, is genuinely unmatched and
		// still exercised below.
		if path == "/health" && (method == http.MethodGet || method == http.MethodHead) {
			t.Skip("GET/HEAD /health is the registered valid case")
		}

		req := httptest.NewRequest(method, path, nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		res := w.Result()
		body, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatalf("read body for %s %s: %v", method, path, err)
		}

		if res.StatusCode == http.StatusOK {
			t.Fatalf("expected non-200 status for %s %s, got 200", method, path)
		}
		if isSPADocument(body) {
			t.Fatalf("%s %s fell back to the SPA document", method, path)
		}
	})
}

// generateHealthPath draws /health and /health/... paths.
func generateHealthPath(t *rapid.T) string {
	seg := rapid.StringMatching(`[a-z][a-z0-9-]{0,10}`)

	switch rapid.IntRange(0, 2).Draw(t, "healthPathShape") {
	case 0:
		return "/health"
	case 1:
		return "/health/" + seg.Draw(t, "healthSeg")
	default:
		n := rapid.IntRange(2, 4).Draw(t, "healthSegments")
		parts := make([]string, 0, n)
		for i := 0; i < n; i++ {
			parts = append(parts, seg.Draw(t, "healthDeepSeg"))
		}
		return "/health/" + strings.Join(parts, "/")
	}
}

// registeredRequest describes a request over the registered route set for
// Property 6, carrying the method, path, and optional JSON body.
type registeredRequest struct {
	method string
	path   string
	body   string
}

// seededProductID is the product row created by the Property 6 seed. The id
// generator can draw it so mutating routes sometimes hit an existing row and
// exercise the success path (which echoes request-derived data) rather than
// only the not-found path.
const seededProductID = "prod-1"

// generateRegisteredRequest draws a request over the registered route set,
// including wildcard routes with generated ids. GET /api/events is excluded
// by the caller because it is a non-terminating SSE stream.
//
// Wildcard ids are drawn from a mix that includes the seeded product id, so
// the mutating routes (PUT/PATCH/DELETE) sometimes address an existing row and
// exercise the success/echo path, not only the not-found/validation path. This
// is the case most likely to differ between the two handlers if routing
// composition were not transparent, so covering it strengthens the property.
func generateRegisteredRequest(t *rapid.T) registeredRequest {
	id := rapid.OneOf(
		rapid.Just(seededProductID),
		rapid.StringMatching(`[a-z0-9-]{1,12}`),
	)

	routes := []func(*rapid.T) registeredRequest{
		func(t *rapid.T) registeredRequest {
			return registeredRequest{http.MethodGet, "/health", ""}
		},
		func(t *rapid.T) registeredRequest {
			return registeredRequest{http.MethodGet, "/api/build", ""}
		},
		func(t *rapid.T) registeredRequest {
			return registeredRequest{http.MethodGet, "/api/scanner/config", ""}
		},
		func(t *rapid.T) registeredRequest {
			return registeredRequest{http.MethodGet, "/api/products", ""}
		},
		func(t *rapid.T) registeredRequest {
			return registeredRequest{http.MethodGet, "/api/products/lookup", ""}
		},
		func(t *rapid.T) registeredRequest {
			return registeredRequest{http.MethodGet, "/api/settings/contribution", ""}
		},
		func(t *rapid.T) registeredRequest {
			return registeredRequest{http.MethodGet, "/api/contributions", ""}
		},
		func(t *rapid.T) registeredRequest {
			return registeredRequest{http.MethodGet, "/api/products/" + id.Draw(t, "productDetailId"), ""}
		},
		func(t *rapid.T) registeredRequest {
			return registeredRequest{http.MethodGet, "/api/products/" + id.Draw(t, "productContributionsId") + "/contributions", ""}
		},
		func(t *rapid.T) registeredRequest {
			return registeredRequest{http.MethodGet, "/api/scans", ""}
		},
		func(t *rapid.T) registeredRequest {
			return registeredRequest{http.MethodGet, "/api/scans/history", ""}
		},
		func(t *rapid.T) registeredRequest {
			return registeredRequest{http.MethodGet, "/api/inventory", ""}
		},
		func(t *rapid.T) registeredRequest {
			return registeredRequest{http.MethodGet, "/api/history", ""}
		},
		func(t *rapid.T) registeredRequest {
			return registeredRequest{http.MethodGet, "/api/shopping-list", ""}
		},
		func(t *rapid.T) registeredRequest {
			return registeredRequest{http.MethodGet, "/api/settings/supply", ""}
		},
		func(t *rapid.T) registeredRequest {
			return registeredRequest{http.MethodGet, "/api/products/" + id.Draw(t, "supplyOverrideId") + "/supply-override", ""}
		},
		func(t *rapid.T) registeredRequest {
			return registeredRequest{http.MethodGet, "/api/inventory/" + id.Draw(t, "itemId") + "/instances", ""}
		},
		func(t *rapid.T) registeredRequest {
			return registeredRequest{http.MethodPut, "/api/products/" + id.Draw(t, "productId"), `{"name":"X","category":"Y"}`}
		},
		func(t *rapid.T) registeredRequest {
			return registeredRequest{http.MethodPatch, "/api/scans/" + id.Draw(t, "scanId"), `{}`}
		},
		func(t *rapid.T) registeredRequest {
			return registeredRequest{http.MethodDelete, "/api/inventory/instances/" + id.Draw(t, "instanceId"), ""}
		},
		func(t *rapid.T) registeredRequest {
			return registeredRequest{http.MethodPost, "/api/inventory/wipe", `{"confirmation":"` + supply.WipePhrase + `"}`}
		},
		func(t *rapid.T) registeredRequest {
			return registeredRequest{http.MethodDelete, "/api/shopping-list/items/" + id.Draw(t, "shoppingItemId"), ""}
		},
	}

	return rapid.SampledFrom(routes).Draw(t, "registeredRoute")(t)
}

// dynamicField matches server-generated identifiers and RFC3339 timestamps
// that differ between two independent calls (a minted UUID, a stamped time),
// so bodies can be normalized before comparison. Property 6 is about routing
// composition, not about a handler returning byte-identical dynamic values.
var dynamicField = regexp.MustCompile(
	`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:\d{2})` +
		`|[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)

// normalizeBody replaces server-generated identifiers and timestamps with a
// placeholder so two independently produced bodies compare equal when they
// differ only in those dynamic fields.
func normalizeBody(body []byte) []byte {
	return dynamicField.ReplaceAll(body, []byte("<dynamic>"))
}

// TestBuildEndpointReportsStampedCommit checks that GET /api/build echoes the
// commit, timestamps, version, and subject the binary was stamped with. Empty
// times and subject are omitted. An empty version is reported as dev. The
// body is JSON, never the SPA document.
func TestBuildEndpointReportsStampedCommit(t *testing.T) {
	originalCommit := buildinfo.Commit
	originalTime := buildinfo.CommittedAt
	originalBuilt := buildinfo.BuiltAt
	originalVersion := buildinfo.Version
	originalSubject := buildinfo.Subject
	t.Cleanup(func() {
		buildinfo.Commit = originalCommit
		buildinfo.CommittedAt = originalTime
		buildinfo.BuiltAt = originalBuilt
		buildinfo.Version = originalVersion
		buildinfo.Subject = originalSubject
	})

	handler, _ := setupTestWithDB(t)

	rapid.Check(t, func(t *rapid.T) {
		commit := rapid.String().Draw(t, "commit")
		committedAt := rapid.String().Draw(t, "committedAt")
		builtAt := rapid.String().Draw(t, "builtAt")
		version := rapid.String().Draw(t, "version")
		subject := rapid.String().Draw(t, "subject")
		buildinfo.Commit = commit
		buildinfo.CommittedAt = committedAt
		buildinfo.BuiltAt = builtAt
		buildinfo.Version = version
		buildinfo.Subject = subject

		req := httptest.NewRequest(http.MethodGet, "/api/build", nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		res := w.Result()
		body, err := io.ReadAll(res.Body)
		res.Body.Close()
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if res.StatusCode != http.StatusOK {
			t.Fatalf("GET /api/build status = %d, want 200; body = %q", res.StatusCode, body)
		}
		if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			t.Fatalf("Content-Type = %q, want application/json; body = %q", ct, body)
		}
		var decoded struct {
			Commit      string  `json:"commit"`
			CommittedAt *string `json:"committedAt"`
			BuiltAt     *string `json:"builtAt"`
			Version     string  `json:"version"`
			Subject     *string `json:"subject"`
		}
		if err := json.Unmarshal(body, &decoded); err != nil {
			t.Fatalf("decode body %q: %v", body, err)
		}
		if decoded.Commit != commit {
			t.Fatalf("commit = %q, want %q", decoded.Commit, commit)
		}
		if decoded.Version != buildinfo.VisibleVersion() {
			t.Fatalf("version = %q, want %q", decoded.Version, buildinfo.VisibleVersion())
		}
		assertOptionalStamp(t, "committedAt", committedAt, decoded.CommittedAt)
		assertOptionalStamp(t, "builtAt", builtAt, decoded.BuiltAt)
		assertOptionalStamp(t, "subject", subject, decoded.Subject)
	})
}

func assertOptionalStamp(t *rapid.T, name, want string, got *string) {
	t.Helper()
	if want == "" {
		if got != nil {
			t.Fatalf("%s = %q, want the field omitted", name, *got)
		}
		return
	}
	if got == nil || *got != want {
		gotValue := "<nil>"
		if got != nil {
			gotValue = *got
		}
		t.Fatalf("%s = %q, want %q", name, gotValue, want)
	}
}

// buildComposedAndAPIHandlers builds the composed root handler and a bare
// apiMux, EACH over its OWN freshly migrated in-memory database, both seeded
// identically by seed. Two databases, not one: a shared database would let a
// mutating route observe the first handler's write when the second handler
// runs, so the two responses could legitimately differ and Property 6 would
// fail on a correct implementation. The construction mirrors setupTestWithDB.
func buildComposedAndAPIHandlers(t *testing.T, seed func(env testEnv)) (http.Handler, http.Handler) {
	t.Helper()

	build := func() (*product.Catalog, *product.LookupService, *product.Refresher, testEnv) {
		db := setupTestDB(t)
		catalog := product.NewCatalog(db)
		clock := newFakeClock(time.Now())

		fakeClients := map[product.ExternalSource]product.BarcodeLookup{}
		externalLookup := product.NewExternalLookup(fakeClients)
		upstream := &fakeUpstream{
			ExternalLookup: externalLookup,
			databases:      map[product.ExternalSource]*fakeProductOpener{},
		}

		refresher := &product.Refresher{
			Catalog:               catalog,
			Upstream:              upstream,
			TTL:                   testProductCacheTTL,
			ExternalLookupEnabled: true,
			Now:                   clock.Now,
		}
		lookupService := &product.LookupService{
			Catalog:   catalog,
			Upstream:  upstream,
			Refresher: refresher,
			Now:       clock.Now,
			MissTTL:   5 * time.Minute,
		}

		env := testEnv{
			T:            t,
			DB:           db,
			ProductStore: catalog,
			Upstream:     upstream,
			Refresher:    refresher,
			Clock:        clock,
			MissTTL:      5 * time.Minute,
		}
		return catalog, lookupService, refresher, env
	}

	catalogA, lookupA, refresherA, envA := build()
	if seed != nil {
		seed(envA)
	}
	composed, _ := NewHandler(catalogA, lookupA, refresherA, envA.DB, WithCartRegistry(cart.NewRegistry(), cart.NewLedger(envA.DB)))

	catalogB, lookupB, refresherB, envB := build()
	if seed != nil {
		seed(envB)
	}
	apiMux, _, _ := newAPIMux(catalogB, lookupB, refresherB, envB.DB, nil)

	return composed, withPrimaryScope(envB.DB, apiMux)
}

// TestMountingWebUIPerturbsNoRoute implements
// Feature: pi-release-build, Property 6: Mounting the web UI perturbs no registered route
// **Validates: Requirements 3.5, 3.6, 3.7**
//
// For any request matching a route registered on apiMux, the response status,
// headers, and body are identical whether the request is sent to apiMux
// directly or to the composed root handler.
func TestMountingWebUIPerturbsNoRoute(t *testing.T) {
	seed := func(env testEnv) {
		setupProduct(seededProductID, "Seeded Product", "Seeded Category")(env)
	}
	composed, apiMux := buildComposedAndAPIHandlers(t, seed)

	rapid.Check(t, func(t *rapid.T) {
		rr := generateRegisteredRequest(t)

		// Skip GET /api/events: it is a non-terminating SSE stream, so a byte
		// comparison would hang rather than fail.
		if rr.method == http.MethodGet && rr.path == "/api/events" {
			t.Skip("GET /api/events is a non-terminating SSE stream")
		}

		composedStatus, composedHeader, composedBody := doRequest(composed, rr)
		apiStatus, apiHeader, apiBody := doRequest(apiMux, rr)

		if composedStatus != apiStatus {
			t.Fatalf("%s %s: status differs: composed=%d apiMux=%d",
				rr.method, rr.path, composedStatus, apiStatus)
		}

		// Drop the Date header before comparing: it is stamped per response
		// and differs by construction, not by routing.
		composedHeader.Del("Date")
		apiHeader.Del("Date")
		// Drop Content-Length too: it is derived from the raw body length, but
		// bodies are only compared after normalizing variable-length dynamic
		// fields (minted ids, stamped timestamps). A route whose two independent
		// responses differ only in the length of such a dynamic value would
		// diverge on Content-Length while the normalized bodies match, which is
		// not a routing-composition difference. The body equality check below
		// covers content; this property is about routing, not byte-identical
		// dynamic values.
		composedHeader.Del("Content-Length")
		apiHeader.Del("Content-Length")
		// The composed handler adds browser headers the bare API mux does not.
		// They are the same on every route, so they are not a routing difference.
		for _, name := range []string{
			"Cache-Control",
			"Content-Security-Policy",
			"Permissions-Policy",
			"Referrer-Policy",
			"X-Content-Type-Options",
			"X-Frame-Options",
		} {
			composedHeader.Del(name)
			apiHeader.Del(name)
		}
		if !headersEqual(composedHeader, apiHeader) {
			t.Fatalf("%s %s: headers differ: composed=%v apiMux=%v",
				rr.method, rr.path, composedHeader, apiHeader)
		}

		cb := normalizeBody(composedBody)
		ab := normalizeBody(apiBody)
		if !bytes.Equal(cb, ab) {
			t.Fatalf("%s %s: body differs: composed=%q apiMux=%q",
				rr.method, rr.path, cb, ab)
		}
	})
}

// doRequest sends rr to handler and returns the status, headers, and body.
func doRequest(handler http.Handler, rr registeredRequest) (int, http.Header, []byte) {
	var reqBody io.Reader
	if rr.body != "" {
		reqBody = strings.NewReader(rr.body)
	}
	req := httptest.NewRequest(rr.method, rr.path, reqBody)
	if rr.body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	res := w.Result()
	body, _ := io.ReadAll(res.Body)
	return res.StatusCode, res.Header, body
}

// headersEqual reports whether two header maps are equal.
func headersEqual(a, b http.Header) bool {
	if len(a) != len(b) {
		return false
	}
	for key, av := range a {
		bv, ok := b[key]
		if !ok || len(av) != len(bv) {
			return false
		}
		for i := range av {
			if av[i] != bv[i] {
				return false
			}
		}
	}
	return true
}
