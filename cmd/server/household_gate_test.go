package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Rhionin/stockbot/internal/server"
)

func TestPublicEntryFailsClosedWithoutHouseholdLogin(t *testing.T) {
	t.Run("unset", func(t *testing.T) {
		t.Setenv("PANTRY_AUTH_FILE", "sentinel")
		os.Unsetenv("PANTRY_AUTH_FILE")
		assertPublicEntryRefused(t, loadHouseholdAuth())
	})
	t.Run("empty", func(t *testing.T) {
		t.Setenv("PANTRY_AUTH_FILE", "  ")
		assertPublicEntryRefused(t, loadHouseholdAuth())
	})
	t.Run("bad file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "household")
		if err := os.WriteFile(path, []byte("not a password file\n"), 0o600); err != nil {
			t.Fatalf("write password file: %v", err)
		}
		t.Setenv("PANTRY_AUTH_FILE", path)
		assertPublicEntryRefused(t, loadHouseholdAuth())
	})
}

func assertPublicEntryRefused(t *testing.T, gate *server.HouseholdAuth) {
	t.Helper()
	if gate == nil || gate.Configured || !gate.FailClosed {
		t.Fatalf("gate = %+v, want a fail-closed login", gate)
	}
	handler, _ := server.NewHandler(nil, nil, nil, nil, server.WithHouseholdAuth(gate))

	api := httptest.NewRequest(http.MethodGet, "/api/scans", nil)
	api.Header.Set("X-Pantry-Entry", "public")
	apiRec := httptest.NewRecorder()
	handler.ServeHTTP(apiRec, api)
	if apiRec.Code != http.StatusUnauthorized {
		t.Fatalf("public API = %d %s", apiRec.Code, apiRec.Body.String())
	}
	if apiRec.Header().Get("WWW-Authenticate") != "" {
		t.Fatal("public API set WWW-Authenticate")
	}
	if !strings.Contains(apiRec.Body.String(), "Sign in required.") {
		t.Fatalf("public API body = %s", apiRec.Body.String())
	}

	page := httptest.NewRequest(http.MethodGet, "/", nil)
	page.Header.Set("X-Pantry-Entry", "public")
	pageRec := httptest.NewRecorder()
	handler.ServeHTTP(pageRec, page)
	if pageRec.Code != http.StatusOK || !strings.Contains(pageRec.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("public page = %d %s", pageRec.Code, pageRec.Header().Get("Content-Type"))
	}

	const notConfigured = `{"error":"Sign-in isn't set up yet.","code":"auth_not_configured"}`
	login := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(`{"username":"ada","password":"synthetic-passphrase"}`))
	login.Header.Set("X-Pantry-Entry", "public")
	loginRec := httptest.NewRecorder()
	handler.ServeHTTP(loginRec, login)
	if loginRec.Code != http.StatusServiceUnavailable || loginRec.Body.String() != notConfigured {
		t.Fatalf("public login = %d %s", loginRec.Code, loginRec.Body.String())
	}

	session := httptest.NewRequest(http.MethodGet, "/api/session", nil)
	session.Header.Set("X-Pantry-Entry", "public")
	sessionRec := httptest.NewRecorder()
	handler.ServeHTTP(sessionRec, session)
	if sessionRec.Code != http.StatusServiceUnavailable || sessionRec.Body.String() != notConfigured {
		t.Fatalf("public session = %d %s", sessionRec.Code, sessionRec.Body.String())
	}

	lan := httptest.NewRequest(http.MethodGet, "/health", nil)
	lanRec := httptest.NewRecorder()
	handler.ServeHTTP(lanRec, lan)
	if lanRec.Code == http.StatusUnauthorized {
		t.Fatal("LAN /health asked for a login")
	}
}

func TestLANInstallWithoutPasswordFile(t *testing.T) {
	t.Setenv("PANTRY_AUTH_FILE", "sentinel")
	os.Unsetenv("PANTRY_AUTH_FILE")
	gate := loadHouseholdAuth()
	if gate == nil || gate.Configured || !gate.FailClosed {
		t.Fatalf("gate = %+v, want a fail-closed login", gate)
	}
	handler, _ := server.NewHandler(nil, nil, nil, nil, server.WithHouseholdAuth(gate))

	session := httptest.NewRequest(http.MethodGet, "/api/session", nil)
	session.RemoteAddr = "192.168.1.20:4000"
	sessionRec := httptest.NewRecorder()
	handler.ServeHTTP(sessionRec, session)
	if sessionRec.Code != http.StatusOK || sessionRec.Body.String() != `{"required":false}` {
		t.Fatalf("LAN session = %d %s", sessionRec.Code, sessionRec.Body.String())
	}

	login := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(`{"username":"ada","password":"synthetic-passphrase"}`))
	login.RemoteAddr = "192.168.1.20:4000"
	loginRec := httptest.NewRecorder()
	handler.ServeHTTP(loginRec, login)
	if loginRec.Code != http.StatusNotFound || loginRec.Body.String() != `{"error":"Sign-in is not used on this address."}` {
		t.Fatalf("LAN login = %d %s", loginRec.Code, loginRec.Body.String())
	}
}
