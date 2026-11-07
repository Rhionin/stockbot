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

// TestStage1UnknownDeviceDeleteReturnsNotFound (#449): an unknown device id in the
// active household is not_found, not forbidden or unauthorized.
func TestStage1UnknownDeviceDeleteReturnsNotFound(t *testing.T) {
	_, _, _, owner, _, _ := stage1Owner(t, "Rowan Owner", "rowan-owner@pantry.test")
	const unknownDevice = "01909999-9999-7000-8000-000000000099"
	del := owner.authed(http.MethodDelete, "/api/devices/"+unknownDevice, nil)
	if del.Code != http.StatusNotFound || !strings.Contains(del.Body.String(), `"code":"not_found"`) {
		t.Fatalf("DELETE unknown device %d %s", del.Code, del.Body.String())
	}
	if strings.Contains(del.Body.String(), `"code":"forbidden_role"`) {
		t.Fatalf("unknown device must not surface role: %s", del.Body.String())
	}
	devices := owner.authed(http.MethodGet, "/api/devices", nil)
	if devices.Code != http.StatusOK {
		t.Fatalf("list devices %d %s", devices.Code, devices.Body.String())
	}
}

// TestStage1MemberDeleteSameHouseholdForbiddenLeavesHubUnrevoked (#449, #386):
// a member's DELETE on an owner-paired hub in the same household is
// forbidden_role and leaves the hub token, queue path, and revoke state intact.
func TestStage1MemberDeleteSameHouseholdForbiddenLeavesHubUnrevoked(t *testing.T) {
	db, _, handler, owner, house, _ := stage1Owner(t, "Elm Owner", "elm-owner@pantry.test")

	pair := owner.authed(http.MethodPost, "/api/devices/pair", map[string]any{})
	if pair.Code != http.StatusCreated {
		t.Fatalf("pair %d %s", pair.Code, pair.Body.String())
	}
	var pairing auth.PairingCode
	if err := json.Unmarshal(pair.Body.Bytes(), &pairing); err != nil {
		t.Fatal(err)
	}
	done := owner.anon(http.MethodPost, "/api/devices/pair/complete", map[string]any{
		"code": pairing.Code, "label": "elm-hub-q7",
		"hub": map[string]string{"kind": "pi_hub", "version": "1.0.0", "bootId": "boot-elm-q7"},
	}, app.EntryPublic)
	if done.Code != http.StatusCreated {
		t.Fatalf("complete %d %s", done.Code, done.Body.String())
	}
	var hub struct {
		DeviceID string `json:"deviceId"`
		Token    string `json:"token"`
	}
	if err := json.Unmarshal(done.Body.Bytes(), &hub); err != nil || hub.DeviceID == "" || !strings.HasPrefix(hub.Token, "sbd_") {
		t.Fatalf("paired %s", done.Body.String())
	}
	revokeAuditsBefore := auditCountHousehold(t, db, house, "device.revoked")

	invite := owner.authed(http.MethodPost, "/api/invites", map[string]string{"role": "member"})
	var memberInvite auth.InviteCreated
	if err := json.Unmarshal(invite.Body.Bytes(), &memberInvite); err != nil || invite.Code != http.StatusCreated {
		t.Fatalf("member invite %d %s", invite.Code, invite.Body.String())
	}
	accepted := owner.anon(http.MethodPost, "/api/invites/accept", map[string]any{
		"code": memberInvite.Code, "account": map[string]string{
			"name": "Elm Member", "email": "elm-member@pantry.test", "password": "correct horse",
		},
	}, app.EntryPublic)
	if accepted.Code != http.StatusOK {
		t.Fatalf("accept member %d %s", accepted.Code, accepted.Body.String())
	}
	member := &stage1Caller{t: t, h: handler, cookie: cookieFrom(t, accepted, stage1Site(t).SessionCookie().Name), house: house}

	del := member.authed(http.MethodDelete, "/api/devices/"+hub.DeviceID, nil)
	if del.Code != http.StatusForbidden || !strings.Contains(del.Body.String(), `"code":"forbidden_role"`) {
		t.Fatalf("member delete same household %d %s", del.Code, del.Body.String())
	}
	if n := auditCountHousehold(t, db, house, "device.revoked"); n != revokeAuditsBefore {
		t.Fatalf("device.revoked audits %d want %d after forbidden delete", n, revokeAuditsBefore)
	}
	var revoked sql.NullString
	if err := db.QueryRow(`SELECT revoked_at FROM devices WHERE id = ? AND household_id = ?`, hub.DeviceID, house).Scan(&revoked); err != nil {
		t.Fatal(err)
	}
	if revoked.Valid {
		t.Fatalf("hub revoked_at %q after member forbidden delete", revoked.String)
	}
	beat := member.call(http.MethodPost, "/api/device/heartbeat", map[string]any{"version": "1"}, app.EntryPublic, map[string]string{
		"Authorization": "Bearer " + hub.Token,
	})
	if beat.Code != http.StatusOK {
		t.Fatalf("hub heartbeat after forbidden delete %d %s", beat.Code, beat.Body.String())
	}
	list := owner.authed(http.MethodGet, "/api/devices", nil)
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), "elm-hub-q7") || strings.Contains(list.Body.String(), `"revokedAt":"`) {
		t.Fatalf("devices list %d %s", list.Code, list.Body.String())
	}
}
