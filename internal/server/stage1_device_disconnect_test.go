package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/auth"
)

// TestStage1DeviceDisconnectRejectsDeviceRoutes (#379): after DELETE /api/devices/{id},
// the paired token must not reach device handlers. Mutant: deviceByTokenHash /
// AuthenticateDevice ignores devices.revoked_at.
func TestStage1DeviceDisconnectRejectsDeviceRoutes(t *testing.T) {
	_, _, _, owner, _, _ := stage1Owner(t, "Disconnect Owner", "disconnect-owner@test")

	pair := owner.authed(http.MethodPost, "/api/devices/pair", map[string]any{})
	if pair.Code != http.StatusCreated {
		t.Fatalf("pair %d %s", pair.Code, pair.Body.String())
	}
	var pairing auth.PairingCode
	if err := json.Unmarshal(pair.Body.Bytes(), &pairing); err != nil {
		t.Fatal(err)
	}
	complete := owner.anon(http.MethodPost, "/api/devices/pair/complete", map[string]any{
		"code":  pairing.Code,
		"label": "Pantry scanner",
		"hub":   map[string]string{"kind": "esp32_scanner", "version": "1.0.0", "bootId": "boot-disconnect"},
	}, app.EntryPublic)
	if complete.Code != http.StatusCreated {
		t.Fatalf("complete %d %s", complete.Code, complete.Body.String())
	}
	var paired struct {
		DeviceID string `json:"deviceId"`
		Token    string `json:"token"`
	}
	if err := json.Unmarshal(complete.Body.Bytes(), &paired); err != nil || paired.Token == "" {
		t.Fatalf("pair body %s", complete.Body.String())
	}
	oldToken := paired.Token
	dev := map[string]string{"Authorization": "Bearer " + oldToken}

	live := owner.call(http.MethodPost, "/api/device/heartbeat", map[string]any{
		"version": "1.0.0", "bootId": "boot-disconnect", "queued": 3,
	}, app.EntryPublic, dev)
	if live.Code != http.StatusOK {
		t.Fatalf("pre-revoke heartbeat %d %s", live.Code, live.Body.String())
	}
	scan := owner.call(http.MethodPost, "/api/device/scans", map[string]any{
		"id": "pre-revoke-1", "barcode": "4000000000011", "direction": "stock_in", "unitCount": 1,
	}, app.EntryPublic, dev)
	if scan.Code != http.StatusOK || !strings.Contains(scan.Body.String(), `"accepted"`) {
		t.Fatalf("pre-revoke scan %d %s", scan.Code, scan.Body.String())
	}

	if rec := owner.authed(http.MethodDelete, "/api/devices/"+paired.DeviceID, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("disconnect device %d %s", rec.Code, rec.Body.String())
	}

	assertDeviceRevoked401(t, owner.call(http.MethodPost, "/api/device/heartbeat", map[string]any{
		"version": "1.0.0", "bootId": "boot-disconnect", "queued": 3,
	}, app.EntryPublic, dev))
	assertDeviceRevoked401(t, owner.call(http.MethodPost, "/api/device/scans", map[string]any{
		"id": "after-revoke-1", "barcode": "4000000000012", "direction": "stock_in", "unitCount": 1,
	}, app.EntryPublic, dev))
	assertDeviceRevoked401(t, owner.call(http.MethodPost, "/api/device/scans", map[string]any{
		"id": "after-revoke-heart", "heartbeat": true,
	}, app.EntryPublic, dev))
	assertDeviceRevoked401(t, owner.call(http.MethodPost, "/api/device/scans", map[string]any{
		"scans": []map[string]any{{"id": "batch-1", "barcode": "4000000000013", "direction": "stock_in"}},
	}, app.EntryPublic, dev))
	assertDeviceRevoked401(t, owner.call(http.MethodPost, "/api/device/token/rotate", map[string]any{}, app.EntryPublic, dev))

	after := owner.call(http.MethodPost, "/api/device/scans", map[string]any{
		"id": "should-not-land", "barcode": "4000000000099", "direction": "stock_in", "unitCount": 1,
	}, app.EntryPublic, dev)
	if after.Code == http.StatusOK {
		t.Fatalf("revoked token still ingested scans: %s", after.Body.String())
	}

	pair2 := owner.authed(http.MethodPost, "/api/devices/pair", map[string]any{})
	if pair2.Code != http.StatusCreated {
		t.Fatalf("re-pair %d %s", pair2.Code, pair2.Body.String())
	}
	var pairing2 auth.PairingCode
	if err := json.Unmarshal(pair2.Body.Bytes(), &pairing2); err != nil {
		t.Fatal(err)
	}
	recomplete := owner.anon(http.MethodPost, "/api/devices/pair/complete", map[string]any{
		"code":  pairing2.Code,
		"label": "Pantry scanner",
		"hub":   map[string]string{"kind": "esp32_scanner", "version": "1.0.1", "bootId": "boot-repair"},
	}, app.EntryPublic)
	if recomplete.Code != http.StatusCreated {
		t.Fatalf("re-complete %d %s", recomplete.Code, recomplete.Body.String())
	}
	var repaired struct {
		DeviceID string `json:"deviceId"`
		Token    string `json:"token"`
	}
	if err := json.Unmarshal(recomplete.Body.Bytes(), &repaired); err != nil || repaired.Token == "" {
		t.Fatalf("re-pair body %s", recomplete.Body.String())
	}
	if repaired.DeviceID == paired.DeviceID {
		t.Fatalf("re-pair reused revoked device id %s", repaired.DeviceID)
	}
	newDev := map[string]string{"Authorization": "Bearer " + repaired.Token}
	if repaired.Token == oldToken {
		t.Fatal("re-pair returned the same token")
	}
	newBeat := owner.call(http.MethodPost, "/api/device/heartbeat", map[string]any{
		"version": "1.0.1", "bootId": "boot-repair", "queued": 0,
	}, app.EntryPublic, newDev)
	if newBeat.Code != http.StatusOK {
		t.Fatalf("re-paired heartbeat %d %s", newBeat.Code, newBeat.Body.String())
	}
	newScan := owner.call(http.MethodPost, "/api/device/scans", map[string]any{
		"id": "repaired-1", "barcode": "4000000000014", "direction": "stock_in", "unitCount": 1,
	}, app.EntryPublic, newDev)
	if newScan.Code != http.StatusOK || !strings.Contains(newScan.Body.String(), `"accepted"`) {
		t.Fatalf("re-paired scan %d %s", newScan.Code, newScan.Body.String())
	}
	assertDeviceRevoked401(t, owner.call(http.MethodPost, "/api/device/heartbeat", map[string]any{
		"version": "1.0.0",
	}, app.EntryPublic, dev))
	assertDeviceRevoked401(t, owner.call(http.MethodPost, "/api/device/scans", map[string]any{
		"id": "old-token-scan", "barcode": "4000000000015", "direction": "stock_in",
	}, app.EntryPublic, dev))

	list := owner.authed(http.MethodGet, "/api/devices", nil)
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), paired.DeviceID) {
		t.Fatalf("devices list %d %s", list.Code, list.Body.String())
	}
	if !strings.Contains(list.Body.String(), `"revokedAt"`) {
		t.Fatal("revoked device should appear with revokedAt in device list")
	}
}

func assertDeviceRevoked401(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status %d want 401 body %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"code":"`+auth.CodeDeviceRevoked+`"`) {
		t.Fatalf("want %s body %s", auth.CodeDeviceRevoked, rec.Body.String())
	}
}
