package server

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/Rhionin/stockbot/internal/brand"
	"github.com/Rhionin/stockbot/internal/product"
)

// compositionMounts are the root mux prefixes. They are how the API mux,
// health, and the SPA catch-all are attached. They are not household routes.
// A root registration outside this set fails the inventory: a household
// route mounted only there would skip the case table.
var compositionMounts = map[string]struct{}{
	"/api":     {},
	"/api/":    {},
	"/health":  {},
	"/health/": {},
	"/":        {},
}

// cataloguedRoute is one pattern read from a Handle or HandleFunc call.
type cataloguedRoute struct {
	pattern  string
	receiver string
	at       string
}

// enumerateRoutes reads route registrations from the server and brand
// sources. Go's ServeMux cannot list its routes, and exporting a registrar
// would be a production change, so the inventory is the source. A pattern
// that is not a string literal or a string constant fails here.
func enumerateRoutes() ([]cataloguedRoute, error) {
	serverDir, brandDir, err := sourceDirs()
	if err != nil {
		return nil, err
	}
	fset := token.NewFileSet()
	serverFiles, err := parseNonTest(fset, serverDir)
	if err != nil {
		return nil, fmt.Errorf("cannot enumerate routes in %s: %w", serverDir, err)
	}
	brandFiles, err := parseNonTest(fset, brandDir)
	if err != nil {
		return nil, fmt.Errorf("cannot enumerate routes in %s: %w", brandDir, err)
	}

	var routes []cataloguedRoute
	mounts := map[string]string{}
	groups := []struct {
		files  []*ast.File
		consts map[string]string
	}{
		{serverFiles, stringConsts(serverFiles)},
		{brandFiles, stringConsts(brandFiles)},
	}
	for _, g := range groups {
		for _, file := range g.files {
			found, seenMounts, err := collectRegistrations(fset, file, g.consts)
			if err != nil {
				return nil, err
			}
			routes = append(routes, found...)
			for pattern, at := range seenMounts {
				if prev, ok := mounts[pattern]; ok {
					return nil, fmt.Errorf("duplicate composition mount %q at %s and %s", pattern, prev, at)
				}
				mounts[pattern] = at
			}
		}
	}

	seen := map[string]string{}
	for _, route := range routes {
		if prev, ok := seen[route.pattern]; ok {
			return nil, fmt.Errorf("duplicate registered route %q at %s and %s", route.pattern, prev, route.at)
		}
		seen[route.pattern] = route.at
	}
	for pattern := range compositionMounts {
		if _, ok := mounts[pattern]; !ok {
			return nil, fmt.Errorf("composition mount %q is not registered; the root mux changed and the inventory cannot see where that route went", pattern)
		}
	}
	if len(mounts) != len(compositionMounts) {
		return nil, fmt.Errorf("root mux registers %d composition mounts, the closed set has %d", len(mounts), len(compositionMounts))
	}
	return routes, nil
}

func sourceDirs() (serverDir, brandDir string, err error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", "", fmt.Errorf("cannot locate the server package to enumerate routes")
	}
	serverDir = filepath.Dir(file)
	brandDir = filepath.Join(serverDir, "..", "brand")
	return serverDir, brandDir, nil
}

func parseNonTest(fset *token.FileSet, dir string) ([]*ast.File, error) {
	pkgs, err := parser.ParseDir(fset, dir, func(info os.FileInfo) bool {
		name := info.Name()
		return strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go")
	}, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	if len(pkgs) != 1 {
		return nil, fmt.Errorf("found %d packages", len(pkgs))
	}
	var files []*ast.File
	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			files = append(files, file)
		}
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no Go files")
	}
	return files, nil
}

func stringConsts(files []*ast.File) map[string]string {
	out := map[string]string{}
	for _, file := range files {
		for _, decl := range file.Decls {
			group, ok := decl.(*ast.GenDecl)
			if !ok || group.Tok != token.CONST {
				continue
			}
			for _, spec := range group.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, name := range vs.Names {
					if i >= len(vs.Values) {
						break
					}
					lit, ok := vs.Values[i].(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						continue
					}
					s, err := strconv.Unquote(lit.Value)
					if err != nil {
						continue
					}
					out[name.Name] = s
				}
			}
		}
	}
	return out
}

