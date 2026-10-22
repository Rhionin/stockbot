package server

import (
	"bytes"
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/cart"
	"github.com/Rhionin/stockbot/internal/product"
	"github.com/steinfletcher/apitest"
	jsonpath "github.com/steinfletcher/apitest-jsonpath"
)

// httpExchange represents a single HTTP request/response pair for declarative testing.
type httpExchange struct {
	method         string
	url            string // alias for path for consistency
	path           string
	query          map[string]string
	body           string
	expectedStatus int
	assertions     []assertion

	// expectedHeaders asserts that each named response header equals the given
	// value exactly. Optional; a nil map adds no assertions. The JSONPath-only
	// assertions field cannot express header checks such as Allow: GET, HEAD.
	expectedHeaders map[string]string

	// bodyContains asserts that the response body contains each substring.
	// Optional; a nil slice adds no assertions. This is what lets a case assert
	// "the response is the HTML document containing X", which JSONPath
	// assertions cannot express.
	bodyContains []string

	// bodyExcludes asserts that the response body does not contain each
	// substring. Use it to prove a secret was not echoed.
	bodyExcludes []string

	// headers are set on the request. Optional.
	headers map[string]string
}

// handlerTestCase defines a single HTTP handler test case for table-driven testing.
type handlerTestCase struct {
	name string

	// setup seeds database state before the primary request.
	setup func(env testEnv)

	// The primary HTTP exchange to test.
	httpExchange

	// afterRequest verifies behavior after the primary request.
	// Prefer exchanges() over direct DB queries.
	afterRequest func(env testEnv)

	// contributor, when set, is the Product Opener writer wired into the
	// handler. Nil keeps sharing local.
	contributor product.UpstreamContributor

	// options are passed to NewHandler after the test defaults.
	options []Option
}

// assertion wraps a JSONPath assertion for cleaner test tables.
// Set absent to assert that path is not present; value is ignored in that case.
type assertion struct {
	path   string
	value  interface{}
	absent bool
}

// fakeUpstream embeds *product.ExternalLookup and holds per-database fakes,
// letting tests express outcomes like "a hit from Open Products Facts, miss from others".
// Embedding the real ExternalLookup means a test that seeds one database and asserts a
// winner is exercising the shipped classifyFanOut and databasePrecedence, not test copies.
type fakeUpstream struct {
	*product.ExternalLookup
	databases map[product.ExternalSource]*fakeProductOpener
}

// Database returns the fake for a specific database, so a test can seed or inspect it.
func (f *fakeUpstream) Database(source product.ExternalSource) *fakeProductOpener {
	return f.databases[source]
}

// testEnv holds everything a setup or afterRequest callback needs.
// All fields share the same underlying database, so writes in setup are
// immediately visible to the handler and to afterRequest exchanges.
type testEnv struct {
	T             *testing.T
	DB            *sql.DB
	ProductStore  *product.Catalog
	Upstream      *fakeUpstream
	OpenFoodFacts *fakeProductOpener // alias for backward compatibility
	Refresher     *product.Refresher
	Clock         *fakeClock
	MissTTL       time.Duration // injected into LookupService, used by exchanges()
	Contributor   product.UpstreamContributor
	Res           *http.Response // populated only inside afterRequest callbacks
	Registry      *cart.Registry // the registry wired into the handler under test
	Options       []Option
}

// runHandlerTests executes a table of handler test cases.
func runHandlerTests(t *testing.T, tests []handlerTestCase) {
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler, env := setupTestWithContributor(t, tt.contributor, tt.options...)
			env.Options = tt.options

			if tt.setup != nil {
				tt.setup(env)
			}

			test := apitest.New().Handler(handler)
			req := buildRequest(test, tt.httpExchange)
			expect := buildExpectations(req, tt.httpExchange)

			var capturedRes *http.Response
			if tt.afterRequest != nil {
				expect = expect.Assert(func(res *http.Response, _ *http.Request) error {
					capturedRes = res
					return nil
				})
			}

			expect.End()

			if tt.afterRequest != nil && capturedRes != nil {
				env.Res = capturedRes
				tt.afterRequest(env)
			}
		})
	}
}

// executeExchange executes a single HTTP exchange against a handler.
func executeExchange(t *testing.T, handler http.Handler, ex httpExchange) {
	test := apitest.New().Handler(handler)
	req := buildRequest(test, ex)
	expect := buildExpectations(req, ex)
	expect.End()
}

