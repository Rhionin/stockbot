package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Rhionin/stockbot/internal/clientmsg"
)

const browserAccept = "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8"

func TestBootGateMigratingPageAndJSON(t *testing.T) {
	gate := newBootGate()
	srv := httptest.NewServer(gate)
	t.Cleanup(srv.Close)

	cases := []struct {
		name   string
		path   string
		accept string
		html   bool
	}{
		{name: "root html", path: "/", accept: "text/html;q=0.9", html: true},
		{name: "deep web route", path: "/inventory/groups/milk", accept: browserAccept, html: true},
		{name: "api json", path: "/api/inventory", accept: "application/json", html: false},
		{name: "wildcard is not a page", path: "/health", accept: "*/*", html: false},
		{name: "browser accept on api", path: "/api/scans", accept: browserAccept, html: false},
		{name: "api root stays json", path: "/api", accept: "text/html", html: false},
		{name: "zero quality is not a page", path: "/", accept: "text/html;q=0,*/*;q=0.8", html: false},
		{name: "substring type is not a page", path: "/", accept: "nottext/html", html: false},
		{name: "bad quality is not a page", path: "/shopping", accept: "text/html;q=nope", html: false},
		{name: "apix is a page", path: "/apix", accept: "text/html", html: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := fetchAccept(t, srv.URL+tc.path, tc.accept)
			if tc.html {
				assertMigratingPage(t, got)
				return
			}
			assertMigratingJSON(t, got)
		})
	}

	gate.use(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, "<!DOCTYPE html><html><body>ready</body></html>")
	}))
	ready := fetchAccept(t, srv.URL+"/", browserAccept)
	if ready.code != http.StatusOK {
		t.Fatalf("after ready status = %d, want 200; body = %q", ready.code, ready.body)
	}
	if strings.Contains(ready.body, clientmsg.MigrationUpdating) || !strings.Contains(ready.body, "ready") {
		t.Fatalf("after ready body = %q, want the real page", ready.body)
	}
}

func TestBootGateMigratingAcceptAndMethods(t *testing.T) {
	gate := newBootGate()
	srv := httptest.NewServer(gate)
	t.Cleanup(srv.Close)

	noAccept := fetchRequest(t, http.MethodGet, srv.URL+"/", "", false)
	assertMigratingJSON(t, noAccept)

	head := fetchRequest(t, http.MethodHead, srv.URL+"/", "text/html", true)
	if head.code != http.StatusServiceUnavailable {
		t.Fatalf("HEAD status = %d, want 503", head.code)
	}
	if head.body != "" {
		t.Fatalf("HEAD body = %q, want none", head.body)
	}
	if head.header.Get("Content-Type") != "text/html; charset=utf-8" {
		t.Fatalf("HEAD Content-Type = %q", head.header.Get("Content-Type"))
	}
	if head.header.Get("Retry-After") != "10" {
		t.Fatalf("HEAD Retry-After = %q", head.header.Get("Retry-After"))
	}
	if head.header.Get("Cache-Control") != "no-store" {
		t.Fatalf("HEAD Cache-Control = %q", head.header.Get("Cache-Control"))
	}

	postPage := fetchRequest(t, http.MethodPost, srv.URL+"/shopping", "text/html", true)
	assertMigratingPage(t, postPage)

	postAPI := fetchRequest(t, http.MethodPost, srv.URL+"/api", "text/html", true)
	assertMigratingJSON(t, postAPI)
}

func assertMigratingPage(t *testing.T, got acceptResult) {
	t.Helper()
	if got.code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body = %q", got.code, got.body)
	}
	if got.header.Get("Content-Type") != "text/html; charset=utf-8" {
		t.Fatalf("Content-Type = %q", got.header.Get("Content-Type"))
	}
	if got.header.Get("Retry-After") != "10" {
		t.Fatalf("Retry-After = %q", got.header.Get("Retry-After"))
	}
	if got.header.Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q", got.header.Get("Cache-Control"))
	}
	if !strings.Contains(got.body, "Stockbot is updating. This takes about a minute.") {
		t.Fatalf("body missing the updating sentence: %s", got.body)
	}
	if !strings.Contains(got.body, `<meta http-equiv="refresh" content="10">`) {
		t.Fatalf("refresh tag must be exactly content=%q:\n%s", "10", got.body)
	}
	if !strings.Contains(got.body, "<title>Stockbot is updating. This takes about a minute.</title>") {
		t.Fatalf("title missing the updating sentence:\n%s", got.body)
	}
	for _, piece := range []string{
		`@media (prefers-color-scheme: dark)`,
		`#0d9488`,
		`max-width: 30rem`,
		`margin: 1.15rem auto 0`,
	} {
		if !strings.Contains(got.body, piece) {
			t.Fatalf("page missing %q:\n%s", piece, got.body)
		}
	}
	lower := strings.ToLower(got.body)
	for _, needle := range []string{`src=`, `href=`, `<script`, `<link`, `@import`, `url(`, `http://`, `https://`} {
		if strings.Contains(lower, needle) {
			t.Fatalf("page references an external asset via %q:\n%s", needle, got.body)
		}
	}
}

func assertMigratingJSON(t *testing.T, got acceptResult) {
	t.Helper()
	if got.code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body = %q", got.code, got.body)
	}
	if got.body != "{\"status\":\"migrating\"}\n" {
		t.Fatalf("body = %q, want the migrating JSON", got.body)
	}
	if got.header.Get("Content-Type") != "application/json" {
		t.Fatalf("Content-Type = %q", got.header.Get("Content-Type"))
	}
	if got.header.Get("Retry-After") != "10" {
		t.Fatalf("Retry-After = %q", got.header.Get("Retry-After"))
	}
	if got.header.Get("Cache-Control") != "" {
		t.Fatalf("Cache-Control = %q, want none on the JSON response", got.header.Get("Cache-Control"))
	}
}

type acceptResult struct {
	code   int
	body   string
	header http.Header
}

func fetchAccept(t *testing.T, rawURL, accept string) acceptResult {
	t.Helper()
	return fetchRequest(t, http.MethodGet, rawURL, accept, true)
}

func fetchRequest(t *testing.T, method, rawURL, accept string, withAccept bool) acceptResult {
	t.Helper()
	req, err := http.NewRequest(method, rawURL, nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if withAccept {
		req.Header.Set("Accept", accept)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, rawURL, err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read %s: %v", rawURL, err)
	}
	return acceptResult{code: res.StatusCode, body: string(body), header: res.Header}
}
