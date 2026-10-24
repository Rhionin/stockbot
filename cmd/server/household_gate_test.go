package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/server"
	"golang.org/x/crypto/bcrypt"
)

func TestLoadHouseholdGateReadsTheStoredPassword(t *testing.T) {
	conn, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := app.RunMigrations(conn); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(previous) })

	closed := loadHouseholdGate(conn)
	if closed == nil || closed.Configured {
		t.Fatal("missing hash was treated as configured")
	}
	if !strings.Contains(logs.String(), "no household password is stored") {
		t.Fatalf("log = %s", logs.String())
	}

	hash, err := bcrypt.GenerateFromPassword([]byte("sample-passphrase-1"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "auth.caddy")
	if err := os.WriteFile(path, []byte("keeper "+string(hash)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := server.ImportHouseholdPassword(context.Background(), conn, path, "sample-session-secret-0123456789abcd"); err != nil {
		t.Fatal(err)
	}
	logs.Reset()
	gate := loadHouseholdGate(conn)
	if gate == nil || !gate.Configured || gate.Username != "keeper" {
		t.Fatalf("gate %+v", gate)
	}
	if !strings.Contains(logs.String(), "signs in as keeper") {
		t.Fatalf("log = %s", logs.String())
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	again := loadHouseholdGate(conn)
	if again == nil || !again.Configured || again.Username != "keeper" {
		t.Fatal("gate still needed the password file")
	}
}

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

func authNotConfiguredFixtureCode(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate auth code fixture")
	}
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "testdata", "contract", "auth_not_configured.json"))
	if err != nil {
		t.Fatalf("read auth code fixture: %v", err)
	}
	var fixture struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatalf("parse auth code fixture: %v", err)
	}
	if fixture.Code == "" {
		t.Fatal("auth code fixture has an empty code")
	}
	return fixture.Code
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

	notConfigured := `{"error":"Sign-in isn't set up yet.","code":"` + authNotConfiguredFixtureCode(t) + `"}`
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
	if loginRec.Code != http.StatusNotFound || loginRec.Body.String() != `{"error":"Sign-in is not used on this address.","code":"not_found"}` {
		t.Fatalf("LAN login = %d %s", loginRec.Code, loginRec.Body.String())
	}
}
