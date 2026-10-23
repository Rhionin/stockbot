package server

import (
	"crypto/tls"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func testHousehold(t *testing.T, user, password string) *HouseholdAuth {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "household")
	body := "basic_auth bcrypt Pantry {\n\t" + user + " " + string(hash) + "\n}\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write hash: %v", err)
	}
	secret := strings.Repeat("s", 32)
	gate, err := LoadHouseholdAuth(path, secret)
	if err != nil {
		t.Fatalf("load household auth: %v", err)
	}
	if !gate.Configured || gate.Username != user {
		t.Fatalf("configured=%v user=%q", gate.Configured, gate.Username)
	}
	return gate
}

func publicHandler(gate *HouseholdAuth) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/session", gate.ServeSession)
	mux.HandleFunc("POST /api/login", gate.ServeLogin)
	mux.HandleFunc("POST /api/logout", gate.ServeLogout)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, "<!doctype html><title>Pantry</title>")
	})
	return gate.Middleware(mux)
}

func withPublicEntry(req *http.Request) {
	req.Header.Set(entryHeader, entryPublic)
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set(clientIPHeader, "203.0.113.8")
	req.RemoteAddr = "10.0.0.8:1234"
}

func TestHouseholdAuthPublicPathsAndLAN(t *testing.T) {
	gate := testHousehold(t, "pantry", "correct-horse-battery")
	handler := publicHandler(gate)

	public := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/telemetry"},
		{http.MethodPost, "/api/telemetry/client"},
		{http.MethodGet, "/api/build"},
		{http.MethodGet, "/brand/logo.png"},
		{http.MethodGet, "/terms"},
		{http.MethodGet, "/privacy"},
		{http.MethodPost, "/api/deploy-hook"},
		{http.MethodPost, "/api/sentry-tunnel"},
	}
	for _, tc := range public {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		withPublicEntry(req)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("%s %s = %d, want 200 without a session", tc.method, tc.path, rec.Code)
		}
	}

	closed := []string{
		"/api/providers/kroger/authorize",
		"/api/providers/kroger/callback",
		"/api/providers/kroger/callback/extra",
		"/api/providers//callback",
		"/api/telemetry/other",
		"/api/build/extra",
		"/api/scans",
		"/health",
	}
	for _, path := range closed {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		withPublicEntry(req)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("GET %s = %d, want 401", path, rec.Code)
		}
		if rec.Header().Get("WWW-Authenticate") != "" {
			t.Errorf("GET %s set WWW-Authenticate, which opens the browser password prompt", path)
		}
		if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
			t.Errorf("GET %s content-type = %q", path, ct)
		}
		if !strings.Contains(rec.Body.String(), "Sign in required.") {
			t.Errorf("GET %s body = %s", path, rec.Body.String())
		}
	}

	// Port 8080 does not send the public-entry header.
	lan := httptest.NewRequest(http.MethodGet, "/api/scans", nil)
	lan.RemoteAddr = "192.168.1.20:4000"
	lanRec := httptest.NewRecorder()
	handler.ServeHTTP(lanRec, lan)
	if lanRec.Code != http.StatusOK {
		t.Fatalf("LAN GET /api/scans = %d, want 200 with no login", lanRec.Code)
	}

	page := httptest.NewRequest(http.MethodGet, "/inventory", nil)
	withPublicEntry(page)
	page.Header.Set("Accept", "text/html")
	pageRec := httptest.NewRecorder()
	handler.ServeHTTP(pageRec, page)
	if pageRec.Code != http.StatusOK {
		t.Fatalf("page load = %d, want the login document", pageRec.Code)
	}
	if !strings.Contains(pageRec.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("page content-type = %q", pageRec.Header().Get("Content-Type"))
	}
	if !strings.Contains(pageRec.Body.String(), "<title>Pantry</title>") {
		t.Fatalf("page body = %s", pageRec.Body.String())
	}
}