func collectRegistrations(fset *token.FileSet, file *ast.File, consts map[string]string) ([]cataloguedRoute, map[string]string, error) {
	var routes []cataloguedRoute
	mounts := map[string]string{}
	var err error
	ast.Inspect(file, func(n ast.Node) bool {
		if err != nil || n == nil {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		recv, ok := handleReceiver(call)
		if !ok {
			return true
		}
		at := formatPos(fset, call.Pos())
		if recv != "apiMux" && recv != "mux" && recv != "root" {
			err = fmt.Errorf("%s: Handle on %s is not a known router (apiMux, mux, or root); the inventory cannot see this route", at, recv)
			return false
		}
		if len(call.Args) < 1 {
			err = fmt.Errorf("%s: Handle has no pattern; the inventory cannot see this route", at)
			return false
		}
		pattern, perr := patternText(call.Args[0], consts)
		if perr != nil {
			err = fmt.Errorf("%s: %w", formatPos(fset, call.Args[0].Pos()), perr)
			return false
		}
		if recv == "root" {
			if _, known := compositionMounts[pattern]; !known {
				err = fmt.Errorf("%s: root registers %q, which is not a composition mount. Add an isolation-case decision; a household route on the root mux would otherwise skip the case table", at, pattern)
				return false
			}
			if prev, ok := mounts[pattern]; ok {
				err = fmt.Errorf("duplicate composition mount %q at %s and %s", pattern, prev, at)
				return false
			}
			mounts[pattern] = at
			return true
		}
		if _, _, bad := concretePath(pattern); bad != nil {
			err = fmt.Errorf("%s: %w", at, bad)
			return false
		}
		routes = append(routes, cataloguedRoute{pattern: pattern, receiver: recv, at: at})
		return true
	})
	return routes, mounts, err
}

func handleReceiver(call *ast.CallExpr) (string, bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || (sel.Sel.Name != "Handle" && sel.Sel.Name != "HandleFunc") {
		return "", false
	}
	id, ok := sel.X.(*ast.Ident)
	if !ok {
		return "", false
	}
	return id.Name, true
}

func patternText(expr ast.Expr, consts map[string]string) (string, error) {
	switch v := expr.(type) {
	case *ast.BasicLit:
		if v.Kind != token.STRING {
			return "", fmt.Errorf("route pattern is not a string literal or string constant")
		}
		s, err := strconv.Unquote(v.Value)
		if err != nil {
			return "", fmt.Errorf("route pattern is not a string literal or string constant: %w", err)
		}
		return s, nil
	case *ast.Ident:
		s, ok := consts[v.Name]
		if !ok {
			return "", fmt.Errorf("route pattern %s is not a string literal or string constant", v.Name)
		}
		return s, nil
	default:
		return "", fmt.Errorf("route pattern is not a string literal or string constant")
	}
}

func formatPos(fset *token.FileSet, pos token.Pos) string {
	p := fset.Position(pos)
	return fmt.Sprintf("%s:%d", filepath.Base(p.Filename), p.Line)
}

// concretePath fills each {name} wildcard with one segment so the request
// matches that pattern and not a longer one. A wildcard the filler does not
// understand fails the inventory instead of matching the wrong route.
func concretePath(pattern string) (method, path string, err error) {
	// Method-less registrations, such as /api/sentry-tunnel, accept every
	// verb. POST is the verb that route is for.
	method = http.MethodPost
	path = pattern
	if sp := strings.IndexByte(pattern, ' '); sp >= 0 {
		method = pattern[:sp]
		path = pattern[sp+1:]
		if !validMethod(method) {
			return "", "", fmt.Errorf("pattern %q has a method the inventory cannot request", pattern)
		}
	}
	if !strings.HasPrefix(path, "/") {
		return "", "", fmt.Errorf("pattern %q has no path", pattern)
	}
	var b strings.Builder
	for i := 0; i < len(path); i++ {
		if path[i] != '{' {
			b.WriteByte(path[i])
			continue
		}
		rel := strings.IndexByte(path[i:], '}')
		if rel < 2 {
			return "", "", fmt.Errorf("pattern %q has a wildcard the inventory cannot fill", pattern)
		}
		name := path[i+1 : i+rel]
		if !validWildcard(name) {
			return "", "", fmt.Errorf("pattern %q has wildcard %q the inventory cannot fill", pattern, name)
		}
		b.WriteString("seg")
		i += rel
	}
	return method, b.String(), nil
}

func validMethod(method string) bool {
	if method == "" {
		return false
	}
	for _, c := range method {
		if c < 'A' || c > 'Z' {
			return false
		}
	}
	return true
}

func validWildcard(name string) bool {
	if name == "" || strings.Contains(name, "...") || strings.ContainsAny(name, "/{}$") {
		return false
	}
	for _, c := range name {
		letter := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
		digit := c >= '0' && c <= '9'
		if !letter && !digit {
			return false
		}
	}
	return true
}

func requestForPattern(pattern string) (*http.Request, error) {
	method, path, err := concretePath(pattern)
	if err != nil {
		return nil, err
	}
	return httptest.NewRequest(method, path, nil), nil
}

// crossCheckLiveRouter requests every catalogued pattern on the mux that
// registers it. mux.Handler must return that same pattern.
func crossCheckLiveRouter(t *testing.T, routes []cataloguedRoute) error {
	t.Helper()
	db := setupTestDB(t)
	apiMux, _, _ := newAPIMux(product.NewCatalog(db), nil, nil, db, nil)
	brandMux := http.NewServeMux()
	brand.Register(brandMux)

	for _, route := range routes {
		var mux *http.ServeMux
		switch route.receiver {
		case "apiMux":
			mux = apiMux
		case "mux":
			mux = brandMux
		default:
			return fmt.Errorf("%s (%s) is not on a mux the inventory can cross-check", route.pattern, route.at)
		}
		req, err := requestForPattern(route.pattern)
		if err != nil {
			return fmt.Errorf("%s (%s): %w", route.pattern, route.at, err)
		}
		_, got := mux.Handler(req)
		if got != route.pattern {
			return fmt.Errorf("%s (%s): live router matched %q", route.pattern, route.at, got)
		}
	}
	return nil
}

// publicSurface is the path set inside isPublicPath and the method patterns
// inside isAuthEndpoint, read from the function bodies.
type publicSurface struct {
	paths map[string]struct{}
	auth  map[string]struct{}
}

func readPublicSurface() (publicSurface, error) {
	serverDir, _, err := sourceDirs()
	if err != nil {
		return publicSurface{}, err
	}
	fset := token.NewFileSet()
	files, err := parseNonTest(fset, serverDir)
	if err != nil {
		return publicSurface{}, fmt.Errorf("cannot read public paths: %w", err)
	}
	consts := stringConsts(files)
	paths, err := publicPathsFrom(findFunc(files, "isPublicPath"), consts)
	if err != nil {
		return publicSurface{}, err
	}
	auth, err := authPatternsFrom(findFunc(files, "isAuthEndpoint"), consts)
	if err != nil {
		return publicSurface{}, err
	}
	return publicSurface{paths: paths, auth: auth}, nil
}

func findFunc(files []*ast.File, name string) *ast.FuncDecl {
	for _, file := range files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if ok && fn.Recv == nil && fn.Name.Name == name {
				return fn
			}
		}
	}
	return nil
}

