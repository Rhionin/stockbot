package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/Rhionin/stockbot/internal/changes"
	"github.com/Rhionin/stockbot/internal/scan"
	"github.com/Rhionin/stockbot/internal/tenant"
)

func TestStage1DeviceAPI(t *testing.T) {
	db := openMigratedDB(t, filepath.Join(t.TempDir(), "pantry.db"))
	setupDir := t.TempDir()
	svc := auth.NewService(db, setupDir)
	site := stage1Site(t)
	handler, _ := setupOnDB(t, db, nil, WithStage1(svc, site))
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
	if err := json.Unmarshal(login.Body.Bytes(), &session); err != nil || session.User == nil || session.ActiveHouseholdID == nil {
		t.Fatalf("session %s", login.Body.String())
	}
	c.cookie = cookieFrom(t, login, site.SessionCookie().Name)
	c.house = *session.ActiveHouseholdID
	if _, err := db.Exec(`UPDATE households SET name = ? WHERE id = ?`, "North Cupboard", c.house); err != nil {
		t.Fatal(err)
	}

	pair := c.authed(http.MethodPost, "/api/devices/pair", map[string]any{})
	if pair.Code != http.StatusCreated || !strings.Contains(pair.Body.String(), `"barcodeValue"`) || !strings.Contains(pair.Body.String(), `"qrValue"`) {
		t.Fatalf("pair %d %s", pair.Code, pair.Body.String())
	}
	var pairing auth.PairingCode
	if err := json.Unmarshal(pair.Body.Bytes(), &pairing); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(pairing.Code, c.house) || strings.Contains(pairing.QRPayload, c.house) {
		t.Fatal("pairing payload named the household")
	}
	foreign := c.call(http.MethodGet, "/api/devices/pair/"+pairing.PairingID, nil, app.EntryPublic, map[string]string{
		"X-Household-Id": "0190aaaa-bbbb-7000-8000-000000000099",
	})
	if foreign.Code != http.StatusNotFound {
		t.Fatalf("foreign pairing %d %s", foreign.Code, foreign.Body.String())
	}
	plain := c.anon(http.MethodPost, "/api/devices/pair/complete", map[string]any{"code": pairing.Code, "label": "Counter"}, app.EntryPublic)
	if plain.Code != http.StatusCreated || strings.Contains(plain.Body.String(), c.house) || strings.Contains(plain.Body.String(), `"config"`) {
		t.Fatalf("plain complete %d %s", plain.Code, plain.Body.String())
	}

	hubPair := c.authed(http.MethodPost, "/api/devices/pair", map[string]any{})
	var hubCode auth.PairingCode
	if err := json.Unmarshal(hubPair.Body.Bytes(), &hubCode); err != nil {
		t.Fatal(err)
	}
	hub := c.anon(http.MethodPost, "/api/devices/pair/complete", map[string]any{
		"code": hubCode.Code, "label": "Counter",
		"hub": map[string]string{"kind": "pi_hub", "version": "1.2.3", "bootId": "boot-9"},
	}, app.EntryLAN)
	if hub.Code != http.StatusCreated || strings.Contains(hub.Body.String(), c.house) || !strings.Contains(hub.Body.String(), `"heartbeatSeconds":60`) || !strings.Contains(hub.Body.String(), `"config"`) {
		t.Fatalf("hub complete %d %s", hub.Code, hub.Body.String())
	}
	var paired struct {
		DeviceID string `json:"deviceId"`
		Token    string `json:"token"`
	}
	if err := json.Unmarshal(hub.Body.Bytes(), &paired); err != nil || !strings.HasPrefix(paired.Token, "sbd_") {
		t.Fatalf("hub body %s", hub.Body.String())
	}
	status := c.authed(http.MethodGet, "/api/devices/pair/"+hubCode.PairingID, nil)
	if status.Code != http.StatusOK || !strings.Contains(status.Body.String(), `"complete"`) || !strings.Contains(status.Body.String(), paired.DeviceID) {
		t.Fatalf("status %d %s", status.Code, status.Body.String())
	}

	dev := map[string]string{"Authorization": "Bearer " + paired.Token}
	if rec := c.call(http.MethodGet, "/api/devices", nil, app.EntryPublic, dev); rec.Code != http.StatusUnauthorized {
		t.Fatalf("device on people route %d %s", rec.Code, rec.Body.String())
	}
	if rec := c.call(http.MethodPost, "/api/device/heartbeat", map[string]any{"version": "1.2.3"}, app.EntryPublic, map[string]string{
		"Authorization": "Bearer " + session.Token,
	}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("session on device route %d %s", rec.Code, rec.Body.String())
	}
	if rec := c.call(http.MethodPost, "/api/device/heartbeat", map[string]any{"version": "1.2.3", "queued": 4}, app.EntryLAN, map[string]string{
		"Authorization":  "Bearer " + paired.Token,
		"X-Household-Id": "0190aaaa-bbbb-7000-8000-000000000099",
	}); rec.Code != http.StatusNotFound {
		t.Fatalf("mismatched household %d %s", rec.Code, rec.Body.String())
	}
	beat := c.call(http.MethodPost, "/api/device/heartbeat", map[string]any{
		"version": "1.2.3", "bootId": "boot-9", "uptimeS": 3, "queued": 4, "droppedCount": 1, "clockSynced": true,
	}, app.EntryLAN, dev)
	if beat.Code != http.StatusOK || !strings.Contains(beat.Body.String(), `"heartbeatSeconds":60`) || !strings.Contains(beat.Body.String(), `"rotateToken":false`) {
		t.Fatalf("heartbeat %d %s", beat.Code, beat.Body.String())
	}
	devices := c.authed(http.MethodGet, "/api/devices", nil)
	if devices.Code != http.StatusOK || !strings.Contains(devices.Body.String(), `"queued":4`) || !strings.Contains(devices.Body.String(), `"online":true`) {
		t.Fatalf("devices %d %s", devices.Code, devices.Body.String())
	}
	cursor, err := changes.Current(context.Background(), db, c.house, changes.ScopeDevices)
	if err != nil {
		t.Fatal(err)
	}
	same := c.authed(http.MethodGet, "/api/devices?since="+url.QueryEscape(cursor), nil)
	if same.Code != http.StatusOK || same.Header().Get("Cache-Control") != "no-store" || !strings.Contains(same.Body.String(), `"changed":false`) || strings.Contains(same.Body.String(), `"devices"`) {
		t.Fatalf("unchanged devices %d %s %s", same.Code, same.Header().Get("Cache-Control"), same.Body.String())
	}
	stale := c.authed(http.MethodGet, "/api/devices?since="+url.QueryEscape(changes.Cursor(c.house, changes.ScopeDevices, 0)), nil)
	if stale.Code != http.StatusOK || stale.Header().Get("Cache-Control") != "no-store" || !strings.Contains(stale.Body.String(), `"changed":true`) || !strings.Contains(stale.Body.String(), `"devices"`) || !strings.Contains(stale.Body.String(), `"queued":4`) {
		t.Fatalf("stale devices %d %s %s", stale.Code, stale.Header().Get("Cache-Control"), stale.Body.String())
	}
	otherCursor := c.authed(http.MethodGet, "/api/devices?since="+url.QueryEscape(changes.Cursor("other-household", changes.ScopeDevices, 0)), nil)
	if otherCursor.Code != http.StatusOK || !strings.Contains(otherCursor.Body.String(), `"changed":true`) || !strings.Contains(otherCursor.Body.String(), `"devices"`) {
		t.Fatalf("foreign cursor %d %s", otherCursor.Code, otherCursor.Body.String())
	}

	batch := c.call(http.MethodPost, "/api/device/scans", map[string]any{"scans": []map[string]any{
		{"id": "s1", "barcode": "990000000099", "direction": "stock_in"},
		{"id": "s-pair", "barcode": "SBP-0123-ABCD-G"},
	}}, app.EntryLAN, dev)
	if batch.Code != http.StatusOK || !strings.Contains(batch.Body.String(), `"created"`) || !strings.Contains(batch.Body.String(), `"rejected"`) {
		t.Fatalf("batch %d %s", batch.Code, batch.Body.String())
	}
	dup := c.call(http.MethodPost, "/api/device/scans", map[string]any{"scans": []map[string]any{
		{"id": "s1", "barcode": "990000000099", "direction": "stock_in"},
		{"id": "s1", "barcode": "111111111117", "direction": "stock_out"},
	}}, app.EntryLAN, dev)
	if dup.Code != http.StatusOK || !strings.Contains(dup.Body.String(), `"duplicate"`) || !strings.Contains(dup.Body.String(), `"conflict"`) {
		t.Fatalf("retry %d %s", dup.Code, dup.Body.String())
	}
	var stored int
	if err := db.QueryRow(`SELECT COUNT(*) FROM scan_entries WHERE household_id = ? AND client_scan_id = 's1'`, c.house).Scan(&stored); err != nil || stored != 1 {
		t.Fatalf("stored %d %v", stored, err)
	}
	tooMany := make([]map[string]any, 51)
	for i := range tooMany {
		tooMany[i] = map[string]any{"id": "x", "barcode": "123"}
	}
	if rec := c.call(http.MethodPost, "/api/device/scans", map[string]any{"scans": tooMany}, app.EntryLAN, dev); rec.Code != http.StatusBadRequest {
		t.Fatalf("51 scans %d %s", rec.Code, rec.Body.String())
	}

	page := c.authed(http.MethodPost, "/api/scans", map[string]any{"barcode": pairing.Code})
	if page.Code != http.StatusBadRequest || strings.Contains(page.Body.String(), pairing.Code) || !strings.Contains(page.Body.String(), scan.PairingScanMessage) {
		t.Fatalf("scan page %d %s", page.Code, page.Body.String())
	}
	lookup := c.authed(http.MethodGet, "/api/products/lookup?barcode="+pairing.Code, nil)
	if lookup.Code != http.StatusBadRequest || strings.Contains(lookup.Body.String(), pairing.Code) {
		t.Fatalf("lookup %d %s", lookup.Code, lookup.Body.String())
	}
	var queued int
	if err := db.QueryRow(`SELECT COUNT(*) FROM scan_entries WHERE household_id = ? AND barcode = ?`, c.house, pairing.Code).Scan(&queued); err != nil || queued != 0 {
		t.Fatalf("pairing barcode stored %d", queued)
	}

	alerts := c.authed(http.MethodGet, "/api/operator/alerts", nil)
	if alerts.Code != http.StatusOK || !strings.Contains(alerts.Body.String(), "North Cupboard") || !strings.Contains(alerts.Body.String(), "scans_dropped") || strings.Contains(alerts.Body.String(), "990000000099") {
		t.Fatalf("alerts %d %s", alerts.Code, alerts.Body.String())
	}

	var directID string
	if err := db.QueryRow(`SELECT id FROM devices WHERE kind = 'direct' AND household_id = ?`, c.house).Scan(&directID); err != nil {
		t.Fatal(err)
	}
	if rec := c.authed(http.MethodDelete, "/api/devices/"+directID, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("direct revoke %d %s", rec.Code, rec.Body.String())
	}
	if rec := c.authed(http.MethodDelete, "/api/devices/"+paired.DeviceID, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("revoke %d %s", rec.Code, rec.Body.String())
	}
	revoked := c.call(http.MethodPost, "/api/device/heartbeat", map[string]any{"version": "1.2.3"}, app.EntryLAN, dev)
	if revoked.Code != http.StatusUnauthorized || !strings.Contains(revoked.Body.String(), auth.CodeDeviceRevoked) {
		t.Fatalf("revoked %d %s", revoked.Code, revoked.Body.String())
	}
	if !strings.Contains(c.authed(http.MethodGet, "/api/devices", nil).Body.String(), `"queued":4`) {
		t.Fatal("revoke dropped the queue depth")
	}

	for i := 0; i < 10; i++ {
		req := stage1Request(http.MethodPost, "/api/devices/pair/complete", "", map[string]any{"code": "SBP-0123-ABCD-0"}, app.EntryPublic, "198.51.100.20:443")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code == http.StatusTooManyRequests {
			t.Fatalf("limited on attempt %d", i+1)
		}
	}
	req := stage1Request(http.MethodPost, "/api/devices/pair/complete", "", map[string]any{"code": "SBP-0123-ABCD-0"}, app.EntryPublic, "198.51.100.20:443")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" || !strings.Contains(rec.Body.String(), "rate_limited") {
		t.Fatalf("ip limit %d retry %q %s", rec.Code, rec.Header().Get("Retry-After"), rec.Body.String())
	}
}

func TestStorageWarningRecordsHouseholdOnly(t *testing.T) {
	scope, err := tenant.New("0190aaaa-bbbb-7000-8000-000000000001")
	if err != nil {
		t.Fatal(err)
	}
	var gotHouse, gotKind string
	ctx := withStorageAlert(tenant.WithScope(context.Background(), scope), func(_ context.Context, householdID, kind string) {
		gotHouse, gotKind = householdID, kind
	})
	req := httptest.NewRequest(http.MethodPost, "/api/scans", nil).WithContext(ctx)
	logStorageWarning(req, errors.New("database is locked"), "db_busy")
	if gotHouse != scope.HouseholdID() || gotKind != "busy" {
		t.Fatalf("busy %s %s", gotHouse, gotKind)
	}
	logStorageWarning(req, errors.New("database or disk is full"), "db_no_space")
	if gotKind != "no_space" || gotHouse != scope.HouseholdID() {
		t.Fatalf("space %s %s", gotHouse, gotKind)
	}
}
