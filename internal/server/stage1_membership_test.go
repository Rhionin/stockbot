package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/Rhionin/stockbot/internal/events"
)

func TestStage1MembershipInvitesDevicesAndRecovery(t *testing.T) {
	db := openMigratedDB(t, filepath.Join(t.TempDir(), "pantry.db"))
	setupDir := t.TempDir()
	svc := auth.NewService(db, setupDir)
	site := stage1Site(t)
	handler, _ := setupOnDB(t, db, nil, WithStage1(svc, site))
	c := &stage1Caller{t: t, h: handler}

	cfg := c.call(http.MethodGet, "/api/config", nil, app.EntryPublic, nil)
	if cfg.Code != http.StatusOK || !strings.Contains(cfg.Body.String(), `"live":"sse"`) || !strings.Contains(cfg.Body.String(), `"entry":"public"`) {
		t.Fatalf("config %d %s", cfg.Code, cfg.Body.String())
	}
	signedOut := c.call(http.MethodGet, "/api/auth/session", nil, app.EntryPublic, nil)
	if signedOut.Code != http.StatusOK || !strings.Contains(signedOut.Body.String(), `"kind":"signed_out"`) {
		t.Fatalf("signed out %d %s", signedOut.Code, signedOut.Body.String())
	}
	if rec := c.call(http.MethodGet, "/api/auth/login", nil, app.EntryPublic, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("stage3 login %d", rec.Code)
	}
	if rec := c.call(http.MethodGet, "/api/auth/callback", nil, app.EntryPublic, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("stage3 callback %d", rec.Code)
	}
	oldClient := c.call(http.MethodGet, "/api/inventory", nil, app.EntryPublic, map[string]string{"X-Stockbot-Client": "expo/0.9.0"})
	if oldClient.Code != http.StatusUpgradeRequired {
		t.Fatalf("upgrade %d %s", oldClient.Code, oldClient.Body.String())
	}

	if rec := c.call(http.MethodGet, "/setup", nil, app.EntryPublic, nil); rec.Code != http.StatusOK {
		t.Fatalf("setup page %d %s", rec.Code, rec.Body.String())
	}
	code, err := auth.ReadSetupCode(setupDir)
	if err != nil {
		t.Fatal(err)
	}
	if rec := c.call(http.MethodPost, "/setup", map[string]string{"setupCode": code}, app.EntryPublic, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("incomplete setup %d", rec.Code)
	}
	if rec := c.call(http.MethodPost, "/setup", map[string]string{"setupCode": "not-a-code"}, app.EntryPublic, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed setup %d %s", rec.Code, rec.Body.String())
	}
	if rec := c.call(http.MethodPost, "/setup", map[string]string{
		"setupCode": "AAAA-AAAA", "name": "Ada Lovelace", "email": "ada@pantry.test", "password": "correct horse",
	}, app.EntryPublic, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("bad setup code %d %s", rec.Code, rec.Body.String())
	}
	if rec := c.call(http.MethodPost, "/setup", map[string]string{
		"setupCode": code, "name": "Ada Lovelace", "email": "Ada@Pantry.Test", "password": "correct horse",
	}, app.EntryPublic, nil); rec.Code != http.StatusOK {
		t.Fatalf("setup %d %s", rec.Code, rec.Body.String())
	}
	if rec := c.call(http.MethodGet, "/setup", nil, app.EntryPublic, nil); rec.Code != http.StatusFound {
		t.Fatalf("setup redirect %d", rec.Code)
	}

	if rec := c.call(http.MethodPost, "/api/auth/login", map[string]string{"email": "ada@pantry.test", "password": "short"}, app.EntryPublic, nil); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), auth.SentencePasswordShort) {
		t.Fatalf("short password %d %s", rec.Code, rec.Body.String())
	}
	if rec := c.call(http.MethodPost, "/api/auth/login", map[string]string{"email": "not an email", "password": "correct horse"}, app.EntryPublic, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad email %d %s", rec.Code, rec.Body.String())
	}
	longPass := strings.Repeat("a", 257)
	if rec := c.call(http.MethodPost, "/api/auth/login", map[string]string{"email": "ada@pantry.test", "password": longPass}, app.EntryPublic, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("long password %d %s", rec.Code, rec.Body.String())
	}
	login := c.call(http.MethodPost, "/api/auth/login", map[string]string{"email": "ada@pantry.test", "password": "correct horse"}, app.EntryPublic, map[string]string{"X-Stockbot-Client": "expo/1.2.3"})
	if login.Code != http.StatusOK || !strings.Contains(login.Body.String(), `"token":"sbs_`) {
		t.Fatalf("expo login %d %s", login.Code, login.Body.String())
	}
	var session auth.SessionView
	if err := json.Unmarshal(login.Body.Bytes(), &session); err != nil || session.User == nil {
		t.Fatalf("session %s %v", login.Body.String(), err)
	}
	ada := cookieFrom(t, login, site.SessionCookie().Name)
	adaID := session.User.ID
	house := *session.ActiveHouseholdID
	c.cookie = ada
	c.house = house
	if rec := c.call(http.MethodGet, "/api/auth/session", nil, app.EntryPublic, map[string]string{"Authorization": "Bearer " + session.Token}); rec.Code != http.StatusOK {
		t.Fatalf("bearer session %d %s", rec.Code, rec.Body.String())
	}

	if rec := c.authed(http.MethodGet, "/api/households", nil); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), house) {
		t.Fatalf("list households %d %s", rec.Code, rec.Body.String())
	}
	detail := c.authed(http.MethodGet, "/api/households/"+house, nil)
	if detail.Code != http.StatusOK {
		t.Fatalf("get household %d %s", detail.Code, detail.Body.String())
	}
	var household auth.HouseholdDetail
	if err := json.Unmarshal(detail.Body.Bytes(), &household); err != nil {
		t.Fatal(err)
	}
	renamed := c.authed(http.MethodPatch, "/api/households/"+house, map[string]string{"name": "Primary Pantry"})
	if renamed.Code != http.StatusOK || !strings.Contains(renamed.Body.String(), "Primary Pantry") {
		t.Fatalf("rename %d %s", renamed.Code, renamed.Body.String())
	}
	if rec := c.authed(http.MethodPatch, "/api/household/settings", map[string]string{"restockMode": "ask_restock"}); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "ask_restock") {
		t.Fatalf("restock %d %s", rec.Code, rec.Body.String())
	}
	if rec := c.authed(http.MethodGet, "/api/households/"+house+"/members", nil); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), adaID) {
		t.Fatalf("members %d %s", rec.Code, rec.Body.String())
	}

	invite := c.authed(http.MethodPost, "/api/invites", map[string]string{"role": "member"})
	if invite.Code != http.StatusCreated {
		t.Fatalf("invite %d %s", invite.Code, invite.Body.String())
	}
	var created auth.InviteCreated
	if err := json.Unmarshal(invite.Body.Bytes(), &created); err != nil || created.Code == "" {
		t.Fatalf("invite body %s", invite.Body.String())
	}
	listed := c.authed(http.MethodGet, "/api/invites", nil)
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), `"status":"open"`) {
		t.Fatalf("list invites %d %s", listed.Code, listed.Body.String())
	}
	if rec := c.call(http.MethodPost, "/api/invites/accept", map[string]any{"code": created.Code, "role": "owner", "account": map[string]string{"name": "Bea", "email": "bea@pantry.test", "password": "correct horse"}}, app.EntryPublic, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("role on accept %d %s", rec.Code, rec.Body.String())
	}
	accepted := c.anon(http.MethodPost, "/api/invites/accept", map[string]any{
		"code": created.Code, "account": map[string]string{"name": "Bea", "email": "bea@pantry.test", "password": "correct horse"},
	}, app.EntryPublic)
	if accepted.Code != http.StatusOK || !strings.Contains(accepted.Body.String(), `"role":"member"`) {
		t.Fatalf("accept %d %s", accepted.Code, accepted.Body.String())
	}
	bea := cookieFrom(t, accepted, site.SessionCookie().Name)
	var beaSession struct {
		User struct {
			ID string `json:"id"`
		} `json:"user"`
	}
	if err := json.Unmarshal(accepted.Body.Bytes(), &beaSession); err != nil || beaSession.User.ID == "" {
		t.Fatalf("bea %s", accepted.Body.String())
	}
	if rec := c.call(http.MethodPost, "/api/invites/preview", map[string]string{"code": created.Code}, app.EntryPublic, nil); rec.Code != http.StatusConflict {
		t.Fatalf("used preview %d %s", rec.Code, rec.Body.String())
	}
	again := c.anon(http.MethodPost, "/api/invites/accept", map[string]any{
		"code": created.Code, "account": map[string]string{"name": "Cam", "email": "cam@pantry.test", "password": "correct horse"},
	}, app.EntryPublic)
	if again.Code != http.StatusConflict {
		t.Fatalf("reuse invite %d %s", again.Code, again.Body.String())
	}
	exists := c.authed(http.MethodPost, "/api/invites", map[string]string{"role": "member"})
	var existsInvite auth.InviteCreated
	if err := json.Unmarshal(exists.Body.Bytes(), &existsInvite); err != nil {
		t.Fatal(err)
	}
	dup := c.anon(http.MethodPost, "/api/invites/accept", map[string]any{
		"code": existsInvite.Code, "account": map[string]string{"name": "Ada Again", "email": "ada@pantry.test", "password": "correct horse"},
	}, app.EntryPublic)
	if dup.Code != http.StatusConflict || !strings.Contains(dup.Body.String(), auth.SentenceAccountExists) {
		t.Fatalf("account exists %d %s", dup.Code, dup.Body.String())
	}

	c.cookie = bea
	if rec := c.authed(http.MethodPatch, "/api/households/"+house, map[string]string{"name": "Nope"}); rec.Code != http.StatusForbidden {
		t.Fatalf("member rename %d %s", rec.Code, rec.Body.String())
	}
	if rec := c.authed(http.MethodPost, "/api/invites", map[string]string{"role": "owner"}); rec.Code != http.StatusForbidden {
		t.Fatalf("member owner invite %d %s", rec.Code, rec.Body.String())
	}
	if rec := c.authed(http.MethodPost, "/api/households/"+house+"/members/"+adaID+"/reset-link", nil); rec.Code != http.StatusForbidden {
		t.Fatalf("member reset link %d %s", rec.Code, rec.Body.String())
	}
	c.cookie = ada
	promoted := c.authed(http.MethodPatch, "/api/households/"+house+"/members/"+beaSession.User.ID, map[string]string{"role": "owner"})
	if promoted.Code != http.StatusOK || !strings.Contains(promoted.Body.String(), `"role":"owner"`) {
		t.Fatalf("promote %d %s", promoted.Code, promoted.Body.String())
	}
	if rec := c.authed(http.MethodPatch, "/api/households/"+house+"/members/"+adaID, map[string]string{"role": "guest"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad role %d %s", rec.Code, rec.Body.String())
	}
	demoted := c.authed(http.MethodPatch, "/api/households/"+house+"/members/"+adaID, map[string]string{"role": "member"})
	if demoted.Code != http.StatusOK {
		t.Fatalf("demote %d %s", demoted.Code, demoted.Body.String())
	}
	c.cookie = bea
	if rec := c.authed(http.MethodPatch, "/api/households/"+house+"/members/"+beaSession.User.ID, map[string]string{"role": "member"}); rec.Code != http.StatusConflict {
		t.Fatalf("last owner demote %d %s", rec.Code, rec.Body.String())
	}
	if rec := c.authed(http.MethodPatch, "/api/households/"+house+"/members/"+adaID, map[string]string{"role": "owner"}); rec.Code != http.StatusOK {
		t.Fatalf("repromote %d %s", rec.Code, rec.Body.String())
	}
	c.cookie = ada

	reset := c.authed(http.MethodPost, "/api/households/"+house+"/members/"+beaSession.User.ID+"/reset-link", nil)
	if reset.Code != http.StatusCreated || !strings.Contains(reset.Body.String(), "/reset/") {
		t.Fatalf("reset link %d %s", reset.Code, reset.Body.String())
	}
	var link struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(reset.Body.Bytes(), &link); err != nil {
		t.Fatal(err)
	}
	resetCode := link.URL[strings.LastIndex(link.URL, "/")+1:]
	if rec := c.call(http.MethodPost, "/api/auth/reset", map[string]string{"code": resetCode, "password": "short"}, app.EntryPublic, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("short reset %d %s", rec.Code, rec.Body.String())
	}
	if rec := c.call(http.MethodPost, "/api/auth/reset", map[string]string{"code": "sbr_missing", "password": "brand new horse"}, app.EntryPublic, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown reset %d %s", rec.Code, rec.Body.String())
	}
	resetDone := c.call(http.MethodPost, "/api/auth/reset", map[string]string{"code": resetCode, "password": "brand new horse"}, app.EntryPublic, nil)
	if resetDone.Code != http.StatusOK {
		t.Fatalf("reset %d %s", resetDone.Code, resetDone.Body.String())
	}
	stale := stage1Do(handler, http.MethodGet, "/api/auth/session", bea, nil, app.EntryPublic, "")
	if stale.Code != http.StatusUnauthorized {
		t.Fatalf("revoked session %d %s", stale.Code, stale.Body.String())
	}
	if rec := c.call(http.MethodPost, "/api/auth/reset", map[string]string{"code": resetCode, "password": "brand new horse"}, app.EntryPublic, nil); rec.Code != http.StatusConflict {
		t.Fatalf("used reset %d %s", rec.Code, rec.Body.String())
	}
	beaLogin := c.call(http.MethodPost, "/api/auth/login", map[string]string{"email": "bea@pantry.test", "password": "brand new horse"}, app.EntryPublic, nil)
	if beaLogin.Code != http.StatusOK {
		t.Fatalf("bea login %d %s", beaLogin.Code, beaLogin.Body.String())
	}
	bea = cookieFrom(t, beaLogin, site.SessionCookie().Name)

	start := c.authed(http.MethodPost, "/api/operator/household-invites", nil)
	if start.Code != http.StatusCreated {
		t.Fatalf("start invite %d %s", start.Code, start.Body.String())
	}
	var startInvite auth.InviteCreated
	if err := json.Unmarshal(start.Body.Bytes(), &startInvite); err != nil {
		t.Fatal(err)
	}
	if rec := c.authed(http.MethodPost, "/api/households", map[string]string{"name": "Cabin"}); rec.Code != http.StatusForbidden {
		t.Fatalf("invite required %d %s", rec.Code, rec.Body.String())
	}
	if rec := c.authed(http.MethodPost, "/api/households", map[string]string{"name": "Cabin", "restockMode": "nope", "inviteCode": startInvite.Code}); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad restock %d %s", rec.Code, rec.Body.String())
	}
	createdHouse := c.authed(http.MethodPost, "/api/households", map[string]string{"name": "Cabin", "restockMode": "ask_hold", "inviteCode": startInvite.Code})
	if createdHouse.Code != http.StatusCreated {
		t.Fatalf("create household %d %s", createdHouse.Code, createdHouse.Body.String())
	}
	var cabin struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(createdHouse.Body.Bytes(), &cabin); err != nil || cabin.ID == "" {
		t.Fatalf("cabin %s", createdHouse.Body.String())
	}
	if rec := c.call(http.MethodPost, "/api/households", map[string]string{"name": "Other", "inviteCode": startInvite.Code}, app.EntryPublic, map[string]string{"Cookie": ada}); rec.Code != http.StatusNotFound && rec.Code != http.StatusConflict {
		t.Fatalf("used invite %d %s", rec.Code, rec.Body.String())
	}
	switched := c.authed(http.MethodPost, "/api/auth/session/household", map[string]string{"householdId": cabin.ID})
	if switched.Code != http.StatusOK || !strings.Contains(switched.Body.String(), cabin.ID) {
		t.Fatalf("switch %d %s", switched.Code, switched.Body.String())
	}
	if rec := c.authed(http.MethodPost, "/api/auth/session/household", map[string]string{"householdId": "0190aaaa-aaaa-7000-8000-000000000099"}); rec.Code != http.StatusNotFound {
		t.Fatalf("switch missing %d %s", rec.Code, rec.Body.String())
	}
	c.house = cabin.ID
	if rec := c.authed(http.MethodDelete, "/api/households/"+cabin.ID, map[string]string{"confirmation": "wrong"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad confirm %d %s", rec.Code, rec.Body.String())
	}
	deleted := c.authed(http.MethodDelete, "/api/households/"+cabin.ID, map[string]string{"confirmation": "Cabin"})
	if deleted.Code != http.StatusOK || !strings.Contains(deleted.Body.String(), `"deletedAt"`) {
		t.Fatalf("delete house %d %s", deleted.Code, deleted.Body.String())
	}
	restored := c.authed(http.MethodPost, "/api/households/"+cabin.ID+"/restore", nil)
	if restored.Code != http.StatusOK || strings.Contains(restored.Body.String(), `"deletedAt":"`) {
		t.Fatalf("restore %d %s", restored.Code, restored.Body.String())
	}
	c.house = house
	if rec := c.authed(http.MethodPost, "/api/operator/household-invites", nil); rec.Code != http.StatusCreated {
		t.Fatalf("second start %d %s", rec.Code, rec.Body.String())
	}
	var second auth.InviteCreated
	_ = json.Unmarshal(c.authed(http.MethodPost, "/api/operator/household-invites", nil).Body.Bytes(), &second)
	revoked := c.authed(http.MethodPost, "/api/invites/"+second.ID+"/revoke", nil)
	if revoked.Code != http.StatusNoContent {
		t.Fatalf("revoke %d %s", revoked.Code, revoked.Body.String())
	}
	signedAccept := c.authed(http.MethodPost, "/api/invites/accept", map[string]any{
		"code": second.Code, "household": map[string]string{"name": "Loft", "restockMode": "auto"},
	})
	if signedAccept.Code != http.StatusNotFound && signedAccept.Code != http.StatusConflict {
		t.Fatalf("revoked accept %d %s", signedAccept.Code, signedAccept.Body.String())
	}

	pair := c.authed(http.MethodPost, "/api/devices/pair", map[string]any{})
	if pair.Code != http.StatusCreated {
		t.Fatalf("pair %d %s", pair.Code, pair.Body.String())
	}
	var pairing auth.PairingCode
	if err := json.Unmarshal(pair.Body.Bytes(), &pairing); err != nil {
		t.Fatal(err)
	}
	status := c.authed(http.MethodGet, "/api/devices/pair/"+pairing.PairingID, nil)
	if status.Code != http.StatusOK || !strings.Contains(status.Body.String(), `"pending"`) {
		t.Fatalf("pair status %d %s", status.Code, status.Body.String())
	}
	if rec := c.call(http.MethodPost, "/api/devices/pair/complete", map[string]any{"code": "SBP-AAAA-AAAA-0"}, app.EntryPublic, nil); rec.Code != http.StatusNotFound && rec.Code != http.StatusBadRequest {
		t.Fatalf("bad pair %d %s", rec.Code, rec.Body.String())
	}
	done := c.call(http.MethodPost, "/api/devices/pair/complete", map[string]any{"code": pairing.Code, "label": "Counter"}, app.EntryPublic, nil)
	if done.Code != http.StatusCreated || strings.Contains(done.Body.String(), `"config"`) {
		t.Fatalf("scanner pair %d %s", done.Code, done.Body.String())
	}
	var paired struct {
		DeviceID string `json:"deviceId"`
		Token    string `json:"token"`
	}
	if err := json.Unmarshal(done.Body.Bytes(), &paired); err != nil || !strings.HasPrefix(paired.Token, "sbd_") {
		t.Fatalf("paired %s", done.Body.String())
	}
	after := c.authed(http.MethodGet, "/api/devices/pair/"+pairing.PairingID, nil)
	if after.Code != http.StatusOK || !strings.Contains(after.Body.String(), `"complete"`) {
		t.Fatalf("complete status %d %s", after.Code, after.Body.String())
	}
	devices := c.authed(http.MethodGet, "/api/devices", nil)
	if devices.Code != http.StatusOK || !strings.Contains(devices.Body.String(), "Counter") {
		t.Fatalf("devices %d %s", devices.Code, devices.Body.String())
	}
	if rec := c.authed(http.MethodPatch, "/api/devices/"+paired.DeviceID, map[string]string{"name": "Front door"}); rec.Code != http.StatusOK {
		t.Fatalf("rename device %d %s", rec.Code, rec.Body.String())
	}
	var directID string
	if err := db.QueryRow(`SELECT id FROM devices WHERE kind = 'direct' AND household_id = ?`, house).Scan(&directID); err != nil {
		t.Fatal(err)
	}
	if rec := c.authed(http.MethodPatch, "/api/devices/"+directID, map[string]string{"name": "Nope"}); rec.Code != http.StatusNotFound {
		t.Fatalf("direct rename %d %s", rec.Code, rec.Body.String())
	}
	dev := map[string]string{"Authorization": "Bearer " + paired.Token}
	if rec := c.call(http.MethodGet, "/api/auth/session", nil, app.EntryPublic, dev); rec.Code != http.StatusUnauthorized {
		t.Fatalf("device on session %d %s", rec.Code, rec.Body.String())
	}
	beat := c.call(http.MethodPost, "/api/device/heartbeat", map[string]any{"version": "1.2.3", "queued": 2, "droppedCount": 1}, app.EntryPublic, dev)
	if beat.Code != http.StatusOK || !strings.Contains(beat.Body.String(), `"rotateToken":false`) {
		t.Fatalf("heartbeat %d %s", beat.Code, beat.Body.String())
	}
	one := c.call(http.MethodPost, "/api/device/scans", map[string]any{"id": "scan-1", "barcode": "000111222333", "direction": "stock_in", "unitCount": 2}, app.EntryPublic, dev)
	if one.Code != http.StatusOK || !strings.Contains(one.Body.String(), `"accepted"`) {
		t.Fatalf("scan %d %s", one.Code, one.Body.String())
	}
	dupScan := c.call(http.MethodPost, "/api/device/scans", map[string]any{"id": "scan-1", "barcode": "000111222333", "direction": "stock_in", "unitCount": 2}, app.EntryPublic, dev)
	if dupScan.Code != http.StatusOK || !strings.Contains(dupScan.Body.String(), `"duplicate"`) {
		t.Fatalf("duplicate %d %s", dupScan.Code, dupScan.Body.String())
	}
	conflict := c.call(http.MethodPost, "/api/device/scans", map[string]any{"id": "scan-1", "barcode": "999", "direction": "stock_out", "unitCount": 1}, app.EntryPublic, dev)
	if conflict.Code != http.StatusConflict || !strings.Contains(conflict.Body.String(), `"code":"conflict"`) {
		t.Fatalf("conflict %d %s", conflict.Code, conflict.Body.String())
	}
	heart := c.call(http.MethodPost, "/api/device/scans", map[string]any{"id": "scan-heart", "heartbeat": true}, app.EntryPublic, dev)
	if heart.Code != http.StatusOK || !strings.Contains(heart.Body.String(), `"accepted"`) {
		t.Fatalf("heartbeat scan %d %s", heart.Code, heart.Body.String())
	}
	batch := c.call(http.MethodPost, "/api/device/scans", map[string]any{"scans": []map[string]any{
		{"id": "scan-bad", "direction": "sideways"},
		{"id": "scan-2", "barcode": "444555666777", "direction": "stock_out"},
	}}, app.EntryPublic, dev)
	if batch.Code != http.StatusOK || !strings.Contains(batch.Body.String(), `"created"`) || !strings.Contains(batch.Body.String(), `"rejected"`) {
		t.Fatalf("batch %d %s", batch.Code, batch.Body.String())
	}
	if rec := c.call(http.MethodPost, "/api/device/scans", map[string]any{"scans": []any{}, "id": "both"}, app.EntryPublic, dev); rec.Code != http.StatusBadRequest {
		t.Fatalf("both shapes %d %s", rec.Code, rec.Body.String())
	}
	if rec := c.call(http.MethodPost, "/api/device/scans", map[string]any{}, app.EntryPublic, dev); rec.Code != http.StatusBadRequest {
		t.Fatalf("empty scan %d %s", rec.Code, rec.Body.String())
	}
	big := c.callRaw(http.MethodPost, "/api/device/scans", strings.Repeat("x", 64*1024+1), app.EntryPublic, dev)
	if big.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("too large %d %s", big.Code, big.Body.String())
	}
	rotated := c.call(http.MethodPost, "/api/device/token/rotate", map[string]any{}, app.EntryPublic, dev)
	if rotated.Code != http.StatusOK {
		t.Fatalf("rotate %d %s", rotated.Code, rotated.Body.String())
	}
	var nextTok struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rotated.Body.Bytes(), &nextTok); err != nil || nextTok.Token == "" {
		t.Fatalf("next token %s", rotated.Body.String())
	}
	if rec := c.call(http.MethodPost, "/api/device/heartbeat", map[string]any{"version": "1.2.3"}, app.EntryPublic, map[string]string{"Authorization": "Bearer " + nextTok.Token}); rec.Code != http.StatusOK {
		t.Fatalf("new token %d %s", rec.Code, rec.Body.String())
	}
	if rec := c.call(http.MethodPost, "/api/device/heartbeat", map[string]any{"version": "1.2.3"}, app.EntryPublic, dev); rec.Code != http.StatusUnauthorized {
		t.Fatalf("old token after use %d %s", rec.Code, rec.Body.String())
	}
	if rec := c.authed(http.MethodDelete, "/api/devices/"+paired.DeviceID, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("revoke device %d %s", rec.Code, rec.Body.String())
	}
	if rec := c.call(http.MethodPost, "/api/device/heartbeat", map[string]any{"version": "1.2.3"}, app.EntryPublic, map[string]string{"Authorization": "Bearer " + nextTok.Token}); rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), auth.CodeDeviceRevoked) {
		t.Fatalf("revoked device %d %s", rec.Code, rec.Body.String())
	}

	if rec := c.call(http.MethodGet, "/", nil, app.EntryPublic, nil); rec.Code != http.StatusOK {
		t.Fatalf("home %d", rec.Code)
	}
	if rec := c.call(http.MethodGet, "/api/telemetry", nil, app.EntryPublic, nil); rec.Code != http.StatusOK {
		t.Fatalf("telemetry %d %s", rec.Code, rec.Body.String())
	}
	if rec := c.call(http.MethodGet, "/api/config", nil, app.EntryPublic, nil); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"entry":"public"`) {
		t.Fatalf("config %d %s", rec.Code, rec.Body.String())
	}
	if rec := c.call(http.MethodPost, "/api/auth/login", map[string]string{"email": "ada@pantry.test", "password": "correct horse"}, app.EntryPublic, nil); rec.Code != http.StatusOK {
		t.Fatalf("login %d %s", rec.Code, rec.Body.String())
	}

	foreign := stage1Request(http.MethodGet, "/api/household/settings", ada, nil, app.EntryPublic, "")
	foreign.Header.Set("X-Household-Id", cabin.ID)
	foreign.Header.Set("Origin", "https://pantry.test")
	foreignRec := httptest.NewRecorder()
	handler.ServeHTTP(foreignRec, foreign)
	if foreignRec.Code != http.StatusOK {
		t.Fatalf("header household %d %s", foreignRec.Code, foreignRec.Body.String())
	}
	badOrigin := stage1Request(http.MethodPost, "/api/auth/logout", ada, nil, app.EntryPublic, "")
	badOrigin.Header.Set("Origin", "https://evil.example")
	badRec := httptest.NewRecorder()
	handler.ServeHTTP(badRec, badOrigin)
	if badRec.Code != http.StatusUnauthorized {
		t.Fatalf("bad origin %d %s", badRec.Code, badRec.Body.String())
	}
	if rec := c.authed(http.MethodPost, "/api/auth/logout", nil); rec.Code != http.StatusNoContent {
		t.Fatalf("logout %d %s", rec.Code, rec.Body.String())
	}
	if rec := stage1Do(handler, http.MethodGet, "/api/auth/session", ada, nil, app.EntryPublic, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("logged out %d %s", rec.Code, rec.Body.String())
	}

	c.cookie = bea
	if rec := c.authed(http.MethodDelete, "/api/households/"+house+"/members/"+adaID, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("remove ada %d %s", rec.Code, rec.Body.String())
	}
	if rec := c.authed(http.MethodDelete, "/api/me", map[string]string{"confirmation": "bea@pantry.test"}); rec.Code != http.StatusConflict {
		t.Fatalf("last owner delete %d %s", rec.Code, rec.Body.String())
	}
	camInvite := c.authed(http.MethodPost, "/api/invites", map[string]string{"role": "member"})
	var camCode auth.InviteCreated
	if err := json.Unmarshal(camInvite.Body.Bytes(), &camCode); err != nil || camInvite.Code != http.StatusCreated {
		t.Fatalf("cam invite %d %s", camInvite.Code, camInvite.Body.String())
	}
	cam := c.anon(http.MethodPost, "/api/invites/accept", map[string]any{
		"code": camCode.Code, "account": map[string]string{"name": "Cam", "email": "cam@pantry.test", "password": "correct horse"},
	}, app.EntryPublic)
	if cam.Code != http.StatusOK {
		t.Fatalf("cam accept %d %s", cam.Code, cam.Body.String())
	}
	camCookie := cookieFrom(t, cam, site.SessionCookie().Name)
	var camUser struct {
		User struct {
			ID string `json:"id"`
		} `json:"user"`
	}
	if err := json.Unmarshal(cam.Body.Bytes(), &camUser); err != nil {
		t.Fatal(err)
	}
	if rec := c.authed(http.MethodDelete, "/api/households/"+house+"/members/"+camUser.User.ID, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("remove cam %d %s", rec.Code, rec.Body.String())
	}
	c.cookie = camCookie
	if rec := c.authed(http.MethodDelete, "/api/me", map[string]string{"confirmation": "nope"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad delete %d %s", rec.Code, rec.Body.String())
	}
	if rec := c.authed(http.MethodDelete, "/api/me", map[string]string{"confirmation": "cam@pantry.test"}); rec.Code != http.StatusNoContent {
		t.Fatalf("delete cam %d %s", rec.Code, rec.Body.String())
	}

	beaLogin2 := c.anon(http.MethodPost, "/api/auth/login", map[string]string{"email": "bea@pantry.test", "password": "brand new horse"}, app.EntryPublic)
	bea = cookieFrom(t, beaLogin2, site.SessionCookie().Name)
	c.cookie = bea
	svc.SetClock(func() time.Time { return time.Now().Add(2 * time.Hour) })
	if rec := c.authed(http.MethodGet, "/api/auth/session", nil); rec.Code != http.StatusOK {
		t.Fatalf("slide %d %s", rec.Code, rec.Body.String())
	}
	svc.SetClock(func() time.Time { return time.Now().Add(31 * 24 * time.Hour) })
	if rec := c.authed(http.MethodGet, "/api/auth/session", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("idle %d %s", rec.Code, rec.Body.String())
	}
	svc.SetClock(func() time.Time { return time.Now() })
	beaLogin3 := c.anon(http.MethodPost, "/api/auth/login", map[string]string{"email": "bea@pantry.test", "password": "brand new horse"}, app.EntryPublic)
	if beaLogin3.Code != http.StatusOK {
		t.Fatalf("relogin %d %s", beaLogin3.Code, beaLogin3.Body.String())
	}
	c.cookie = cookieFrom(t, beaLogin3, site.SessionCookie().Name)
	expiring := c.authed(http.MethodPost, "/api/invites", map[string]string{"role": "member"})
	var expiringCode auth.InviteCreated
	if err := json.Unmarshal(expiring.Body.Bytes(), &expiringCode); err != nil || expiring.Code != http.StatusCreated {
		t.Fatalf("expiring invite %d %s", expiring.Code, expiring.Body.String())
	}
	svc.SetClock(func() time.Time { return time.Now().Add(8 * 24 * time.Hour) })
	if rec := c.call(http.MethodPost, "/api/invites/preview", map[string]string{"code": expiringCode.Code}, app.EntryPublic, nil); rec.Code != http.StatusConflict {
		t.Fatalf("expired invite %d %s", rec.Code, rec.Body.String())
	}

	svc.SetClock(func() time.Time { return time.Now() })
	if err := svc.SetPassword(context.Background(), beaSession.User.ID, "another horse"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Authenticate(context.Background(), "nobody@pantry.test", "correct horse"); err == nil {
		t.Fatal("unknown email authenticated")
	}
	if err := svc.AdminIssue(context.Background(), false); err == nil {
		t.Fatal("setup code issued after an account existed")
	}
	if err := svc.AdminIssue(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	recoverCode, err := auth.ReadSetupCode(setupDir)
	if err != nil {
		t.Fatal(err)
	}
	if rec := c.call(http.MethodGet, "/setup", nil, app.EntryPublic, nil); rec.Code != http.StatusOK {
		t.Fatalf("recover page %d %s", rec.Code, rec.Body.String())
	}
	if rec := c.call(http.MethodPost, "/setup", map[string]string{
		"setupCode": recoverCode, "email": "bea@pantry.test", "newPassword": "recovered horse",
	}, app.EntryPublic, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("recover non-operator %d %s", rec.Code, rec.Body.String())
	}
	if rec := c.call(http.MethodPost, "/setup", map[string]string{
		"setupCode": recoverCode, "email": "ada@pantry.test", "newPassword": "recovered horse",
	}, app.EntryPublic, nil); rec.Code != http.StatusOK {
		t.Fatalf("recover %d %s", rec.Code, rec.Body.String())
	}
	openAccountEvents(t, svc, beaSession.User.ID)
}

func stage1Site(t *testing.T) app.RuntimeConfig {
	t.Helper()
	site, err := app.LoadRuntimeConfig(func(key string) string {
		if key == "APP_BASE_URL" {
			return "https://pantry.test"
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	return site
}

type stage1Caller struct {
	t      *testing.T
	h      http.Handler
	cookie string
	house  string
}

func (c *stage1Caller) authed(method, path string, body any) *httptest.ResponseRecorder {
	c.t.Helper()
	return c.call(method, path, body, app.EntryPublic, nil)
}

func (c *stage1Caller) anon(method, path string, body any, entry app.Entry) *httptest.ResponseRecorder {
	c.t.Helper()
	saved := c.cookie
	c.cookie = ""
	defer func() { c.cookie = saved }()
	return c.call(method, path, body, entry, nil)
}

func (c *stage1Caller) call(method, path string, body any, entry app.Entry, hdr map[string]string) *httptest.ResponseRecorder {
	c.t.Helper()
	req := stage1Request(method, path, c.cookie, body, entry, "")
	if c.house != "" && req.Header.Get("X-Household-Id") == "" && strings.Contains(path, "/api/") {
		req.Header.Set("X-Household-Id", c.house)
	}
	for k, v := range hdr {
		if k == "Cookie" {
			continue
		}
		req.Header.Set(k, v)
	}
	if authz := hdr["Authorization"]; strings.HasPrefix(authz, "Bearer ") {
		req.Header.Del("Cookie")
	}
	rec := httptest.NewRecorder()
	c.h.ServeHTTP(rec, req)
	return rec
}

func (c *stage1Caller) callRaw(method, path, body string, entry app.Entry, hdr map[string]string) *httptest.ResponseRecorder {
	c.t.Helper()
	req := stage1Request(method, path, "", nil, entry, "")
	req.Body = io.NopCloser(strings.NewReader(body))
	req.ContentLength = int64(len(body))
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	c.h.ServeHTTP(rec, req)
	return rec
}

func openAccountEvents(t *testing.T, svc *auth.Service, userID string) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/events", nil)
	req = req.WithContext(auth.WithPrincipal(ctx, auth.Principal{Kind: "account", UserID: userID, HouseholdID: "house-north"}))
	handler := &EventsHandler{Broadcaster: events.NewBroadcaster(), Streams: svc.Streams(), Keepalive: time.Hour}
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		handler.Handle(rec, req)
		close(done)
	}()
	deadline := time.Now().Add(2 * time.Second)
	for rec.Code == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("event stream did not stop")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("events %d %s", rec.Code, rec.Body.String())
	}
	svc.Streams().Kick(auth.UserStreamKey(userID))
	refused := false
	for i := 0; i < 12; i++ {
		bound, err := svc.Streams().Bind(context.Background(), auth.UserStreamKey(userID))
		if err != nil {
			refused = bound == nil
			break
		}
	}
	if !refused {
		t.Fatal("event stream cap was not enforced")
	}
}