func publicPathsFrom(fn *ast.FuncDecl, consts map[string]string) (map[string]struct{}, error) {
	sw, err := pathSwitch(fn)
	if err != nil {
		return nil, err
	}
	paths := map[string]struct{}{}
	sawDefault := false
	for _, stmt := range sw.Body.List {
		clause, ok := stmt.(*ast.CaseClause)
		if !ok {
			return nil, fmt.Errorf("%s switch is not a list of cases", fn.Name.Name)
		}
		kind, _, err := classifyReturn(clause.Body)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", fn.Name.Name, err)
		}
		if clause.List == nil {
			sawDefault = true
			if kind != resultFalse {
				return nil, fmt.Errorf("%s default is not return false; the public allowlist cannot be cross-checked", fn.Name.Name)
			}
			continue
		}
		if kind != resultTrue {
			return nil, fmt.Errorf("%s case is not return true; the public allowlist cannot be cross-checked", fn.Name.Name)
		}
		for _, expr := range clause.List {
			path, err := patternText(expr, consts)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", fn.Name.Name, err)
			}
			paths[path] = struct{}{}
		}
	}
	if !sawDefault {
		return nil, fmt.Errorf("%s has no default; the public allowlist cannot be cross-checked", fn.Name.Name)
	}
	return paths, nil
}

