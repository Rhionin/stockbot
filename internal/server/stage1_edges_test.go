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
)

func TestStage1Edges(t *testing.T) {
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
	}, app.EntryPublic, "")
	if setup.Code != http.StatusOK {
		t.Fatalf("setup %d %s", setup.Code, setup.Body.String())
	}

	for _, path := range []string{"/health", "/terms", "/privacy", "/api/build", "/brand/logo.png", "/api/telemetry"} {
		rec := stage1Do(handler, http.MethodGet, path, "", nil, app.EntryPublic, "")
		if rec.Code == 0 {
			t.Fatalf("%s produced no status", path)
		}
	}

	gate := &stage1{svc: svc, site: site}
	bare := httptest.NewRecorder()
	gate.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})).ServeHTTP(bare, httptest.NewRequest(http.MethodGet, "/health", nil))
	if bare.Code != http.StatusNoContent {
		t.Fatalf("missing entry %d", bare.Code)
	}
	lanBearerReq := stage1Request(http.MethodGet, "/api/inventory", "", nil, app.EntryPublic, "")
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
	mismatch := stage1Request(http.MethodPost, "/api/device/heartbeat", "", map[string]any{"version": "1.2.3"}, app.EntryPublic, "")
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
	account = account.WithContext(auth.WithPrincipal(account.Context(), auth.Principal{Kind: "account", UserID: "user-1", SessionID: "sess-hash"}))
	if eventStreamKey(account) != auth.SessionStreamKey("sess-hash") {
		t.Fatalf("account stream %q", eventStreamKey(account))
	}
	unknown := httptest.NewRequest(http.MethodGet, "/api/events", nil)
	unknown = unknown.WithContext(auth.WithPrincipal(unknown.Context(), auth.Principal{Kind: "device"}))
	if eventStreamKey(unknown) != "" || eventStreamKey(httptest.NewRequest(http.MethodGet, "/api/events", nil)) != "" {
		t.Fatal("unexpected stream key")
	}
	openStream(t, svc, auth.Principal{Kind: "account", UserID: "user-1", SessionID: "sess-hash"})
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
