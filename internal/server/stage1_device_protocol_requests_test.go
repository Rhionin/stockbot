package server

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/google/uuid"
)

type deviceProtocolRequestVector struct {
	Name            string `json:"name"`
	Method          string `json:"method"`
	Endpoint        string `json:"endpoint"`
	Body            string `json:"body"`
	DeviceReachable bool   `json:"deviceReachable"`
	ExpectedStatus  *int   `json:"expectedStatus"`
}

// Observed stage1 error codes for request vectors that expect HTTP 400.
var deviceProtocol400ErrorCodes = map[string]string{
	"pair-escape-unicode":    "validation",
	"scan-single-escaped":    "validation",
	"scan-single-max-fields": "validation",
}

type deviceProtocolSnap struct {
	Devices           int
	Scans             int
	PairingsCompleted int
}

type deviceProtocolDeviceSnap struct {
	Queued    int
	LastSeen  string
	BootID    string
	HubVer    string
	Battery   sql.NullInt64
}

func loadDeviceProtocolRequestVectors(t *testing.T) []deviceProtocolRequestVector {
	t.Helper()
	path := filepath.Join(contractRepoRoot(t), "testdata", "device-protocol", "requests.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("device-protocol requests.json: %v", err)
	}
	var vectors []deviceProtocolRequestVector
	if err := json.Unmarshal(raw, &vectors); err != nil {
		t.Fatalf("parse requests.json: %v", err)
	}
	if len(vectors) == 0 {
		t.Fatal("requests.json has zero vectors")
	}
	for _, v := range vectors {
		if v.ExpectedStatus == nil {
			t.Fatalf("vector %q missing expectedStatus", v.Name)
		}
	}
	return vectors
}

func pairingCodeInBody(body string) (string, bool) {
	var partial struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal([]byte(body), &partial); err != nil {
		return "", false
	}
	return partial.Code, partial.Code != ""
}

func seedPendingPairingCode(t *testing.T, db *sql.DB, householdID, userID, rawCode string) string {
	t.Helper()
	norm, well, check := auth.NormalizePairingCode(rawCode)
	if !well || !check {
		t.Fatalf("cannot seed pairing code %q (well=%v check=%v)", rawCode, well, check)
	}
	hash := auth.HashOpaque(norm)
	if _, err := db.Exec(`DELETE FROM pairing_codes WHERE code_hash = ?`, hash); err != nil {
		t.Fatal(err)
	}
	exp := time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339)
	id := uuid.NewString()
	if _, err := db.Exec(`
		INSERT INTO pairing_codes (id, household_id, code_hash, created_by, expires_at)
		VALUES (?, ?, ?, ?, ?)`,
		id, householdID, hash, userID, exp); err != nil {
		t.Fatal(err)
	}
	return hash
}

func deviceProtocolTableSnap(t *testing.T, db *sql.DB, house string) deviceProtocolSnap {
	t.Helper()
	var snap deviceProtocolSnap
	if err := db.QueryRow(`SELECT COUNT(*) FROM devices WHERE household_id = ?`, house).Scan(&snap.Devices); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM scan_entries WHERE household_id = ?`, house).Scan(&snap.Scans); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM pairing_codes
		WHERE household_id = ? AND completed_device_id IS NOT NULL AND completed_device_id != ''`, house).Scan(&snap.PairingsCompleted); err != nil {
		t.Fatal(err)
	}
	return snap
}

func deviceProtocolPairingConsumed(t *testing.T, db *sql.DB, codeHash string) bool {
	t.Helper()
	var device sql.NullString
	err := db.QueryRow(`SELECT completed_device_id FROM pairing_codes WHERE code_hash = ?`, codeHash).Scan(&device)
	if err == sql.ErrNoRows {
		return false
	}
	if err != nil {
		t.Fatal(err)
	}
	return device.Valid && device.String != ""
}

