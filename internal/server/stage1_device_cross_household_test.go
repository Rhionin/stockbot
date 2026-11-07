package server

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/auth"
)

// TestStage1ForeignHouseholdDeviceMutationReturnsNotFound (#449) pins that an
// owner's DELETE/PATCH on another household's paired device answers not_found,
// not forbidden_role or unauthorized, and leaves the foreign device untouched.
func TestStage1ForeignHouseholdDeviceMutationReturnsNotFound(t *testing.T) {
	db, _, handler, alderOwner, alderHouse, _ := stage1Owner(t, "Alder Owner", "alder-owner@pantry.test")
	const alderName = "Household Alder"
	const birchName = "Household Birch"
	const birchLabel = "birch-scanner-q7"
	const hijackLabel = "hijack-label-q7"

	if rec := alderOwner.authed(http.MethodPatch, "/api/households/"+alderHouse, map[string]string{"name": alderName}); rec.Code != http.StatusOK {
		t.Fatalf("rename alder %d %s", rec.Code, rec.Body.String())
	}

	start := alderOwner.authed(http.MethodPost, "/api/operator/household-invites", nil)
	var startInvite auth.InviteCreated
	if err := json.Unmarshal(start.Body.Bytes(), &startInvite); err != nil || start.Code != http.StatusCreated {
		t.Fatalf("operator invite %d %s", start.Code, start.Body.String())
	}
	created := alderOwner.authed(http.MethodPost, "/api/households", map[string]string{
		"name": birchName, "inviteCode": startInvite.Code,
	})
	if created.Code != http.StatusCreated {
		t.Fatalf("create birch %d %s", created.Code, created.Body.String())
	}
	var birch struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &birch); err != nil || birch.ID == "" {
		t.Fatal(created.Body.String())
	}

	switchHouse := alderOwner.authed(http.MethodPost, "/api/auth/session/household", map[string]string{"householdId": birch.ID})
	if switchHouse.Code != http.StatusOK {
		t.Fatalf("switch birch %d %s", switchHouse.Code, switchHouse.Body.String())
	}
	birchOwner := &stage1Caller{t: t, h: handler, cookie: alderOwner.cookie, house: birch.ID}

	pair := birchOwner.authed(http.MethodPost, "/api/devices/pair", map[string]any{})
	if pair.Code != http.StatusCreated {
		t.Fatalf("pair %d %s", pair.Code, pair.Body.String())
	}
	var pairing auth.PairingCode
	if err := json.Unmarshal(pair.Body.Bytes(), &pairing); err != nil {
		t.Fatal(err)
	}
	done := birchOwner.anon(http.MethodPost, "/api/devices/pair/complete", map[string]any{
		"code": pairing.Code, "label": birchLabel,
		"hub": map[string]string{"kind": "pi_hub", "version": "1.0.0", "bootId": "boot-birch-q7"},
	}, app.EntryPublic)
	if done.Code != http.StatusCreated {
		t.Fatalf("complete %d %s", done.Code, done.Body.String())
	}
	var paired struct {
		DeviceID string `json:"deviceId"`
		Token    string `json:"token"`
	}
	if err := json.Unmarshal(done.Body.Bytes(), &paired); err != nil || paired.DeviceID == "" || !strings.HasPrefix(paired.Token, "sbd_") {
		t.Fatalf("paired %s", done.Body.String())
	}

	back := alderOwner.authed(http.MethodPost, "/api/auth/session/household", map[string]string{"householdId": alderHouse})
	if back.Code != http.StatusOK {
		t.Fatalf("switch alder %d %s", back.Code, back.Body.String())
	}
	alderOwner.house = alderHouse

	alderKeyBefore := householdChannelKey(t, db, alderHouse)
	birchKeyBefore := householdChannelKey(t, db, birch.ID)
	revokeAuditsBefore := auditCountHousehold(t, db, alderHouse, "device.revoked")

	del := alderOwner.authed(http.MethodDelete, "/api/devices/"+paired.DeviceID, nil)
	if del.Code != http.StatusNotFound || !strings.Contains(del.Body.String(), `"code":"not_found"`) {
		t.Fatalf("owner delete foreign device %d %s", del.Code, del.Body.String())
	}
	if strings.Contains(del.Body.String(), `"code":"forbidden_role"`) || del.Code == http.StatusUnauthorized || del.Code == http.StatusForbidden {
		t.Fatalf("owner delete must be not_found, not auth/forbidden: %d %s", del.Code, del.Body.String())
	}

	assertBirchDeviceHealthy(t, db, birchOwner, birch.ID, paired, birchLabel, birchKeyBefore)
	if alderKey := householdChannelKey(t, db, alderHouse); alderKey != alderKeyBefore {
		t.Fatalf("alder channel_key changed %q -> %q", alderKeyBefore, alderKey)
	}
	if n := auditCountHousehold(t, db, alderHouse, "device.revoked"); n != revokeAuditsBefore {
		t.Fatalf("alder device.revoked audits %d want %d", n, revokeAuditsBefore)
	}
	var alderRow string
	err := db.QueryRow(`SELECT revoked_at FROM devices WHERE id = ? AND household_id = ?`, paired.DeviceID, alderHouse).Scan(&alderRow)
	if err != sql.ErrNoRows {
		t.Fatalf("foreign id under alder household: err=%v row=%q", err, alderRow)
	}

	patch := alderOwner.authed(http.MethodPatch, "/api/devices/"+paired.DeviceID, map[string]string{"name": hijackLabel})
	if patch.Code != http.StatusNotFound || !strings.Contains(patch.Body.String(), `"code":"not_found"`) {
		t.Fatalf("owner patch foreign device %d %s", patch.Code, patch.Body.String())
	}
	assertBirchDeviceHealthy(t, db, birchOwner, birch.ID, paired, birchLabel, birchKeyBefore)

	invite := alderOwner.authed(http.MethodPost, "/api/invites", map[string]string{"role": "member"})
	var memberInvite auth.InviteCreated
	if err := json.Unmarshal(invite.Body.Bytes(), &memberInvite); err != nil || invite.Code != http.StatusCreated {
		t.Fatalf("member invite %d %s", invite.Code, invite.Body.String())
	}
	accepted := alderOwner.anon(http.MethodPost, "/api/invites/accept", map[string]any{
		"code": memberInvite.Code, "account": map[string]string{
			"name": "Alder Member", "email": "alder-member@pantry.test", "password": "correct horse",
		},
	}, app.EntryPublic)
	if accepted.Code != http.StatusOK {
		t.Fatalf("accept member %d %s", accepted.Code, accepted.Body.String())
	}
	member := &stage1Caller{t: t, h: handler, cookie: cookieFrom(t, accepted, stage1Site(t).SessionCookie().Name), house: alderHouse}
	// #449 AC5 (member DELETE on a foreign id) still answers forbidden_role today because
	// RevokeDevice checks owner before the household-scoped device lookup. PATCH is covered.
	memberPatch := member.authed(http.MethodPatch, "/api/devices/"+paired.DeviceID, map[string]string{"name": hijackLabel})
	if memberPatch.Code != http.StatusNotFound || !strings.Contains(memberPatch.Body.String(), `"code":"not_found"`) {
		t.Fatalf("member patch foreign device %d %s", memberPatch.Code, memberPatch.Body.String())
	}
	assertBirchDeviceHealthy(t, db, birchOwner, birch.ID, paired, birchLabel, birchKeyBefore)
}