// buildRequest builds an apitest request from an httpExchange.
func buildRequest(test *apitest.APITest, ex httpExchange) *apitest.Request {
	// Use url if provided, otherwise fall back to path
	requestPath := ex.path
	if ex.url != "" {
		requestPath = ex.url
	}

	var req *apitest.Request
	switch ex.method {
	case "GET":
		req = test.Get(requestPath)
	case "POST":
		req = test.Post(requestPath)
	case "PUT":
		req = test.Put(requestPath)
	case "PATCH":
		req = test.Patch(requestPath)
	case "DELETE":
		req = test.Delete(requestPath)
	default:
		panic("unsupported method: " + ex.method)
	}

	for key, val := range ex.query {
		req = req.Query(key, val)
	}

	if ex.body != "" {
		req = req.JSON(ex.body)
	}
	for key, val := range ex.headers {
		req = req.Header(key, val)
	}

	return req
}

// buildExpectations builds apitest expectations from an httpExchange.
func buildExpectations(req *apitest.Request, ex httpExchange) *apitest.Response {
	expect := req.Expect(nil).Status(ex.expectedStatus)

	for _, a := range ex.assertions {
		if a.absent {
			expect = expect.Assert(jsonpath.NotPresent(a.path))
			continue
		}
		expect = expect.Assert(jsonpath.Equal(a.path, a.value))
	}

	for name, value := range ex.expectedHeaders {
		name, value := name, value
		expect = expect.Assert(func(res *http.Response, _ *http.Request) error {
			if got := res.Header.Get(name); got != value {
				return fmt.Errorf("expected header %q to equal %q, got %q", name, value, got)
			}
			return nil
		})
	}

	if len(ex.bodyContains) > 0 {
		substrings := ex.bodyContains
		expect = expect.Assert(func(res *http.Response, _ *http.Request) error {
			b, err := io.ReadAll(res.Body)
			if err != nil {
				return fmt.Errorf("read response body: %w", err)
			}
			// Restore the body so any later assertions still see it.
			res.Body = io.NopCloser(bytes.NewReader(b))
			body := string(b)
			for _, want := range substrings {
				if !strings.Contains(body, want) {
					return fmt.Errorf("expected body to contain %q, got %q", want, body)
				}
			}
			return nil
		})
	}

	if len(ex.bodyExcludes) > 0 {
		forbidden := ex.bodyExcludes
		expect = expect.Assert(func(res *http.Response, _ *http.Request) error {
			b, err := io.ReadAll(res.Body)
			if err != nil {
				return fmt.Errorf("read response body: %w", err)
			}
			res.Body = io.NopCloser(bytes.NewReader(b))
			body := string(b)
			for _, secret := range forbidden {
				if strings.Contains(body, secret) {
					return fmt.Errorf("response body contains forbidden substring %q", secret)
				}
			}
			return nil
		})
	}

	return expect
}

// handlerFromEnv rebuilds the HTTP handler on the same database, registry, and
// refresher the primary request used, so a follow-up call sees that write.
func handlerFromEnv(env testEnv) http.Handler {
	// Reuse env.Refresher rather than building a fresh one: a second
	// Refresher would own a different WaitGroup and in-flight map, so
	// env.Refresher.Wait() would return without awaiting goroutines this
	// handler's exchanges started, and tests would flake silently.
	// Similarly, reuse env.Upstream and env.Clock to maintain the same state
	// across exchanges. Carry through env.MissTTL so miss-gate tests work
	// correctly: a missing MissTTL there silently defaults to zero and
	// makes every cached-miss assertion in an afterRequest sequence pass for
	// the wrong reason.
	var now func() time.Time
	if env.Clock != nil {
		now = env.Clock.Now
	}
	registry := env.Registry
	if registry == nil {
		registry = cart.NewRegistry()
	}
	opts := []Option{WithCartRegistry(registry, cart.NewLedger(env.DB))}
	if env.Contributor != nil {
		opts = append(opts, WithContributor(env.Contributor))
	}
	opts = append(opts, env.Options...)
	handler, _ := NewHandler(env.ProductStore, &product.LookupService{
		Catalog:   env.ProductStore,
		Upstream:  env.Upstream,
		Refresher: env.Refresher,
		Now:       now,
		MissTTL:   env.MissTTL,
	}, env.Refresher, env.DB, opts...)
	return handler
}

// exchanges returns an afterRequest callback that fires a sequence of HTTP requests
// against the same handler and database, verifying state through the HTTP contract.
func exchanges(exs ...httpExchange) func(env testEnv) {
	return func(env testEnv) {
		handler := handlerFromEnv(env)

		for i, ex := range exs {
			env.T.Run("", func(t *testing.T) {
				executeExchange(t, handler, ex)
			})

			if env.T.Failed() {
				env.T.Logf("exchange %d failed, stopping sequence", i)
				break
			}
		}
	}
}
