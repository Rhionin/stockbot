package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"
)

// TestCrossHouseholdIsolation runs every case that is not quarantined.
// Today that is the public allowlist and the ops allowlist. Each response
// must contain no marker from either invented household. GET /api/telemetry
// is one of those routes: its snapshot is process-wide and must not carry
// household ids or names. Quarantine waits on #377 and is not requested.
func TestCrossHouseholdIsolation(t *testing.T) {
	if len(publicAllowlist)+len(opsAllowlist)+len(isolationCases) == 0 {
		t.Fatal("isolation suite has no cases to run")
	}
	fx := newTwoHouseholds(t)
	for _, entry := range publicAllowlist {
		t.Run(entry.pattern, func(t *testing.T) {
			assertAllowlistHasNoHouseholdData(t, fx, entry.pattern)
		})
	}
	for _, entry := range opsAllowlist {
		t.Run(entry.pattern, func(t *testing.T) {
			assertAllowlistHasNoHouseholdData(t, fx, entry.pattern)
		})
	}
	for pattern, entry := range isolationCases {
		t.Run(pattern, func(t *testing.T) {
			if entry.check == nil {
				t.Fatalf("isolation case %s has no check", pattern)
			}
			entry.check(t, fx)
		})
	}
}

func assertAllowlistHasNoHouseholdData(t *testing.T, fx *twoHouseholds, pattern string) {
	t.Helper()
	req, err := requestForPattern(pattern)
	if err != nil {
		t.Fatalf("request %s: %v", pattern, err)
	}
	// Alder is the caller. Birch is the other household. Public and ops
	// bodies carry neither household's data, so both are checked.
	fx.principalFor(fx.Alder).Apply(req)
	rec := httptest.NewRecorder()
	fx.Handler.ServeHTTP(rec, req)
	body, err := io.ReadAll(rec.Result().Body)
	if err != nil {
		t.Fatalf("read %s: %v", pattern, err)
	}
	assertReachedAllowlistRoute(t, pattern, rec.Code, rec.Header(), body)
	assertNoLeak(t, body, fx.Birch)
	assertNoLeak(t, body, fx.Alder)
}

func assertReachedAllowlistRoute(t *testing.T, pattern string, code int, header http.Header, body []byte) {
	t.Helper()
	switch pattern {
	case "GET /api/telemetry":
		if code != http.StatusOK {
			t.Fatalf("GET /api/telemetry status = %d, body %s", code, snippet(body))
		}
		var snap struct {
			Status string `json:"status"`
		}
		if err := json.Unmarshal(body, &snap); err != nil {
			t.Fatalf("GET /api/telemetry body: %v", err)
		}
		if snap.Status != "ok" {
			t.Fatalf("GET /api/telemetry status field = %q, want ok", snap.Status)
		}
	case "GET /health":
		if code != http.StatusOK {
			t.Fatalf("GET /health status = %d, body %s", code, snippet(body))
		}
		if !strings.Contains(string(body), `"status":"ok"`) {
			t.Fatalf("GET /health body = %s", snippet(body))
		}
	case "GET /api/build", "GET /api/session", "GET /terms", "GET /privacy", "GET /brand/logo.png":
		expectStatus(t, pattern, code, body, http.StatusOK)
	case "POST /api/login":
		expectStatus(t, pattern, code, body, http.StatusNotFound)
		expectBody(t, pattern, body, "Sign-in is not used on this address.")
	case "POST /api/logout":
		expectStatus(t, pattern, code, body, http.StatusOK)
		if strings.TrimSpace(string(body)) != "{}" {
			t.Fatalf("POST /api/logout body = %s", snippet(body))
		}
	case "POST /api/telemetry/client":
		expectStatus(t, pattern, code, body, http.StatusBadRequest)
		expectBody(t, pattern, body, "invalid report")
	case "POST /api/deploy-hook":
		expectStatus(t, pattern, code, body, http.StatusServiceUnavailable)
		expectBody(t, pattern, body, "deploy hook is not configured")
	case "/api/sentry-tunnel":
		expectStatus(t, pattern, code, body, http.StatusNotFound)
		expectBody(t, pattern, body, "Not found.")
	default:
		t.Fatalf("%s has no status check, so the leak assertion may be looking at an error page", pattern)
	}
	if pattern == "GET /brand/logo.png" && !strings.HasPrefix(header.Get("Content-Type"), "image/png") {
		t.Fatalf("GET /brand/logo.png content-type = %q", header.Get("Content-Type"))
	}
}

func expectStatus(t *testing.T, pattern string, code int, body []byte, want int) {
	t.Helper()
	if code != want {
		t.Fatalf("%s status = %d, want %d, body %s", pattern, code, want, snippet(body))
	}
}

func expectBody(t *testing.T, pattern string, body []byte, needle string) {
	t.Helper()
	if !strings.Contains(string(body), needle) {
		t.Fatalf("%s body = %s, want %q", pattern, snippet(body), needle)
	}
}

func snippet(body []byte) string {
	if !utf8.Valid(body) {
		return fmt.Sprintf("%d bytes", len(body))
	}
	const max = 180
	if len(body) > max {
		return string(body[:max]) + "…"
	}
	return string(body)
}