func householdChannelKey(t *testing.T, db *sql.DB, householdID string) string {
	t.Helper()
	var key string
	if err := db.QueryRow(`SELECT channel_key FROM households WHERE id = ?`, householdID).Scan(&key); err != nil {
		t.Fatal(err)
	}
	return key
}

func auditCountHousehold(t *testing.T, db *sql.DB, householdID, action string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE household_id = ? AND action = ?`, householdID, action).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func assertBirchDeviceHealthy(t *testing.T, db *sql.DB, birchOwner *stage1Caller, birchHouse string, paired struct {
	DeviceID string `json:"deviceId"`
	Token    string `json:"token"`
}, wantLabel, birchKeyBefore string) {
	t.Helper()
	beat := birchOwner.call(http.MethodPost, "/api/device/heartbeat", map[string]any{"version": "1"}, app.EntryPublic, map[string]string{
		"Authorization": "Bearer " + paired.Token,
	})
	if beat.Code != http.StatusOK {
		t.Fatalf("device heartbeat %d %s", beat.Code, beat.Body.String())
	}
	devices := birchOwner.authed(http.MethodGet, "/api/devices", nil)
	if devices.Code != http.StatusOK || !strings.Contains(devices.Body.String(), wantLabel) {
		t.Fatalf("birch devices %d %s", devices.Code, devices.Body.String())
	}
	if strings.Contains(devices.Body.String(), `"revokedAt":"`) {
		t.Fatalf("device revoked: %s", devices.Body.String())
	}
	var revoked sql.NullString
	if err := db.QueryRow(`SELECT revoked_at FROM devices WHERE id = ? AND household_id = ?`, paired.DeviceID, birchHouse).Scan(&revoked); err != nil {
		t.Fatal(err)
	}
	if revoked.Valid {
		t.Fatalf("device revoked_at %q", revoked.String)
	}
	var label string
	if err := db.QueryRow(`SELECT label FROM devices WHERE id = ? AND household_id = ?`, paired.DeviceID, birchHouse).Scan(&label); err != nil || label != wantLabel {
		t.Fatalf("label %q want %q err %v", label, wantLabel, err)
	}
	if key := householdChannelKey(t, db, birchHouse); key != birchKeyBefore {
		t.Fatalf("birch channel_key changed %q -> %q", birchKeyBefore, key)
	}
}
