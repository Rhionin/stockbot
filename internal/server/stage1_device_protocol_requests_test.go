package server

import (
	"database/sql"
	"encoding/json"
	"net/http"
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

func seedPendingPairingCode(t *testing.T, db *sql.DB, householdID, userID, rawCode string) {
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
}

func deviceProtocolHeaders(endpoint, deviceToken string) map[string]string {
	if strings.HasPrefix(endpoint, "/api/device/") && deviceToken != "" {
		return map[string]string{"Authorization": "Bearer " + deviceToken}
	}
	return nil
}

// TestStage1DeviceProtocolRequestVectors replays committed firmware request bodies
// through the production stage1 HTTP router.
func TestStage1DeviceProtocolRequestVectors(t *testing.T) {
	vectors := loadDeviceProtocolRequestVectors(t)
	db, _, handler, _, house, userID := stage1Owner(t, "Vector Owner", "vector-owner@pantry.test")
	caller := &stage1Caller{t: t, h: handler}

	var deviceToken string
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

			if v.Endpoint == "/api/devices/pair/complete" {
				code, ok := pairingCodeInBody(v.Body)
				if !ok {
					t.Fatal("pairing body missing code")
				}
				if _, well, check := auth.NormalizePairingCode(code); well && check {
					seedPendingPairingCode(t, db, house, userID, code)
				}
			}

			rec := caller.callRaw(v.Method, v.Endpoint, v.Body, app.EntryPublic, deviceProtocolHeaders(v.Endpoint, deviceToken))
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
			}

			if v.Endpoint == "/api/devices/pair/complete" && want == http.StatusCreated {
				var paired struct {
					Token string `json:"token"`
				}
				if err := json.Unmarshal(rec.Body.Bytes(), &paired); err != nil || paired.Token == "" {
					t.Fatalf("pair response %s err=%v", rec.Body.String(), err)
				}
				deviceToken = paired.Token
			}
		})
	}

	if len(seen) != len(vectors) {
		t.Fatalf("exercised %d vectors, file has %d", len(seen), len(vectors))
	}
}