func TestHouseholdAuthCookieAndBasic(t *testing.T) {
	const password = "correct-horse-battery"
	gate := testHousehold(t, "pantry", password)
	now := time.Date(2026, 10, 8, 15, 0, 0, 0, time.UTC)
	gate.now = func() time.Time { return now }
	handler := publicHandler(gate)

	login := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(`{"username":"pantry","password":"correct-horse-battery"}`))
	withPublicEntry(login)
	login.Header.Set("Content-Type", "application/json")
	loginRec := httptest.NewRecorder()
	handler.ServeHTTP(loginRec, login)
	if loginRec.Code != http.StatusOK {
		t.Fatalf("login = %d %s", loginRec.Code, loginRec.Body.String())
	}
	setCookie := loginRec.Header().Get("Set-Cookie")
	for _, flag := range []string{"HttpOnly", "Secure", "SameSite=Lax", "pantry_session="} {
		if !strings.Contains(setCookie, flag) {
			t.Errorf("Set-Cookie %q missing %s", setCookie, flag)
		}
	}
	cookies := loginRec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies = %d", len(cookies))
	}
	cookie := cookies[0]
	if !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("cookie httpOnly=%v secure=%v sameSite=%v", cookie.HttpOnly, cookie.Secure, cookie.SameSite)
	}
	if cookie.Path != "/" {
		t.Fatalf("cookie path = %q", cookie.Path)
	}
	if cookie.MaxAge < int((90 * 24 * time.Hour).Seconds()) {
		t.Fatalf("cookie max-age = %d, want a long phone session", cookie.MaxAge)
	}

	authed := httptest.NewRequest(http.MethodGet, "/api/scans", nil)
	withPublicEntry(authed)
	authed.AddCookie(cookie)
	authedRec := httptest.NewRecorder()
	handler.ServeHTTP(authedRec, authed)
	if authedRec.Code != http.StatusOK {
		t.Fatalf("cookie request = %d", authedRec.Code)
	}

	session := httptest.NewRequest(http.MethodGet, "/api/session", nil)
	withPublicEntry(session)
	session.AddCookie(cookie)
	sessionRec := httptest.NewRecorder()
	handler.ServeHTTP(sessionRec, session)
	if sessionRec.Code != http.StatusOK || !strings.Contains(sessionRec.Body.String(), `"username":"pantry"`) {
		t.Fatalf("session = %d %s", sessionRec.Code, sessionRec.Body.String())
	}

	tampered := httptest.NewRequest(http.MethodGet, "/api/scans", nil)
	withPublicEntry(tampered)
	tampered.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie.Value + "x"})
	tamperedRec := httptest.NewRecorder()
	handler.ServeHTTP(tamperedRec, tampered)
	if tamperedRec.Code != http.StatusUnauthorized {
		t.Fatalf("tampered cookie = %d", tamperedRec.Code)
	}

	now = now.Add(sessionLifetime + time.Minute)
	expired := httptest.NewRequest(http.MethodGet, "/api/scans", nil)
	withPublicEntry(expired)
	expired.AddCookie(cookie)
	expiredRec := httptest.NewRecorder()
	handler.ServeHTTP(expiredRec, expired)
	if expiredRec.Code != http.StatusUnauthorized {
		t.Fatalf("expired cookie = %d", expiredRec.Code)
	}

	plain := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(`{"username":"pantry","password":"correct-horse-battery"}`))
	plain.Header.Set(entryHeader, entryPublic)
	plain.Header.Set("X-Forwarded-Proto", "http")
	plain.Header.Set(clientIPHeader, "203.0.113.9")
	plain.RemoteAddr = "10.0.0.9:1234"
	plainRec := httptest.NewRecorder()
	handler.ServeHTTP(plainRec, plain)
	if plainRec.Code != http.StatusOK {
		t.Fatalf("http login = %d %s", plainRec.Code, plainRec.Body.String())
	}
	plainCookies := plainRec.Result().Cookies()
	if len(plainCookies) != 1 || !plainCookies[0].Secure {
		t.Fatalf("public-site cookie must be Secure even when the proxy connection is http: %s", plainRec.Header().Get("Set-Cookie"))
	}

	basic := httptest.NewRequest(http.MethodGet, "/api/scans", nil)
	withPublicEntry(basic)
	basic.SetBasicAuth("pantry", password)
	basicRec := httptest.NewRecorder()
	handler.ServeHTTP(basicRec, basic)
	if basicRec.Code != http.StatusOK {
		t.Fatalf("basic auth = %d %s", basicRec.Code, basicRec.Body.String())
	}

	wrong := httptest.NewRequest(http.MethodGet, "/api/scans", nil)
	withPublicEntry(wrong)
	wrong.Header.Set(clientIPHeader, "203.0.113.10")
	wrong.SetBasicAuth("pantry", "not-the-password")
	wrongRec := httptest.NewRecorder()
	handler.ServeHTTP(wrongRec, wrong)
	if wrongRec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong basic = %d", wrongRec.Code)
	}

	logout := httptest.NewRequest(http.MethodPost, "/api/logout", nil)
	withPublicEntry(logout)
	logout.AddCookie(cookie)
	logoutRec := httptest.NewRecorder()
	handler.ServeHTTP(logoutRec, logout)
	if logoutRec.Code != http.StatusOK {
		t.Fatalf("logout = %d", logoutRec.Code)
	}
	cleared := logoutRec.Header().Get("Set-Cookie")
	if !strings.Contains(cleared, "pantry_session=") || !strings.Contains(cleared, "Max-Age=0") {
		t.Fatalf("logout cookie = %s", cleared)
	}
}