func authPatternsFrom(fn *ast.FuncDecl, consts map[string]string) (map[string]struct{}, error) {
	sw, err := pathSwitch(fn)
	if err != nil {
		return nil, err
	}
	patterns := map[string]struct{}{}
	sawDefault := false
	for _, stmt := range sw.Body.List {
		clause, ok := stmt.(*ast.CaseClause)
		if !ok {
			return nil, fmt.Errorf("%s switch is not a list of cases", fn.Name.Name)
		}
		kind, method, err := classifyReturn(clause.Body)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", fn.Name.Name, err)
		}
		if clause.List == nil {
			sawDefault = true
			if kind != resultFalse {
				return nil, fmt.Errorf("%s default is not return false; the public allowlist cannot be cross-checked", fn.Name.Name)
			}
			continue
		}
		if kind != resultMethod {
			return nil, fmt.Errorf("%s case does not compare r.Method; the public allowlist cannot be cross-checked", fn.Name.Name)
		}
		for _, expr := range clause.List {
			path, err := patternText(expr, consts)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", fn.Name.Name, err)
			}
			patterns[method+" "+path] = struct{}{}
		}
	}
	if !sawDefault {
		return nil, fmt.Errorf("%s has no default; the public allowlist cannot be cross-checked", fn.Name.Name)
	}
	return patterns, nil
}

func pathSwitch(fn *ast.FuncDecl) (*ast.SwitchStmt, error) {
	if fn == nil || fn.Body == nil {
		return nil, fmt.Errorf("public-path function is missing; the public allowlist cannot be cross-checked")
	}
	if len(fn.Body.List) != 1 {
		return nil, fmt.Errorf("%s is not only a switch on r.URL.Path; the public allowlist cannot be cross-checked", fn.Name.Name)
	}
	sw, ok := fn.Body.List[0].(*ast.SwitchStmt)
	if !ok || !isURLPathSwitch(sw.Tag) {
		return nil, fmt.Errorf("%s is not a switch on r.URL.Path; the public allowlist cannot be cross-checked", fn.Name.Name)
	}
	return sw, nil
}

func isURLPathSwitch(tag ast.Expr) bool {
	path, ok := tag.(*ast.SelectorExpr)
	if !ok || path.Sel.Name != "Path" {
		return false
	}
	url, ok := path.X.(*ast.SelectorExpr)
	if !ok || url.Sel.Name != "URL" {
		return false
	}
	recv, ok := url.X.(*ast.Ident)
	return ok && recv.Name == "r"
}

type returnKind int

const (
	resultFalse returnKind = iota
	resultTrue
	resultMethod
)

var httpMethodConsts = map[string]string{
	"MethodGet":     http.MethodGet,
	"MethodHead":    http.MethodHead,
	"MethodPost":    http.MethodPost,
	"MethodPut":     http.MethodPut,
	"MethodPatch":   http.MethodPatch,
	"MethodDelete":  http.MethodDelete,
	"MethodConnect": http.MethodConnect,
	"MethodOptions": http.MethodOptions,
	"MethodTrace":   http.MethodTrace,
}

func classifyReturn(body []ast.Stmt) (returnKind, string, error) {
	if len(body) != 1 {
		return 0, "", fmt.Errorf("case is not a single return")
	}
	ret, ok := body[0].(*ast.ReturnStmt)
	if !ok || len(ret.Results) != 1 {
		return 0, "", fmt.Errorf("case is not a single return")
	}
	switch v := ret.Results[0].(type) {
	case *ast.Ident:
		switch v.Name {
		case "true":
			return resultTrue, "", nil
		case "false":
			return resultFalse, "", nil
		default:
			return 0, "", fmt.Errorf("case returns %s", v.Name)
		}
	case *ast.BinaryExpr:
		if v.Op != token.EQL {
			return 0, "", fmt.Errorf("case return is not a method comparison")
		}
		recv, ok := v.X.(*ast.SelectorExpr)
		if !ok || recv.Sel.Name != "Method" {
			return 0, "", fmt.Errorf("case return does not compare r.Method")
		}
		if id, ok := recv.X.(*ast.Ident); !ok || id.Name != "r" {
			return 0, "", fmt.Errorf("case return does not compare r.Method")
		}
		sel, ok := v.Y.(*ast.SelectorExpr)
		if !ok {
			return 0, "", fmt.Errorf("case return does not compare r.Method to an http method")
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok || pkg.Name != "http" {
			return 0, "", fmt.Errorf("case return does not compare r.Method to an http method")
		}
		method, ok := httpMethodConsts[sel.Sel.Name]
		if !ok {
			return 0, "", fmt.Errorf("unknown method constant http.%s", sel.Sel.Name)
		}
		return resultMethod, method, nil
	default:
		return 0, "", fmt.Errorf("case return is not a boolean or a method comparison")
	}
}
