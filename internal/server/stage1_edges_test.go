package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/Rhionin/stockbot/internal/events"
	"golang.org/x/crypto/bcrypt"
)

func TestPantryCookieAndStage1Edges(t *testing.T) {
	now := time.Now()
	secret := []byte("secret")
	good := signPayload(secret, "pantry", now.Add(time.Hour))
	if !pantryCookieOK(secret, "pantry", now, good) {
		t.Fatal("valid cookie")
	}
	if pantryCookieOK(nil, "pantry", now, good) || pantryCookieOK(secret, "", now, good) || pantryCookieOK(secret, "pantry", now, "") {
		t.Fatal("empty cookie material was accepted")
	}
	if pantryCookieOK(secret, "pantry", now, "nodot") || pantryCookieOK(secret, "pantry", now, "@@@.@@@") {
		t.Fatal("malformed cookie was accepted")
	}
	forged := signPayload([]byte("other"), "pantry", now.Add(time.Hour))
	if pantryCookieOK(secret, "pantry", now, forged) {
		t.Fatal("forged cookie was accepted")
	}
	otherUser := signPayload(secret, "someone", now.Add(time.Hour))
	if pantryCookieOK(secret, "pantry", now, otherUser) {
		t.Fatal("other user was accepted")
	}
	expired := signPayload(secret, "pantry", now.Add(-time.Hour))
	if pantryCookieOK(secret, "pantry", now, expired) {
		t.Fatal("expired cookie was accepted")
	}
	broken := signPayload(secret, "pantry", now.Add(time.Hour))
	if pantryCookieOK(secret, "pantry", now, broken+"x") {
		t.Fatal("tampered mac was accepted")
	}

	db := openMigratedDB(t, filepath.Join(t.TempDir(), "pantry.db"))
	setupDir := t.TempDir()
	svc := auth.NewService(db, setupDir)
	site := stage1Site(t)
	handler, _ := setupOnDB(t, db, nil, WithStage1(svc, site))
	if err := svc.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	code, err := auth.ReadSetupCode(setupDir)
	if err != nil {
		t.Fatal(err)
	}
	setup := stage1Do(handler, http.MethodPost, "/setup", "", map[string]string{
		"setupCode": code, "name": "Ada Lovelace", "email": "ada@pantry.test", "password": "correct horse",
	}, app.EntryLAN, "")
	if setup.Code != http.StatusOK {
		t.Fatalf("setup %d %s", setup.Code, setup.Body.String())
	}
	var house string
	if err := db.QueryRow(`SELECT id FROM households WHERE is_primary = 1`).Scan(&house); err != nil {
		t.Fatal(err)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte("shared-secret"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO household_password (household_id, login_name, pw_hash, state, session_key) VALUES (?, 'pantry', ?, 'on', 'c2VjcmV0')`, house, string(hash)); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{"/health", "/terms", "/privacy", "/api/build", "/brand/logo.png", "/api/telemetry"} {
		rec := stage1Do(handler, http.MethodGet, path, "", nil, app.EntryPublic, "")
		if rec.Code == 0 {
			t.Fatalf("%s produced no status", path)
		}
	}
	lanTelemetry := stage1Do(handler, http.MethodGet, "/api/telemetry", "", nil, app.EntryLAN, "")
	if lanTelemetry.Code == http.StatusUnauthorized {
		t.Fatalf("lan telemetry while the password is on: %d %s", lanTelemetry.Code, lanTelemetry.Body.String())
	}
	cookie := signPayload(secret, "pantry", time.Now().Add(time.Hour))
	settings := httptest.NewRecorder()
	req := stage1Request(http.MethodGet, "/api/household/settings", "", nil, app.EntryPublic, "203.0.113.8:443")
	req.Header.Set("X-Pantry-Client-IP", "203.0.113.9")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	handler.ServeHTTP(settings, req)
	if settings.Code != http.StatusOK {
		t.Fatalf("household cookie %d %s", settings.Code, settings.Body.String())
	}
	badCookie := httptest.NewRecorder()
	bad := stage1Request(http.MethodGet, "/api/household/settings", "", nil, app.EntryPublic, "203.0.113.8")
	bad.Header.Set("X-Pantry-Client-IP", "203.0.113.9, 198.51.100.2")
	bad.AddCookie(&http.Cookie{Name: sessionCookie, Value: "not-a-cookie"})
	handler.ServeHTTP(badCookie, bad)
	if badCookie.Code != http.StatusUnauthorized {
		t.Fatalf("bad household cookie %d %s", badCookie.Code, badCookie.Body.String())
	}

	gate := &stage1{svc: svc, site: site}
	bare := httptest.NewRecorder()
	gate.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})).ServeHTTP(bare, httptest.NewRequest(http.MethodGet, "/health", nil))
	if bare.Code != http.StatusNoContent {
		t.Fatalf("missing entry %d", bare.Code)
	}
	lanBearerReq := stage1Request(http.MethodGet, "/api/inventory", "", nil, app.EntryLAN, "")
	lanBearerReq.Header.Set("Authorization", "Bearer sbs_not-a-session")
	lanBearer := httptest.NewRecorder()
	handler.ServeHTTP(lanBearer, lanBearerReq)
	if lanBearer.Code != http.StatusUnauthorized {
		t.Fatalf("lan bearer %d %s", lanBearer.Code, lanBearer.Body.String())
	}

	pair := stage1Do(handler, http.MethodPost, "/api/auth/login", "", map[string]string{
		"email": "ada@pantry.test", "password": "correct horse",
	}, app.EntryPublic, "")
	ada := cookieFrom(t, pair, site.SessionCookie().Name)
	created := stage1Do(handler, http.MethodPost, "/api/devices/pair", ada, map[string]any{}, app.EntryPublic, "")
	if created.Code != http.StatusCreated {
		t.Fatalf("pair %d %s", created.Code, created.Body.String())
	}
	var pairing auth.PairingCode
	if err := json.Unmarshal(created.Body.Bytes(), &pairing); err != nil {
		t.Fatal(err)
	}
	done := stage1Do(handler, http.MethodPost, "/api/devices/pair/complete", "", map[string]any{
		"code": pairing.Code, "label": "Counter",
	}, app.EntryPublic, "")
	if done.Code != http.StatusCreated {
		t.Fatalf("complete %d %s", done.Code, done.Body.String())
	}
	var paired struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(done.Body.Bytes(), &paired); err != nil || paired.Token == "" {
		t.Fatalf("token %s %v", done.Body.String(), err)
	}
	mismatch := stage1Request(http.MethodPost, "/api/device/heartbeat", "", map[string]any{"version": "1.2.3"}, app.EntryLAN, "")
	mismatch.Header.Set("Authorization", "Bearer "+paired.Token)
	mismatch.Header.Set("X-Household-Id", "0190aaaa-aaaa-7000-8000-000000000099")
	mismatchRec := httptest.NewRecorder()
	handler.ServeHTTP(mismatchRec, mismatch)
	if mismatchRec.Code != http.StatusNotFound {
		t.Fatalf("device household %d %s", mismatchRec.Code, mismatchRec.Body.String())
	}
	sessionOnDevice := stage1Request(http.MethodPost, "/api/device/heartbeat", "", nil, app.EntryPublic, "")
	sessionOnDevice.Header.Set("Authorization", "Bearer sbs_not-a-device")
	sessionRec := httptest.NewRecorder()
	handler.ServeHTTP(sessionRec, sessionOnDevice)
	if sessionRec.Code != http.StatusUnauthorized {
		t.Fatalf("session on device %d %s", sessionRec.Code, sessionRec.Body.String())
	}

	for _, code := range []string{auth.CodeBusy, auth.CodeNoSpace, auth.CodeTooLarge, "unwritable", "other"} {
		if stage1Status(code) == 0 {
			t.Fatalf("status %s", code)
		}
	}
	plain := httptest.NewRecorder()
	writeStage1(plain, errString("boom"))
	if plain.Code != http.StatusInternalServerError {
		t.Fatalf("plain %d", plain.Code)
	}
	limited := httptest.NewRecorder()
	writeStage1(limited, &auth.Error{Code: auth.CodeRateLimited, Message: auth.SentenceRate, Retry: time.Millisecond})
	if limited.Header().Get("Retry-After") != "1" {
		t.Fatalf("retry %q", limited.Header().Get("Retry-After"))
	}

	account := httptest.NewRequest(http.MethodGet, "/api/events", nil)
	account = account.WithContext(auth.WithPrincipal(account.Context(), auth.Principal{Kind: "household_password"}))
	if eventStreamKey(account) != auth.HouseholdPasswordStreamKey() {
		t.Fatalf("household stream %q", eventStreamKey(account))
	}
	lan := httptest.NewRequest(http.MethodGet, "/api/events", nil)
	lan.RemoteAddr = "192.0.2.10:9"
	lan = lan.WithContext(auth.WithPrincipal(lan.Context(), auth.Principal{Kind: "lan"}))
	if eventStreamKey(lan) != "lan:192.0.2.10" {
		t.Fatalf("lan stream %q", eventStreamKey(lan))
	}
	public := httptest.NewRequest(http.MethodGet, "/api/events", nil)
	public.RemoteAddr = "192.0.2.10"
	public.Header.Set(clientIPHeader, "198.51.100.20")
	public = public.WithContext(app.WithEntry(public.Context(), app.EntryPublic))
	public = public.WithContext(auth.WithPrincipal(public.Context(), auth.Principal{Kind: "lan"}))
	if eventStreamKey(public) != "lan:198.51.100.20" {
		t.Fatalf("public stream %q", eventStreamKey(public))
	}
	unknown := httptest.NewRequest(http.MethodGet, "/api/events", nil)
	unknown = unknown.WithContext(auth.WithPrincipal(unknown.Context(), auth.Principal{Kind: "device"}))
	if eventStreamKey(unknown) != "" || eventStreamKey(httptest.NewRequest(http.MethodGet, "/api/events", nil)) != "" {
		t.Fatal("unexpected stream key")
	}
	openStream(t, svc, auth.Principal{Kind: "household_password"})
	openStream(t, svc, auth.Principal{Kind: "lan"})
}

func openStream(t *testing.T, svc *auth.Service, principal auth.Principal) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/events", nil)
	req.RemoteAddr = "192.0.2.10:9"
	req = req.WithContext(auth.WithPrincipal(ctx, principal))
	handler := &EventsHandler{Broadcaster: events.NewBroadcaster(), Streams: svc.Streams(), Keepalive: time.Hour}
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		handler.Handle(rec, req)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("stream did not stop")
	}
}

type errString string

func (e errString) Error() string { return string(e) }