func TestHouseholdAuthRateLimit(t *testing.T) {
	gate := testHousehold(t, "pantry", "correct-horse-battery")
	now := time.Date(2026, 10, 8, 15, 0, 0, 0, time.UTC)
	gate.now = func() time.Time { return now }
	handler := publicHandler(gate)

	for i := 0; i < maxAuthFailures; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(`{"username":"pantry","password":"wrong-password-value"}`))
		withPublicEntry(req)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d = %d %s", i+1, rec.Code, rec.Body.String())
		}
	}

	locked := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(`{"username":"pantry","password":"correct-horse-battery"}`))
	withPublicEntry(locked)
	lockedRec := httptest.NewRecorder()
	handler.ServeHTTP(lockedRec, locked)
	if lockedRec.Code != http.StatusTooManyRequests {
		t.Fatalf("locked login = %d %s", lockedRec.Code, lockedRec.Body.String())
	}
	if lockedRec.Header().Get("Retry-After") == "" {
		t.Fatal("locked login missing Retry-After")
	}
	if _, err := strconv.Atoi(lockedRec.Header().Get("Retry-After")); err != nil {
		t.Fatalf("Retry-After = %q", lockedRec.Header().Get("Retry-After"))
	}
	if !strings.Contains(lockedRec.Body.String(), "Too many sign-in attempts") {
		t.Fatalf("locked body = %s", lockedRec.Body.String())
	}

	// A correct Basic header is held to the same limit.
	basic := httptest.NewRequest(http.MethodGet, "/api/scans", nil)
	withPublicEntry(basic)
	basic.SetBasicAuth("pantry", "correct-horse-battery")
	basicRec := httptest.NewRecorder()
	handler.ServeHTTP(basicRec, basic)
	if basicRec.Code != http.StatusTooManyRequests {
		t.Fatalf("locked basic = %d", basicRec.Code)
	}

	open := httptest.NewRequest(http.MethodGet, "/api/telemetry", nil)
	withPublicEntry(open)
	openRec := httptest.NewRecorder()
	handler.ServeHTTP(openRec, open)
	if openRec.Code != http.StatusOK {
		t.Fatalf("public path during lockout = %d", openRec.Code)
	}

	// Another address is not paused, and knowing the username does not lock the household.
	other := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(`{"username":"pantry","password":"correct-horse-battery"}`))
	other.Header.Set(entryHeader, entryPublic)
	other.Header.Set(clientIPHeader, "203.0.113.40")
	other.RemoteAddr = "10.0.0.40:1234"
	otherRec := httptest.NewRecorder()
	handler.ServeHTTP(otherRec, other)
	if otherRec.Code != http.StatusOK {
		t.Fatalf("login from another address = %d %s", otherRec.Code, otherRec.Body.String())
	}
	otherCookies := otherRec.Result().Cookies()
	if len(otherCookies) != 1 {
		t.Fatalf("other address cookies = %d", len(otherCookies))
	}

	// The paused address can still use a session it already has.
	kept := httptest.NewRequest(http.MethodGet, "/api/scans", nil)
	withPublicEntry(kept)
	kept.AddCookie(otherCookies[0])
	keptRec := httptest.NewRecorder()
	handler.ServeHTTP(keptRec, kept)
	if keptRec.Code != http.StatusOK {
		t.Fatalf("session cookie while the address is paused = %d", keptRec.Code)
	}

	now = now.Add(authFailureWindow + time.Minute)
	again := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(`{"username":"pantry","password":"correct-horse-battery"}`))
	withPublicEntry(again)
	againRec := httptest.NewRecorder()
	handler.ServeHTTP(againRec, again)
	if againRec.Code != http.StatusOK {
		t.Fatalf("login after the window = %d %s", againRec.Code, againRec.Body.String())
	}
}

