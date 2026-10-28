package server

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/auth"
)

func TestStage1ESP32ScannerBackend(t *testing.T) {
	db := openMigratedDB(t, filepath.Join(t.TempDir(), "esp32.db"))
	setupDir := t.TempDir()
	svc := auth.NewService(db, setupDir)
	if err := svc.Boot(context.Background()); err != nil {
		t.Fatal(err)
	}
	site := stage1Site(t)
	handler, env := setupOnDB(t, db, nil, WithStage1(svc, site))
	c := &stage1Caller{t: t, h: handler}

	if rec := c.call(http.MethodGet, "/setup", nil, app.EntryPublic, nil); rec.Code != http.StatusOK {
		t.Fatalf("setup page %d", rec.Code)
	}
	code, err := auth.ReadSetupCode(setupDir)
	if err != nil {
		t.Fatal(err)
	}
	if rec := c.call(http.MethodPost, "/setup", map[string]string{
		"setupCode": code, "name": "Pat", "email": "pat@example.test", "password": "correct horse",
	}, app.EntryPublic, nil); rec.Code != http.StatusOK {
		t.Fatalf("setup %d", rec.Code)
	}
	login := c.call(http.MethodPost, "/api/auth/login", map[string]string{
		"email": "pat@example.test", "password": "correct horse",
	}, app.EntryPublic, nil)
	var session auth.SessionView
	if err := json.Unmarshal(login.Body.Bytes(), &session); err != nil || session.ActiveHouseholdID == nil {
		t.Fatal(err)
	}
	c.cookie = cookieFrom(t, login, site.SessionCookie().Name)
	c.house = *session.ActiveHouseholdID

	setupProductWithBarcode("0190dddd-7000-8000-8000-000000000101", "Canned Beans", "Pantry", "4000000000011")(env)

	pair := c.authed(http.MethodPost, "/api/devices/pair", map[string]any{})
	var pairing auth.PairingCode
	if err := json.Unmarshal(pair.Body.Bytes(), &pairing); err != nil {
		t.Fatal(err)
	}
	complete := c.anon(http.MethodPost, "/api/devices/pair/complete", map[string]any{
		"code": pairing.Code, "label": "Pantry pistol",
		"hub":  map[string]string{"kind": "esp32_scanner", "version": "1.0.0", "bootId": "boot-esp"},
	}, app.EntryPublic)
	if complete.Code != http.StatusCreated || !strings.Contains(complete.Body.String(), `"defaultDirection":"stock_out"`) ||
		!strings.Contains(complete.Body.String(), `"commitMode":"queue"`) || !strings.Contains(complete.Body.String(), `"inactivityRevertSeconds":300`) {
		t.Fatalf("pair complete %d %s", complete.Code, complete.Body.String())
	}
	var paired struct {
		DeviceID string `json:"deviceId"`
		Token    string `json:"token"`
	}
	if err := json.Unmarshal(complete.Body.Bytes(), &paired); err != nil {
		t.Fatal(err)
	}
	dev := map[string]string{"Authorization": "Bearer " + paired.Token}

	patch := c.authed(http.MethodPatch, "/api/devices/"+paired.DeviceID, map[string]any{
		"commitMode": "immediate", "defaultDirection": "stock_in", "inactivityRevertSeconds": 600,
	})
	if patch.Code != http.StatusOK || !strings.Contains(patch.Body.String(), `"commitMode":"immediate"`) {
		t.Fatalf("patch %d %s", patch.Code, patch.Body.String())
	}
	badPatch := c.authed(http.MethodPatch, "/api/devices/"+paired.DeviceID, map[string]any{"inactivityRevertSeconds": 10})
	if badPatch.Code != http.StatusBadRequest || !strings.Contains(badPatch.Body.String(), `"code":"invalid_input"`) {
		t.Fatalf("bad patch %d %s", badPatch.Code, badPatch.Body.String())
	}

	beat := c.call(http.MethodPost, "/api/device/heartbeat", map[string]any{
		"version": "1.0.0", "bootId": "boot-esp", "uptimeS": 1, "queued": 0, "clockSynced": true,
		"batteryPct": 88, "charging": true,
	}, app.EntryPublic, dev)
	if beat.Code != http.StatusOK || !strings.Contains(beat.Body.String(), `"commitMode":"immediate"`) {
		t.Fatalf("heartbeat %d %s", beat.Code, beat.Body.String())
	}
	devices := c.authed(http.MethodGet, "/api/devices", nil)
	if devices.Code != http.StatusOK || !strings.Contains(devices.Body.String(), `"batteryPct":88`) || !strings.Contains(devices.Body.String(), `"charging":true`) {
		t.Fatalf("devices %d %s", devices.Code, devices.Body.String())
	}

	qr := c.call(http.MethodPost, "/api/device/scans", map[string]any{
		"id": "qr-1", "barcode": "https://example.com/promo", "direction": "stock_out",
	}, app.EntryPublic, dev)
	if qr.Code != http.StatusBadRequest {
		t.Fatalf("qr scan %d", qr.Code)
	}
	var qrCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM scan_entries WHERE household_id = ? AND status = 'qr'`, c.house).Scan(&qrCount); err != nil || qrCount != 0 {
		t.Fatalf("qr rows %d", qrCount)
	}

	stockIn := c.call(http.MethodPost, "/api/device/scans", map[string]any{
		"id": "imm-in", "barcode": "4000000000011", "direction": "stock_in", "unitCount": 1,
	}, app.EntryPublic, dev)
	if stockIn.Code != http.StatusOK || !strings.Contains(stockIn.Body.String(), `"scanStatus":"committed"`) ||
		!strings.Contains(stockIn.Body.String(), `"delta":1`) || !strings.Contains(stockIn.Body.String(), `"onHand":1`) {
		t.Fatalf("immediate in %d %s", stockIn.Code, stockIn.Body.String())
	}
	dupIn := c.call(http.MethodPost, "/api/device/scans", map[string]any{
		"id": "imm-in", "barcode": "4000000000011", "direction": "stock_in", "unitCount": 1,
	}, app.EntryPublic, dev)
	if dupIn.Code != http.StatusOK || !strings.Contains(dupIn.Body.String(), `"status":"duplicate"`) || !strings.Contains(dupIn.Body.String(), `"scanStatus":"committed"`) {
		t.Fatalf("dup in %d %s", dupIn.Code, dupIn.Body.String())
	}

	stockOut := c.call(http.MethodPost, "/api/device/scans", map[string]any{
		"id": "imm-out", "barcode": "4000000000011", "direction": "stock_out", "unitCount": 1,
	}, app.EntryPublic, dev)
	if stockOut.Code != http.StatusOK || !strings.Contains(stockOut.Body.String(), `"onHand":0`) || !strings.Contains(stockOut.Body.String(), `"delta":-1`) {
		t.Fatalf("immediate out %d %s", stockOut.Code, stockOut.Body.String())
	}

	// Queue mode on a second scanner
	pair2 := c.authed(http.MethodPost, "/api/devices/pair", map[string]any{})
	var code2 auth.PairingCode
	if err := json.Unmarshal(pair2.Body.Bytes(), &code2); err != nil {
		t.Fatal(err)
	}
	complete2 := c.anon(http.MethodPost, "/api/devices/pair/complete", map[string]any{
		"code": code2.Code, "label": "Queue scanner",
		"hub":  map[string]string{"kind": "esp32_scanner", "version": "1.0.0", "bootId": "boot-q"},
	}, app.EntryPublic)
	var paired2 struct {
		DeviceID string `json:"deviceId"`
		Token    string `json:"token"`
	}
	if err := json.Unmarshal(complete2.Body.Bytes(), &paired2); err != nil {
		t.Fatal(err)
	}
	dev2 := map[string]string{"Authorization": "Bearer " + paired2.Token}
	queued := c.call(http.MethodPost, "/api/device/scans", map[string]any{
		"id": "q1", "barcode": "4000000000011", "direction": "stock_in", "unitCount": 1,
	}, app.EntryPublic, dev2)
	if queued.Code != http.StatusOK || !strings.Contains(queued.Body.String(), `"scanStatus":"queued"`) || !strings.Contains(queued.Body.String(), `"queuedCount":1`) {
		t.Fatalf("queued %d %s", queued.Code, queued.Body.String())
	}
	queued2 := c.call(http.MethodPost, "/api/device/scans", map[string]any{
		"id": "q2", "barcode": "4000000000011", "direction": "stock_in", "unitCount": 1,
	}, app.EntryPublic, dev2)
	if queued2.Code != http.StatusOK || !strings.Contains(queued2.Body.String(), `"queuedCount":2`) {
		t.Fatalf("queued merge %d %s", queued2.Code, queued2.Body.String())
	}
	flagged := c.call(http.MethodPost, "/api/device/scans", map[string]any{
		"id": "unk", "barcode": "9998887776665", "direction": "stock_out",
	}, app.EntryPublic, dev2)
	if flagged.Code != http.StatusOK || !strings.Contains(flagged.Body.String(), `"scanStatus":"flagged"`) || !strings.Contains(flagged.Body.String(), `"known":false`) {
		t.Fatalf("flagged %d %s", flagged.Code, flagged.Body.String())
	}

	backfillDev := c.authed(http.MethodPatch, "/api/devices/"+paired.DeviceID, map[string]any{"commitMode": "immediate"})
	if backfillDev.Code != http.StatusOK {
		t.Fatal(backfillDev.Code)
	}
	backfill := c.call(http.MethodPost, "/api/device/scans", map[string]any{
		"id": "bf1", "barcode": "4000000000011", "direction": "stock_out", "unitCount": 1,
	}, app.EntryPublic, dev)
	if backfill.Code != http.StatusOK || !strings.Contains(backfill.Body.String(), `"backfilled":true`) || !strings.Contains(backfill.Body.String(), `"onHand":0`) {
		t.Fatalf("backfill %d %s", backfill.Code, backfill.Body.String())
	}
	var moves int
	if err := db.QueryRow(`SELECT COUNT(*) FROM stock_moves WHERE household_id = ? AND scan_entry_id IN (
		SELECT id FROM scan_entries WHERE household_id = ? AND client_scan_id = 'bf1')`, c.house, c.house).Scan(&moves); err != nil || moves < 2 {
		t.Fatalf("backfill moves %d", moves)
	}
}
