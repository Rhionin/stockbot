package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/Rhionin/stockbot/internal/hub"
)

func TestHubOfflineFlushReachesDeviceAPI(t *testing.T) {
	db := openMigratedDB(t, filepath.Join(t.TempDir(), "pantry.db"))
	setupDir := t.TempDir()
	svc := auth.NewService(db, setupDir)
	site := stage1Site(t)
	handler, _ := setupOnDB(t, db, nil, WithStage1(svc, site))
	var offline atomic.Bool
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if offline.Load() {
			http.Error(w, "offline", http.StatusServiceUnavailable)
			return
		}
		r = r.WithContext(app.WithEntry(r.Context(), app.EntryLAN))
		handler.ServeHTTP(w, r)
	}))
	defer proxy.Close()

	c := &stage1Caller{t: t, h: handler}
	if rec := c.call(http.MethodGet, "/setup", nil, app.EntryLAN, nil); rec.Code != http.StatusOK {
		t.Fatalf("setup page %d", rec.Code)
	}
	code, err := auth.ReadSetupCode(setupDir)
	if err != nil {
		t.Fatal(err)
	}
	if rec := c.call(http.MethodPost, "/setup", map[string]string{
		"setupCode": code, "name": "Nia North", "email": "nia@example.test", "password": "correct horse",
	}, app.EntryLAN, nil); rec.Code != http.StatusOK {
		t.Fatalf("setup %d %s", rec.Code, rec.Body.String())
	}
	login := c.call(http.MethodPost, "/api/auth/login", map[string]string{
		"email": "nia@example.test", "password": "correct horse",
	}, app.EntryPublic, map[string]string{"X-Stockbot-Client": "expo/1.2.3"})
	if login.Code != http.StatusOK {
		t.Fatalf("login %d %s", login.Code, login.Body.String())
	}
	var session auth.SessionView
	if err := json.Unmarshal(login.Body.Bytes(), &session); err != nil || session.ActiveHouseholdID == nil {
		t.Fatalf("session %s", login.Body.String())
	}
	c.cookie = cookieFrom(t, login, site.SessionCookie().Name)
	c.house = *session.ActiveHouseholdID

	pair := c.authed(http.MethodPost, "/api/devices/pair", map[string]any{})
	if pair.Code != http.StatusCreated {
		t.Fatalf("pair %d %s", pair.Code, pair.Body.String())
	}
	var pairing auth.PairingCode
	if err := json.Unmarshal(pair.Body.Bytes(), &pairing); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	var mono int64
	h, err := hub.Open(hub.Options{
		ServerURL:   proxy.URL,
		StatePath:   filepath.Join(dir, "state.json"),
		BufferPath:  filepath.Join(dir, "buffer.sqlite"),
		ReleaseRoot: filepath.Join(dir, "opt"),
		Name:        "Counter",
		Version:     "1.2.3",
		BootID:      "boot-hub",
		Mono:        func() int64 { return mono },
		Sleep: func(context.Context, time.Duration) error {
			return errors.New("still offline")
		},
		Rand:   func() float64 { return 1 },
		Synced: func(context.Context) (bool, error) { return false, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	if err := h.Pair(context.Background(), pairing.Code); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("credential mode %o", info.Mode().Perm())
	}
	raw, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err != nil || !strings.Contains(string(raw), "sbd_") {
		t.Fatal("token was not stored")
	}

	offline.Store(true)
	mono = 1000
	for _, barcode := range []string{"000111222333", "000111222334", "000111222335"} {
		if err := h.HandleLine(context.Background(), barcode); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.FlushAll(context.Background()); err == nil {
		t.Fatal("offline flush reported success")
	}
	if depth, _ := h.Queued(); depth != 3 {
		t.Fatalf("queued during outage = %d", depth)
	}
	mono = 6000
	if err := h.Beat(context.Background()); err == nil {
		t.Fatal("heartbeat succeeded while offline")
	}

	offline.Store(false)
	if err := h.Beat(context.Background()); err != nil {
		t.Fatal(err)
	}
	devices := c.authed(http.MethodGet, "/api/devices", nil)
	if devices.Code != http.StatusOK || !strings.Contains(devices.Body.String(), `"queued":3`) {
		t.Fatalf("devices %d %s", devices.Code, devices.Body.String())
	}
	if err := h.FlushAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := h.FlushAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if depth, _ := h.Queued(); depth != 0 {
		t.Fatalf("queue after flush = %d", depth)
	}
	scans := c.authed(http.MethodGet, "/api/scans", nil)
	if scans.Code != http.StatusOK {
		t.Fatalf("scans %d %s", scans.Code, scans.Body.String())
	}
	body := scans.Body.String()
	for _, barcode := range []string{"000111222333", "000111222334", "000111222335"} {
		if strings.Count(body, barcode) != 1 {
			t.Fatalf("barcode %s count in %s", barcode, body)
		}
	}
}