func readDeviceProtocolDeviceSnap(t *testing.T, db *sql.DB, house, deviceID string) deviceProtocolDeviceSnap {
	t.Helper()
	var snap deviceProtocolDeviceSnap
	err := db.QueryRow(`
		SELECT COALESCE(queued, 0), COALESCE(last_seen_at, ''), COALESCE(boot_id, ''),
			COALESCE(hub_version, ''), battery_pct
		FROM devices WHERE household_id = ? AND id = ?`, house, deviceID).Scan(
		&snap.Queued, &snap.LastSeen, &snap.BootID, &snap.HubVer, &snap.Battery)
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

func deviceProtocolPairedScanner(t *testing.T, c *stage1Caller) (token, deviceID string) {
	t.Helper()
	pair := c.authed(http.MethodPost, "/api/devices/pair", map[string]any{})
	if pair.Code != http.StatusCreated {
		t.Fatalf("pair %d %s", pair.Code, pair.Body.String())
	}
	var issued auth.PairingCode
	if err := json.Unmarshal(pair.Body.Bytes(), &issued); err != nil {
		t.Fatal(err)
	}
	done := c.anon(http.MethodPost, "/api/devices/pair/complete", map[string]any{
		"code": issued.Code, "label": "protocol-fixture",
		"hub":  map[string]string{"kind": "esp32_scanner", "version": "1.0.0", "bootId": "boot-fixture"},
	}, app.EntryPublic)
	if done.Code != http.StatusCreated {
		t.Fatalf("pair complete %d %s", done.Code, done.Body.String())
	}
	var paired struct {
		DeviceID string `json:"deviceId"`
		Token    string `json:"token"`
	}
	if err := json.Unmarshal(done.Body.Bytes(), &paired); err != nil || paired.Token == "" {
		t.Fatalf("pair body %s", done.Body.String())
	}
	return paired.Token, paired.DeviceID
}

func deviceProtocolHeaders(deviceToken string) map[string]string {
	if deviceToken == "" {
		return nil
	}
	return map[string]string{"Authorization": "Bearer " + deviceToken}
}

func assertDeviceProtocolSnapUnchanged(t *testing.T, before, after deviceProtocolSnap) {
	t.Helper()
	if before != after {
		t.Fatalf("table counts changed: before=%+v after=%+v", before, after)
	}
}

func assertDeviceProtocolDeviceSnapUnchanged(t *testing.T, before, after deviceProtocolDeviceSnap) {
	t.Helper()
	if before != after {
		t.Fatalf("device row changed: before=%+v after=%+v", before, after)
	}
}

func assertDeviceProtocolScanEffect(t *testing.T, db *sql.DB, house, deviceID string, rec *httptest.ResponseRecorder, body string) {
	t.Helper()
	type scanItem struct {
		ID      string `json:"id"`
		Barcode string `json:"barcode"`
	}
	var req struct {
		ID    string     `json:"id"`
		Barcode string `json:"barcode"`
		Scans []scanItem `json:"scans"`
	}
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatal(err)
	}
	if len(req.Scans) > 0 {
		var resp struct {
			Results []struct {
				ID     string `json:"id"`
				Status string `json:"status"`
			} `json:"results"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		if len(resp.Results) != len(req.Scans) {
			t.Fatalf("results len=%d want=%d", len(resp.Results), len(req.Scans))
		}
		for _, item := range req.Scans {
			var n int
			if err := db.QueryRow(`
				SELECT COUNT(*) FROM scan_entries
				WHERE household_id = ? AND device_id = ? AND client_scan_id = ?`,
				house, deviceID, item.ID).Scan(&n); err != nil || n != 1 {
				t.Fatalf("scan row for %q count=%d err=%v", item.ID, n, err)
			}
		}
		return
	}
	if req.ID == "" {
		t.Fatal("scan body missing id")
	}
	var resp struct {
		ID      string `json:"id"`
		Status  string `json:"status"`
		Barcode string `json:"barcode"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.ID != req.ID || resp.Status == "" {
		t.Fatalf("scan response %#v", resp)
	}
	var storedBarcode string
	if err := db.QueryRow(`
		SELECT barcode FROM scan_entries
		WHERE household_id = ? AND device_id = ? AND client_scan_id = ?`,
		house, deviceID, req.ID).Scan(&storedBarcode); err != nil {
		t.Fatalf("scan row: %v", err)
	}
	wantBarcode := strings.TrimSpace(req.Barcode)
	if storedBarcode != wantBarcode && storedBarcode != strings.TrimSpace(resp.Barcode) {
		t.Fatalf("stored barcode=%q response=%q body=%q", storedBarcode, resp.Barcode, wantBarcode)
	}
}

func assertDeviceProtocolHeartbeatEffect(t *testing.T, db *sql.DB, house, deviceID string, rec *httptest.ResponseRecorder, body string) {
	t.Helper()
	var req struct {
		Version    string `json:"version"`
		BootID     string `json:"bootId"`
		Queued     int    `json:"queued"`
		BatteryPct *int   `json:"batteryPct"`
	}
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatal(err)
	}
	var resp struct {
		HeartbeatSeconds int `json:"heartbeatSeconds"`
		Config           struct {
			DefaultDirection string `json:"defaultDirection"`
		} `json:"config"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.HeartbeatSeconds != 60 || resp.Config.DefaultDirection == "" {
		t.Fatalf("heartbeat response %#v", resp)
	}
	var queued int
	var bootID, hubVersion string
	var battery sql.NullInt64
	if err := db.QueryRow(`
		SELECT queued, boot_id, hub_version, battery_pct
		FROM devices WHERE household_id = ? AND id = ?`, house, deviceID).Scan(&queued, &bootID, &hubVersion, &battery); err != nil {
		t.Fatal(err)
	}
	if queued != req.Queued {
		t.Fatalf("queued=%d want=%d", queued, req.Queued)
	}
	if bootID != req.BootID {
		t.Fatalf("boot_id=%q want=%q", bootID, req.BootID)
	}
	if req.Version != "" && hubVersion != req.Version {
		t.Fatalf("hub_version=%q want=%q", hubVersion, req.Version)
	}
	if req.BatteryPct != nil {
		if !battery.Valid || int(battery.Int64) != *req.BatteryPct {
			t.Fatalf("battery_pct=%v want=%d", battery, *req.BatteryPct)
		}
	}
}

// TestStage1DeviceProtocolRequestVectors replays committed firmware request bodies
// through the production stage1 HTTP router.
func TestStage1DeviceProtocolRequestVectors(t *testing.T) {
	vectors := loadDeviceProtocolRequestVectors(t)
	seen := make(map[string]struct{}, len(vectors))

	for _, v := range vectors {
		v := v
		if _, dup := seen[v.Name]; dup {
			t.Fatalf("duplicate vector name %q", v.Name)
		}
		seen[v.Name] = struct{}{}

		t.Run(v.Name, func(t *testing.T) {
			want := *v.ExpectedStatus
			if want == http.StatusBadRequest {
				if _, ok := deviceProtocol400ErrorCodes[v.Name]; !ok {
					t.Fatalf("vector %q expectedStatus 400 has no error code map entry", v.Name)
				}
			}

			db, _, _, caller, house, userID := stage1Owner(t, "Vector Owner", "vector-"+v.Name+"@pantry.test")

			var deviceToken, deviceID string
			var pendingPairHash string

			if v.Endpoint == "/api/devices/pair/complete" {
				code, ok := pairingCodeInBody(v.Body)
				if !ok {
					t.Fatal("pairing body missing code")
				}
				if _, well, check := auth.NormalizePairingCode(code); well && check {
					pendingPairHash = seedPendingPairingCode(t, db, house, userID, code)
				} else if v.Name == "pair-escape-unicode" {
					pendingPairHash = seedPendingPairingCode(t, db, house, userID, "SBP-0123-ABCD-G")
				}
			} else if strings.HasPrefix(v.Endpoint, "/api/device/") {
				deviceToken, deviceID = deviceProtocolPairedScanner(t, caller)
			}

			before := deviceProtocolTableSnap(t, db, house)
			var beforeDev deviceProtocolDeviceSnap
			if deviceID != "" {
				beforeDev = readDeviceProtocolDeviceSnap(t, db, house, deviceID)
			}

			rec := caller.callRaw(v.Method, v.Endpoint, v.Body, app.EntryPublic, deviceProtocolHeaders(deviceToken))
			if rec.Code != want {
				t.Fatalf("status=%d want=%d body=%s", rec.Code, want, rec.Body.String())
			}
			if v.DeviceReachable && (rec.Code < 200 || rec.Code >= 300) {
				t.Fatalf("deviceReachable=true but status=%d", rec.Code)
			}

			if want == http.StatusBadRequest {
				wantCode := deviceProtocol400ErrorCodes[v.Name]
				var errBody struct {
					Code string `json:"code"`
				}
				if err := json.Unmarshal(rec.Body.Bytes(), &errBody); err != nil {
					t.Fatalf("decode error body: %v", err)
				}
				if errBody.Code != wantCode {
					t.Fatalf("error code=%q want=%q body=%s", errBody.Code, wantCode, rec.Body.String())
				}
				after := deviceProtocolTableSnap(t, db, house)
				assertDeviceProtocolSnapUnchanged(t, before, after)
				if deviceID != "" {
					afterDev := readDeviceProtocolDeviceSnap(t, db, house, deviceID)
					assertDeviceProtocolDeviceSnapUnchanged(t, beforeDev, afterDev)
				}
				if pendingPairHash != "" && deviceProtocolPairingConsumed(t, db, pendingPairHash) {
					t.Fatal("pairing code was consumed on validation failure")
				}
				return
			}

			switch {
			case v.Endpoint == "/api/devices/pair/complete":
				var paired struct {
					Token    string `json:"token"`
					DeviceID string `json:"deviceId"`
				}
				if err := json.Unmarshal(rec.Body.Bytes(), &paired); err != nil || paired.Token == "" || paired.DeviceID == "" {
					t.Fatalf("pair response %s err=%v", rec.Body.String(), err)
				}
				after := deviceProtocolTableSnap(t, db, house)
				if after.Devices != before.Devices+1 || after.PairingsCompleted != before.PairingsCompleted+1 {
					t.Fatalf("pair side effect before=%+v after=%+v", before, after)
				}
				if pendingPairHash != "" && !deviceProtocolPairingConsumed(t, db, pendingPairHash) {
					t.Fatal("pairing code not marked complete")
				}
			case v.Endpoint == "/api/device/scans":
				assertDeviceProtocolScanEffect(t, db, house, deviceID, rec, v.Body)
			case v.Endpoint == "/api/device/heartbeat":
				assertDeviceProtocolHeartbeatEffect(t, db, house, deviceID, rec, v.Body)
			}
		})
	}

	if len(seen) != len(vectors) {
		t.Fatalf("exercised %d vectors, file has %d", len(seen), len(vectors))
	}
}
