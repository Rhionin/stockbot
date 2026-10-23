package server

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"sort"
	"strings"
	"testing"
)

// routeDecision is one pattern's isolation category. A route is in exactly
// one category. check is set only for a real isolation case.
type routeDecision struct {
	category isoCategory
	kind     isoKind
	reason   string
	check    func(*testing.T, *twoHouseholds)
}

func routeDecisions() (map[string]routeDecision, error) {
	return assembleDecisions(publicAllowlist, opsAllowlist, isolationCases, quarantine)
}

func assembleDecisions(public, ops []allowEntry, isolation, quarantined map[string]isolationCase) (map[string]routeDecision, error) {
	out := map[string]routeDecision{}
	add := func(pattern string, d routeDecision) error {
		if pattern == "" {
			return fmt.Errorf("isolation case table has an empty pattern")
		}
		if d.reason == "" {
			return fmt.Errorf("route %q has an empty reason", pattern)
		}
		if prev, ok := out[pattern]; ok {
			return fmt.Errorf("duplicate isolation entry %q is both %s and %s", pattern, prev.category, d.category)
		}
		switch d.category {
		case catPublic, catOps:
			if d.kind != "" {
				return fmt.Errorf("allowlist route %q carries isolation kind %q", pattern, d.kind)
			}
			if d.check != nil {
				return fmt.Errorf("allowlist route %q has an isolation check; move it to isolationCases", pattern)
			}
		case catIsolation:
			if err := knownKind(pattern, d.kind); err != nil {
				return err
			}
			if d.check == nil {
				return fmt.Errorf("isolation case %q has no check", pattern)
			}
		case catQuarantine:
			if err := knownKind(pattern, d.kind); err != nil {
				return err
			}
			if d.check != nil {
				return fmt.Errorf("quarantine entry %q has a check; move it to isolationCases and lower maxQuarantined", pattern)
			}
		default:
			return fmt.Errorf("route %q has unknown category %q", pattern, d.category)
		}
		out[pattern] = d
		return nil
	}
	for _, entry := range public {
		if err := add(entry.pattern, routeDecision{category: catPublic, reason: entry.reason}); err != nil {
			return nil, err
		}
	}
	for _, entry := range ops {
		if err := add(entry.pattern, routeDecision{category: catOps, reason: entry.reason}); err != nil {
			return nil, err
		}
	}
	for pattern, entry := range isolation {
		if err := add(pattern, routeDecision{category: catIsolation, kind: entry.kind, reason: entry.reason, check: entry.check}); err != nil {
			return nil, err
		}
	}
	for pattern, entry := range quarantined {
		if err := add(pattern, routeDecision{category: catQuarantine, kind: entry.kind, reason: entry.reason, check: entry.check}); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func knownKind(pattern string, kind isoKind) error {
	switch kind {
	case kindForeignID, kindListLeak, kindWriteScope, kindScopedCollection:
		return nil
	default:
		return fmt.Errorf("route %q has unknown isolation kind %q", pattern, kind)
	}
}

// coverageProblems reports missing decisions, stale entries, public-path
// drift, and a quarantine list that is not exactly maxQuarantined.
func coverageProblems(routes servedRoutes, decisions map[string]routeDecision) []string {
	var problems []string
	seen := map[string]cataloguedRoute{}
	for _, route := range routes {
		if prev, ok := seen[route.pattern]; ok {
			problems = append(problems, fmt.Sprintf("duplicate registered route %q at %s and %s", route.pattern, prev.at, route.at))
			continue
		}
		seen[route.pattern] = route
		decision, ok := decisions[route.pattern]
		if !ok {
			problems = append(problems, fmt.Sprintf("unlisted route %q (%s) has no isolation-case decision", route.pattern, route.at))
			continue
		}
		if msg := publicDrift(route.pattern, decision); msg != "" {
			problems = append(problems, msg)
		}
	}
	for pattern := range decisions {
		if _, ok := seen[pattern]; !ok {
			problems = append(problems, fmt.Sprintf("stale isolation entry %q matches no registered route", pattern))
		}
	}
	quarantined := 0
	for _, decision := range decisions {
		if decision.category == catQuarantine {
			quarantined++
		}
	}
	switch {
	case quarantined > maxQuarantined:
		problems = append(problems, fmt.Sprintf("quarantine grew to %d routes; the ceiling is %d and it can only shrink. New routes need a public allowlist, ops allowlist, or real isolation case. Do not raise the ceiling.", quarantined, maxQuarantined))
	case quarantined < maxQuarantined:
		problems = append(problems, fmt.Sprintf("quarantine shrank to %d routes; lower maxQuarantined from %d to %d in this change so the ceiling cannot grow back", quarantined, maxQuarantined, quarantined))
	}
	sort.Strings(problems)
	return problems
}

func publicDrift(pattern string, decision routeDecision) string {
	req, err := requestForPattern(pattern)
	if err != nil {
		return fmt.Sprintf("%s: %v", pattern, err)
	}
	open := isPublicPath(req) || isAuthEndpoint(req)
	if open && decision.category != catPublic {
		return fmt.Sprintf("%s is open on the public entry (isPublicPath or isAuthEndpoint) but its case category is %s, not the public allowlist", pattern, decision.category)
	}
	if !open && decision.category == catPublic {
		return fmt.Sprintf("%s is on the public allowlist but isPublicPath and isAuthEndpoint are both false", pattern)
	}
	return ""
}

// publicAllowlistMatchesSource fails when the allowlist and the server's
// public-path functions list different paths. Calling the functions catches
// a registered route. Reading the function bodies catches a path that is
// public in source but has no route yet.
func publicAllowlistMatchesSource() error {
	surface, err := readPublicSurface()
	if err != nil {
		return err
	}
	allowPaths := map[string]struct{}{}
	allowAuth := map[string]struct{}{}
	for _, entry := range publicAllowlist {
		req, err := requestForPattern(entry.pattern)
		if err != nil {
			return fmt.Errorf("%s: %w", entry.pattern, err)
		}
		openPath := isPublicPath(req)
		auth := isAuthEndpoint(req)
		switch {
		case openPath && auth:
			return fmt.Errorf("%s matches both isPublicPath and isAuthEndpoint", entry.pattern)
		case openPath:
			allowPaths[req.URL.Path] = struct{}{}
		case auth:
			allowAuth[entry.pattern] = struct{}{}
		default:
			return fmt.Errorf("%s is on the public allowlist but isPublicPath and isAuthEndpoint are both false", entry.pattern)
		}
	}
	if msg := setDiff("isPublicPath", surface.paths, allowPaths); msg != "" {
		return fmt.Errorf("public allowlist drifted from isPublicPath: %s", msg)
	}
	if msg := setDiff("isAuthEndpoint", surface.auth, allowAuth); msg != "" {
		return fmt.Errorf("public allowlist drifted from isAuthEndpoint: %s", msg)
	}
	return nil
}

func setDiff(name string, source, allow map[string]struct{}) string {
	var onlySource, onlyAllow []string
	for item := range source {
		if _, ok := allow[item]; !ok {
			onlySource = append(onlySource, item)
		}
	}
	for item := range allow {
		if _, ok := source[item]; !ok {
			onlyAllow = append(onlyAllow, item)
		}
	}
	if len(onlySource) == 0 && len(onlyAllow) == 0 {
		return ""
	}
	sort.Strings(onlySource)
	sort.Strings(onlyAllow)
	return fmt.Sprintf("%s has %s, allowlist has %s", name, strings.Join(onlySource, ", "), strings.Join(onlyAllow, ", "))
}

func TestEveryRouteHasIsolationCase(t *testing.T) {
	routes, err := enumerateRoutes()
	if err != nil {
		t.Fatalf("cannot enumerate routes: %v", err)
	}
	// coverageProblems takes servedRoutes, the type only the live check
	// returns. Assigning the source inventory here does not compile.
	served, err := crossCheckLiveRouter(t, routes)
	if err != nil {
		t.Fatalf("live router: %v", err)
	}
	decisions, err := routeDecisions()
	if err != nil {
		t.Fatalf("isolation case table: %v", err)
	}
	if problems := coverageProblems(served, decisions); len(problems) > 0 {
		t.Fatalf("isolation coverage:\n%s", strings.Join(problems, "\n"))
	}
	if err := publicAllowlistMatchesSource(); err != nil {
		t.Fatalf("public allowlist: %v", err)
	}
	quarantined := 0
	for _, decision := range decisions {
		if decision.category == catQuarantine {
			quarantined++
		}
	}
	t.Logf("isolation coverage: %d routes, %d public, %d ops, %d isolation, %d quarantined (ceiling %d)",
		len(routes), len(publicAllowlist), len(opsAllowlist), len(isolationCases), quarantined, maxQuarantined)
}

func TestRouteInventoryRejectsNonLiteralPattern(t *testing.T) {
	const src = "package server\nfunc register() {\n\tapiMux.HandleFunc(routePattern, nil)\n}\n"
	fset, file := parseSnippet(t, "register.go", src)
	_, _, err := collectRegistrations(fset, file, map[string]string{})
	if err == nil || !strings.Contains(err.Error(), "not a string literal or string constant") {
		t.Fatalf("error = %v, want a non-literal pattern failure", err)
	}
}

func TestCoverageCheckRejectsUnlistedRoute(t *testing.T) {
	const probe = "GET /api/isolation-probe"
	// Registration goes through a struct field and a helper. An identifier
	// walk of HandleFunc would not see either shape. The pattern that
	// reaches the case table is the one the mux serves.
	mux := http.NewServeMux()
	type holder struct{ m *http.ServeMux }
	registerIsolationProbe(holder{m: mux}.m, probe)

	served, err := patternsServed(mux)
	if err != nil {
		t.Fatalf("list test router: %v", err)
	}
	var found []cataloguedRoute
	for pattern := range served {
		found = append(found, cataloguedRoute{pattern: pattern, at: "struct field"})
	}
	if len(found) != 1 || found[0].pattern != probe {
		t.Fatalf("test router serves %v, want %s", found, probe)
	}

	routes, decisions := coverageBaseline(t)
	problems := coverageProblems(append(servedRoutes(routes), found...), decisions)
	if !problemContains(problems, `unlisted route "`+probe+`" (struct field) has no isolation-case decision`) {
		t.Fatalf("problems = %v, want an unlisted-route failure", problems)
	}
}

// registerIsolationProbe is the helper a route can hide behind. The caller
// passes the mux through a struct field.
func registerIsolationProbe(mux *http.ServeMux, pattern string) {
	mux.HandleFunc(pattern, func(http.ResponseWriter, *http.Request) {})
}

func TestLiveRouterCheckRunsBothWays(t *testing.T) {
	const probe = "GET /api/isolation-probe"
	mux := http.NewServeMux()
	type holder struct{ m *http.ServeMux }
	registerIsolationProbe(holder{m: mux}.m, probe)

	_, err := crossCheckMuxes(nil, mux, http.NewServeMux())
	if err == nil || !strings.Contains(err.Error(), `unlisted route "`+probe+`"`) {
		t.Fatalf("route served but not inventoried: err = %v", err)
	}

	_, err = crossCheckMuxes(
		[]cataloguedRoute{{pattern: "GET /health", receiver: "apiMux", at: "test"}},
		http.NewServeMux(),
		http.NewServeMux(),
	)
	if err == nil || !strings.Contains(err.Error(), "GET /health") || !strings.Contains(err.Error(), "does not serve") {
		t.Fatalf("inventoried route the router does not serve: err = %v", err)
	}
}

func TestRouteInventoryRejectsHiddenRegistration(t *testing.T) {
	const fieldSrc = "package server\n" +
		"type holder struct{ m *http.ServeMux }\n" +
		"func register(h holder) { h.m.HandleFunc(\"GET /api/isolation-probe\", nil) }\n"
	fset, file := parseSnippet(t, "field.go", fieldSrc)
	_, _, err := collectRegistrations(fset, file, map[string]string{})
	if err == nil || !strings.Contains(err.Error(), "not a plain router variable") {
		t.Fatalf("struct-field registration err = %v", err)
	}

	const valueSrc = "package server\n" +
		"func register() {\n" +
		"\tf := apiMux.HandleFunc\n" +
		"\tf(\"GET /api/isolation-probe\", nil)\n" +
		"}\n"
	fset, file = parseSnippet(t, "value.go", valueSrc)
	_, _, err = collectRegistrations(fset, file, map[string]string{})
	if err == nil || !strings.Contains(err.Error(), "method value") {
		t.Fatalf("method-value registration err = %v", err)
	}
}

func TestRouteInventoryRejectsRootHandoff(t *testing.T) {
	// brand.Register and Middleware are the only calls allowed to take root.
	// other.Mount is a helper in another package. Routes it registered on
	// root would never reach the live check.
	const src = "package server\n" +
		"func register() {\n" +
		"\tbrand.Register(root)\n" +
		"\thook.Middleware(root)\n" +
		"\tother.Mount(root)\n" +
		"}\n"
	fset, file := parseSnippet(t, "handoff.go", src)
	_, _, err := collectRegistrations(fset, file, map[string]string{})
	if err == nil || !strings.Contains(err.Error(), "root mux passed to other.Mount") {
		t.Fatalf("root handoff err = %v", err)
	}
}

func TestPublicDrift(t *testing.T) {
	open := routeDecision{category: catQuarantine, kind: kindListLeak, reason: waitsOnHouseholdScope}
	if msg := publicDrift("GET /api/build", open); !strings.Contains(msg, "open on the public entry") {
		t.Fatalf("public route classified as quarantine: %q", msg)
	}
	closed := routeDecision{category: catPublic, reason: "should not be public"}
	if msg := publicDrift("GET /health", closed); !strings.Contains(msg, "public allowlist") {
		t.Fatalf("ops route classified as public: %q", msg)
	}
	if msg := publicDrift("GET /api/products", open); msg != "" {
		t.Fatalf("quarantined route reported drift: %q", msg)
	}
	matching := routeDecision{category: catPublic, reason: "build identity"}
	if msg := publicDrift("GET /api/build", matching); msg != "" {
		t.Fatalf("public route in the public allowlist reported drift: %q", msg)
	}
}

func TestCoverageCheckRejectsDeletedCase(t *testing.T) {
	const dropped = "GET /api/telemetry"
	routes, decisions := coverageBaseline(t)
	if _, ok := decisions[dropped]; !ok {
		t.Fatalf("case table has no %s entry to delete", dropped)
	}
	delete(decisions, dropped)
	problems := coverageProblems(servedRoutes(routes), decisions)
	if !problemContains(problems, `unlisted route "`+dropped+`"`) {
		t.Fatalf("problems = %v, want an unlisted-route failure for the deleted case", problems)
	}
}

func TestCoverageCheckRejectsStaleAndGrowth(t *testing.T) {
	routes, decisions := coverageBaseline(t)

	stale := cloneDecisions(decisions)
	stale["GET /api/not-a-route"] = routeDecision{category: catOps, reason: "not registered"}
	if !problemContains(coverageProblems(servedRoutes(routes), stale), `stale isolation entry "GET /api/not-a-route"`) {
		t.Fatal("stale entry was accepted")
	}

	const extra = "GET /api/isolation-probe-extra"
	grownRoutes := append(append([]cataloguedRoute{}, routes...), cataloguedRoute{pattern: extra, at: "test-only router"})
	grown := cloneDecisions(decisions)
	grown[extra] = routeDecision{category: catQuarantine, kind: kindListLeak, reason: waitsOnHouseholdScope}
	if !problemContains(coverageProblems(servedRoutes(grownRoutes), grown), "quarantine grew") {
		t.Fatal("quarantine growth was accepted")
	}

	dup, err := assembleDecisions(
		[]allowEntry{{pattern: "GET /health", reason: "ops"}, {pattern: "GET /health", reason: "again"}},
		nil, nil, nil,
	)
	if err == nil || !strings.Contains(err.Error(), "duplicate isolation entry") {
		t.Fatalf("duplicate entry err = %v, decisions = %d", err, len(dup))
	}
}

func coverageBaseline(t *testing.T) ([]cataloguedRoute, map[string]routeDecision) {
	t.Helper()
	routes, err := enumerateRoutes()
	if err != nil {
		t.Fatalf("cannot enumerate routes: %v", err)
	}
	decisions, err := routeDecisions()
	if err != nil {
		t.Fatalf("isolation case table: %v", err)
	}
	if problems := coverageProblems(servedRoutes(routes), decisions); len(problems) > 0 {
		t.Fatalf("baseline coverage is not green:\n%s", strings.Join(problems, "\n"))
	}
	return routes, cloneDecisions(decisions)
}

func cloneDecisions(in map[string]routeDecision) map[string]routeDecision {
	out := make(map[string]routeDecision, len(in))
	for pattern, decision := range in {
		out[pattern] = decision
	}
	return out
}

func problemContains(problems []string, needle string) bool {
	for _, problem := range problems {
		if strings.Contains(problem, needle) {
			return true
		}
	}
	return false
}

func parseSnippet(t *testing.T, name, src string) (*token.FileSet, *ast.File) {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, name, src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse snippet: %v", err)
	}
	return fset, file
}