func TestHouseholdAuthFailClosedWithoutHash(t *testing.T) {
	gate, err := LoadHouseholdAuth(filepath.Join(t.TempDir(), "missing"), "")
	if err == nil {
		t.Fatal("missing password file returned no error")
	}
	if gate == nil || gate.Configured || !gate.FailClosed {
		t.Fatalf("gate = %+v", gate)
	}
	handler := publicHandler(gate)

	api := httptest.NewRequest(http.MethodGet, "/api/scans", nil)
	withPublicEntry(api)
	apiRec := httptest.NewRecorder()
	handler.ServeHTTP(apiRec, api)
	if apiRec.Code != http.StatusUnauthorized {
		t.Fatalf("missing hash API = %d", apiRec.Code)
	}

	page := httptest.NewRequest(http.MethodGet, "/", nil)
	withPublicEntry(page)
	pageRec := httptest.NewRecorder()
	handler.ServeHTTP(pageRec, page)
	if pageRec.Code != http.StatusOK {
		t.Fatalf("missing hash page = %d, want the login document", pageRec.Code)
	}

	telemetry := httptest.NewRequest(http.MethodGet, "/api/telemetry", nil)
	withPublicEntry(telemetry)
	telemetryRec := httptest.NewRecorder()
	handler.ServeHTTP(telemetryRec, telemetry)
	if telemetryRec.Code != http.StatusOK {
		t.Fatalf("telemetry = %d", telemetryRec.Code)
	}

	build := httptest.NewRequest(http.MethodGet, "/api/build", nil)
	withPublicEntry(build)
	buildRec := httptest.NewRecorder()
	handler.ServeHTTP(buildRec, build)
	if buildRec.Code != http.StatusOK {
		t.Fatalf("build = %d", buildRec.Code)
	}

	lan := httptest.NewRequest(http.MethodGet, "/api/scans", nil)
	lanRec := httptest.NewRecorder()
	handler.ServeHTTP(lanRec, lan)
	if lanRec.Code != http.StatusOK {
		t.Fatalf("LAN with a missing hash = %d", lanRec.Code)
	}
}

func TestHouseholdAuthCredentialEdges(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "household")
	write := func(body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("write hash: %v", err)
		}
	}

	write("not a caddy file\n")
	if _, err := LoadHouseholdAuth(path, strings.Repeat("s", 32)); err == nil {
		t.Fatal("a file with no bcrypt hash loaded")
	}
	write("basic_auth bcrypt Pantry {\n\t1pantry $2a$04$abcdefghijklmnopqrstuuabcdefghijklmnopqrstuvwxyz012\n}\n")
	if _, err := LoadHouseholdAuth(path, strings.Repeat("s", 32)); err == nil {
		t.Fatal("a username that does not start with a letter loaded")
	}
	write("basic_auth bcrypt Pantry {\n\tpantry $2a$not-a-real-hash\n}\n")
	if _, err := LoadHouseholdAuth(path, strings.Repeat("s", 32)); err == nil {
		t.Fatal("a non-bcrypt hash loaded")
	}
	if _, ok := sessionKey("short"); ok {
		t.Fatal("a short session secret was accepted")
	}

	hash, err := bcrypt.GenerateFromPassword([]byte("correct-horse-battery"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	write("basic_auth bcrypt Pantry {\n\tpantry " + string(hash) + "\n}\n")
	gate, err := LoadHouseholdAuth(path, "  ")
	if err == nil || gate == nil || !gate.Configured {
		t.Fatalf("missing secret err=%v configured=%v", err, gate != nil && gate.Configured)
	}
	handler := publicHandler(gate)

	login := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(`{"username":"pantry","password":"correct-horse-battery"}`))
	login.Header.Set(entryHeader, entryPublic)
	login.Header.Set("X-Forwarded-Proto", "https, http")
	login.TLS = &tls.ConnectionState{}
	login.RemoteAddr = "192.0.2.10:9"
	loginRec := httptest.NewRecorder()
	handler.ServeHTTP(loginRec, login)
	if loginRec.Code != http.StatusOK {
		t.Fatalf("login with an ephemeral secret = %d %s", loginRec.Code, loginRec.Body.String())
	}
	if !strings.Contains(loginRec.Header().Get("Set-Cookie"), "Secure") {
		t.Fatalf("https cookie = %s", loginRec.Header().Get("Set-Cookie"))
	}

	lanLogin := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(`{}`))
	lanRec := httptest.NewRecorder()
	handler.ServeHTTP(lanRec, lanLogin)
	if lanRec.Code != http.StatusNotFound {
		t.Fatalf("LAN login = %d %s", lanRec.Code, lanRec.Body.String())
	}

	bad := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(`{`))
	bad.Header.Set(entryHeader, entryPublic)
	bad.RemoteAddr = "192.0.2.11:9"
	badRec := httptest.NewRecorder()
	handler.ServeHTTP(badRec, bad)
	if badRec.Code != http.StatusBadRequest {
		t.Fatalf("bad login body = %d %s", badRec.Code, badRec.Body.String())
	}

	session := httptest.NewRequest(http.MethodGet, "/api/session", nil)
	session.Header.Set(entryHeader, entryPublic)
	session.SetBasicAuth("pantry", "correct-horse-battery")
	session.RemoteAddr = "192.0.2.12:9"
	sessionRec := httptest.NewRecorder()
	handler.ServeHTTP(sessionRec, session)
	if sessionRec.Code != http.StatusOK || !strings.Contains(sessionRec.Body.String(), `"username":"pantry"`) {
		t.Fatalf("basic session = %d %s", sessionRec.Code, sessionRec.Body.String())
	}

	forged := httptest.NewRequest(http.MethodGet, "/api/scans", nil)
	forged.Header.Set(entryHeader, entryPublic)
	forged.Header.Set(clientIPHeader, "203.0.113.8, 198.51.100.2")
	forged.RemoteAddr = "192.0.2.13:9"
	forged.Header.Set("Authorization", "Basic "+base64.URLEncoding.EncodeToString([]byte("pantry:correct-horse-battery")))
	forgedRec := httptest.NewRecorder()
	handler.ServeHTTP(forgedRec, forged)
	if forgedRec.Code != http.StatusOK {
		t.Fatalf("basic auth with a forged client IP = %d", forgedRec.Code)
	}

	broken := httptest.NewRequest(http.MethodGet, "/api/scans", nil)
	broken.Header.Set(entryHeader, entryPublic)
	broken.Header.Set("Authorization", "Basic !!!")
	broken.RemoteAddr = "192.0.2.14:9"
	brokenRec := httptest.NewRecorder()
	handler.ServeHTTP(brokenRec, broken)
	if brokenRec.Code != http.StatusUnauthorized {
		t.Fatalf("malformed basic = %d", brokenRec.Code)
	}

	head := httptest.NewRequest(http.MethodHead, "/inventory", nil)
	head.Header.Set(entryHeader, entryPublic)
	headRec := httptest.NewRecorder()
	handler.ServeHTTP(headRec, head)
	if headRec.Code != http.StatusOK {
		t.Fatalf("HEAD page = %d", headRec.Code)
	}

	postPage := httptest.NewRequest(http.MethodPost, "/inventory", nil)
	postPage.Header.Set(entryHeader, entryPublic)
	postPageRec := httptest.NewRecorder()
	handler.ServeHTTP(postPageRec, postPage)
	if postPageRec.Code != http.StatusUnauthorized {
		t.Fatalf("POST page = %d", postPageRec.Code)
	}

	callback := httptest.NewRequest(http.MethodPost, "/api/providers/kroger/callback", nil)
	callback.Header.Set(entryHeader, entryPublic)
	callbackRec := httptest.NewRecorder()
	handler.ServeHTTP(callbackRec, callback)
	if callbackRec.Code != http.StatusUnauthorized {
		t.Fatalf("POST callback = %d", callbackRec.Code)
	}

	odd := httptest.NewRequest(http.MethodGet, "/api/providers/kro.ger/callback", nil)
	odd.Header.Set(entryHeader, entryPublic)
	oddRec := httptest.NewRecorder()
	handler.ServeHTTP(oddRec, odd)
	if oddRec.Code != http.StatusUnauthorized {
		t.Fatalf("odd provider id = %d", oddRec.Code)
	}

	closed, err := LoadHouseholdAuth(filepath.Join(t.TempDir(), "missing"), "")
	if err == nil {
		t.Fatal("missing file returned no error")
	}
	closedHandler := publicHandler(closed)
	unavailable := httptest.NewRequest(http.MethodGet, "/api/session", nil)
	unavailable.Header.Set(entryHeader, entryPublic)
	unavailableRec := httptest.NewRecorder()
	closedHandler.ServeHTTP(unavailableRec, unavailable)
	assertSignInNotConfigured(t, unavailableRec)
	closedLogin := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(`{"username":"ada","password":"synthetic-passphrase"}`))
	closedLogin.Header.Set(entryHeader, entryPublic)
	closedLogin.Header.Set("Content-Type", "application/json")
	closedLoginRec := httptest.NewRecorder()
	closedHandler.ServeHTTP(closedLoginRec, closedLogin)
	assertSignInNotConfigured(t, closedLoginRec)

	wired, _ := NewHandler(nil, nil, nil, nil, WithHouseholdAuth(gate))
	wiredReq := httptest.NewRequest(http.MethodGet, "/api/session", nil)
	wiredReq.Header.Set(entryHeader, entryPublic)
	wiredReq.SetBasicAuth("pantry", "correct-horse-battery")
	wiredRec := httptest.NewRecorder()
	wired.ServeHTTP(wiredRec, wiredReq)
	if wiredRec.Code != http.StatusOK || !strings.Contains(wiredRec.Body.String(), `"required":true`) {
		t.Fatalf("wired session = %d %s", wiredRec.Code, wiredRec.Body.String())
	}
}

func TestGETLoginIsMethodNotAllowed(t *testing.T) {
	handler, _ := NewHandler(nil, nil, nil, nil, WithHouseholdAuth(FailClosedHouseholdAuth()))
	req := httptest.NewRequest(http.MethodGet, "/api/login", nil)
	req.RemoteAddr = "192.168.1.20:4000"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed || rec.Body.String() != "Method Not Allowed\n" {
		t.Fatalf("GET /api/login = %d %q", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Allow") != "POST" {
		t.Fatalf("Allow = %q", rec.Header().Get("Allow"))
	}
}

func TestPublicLoginNotConfiguredSkipsBody(t *testing.T) {
	handler, _ := NewHandler(nil, nil, nil, nil, WithHouseholdAuth(FailClosedHouseholdAuth()))
	req := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader("not-json"))
	withPublicEntry(req)
	req.Header.Set("Content-Type", "text/plain")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	assertSignInNotConfigured(t, rec)
}

func TestLogoutWithoutPasswordFile(t *testing.T) {
	handler, _ := NewHandler(nil, nil, nil, nil, WithHouseholdAuth(FailClosedHouseholdAuth()))

	lan := httptest.NewRequest(http.MethodPost, "/api/logout", nil)
	lan.RemoteAddr = "192.168.1.20:4000"
	lanRec := httptest.NewRecorder()
	handler.ServeHTTP(lanRec, lan)
	if lanRec.Code != http.StatusOK || lanRec.Body.String() != "{}" {
		t.Fatalf("LAN logout = %d %s", lanRec.Code, lanRec.Body.String())
	}
	const cleared = "pantry_session=; Path=/; Expires=Thu, 01 Jan 1970 00:00:00 GMT; Max-Age=0; HttpOnly; SameSite=Lax"
	if lanRec.Header().Get("Set-Cookie") != cleared {
		t.Fatalf("LAN logout Set-Cookie = %q", lanRec.Header().Get("Set-Cookie"))
	}

	pub := httptest.NewRequest(http.MethodPost, "/api/logout", nil)
	withPublicEntry(pub)
	pubRec := httptest.NewRecorder()
	handler.ServeHTTP(pubRec, pub)
	if pubRec.Code != http.StatusUnauthorized || pubRec.Body.String() != `{"error":"Sign in required."}` {
		t.Fatalf("public logout = %d %s", pubRec.Code, pubRec.Body.String())
	}
	if pubRec.Header().Get("Set-Cookie") != "" {
		t.Fatalf("public logout Set-Cookie = %q", pubRec.Header().Get("Set-Cookie"))
	}
}

func TestSignInNotConfiguredErrorCode(t *testing.T) {
	handler, _ := NewHandler(nil, nil, nil, nil, WithHouseholdAuth(FailClosedHouseholdAuth()))

	session := httptest.NewRequest(http.MethodGet, "/api/session", nil)
	withPublicEntry(session)
	sessionRec := httptest.NewRecorder()
	handler.ServeHTTP(sessionRec, session)
	assertSignInNotConfigured(t, sessionRec)

	login := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(`{"username":"ada","password":"synthetic-passphrase"}`))
	withPublicEntry(login)
	login.Header.Set("Content-Type", "application/json")
	loginRec := httptest.NewRecorder()
	handler.ServeHTTP(loginRec, login)
	assertSignInNotConfigured(t, loginRec)
}

func assertSignInNotConfigured(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	const want = `{"error":"Sign-in isn't set up yet.","code":"auth_not_configured"}`
	if rec.Code != http.StatusServiceUnavailable || rec.Body.String() != want {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("content-type = %q", rec.Header().Get("Content-Type"))
	}
	if rec.Header().Get("Retry-After") != "" {
		t.Fatalf("Retry-After = %q", rec.Header().Get("Retry-After"))
	}
}

func TestSessionOpenWhenAuthIsNotConfigured(t *testing.T) {
	handler := publicHandler(nil)
	req := httptest.NewRequest(http.MethodGet, "/api/session", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"required":false`) {
		t.Fatalf("session = %d %s", rec.Code, rec.Body.String())
	}
}

// TestRequestHTTPSSessionCookie checks the Secure flag on a real Set-Cookie.
// A public-hostname login is always Secure: cookieSecure returns before
// requestHTTPS so a tunnel that reports X-Forwarded-Proto: http cannot drop
// the flag. requestHTTPS itself is what a session cookie uses when that
// short-circuit does not apply, which logout does.
func TestRequestHTTPSSessionCookie(t *testing.T) {
	gate := testHousehold(t, "pantry", "correct-horse-battery")
	handler, _ := NewHandler(nil, nil, nil, nil, WithHouseholdAuth(gate))
	plain := httptest.NewServer(handler)
	t.Cleanup(plain.Close)
	tlsSrv := httptest.NewTLSServer(handler)
	t.Cleanup(tlsSrv.Close)

	const loginBody = `{"username":"pantry","password":"correct-horse-battery"}`
	tests := []struct {
		name       string
		base       *httptest.Server
		client     *http.Client
		path       string
		body       string
		headers    map[string]string
		wantStatus int
		wantSecure bool
	}{
		{
			name: "public login over plain http is still Secure",
			base: plain, client: plain.Client(), path: "/api/login", body: loginBody,
			headers: map[string]string{entryHeader: entryPublic}, wantStatus: http.StatusOK, wantSecure: true,
		},
		{
			name: "public login with X-Forwarded-Proto https is Secure",
			base: plain, client: plain.Client(), path: "/api/login", body: loginBody,
			headers:    map[string]string{entryHeader: entryPublic, "X-Forwarded-Proto": "https"},
			wantStatus: http.StatusOK, wantSecure: true,
		},
		{
			name: "public login over TLS is Secure",
			base: tlsSrv, client: tlsSrv.Client(), path: "/api/login", body: loginBody,
			headers: map[string]string{entryHeader: entryPublic}, wantStatus: http.StatusOK, wantSecure: true,
		},
		{
			name: "plain http without a forwarded proto is not Secure",
			base: plain, client: plain.Client(), path: "/api/logout",
			wantStatus: http.StatusOK, wantSecure: false,
		},
		{
			name: "X-Forwarded-Proto http is not Secure",
			base: plain, client: plain.Client(), path: "/api/logout",
			headers: map[string]string{"X-Forwarded-Proto": "http, https"}, wantStatus: http.StatusOK, wantSecure: false,
		},
		{
			name: "X-Forwarded-Proto https sets Secure",
			base: plain, client: plain.Client(), path: "/api/logout",
			headers: map[string]string{"X-Forwarded-Proto": " HTTPS , http"}, wantStatus: http.StatusOK, wantSecure: true,
		},
		{
			name: "direct TLS sets Secure even when the proto header says http",
			base: tlsSrv, client: tlsSrv.Client(), path: "/api/logout",
			headers: map[string]string{"X-Forwarded-Proto": "http"}, wantStatus: http.StatusOK, wantSecure: true,
		},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodPost, tt.base.URL+tt.path, strings.NewReader(tt.body))
			if err != nil {
				t.Fatalf("request: %v", err)
			}
			req.Header.Set(clientIPHeader, "203.0.113."+strconv.Itoa(20+i))
			for key, val := range tt.headers {
				req.Header.Set(key, val)
			}
			res, err := tt.client.Do(req)
			if err != nil {
				t.Fatalf("do: %v", err)
			}
			defer res.Body.Close()
			if res.StatusCode != tt.wantStatus {
				b, _ := io.ReadAll(res.Body)
				t.Fatalf("status %d: %s", res.StatusCode, b)
			}
			cookies := res.Cookies()
			if len(cookies) != 1 || cookies[0].Name != sessionCookie {
				t.Fatalf("cookies = %+v", cookies)
			}
			raw := res.Header.Get("Set-Cookie")
			if cookies[0].Secure != tt.wantSecure || strings.Contains(raw, "; Secure") != tt.wantSecure {
				t.Fatalf("Secure = %v, Set-Cookie %q", cookies[0].Secure, raw)
			}
		})
	}
}
