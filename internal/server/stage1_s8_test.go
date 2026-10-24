package server

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
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
)

func TestStage1ExportRoutesAreOwnerOnlyAndSecretFree(t *testing.T) {
	db, svc, handler, owner, house, userID := stage1Owner(t, "Nia North", "nia@pantry.test")
	when := time.Date(2026, 4, 2, 15, 4, 5, 0, time.UTC)
	svc.SetClock(func() time.Time { return when })
	if rec := owner.authed(http.MethodPatch, "/api/households/"+house, map[string]string{"name": "north-pantry-z9"}); rec.Code != http.StatusOK {
		t.Fatalf("rename %d %s", rec.Code, rec.Body.String())
	}
	const south = "0190aaaa-bbbb-7000-8000-0000000000b2"
	execSQL(t, db, `INSERT INTO households (id, name, restock_mode, created_at, channel_key, is_primary) VALUES (?, 'south-pantry-z9', '', '2026-01-01T00:00:00Z', 'channel-south-q7', 0)`, south)
	execSQL(t, db, `UPDATE households SET channel_key = 'channel-sentinel-q7' WHERE id = ?`, house)
	execSQL(t, db, `INSERT INTO products (id, name) VALUES ('p-north', 'north-catalog-q7'), ('p-south', 'south-catalog-q7'), ('p-unused', 'unused-catalog-q7')`)
	execSQL(t, db, `INSERT INTO household_product_overrides (household_id, product_id, display_name, updated_at) VALUES (?, 'p-north', 'north-typed-q7', '2026-01-01T00:00:00Z'), (?, 'p-north', 'foreign-typed-q7', '2026-01-01T00:00:00Z'), (?, 'p-south', 'south-typed-q7', '2026-01-01T00:00:00Z')`, house, south, south)
	execSQL(t, db, `INSERT INTO items (id, user_id, product_id, household_id) VALUES ('i-north', ?, 'p-north', ?), ('i-south', 'acct-south', 'p-south', ?)`, userID, house, south)
	execSQL(t, db, `INSERT INTO shopping_list_items (id, user_id, item_id, quantity, source, household_id) VALUES ('shop-north', ?, 'i-north', 2, 'manual', ?)`, userID, house)
	execSQL(t, db, `INSERT INTO household_password (household_id, pw_hash, state, session_key) VALUES (?, 'pw-hash-sentinel-q7', 'on', 'session-key-sentinel-q7')`, house)
	execSQL(t, db, `INSERT INTO users (id, name, email_norm, pw_hash, created_at) VALUES ('acct-south', 'Bea South', 'bea-south@pantry.test', 'account-hash-q7', '2026-01-01T00:00:00Z')`)

	invite := owner.authed(http.MethodPost, "/api/invites", map[string]string{"role": "member"})
	if invite.Code != http.StatusCreated {
		t.Fatalf("invite %d %s", invite.Code, invite.Body.String())
	}
	var created auth.InviteCreated
	if err := json.Unmarshal(invite.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	accepted := owner.anon(http.MethodPost, "/api/invites/accept", map[string]any{
		"code": created.Code, "account": map[string]string{"name": "Bea North", "email": "bea@pantry.test", "password": "correct horse"},
	}, app.EntryPublic)
	if accepted.Code != http.StatusOK {
		t.Fatalf("accept %d %s", accepted.Code, accepted.Body.String())
	}
	member := &stage1Caller{t: t, h: handler, cookie: cookieFrom(t, accepted, stage1Site(t).SessionCookie().Name), house: house}

	if rec := member.authed(http.MethodPost, "/api/export", map[string]any{}); rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `"code":"forbidden_role"`) {
		t.Fatalf("member export %d %s", rec.Code, rec.Body.String())
	}
	if rec := owner.authed(http.MethodGet, "/api/households/"+south+"/export", nil); rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), `"code":"not_found"`) {
		t.Fatalf("foreign export %d %s", rec.Code, rec.Body.String())
	}
	if rec := owner.call(http.MethodGet, "/api/households/0190aaaa-bbbb-7000-8000-000000000099/export", nil, app.EntryPublic, map[string]string{"X-Household-Id": "0190aaaa-bbbb-7000-8000-000000000099"}); rec.Code != http.StatusNotFound {
		t.Fatalf("missing household %d %s", rec.Code, rec.Body.String())
	}

	before := auditCount(t, db, "export.downloaded")
	createdJob := owner.authed(http.MethodPost, "/api/export", map[string]any{})
	if createdJob.Code != http.StatusAccepted || !strings.Contains(createdJob.Body.String(), `"status":"ready"`) {
		t.Fatalf("create %d %s", createdJob.Code, createdJob.Body.String())
	}
	var job struct {
		JobID string `json:"jobId"`
	}
	if err := json.Unmarshal(createdJob.Body.Bytes(), &job); err != nil || job.JobID == "" {
		t.Fatal(createdJob.Body.String())
	}
	if auditCount(t, db, "export.downloaded") != before {
		t.Fatal("creating a job wrote export.downloaded")
	}
	status := owner.call(http.MethodGet, "/api/export/"+job.JobID, nil, app.EntryPublic, map[string]string{"X-Household-Id": ""})
	if status.Code != http.StatusOK || !strings.Contains(status.Body.String(), `"/api/export/`+job.JobID+`/stockbot-export.json"`) || !strings.Contains(status.Body.String(), `"/api/export/`+job.JobID+`/items.csv"`) {
		t.Fatalf("status %d %s", status.Code, status.Body.String())
	}
	if auditCount(t, db, "export.downloaded") != before {
		t.Fatal("polling a job wrote export.downloaded")
	}
	if rec := member.call(http.MethodGet, "/api/export/"+job.JobID, nil, app.EntryPublic, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("member job %d %s", rec.Code, rec.Body.String())
	}
	alias := owner.authed(http.MethodPost, "/api/household/exports", map[string]any{})
	if alias.Code != http.StatusAccepted {
		t.Fatalf("alias create %d %s", alias.Code, alias.Body.String())
	}
	var aliasJob struct {
		JobID string `json:"jobId"`
	}
	if err := json.Unmarshal(alias.Body.Bytes(), &aliasJob); err != nil {
		t.Fatal(err)
	}
	aliasStatus := owner.authed(http.MethodGet, "/api/household/exports/"+aliasJob.JobID, nil)
	if aliasStatus.Code != http.StatusOK || !strings.Contains(aliasStatus.Body.String(), aliasJob.JobID) {
		t.Fatalf("alias status %d %s", aliasStatus.Code, aliasStatus.Body.String())
	}

	zipRec := owner.authed(http.MethodGet, "/api/household/exports/"+aliasJob.JobID+"/download", nil)
	assertExportZip(t, zipRec, "north-typed-q7", []string{"south-typed-q7", "south-pantry-z9", "unused-catalog-q7", "foreign-typed-q7", "channel-sentinel-q7", "pw-hash-sentinel-q7", "session-key-sentinel-q7", "sbd_", "sbs_", "nia@pantry.test", "bea@pantry.test"})
	if auditCount(t, db, "export.downloaded") != before+1 {
		t.Fatalf("download audits %d", auditCount(t, db, "export.downloaded"))
	}
	jsonRec := owner.authed(http.MethodGet, "/api/export/"+job.JobID+"/stockbot-export.json", nil)
	if jsonRec.Code != http.StatusOK || !strings.Contains(jsonRec.Header().Get("Content-Disposition"), "stockbot-export.json") || !strings.Contains(jsonRec.Body.String(), `"schemaVersion":1`) {
		t.Fatalf("json %d %s", jsonRec.Code, jsonRec.Body.String())
	}
	itemsRec := owner.authed(http.MethodGet, "/api/export/"+job.JobID+"/items.csv", nil)
	if itemsRec.Code != http.StatusOK || !strings.Contains(itemsRec.Body.String(), "north-typed-q7") || !strings.HasPrefix(itemsRec.Body.String(), "name,barcode,on-hand count,expires,category,group,stock target") {
		t.Fatalf("items csv %d %s", itemsRec.Code, itemsRec.Body.String())
	}
	shopRec := owner.authed(http.MethodGet, "/api/export/"+job.JobID+"/shopping-list.csv", nil)
	if shopRec.Code != http.StatusOK || !strings.Contains(shopRec.Body.String(), "north-typed-q7,2") || !strings.HasPrefix(shopRec.Body.String(), "name,quantity") {
		t.Fatalf("shopping csv %d %s", shopRec.Code, shopRec.Body.String())
	}
	direct := owner.authed(http.MethodGet, "/api/households/"+house+"/export", nil)
	assertExportZip(t, direct, "north-pantry-z9", []string{"south-typed-q7", "channel-sentinel-q7"})

	when = when.Add(16 * time.Minute)
	if rec := owner.authed(http.MethodGet, "/api/export/"+job.JobID, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("expired job %d %s", rec.Code, rec.Body.String())
	}
}

func TestStage1SoftDeleteRestorePurgeAndDeviceRevoke(t *testing.T) {
	db, svc, handler, owner, house, userID := stage1Owner(t, "Nia North", "nia@pantry.test")
	if rec := owner.authed(http.MethodPatch, "/api/households/"+house, map[string]string{"name": "north-pantry-z9"}); rec.Code != http.StatusOK {
		t.Fatalf("rename %d %s", rec.Code, rec.Body.String())
	}
	execSQL(t, db, `INSERT INTO push_subscriptions (household_id, endpoint, p256dh, auth, user_id) VALUES (?, 'https://push.test/nia', 'p256', 'authb', ?)`, house, userID)
	execSQL(t, db, `UPDATE sessions SET active_household_id = ? WHERE user_id = ?`, house, userID)
	pair := owner.authed(http.MethodPost, "/api/devices/pair", map[string]any{})
	if pair.Code != http.StatusCreated {
		t.Fatalf("pair %d %s", pair.Code, pair.Body.String())
	}
	var pairing auth.PairingCode
	if err := json.Unmarshal(pair.Body.Bytes(), &pairing); err != nil {
		t.Fatal(err)
	}
	completed := owner.anon(http.MethodPost, "/api/devices/pair/complete", map[string]any{"code": pairing.Code, "label": "scanner-label-q7"}, app.EntryPublic)
	if completed.Code != http.StatusCreated {
		t.Fatalf("complete %d %s", completed.Code, completed.Body.String())
	}
	var paired struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(completed.Body.Bytes(), &paired); err != nil || !strings.HasPrefix(paired.Token, "sbd_") {
		t.Fatalf("token %s", completed.Body.String())
	}
	invite := owner.authed(http.MethodPost, "/api/invites", map[string]string{"role": "member"})
	var created auth.InviteCreated
	if err := json.Unmarshal(invite.Body.Bytes(), &created); err != nil || invite.Code != http.StatusCreated {
		t.Fatalf("invite %d %s", invite.Code, invite.Body.String())
	}
	memberAccepted := owner.anon(http.MethodPost, "/api/invites/accept", map[string]any{
		"code": created.Code, "account": map[string]string{"name": "Bea North", "email": "bea@pantry.test", "password": "correct horse"},
	}, app.EntryPublic)
	member := &stage1Caller{t: t, h: handler, cookie: cookieFrom(t, memberAccepted, stage1Site(t).SessionCookie().Name), house: house}

	req := stage1Request(http.MethodGet, "/api/events", owner.cookie, nil, app.EntryPublic, "")
	req.Header.Set("X-Household-Id", house)
	req.Header.Set("Origin", "https://pantry.test")
	ctx, cancel := context.WithCancel(req.Context())
	defer cancel()
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(rec, req)
		close(done)
	}()
	deadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(rec.Body.String(), ": ok\n\n") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !strings.Contains(rec.Body.String(), ": ok\n\n") {
		t.Fatalf("events did not start %d %s", rec.Code, rec.Body.String())
	}

	if bad := owner.authed(http.MethodDelete, "/api/households/"+house, map[string]string{"confirmation": "nope"}); bad.Code != http.StatusBadRequest || !strings.Contains(bad.Body.String(), `"code":"confirmation_mismatch"`) {
		t.Fatalf("mismatch %d %s", bad.Code, bad.Body.String())
	}
	if rec := member.authed(http.MethodDelete, "/api/households/"+house, map[string]string{"confirmation": "north-pantry-z9"}); rec.Code != http.StatusForbidden {
		t.Fatalf("member delete %d %s", rec.Code, rec.Body.String())
	}
	deleted := owner.authed(http.MethodDelete, "/api/households/"+house, map[string]string{"confirmation": " north-pantry-z9 "})
	if deleted.Code != http.StatusOK || !strings.Contains(deleted.Body.String(), auth.SentenceDeleteHouse) || !strings.Contains(deleted.Body.String(), `"deletedAt"`) {
		t.Fatalf("delete %d %s", deleted.Code, deleted.Body.String())
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("event stream stayed open after delete")
	}
	var active string
	if err := db.QueryRow(`SELECT active_household_id FROM sessions WHERE user_id = ?`, userID).Scan(&active); err != nil || active != "" {
		t.Fatalf("active household %q %v", active, err)
	}
	var pushes int
	if err := db.QueryRow(`SELECT COUNT(*) FROM push_subscriptions WHERE household_id = ?`, house).Scan(&pushes); err != nil || pushes != 0 {
		t.Fatalf("pushes %d %v", pushes, err)
	}
	var revoked string
	if err := db.QueryRow(`SELECT COALESCE(revoked_at, '') FROM devices WHERE household_id = ? AND label = 'scanner-label-q7'`, house).Scan(&revoked); err != nil || revoked == "" {
		t.Fatalf("device revoked %q %v", revoked, err)
	}
	var audits int
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action = 'household.deleted' AND household_id = ?`, house).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("deleted audit %d %v", audits, err)
	}
	beat := owner.call(http.MethodPost, "/api/device/heartbeat", map[string]any{"version": "1"}, app.EntryPublic, map[string]string{"Authorization": "Bearer " + paired.Token})
	if beat.Code != http.StatusUnauthorized || !strings.Contains(beat.Body.String(), `"code":"device_revoked"`) {
		t.Fatalf("heartbeat %d %s", beat.Code, beat.Body.String())
	}
	if rec := owner.authed(http.MethodGet, "/api/inventory", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("inventory after delete %d %s", rec.Code, rec.Body.String())
	}
	if rec := member.authed(http.MethodPost, "/api/households/"+house+"/restore", nil); rec.Code != http.StatusForbidden {
		t.Fatalf("member restore %d %s", rec.Code, rec.Body.String())
	}
	direct := owner.authed(http.MethodGet, "/api/households/"+house+"/export", nil)
	if direct.Code != http.StatusOK || !strings.Contains(direct.Header().Get("Content-Type"), "application/zip") {
		t.Fatalf("export during window %d %s", direct.Code, direct.Body.String())
	}

	restored := owner.authed(http.MethodPost, "/api/households/"+house+"/restore", nil)
	if restored.Code != http.StatusOK || strings.Contains(restored.Body.String(), `"deletedAt":"`) {
		t.Fatalf("restore %d %s", restored.Code, restored.Body.String())
	}
	if err := db.QueryRow(`SELECT COALESCE(revoked_at, '') FROM devices WHERE household_id = ? AND label = 'scanner-label-q7'`, house).Scan(&revoked); err != nil || revoked == "" {
		t.Fatal("restore un-revoked the device")
	}
	beat = owner.call(http.MethodPost, "/api/device/heartbeat", map[string]any{"version": "1"}, app.EntryPublic, map[string]string{"Authorization": "Bearer " + paired.Token})
	if beat.Code != http.StatusUnauthorized || !strings.Contains(beat.Body.String(), `"code":"device_revoked"`) {
		t.Fatalf("heartbeat after restore %d %s", beat.Code, beat.Body.String())
	}
	if rec := owner.authed(http.MethodGet, "/api/inventory", nil); rec.Code != http.StatusOK {
		t.Fatalf("inventory after restore %d %s", rec.Code, rec.Body.String())
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action = 'household.restored' AND household_id = ?`, house).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("restored audit %d %v", audits, err)
	}

	if rec := owner.authed(http.MethodDelete, "/api/households/"+house, map[string]string{"confirmation": "north-pantry-z9"}); rec.Code != http.StatusOK {
		t.Fatalf("second delete %d %s", rec.Code, rec.Body.String())
	}
	execSQL(t, db, `UPDATE households SET purge_after = '2020-01-01T00:00:00Z' WHERE id = ?`, house)
	if rec := owner.authed(http.MethodPost, "/api/households/"+house+"/restore", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("restore after window %d %s", rec.Code, rec.Body.String())
	}
	if rec := owner.authed(http.MethodGet, "/api/households/"+house+"/export", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("export after window %d %s", rec.Code, rec.Body.String())
	}
	result, err := svc.PurgeDue(context.Background())
	if err != nil || result.Households < 1 || !result.SecureDelete || result.Largest > 1000 {
		t.Fatalf("purge %+v %v", result, err)
	}
	var left int
	if err := db.QueryRow(`SELECT COUNT(*) FROM households WHERE id = ?`, house).Scan(&left); err != nil || left != 0 {
		t.Fatalf("household remained %d", left)
	}
	if rec := owner.authed(http.MethodPost, "/api/households/"+house+"/restore", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("restore after purge %d %s", rec.Code, rec.Body.String())
	}
	var marker string
	if err := db.QueryRow(`SELECT target_id FROM audit_log WHERE action = 'household.purged' AND household_id = ?`, house).Scan(&marker); err != nil || marker != house {
		t.Fatalf("purge audit %q %v", marker, err)
	}
}

func TestStage1AccountDeleteBlocksTheLastOwner(t *testing.T) {
	db, _, _, owner, house, userID := stage1Owner(t, "Nia North", "nia@pantry.test")
	if rec := owner.authed(http.MethodDelete, "/api/me", map[string]string{"confirmation": "other@pantry.test"}); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"code":"confirmation_mismatch"`) {
		t.Fatalf("mismatch %d %s", rec.Code, rec.Body.String())
	}
	if rec := owner.callRaw(http.MethodDelete, "/api/me", "{", app.EntryPublic, map[string]string{"Cookie": "__Host-stockbot_session=" + owner.cookie, "Origin": "https://pantry.test"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad json %d %s", rec.Code, rec.Body.String())
	}
	blocked := owner.authed(http.MethodDelete, "/api/me", map[string]string{"confirmation": "nia@pantry.test"})
	if blocked.Code != http.StatusConflict || !strings.Contains(blocked.Body.String(), auth.LastOwnerSentence("Primary")) {
		t.Fatalf("last owner %d %s", blocked.Code, blocked.Body.String())
	}

	start := owner.authed(http.MethodPost, "/api/operator/household-invites", nil)
	var startInvite auth.InviteCreated
	if err := json.Unmarshal(start.Body.Bytes(), &startInvite); err != nil || start.Code != http.StatusCreated {
		t.Fatalf("start %d %s", start.Code, start.Body.String())
	}
	created := owner.authed(http.MethodPost, "/api/households", map[string]string{"name": "south-pantry-z9", "inviteCode": startInvite.Code})
	if created.Code != http.StatusCreated {
		t.Fatalf("create %d %s", created.Code, created.Body.String())
	}
	var south struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &south); err != nil || south.ID == "" {
		t.Fatal(created.Body.String())
	}
	still := owner.authed(http.MethodDelete, "/api/me", map[string]string{"confirmation": "nia@pantry.test"})
	if still.Code != http.StatusConflict || !strings.Contains(still.Body.String(), "south-pantry-z9") && !strings.Contains(still.Body.String(), "Primary") {
		t.Fatalf("still last owner %d %s", still.Code, still.Body.String())
	}
	owner.house = south.ID
	if rec := owner.authed(http.MethodDelete, "/api/households/"+south.ID, map[string]string{"confirmation": "south-pantry-z9"}); rec.Code != http.StatusOK {
		t.Fatalf("delete south %d %s", rec.Code, rec.Body.String())
	}
	owner.house = house
	invite := owner.authed(http.MethodPost, "/api/invites", map[string]string{"role": "owner"})
	var createdInvite auth.InviteCreated
	if err := json.Unmarshal(invite.Body.Bytes(), &createdInvite); err != nil || invite.Code != http.StatusCreated {
		t.Fatalf("invite %d %s", invite.Code, invite.Body.String())
	}
	accepted := owner.anon(http.MethodPost, "/api/invites/accept", map[string]any{
		"code": createdInvite.Code, "account": map[string]string{"name": "Bea North", "email": "bea@pantry.test", "password": "correct horse"},
	}, app.EntryPublic)
	if accepted.Code != http.StatusOK {
		t.Fatalf("accept %d %s", accepted.Code, accepted.Body.String())
	}
	execSQL(t, db, `INSERT INTO push_subscriptions (household_id, endpoint, p256dh, auth, user_id) VALUES (?, 'https://push.test/nia', 'p256', 'authb', ?)`, house, userID)
	execSQL(t, db, `INSERT INTO audit_log (id, household_id, actor_kind, actor_id, action, target_kind, target_id, at) VALUES ('aud-1', ?, 'account', ?, 'household.renamed', 'household', ?, '2026-04-01T00:00:00Z')`, house, userID, house)

	gone := owner.authed(http.MethodDelete, "/api/me", map[string]string{"confirmation": " Nia@Pantry.Test "})
	if gone.Code != http.StatusNoContent {
		t.Fatalf("delete account %d %s", gone.Code, gone.Body.String())
	}
	var users, memberships, pushes int
	if err := db.QueryRow(`SELECT COUNT(*) FROM users WHERE id = ?`, userID).Scan(&users); err != nil || users != 0 {
		t.Fatalf("user %d", users)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM memberships WHERE user_id = ?`, userID).Scan(&memberships); err != nil || memberships != 0 {
		t.Fatalf("memberships %d", memberships)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM push_subscriptions WHERE user_id = ?`, userID).Scan(&pushes); err != nil || pushes != 0 {
		t.Fatalf("pushes %d", pushes)
	}
	var revoked int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE user_id = ? AND revoked_at IS NULL`, userID).Scan(&revoked); err != nil || revoked != 0 {
		t.Fatalf("live sessions %d", revoked)
	}
	var actor string
	if err := db.QueryRow(`SELECT actor_id FROM audit_log WHERE id = 'aud-1'`).Scan(&actor); err != nil || !strings.HasPrefix(actor, "deleted:") || actor == userID {
		t.Fatalf("pseudonym %q", actor)
	}
	if err := db.QueryRow(`SELECT actor_id FROM audit_log WHERE action = 'account.deleted'`).Scan(&actor); err != nil || !strings.HasPrefix(actor, "deleted:") {
		t.Fatalf("account audit %q %v", actor, err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE actor_id = ?`, userID).Scan(&users); err != nil || users != 0 {
		t.Fatalf("raw actor remained %d", users)
	}
	var bea int
	if err := db.QueryRow(`SELECT COUNT(*) FROM users WHERE email_norm = 'bea@pantry.test'`).Scan(&bea); err != nil || bea != 1 {
		t.Fatalf("other owner removed %d", bea)
	}
	if rec := owner.authed(http.MethodGet, "/api/auth/session", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("old session %d %s", rec.Code, rec.Body.String())
	}
}

func TestStage1ImportRoutesStayDeferred(t *testing.T) {
	db, _, _, owner, _, _ := stage1Owner(t, "Nia North", "nia@pantry.test")
	var before int
	if err := db.QueryRow(`SELECT COUNT(*) FROM products`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/import", "/api/household/import/uploads"} {
		rec := owner.authed(http.MethodPost, path, map[string]any{"anything": "value"})
		if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), `"code":"not_found"`) {
			t.Fatalf("%s %d %s", path, rec.Code, rec.Body.String())
		}
	}
	if rec := owner.authed(http.MethodGet, "/api/import/0190aaaa-bbbb-7000-8000-000000000099", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("import job %d %s", rec.Code, rec.Body.String())
	}
	var after int
	if err := db.QueryRow(`SELECT COUNT(*) FROM products`).Scan(&after); err != nil || after != before {
		t.Fatalf("import wrote products %d -> %d", before, after)
	}
	var items int
	if err := db.QueryRow(`SELECT COUNT(*) FROM items`).Scan(&items); err != nil || items != 0 {
		t.Fatalf("import wrote items %d", items)
	}
}

func stage1Owner(t *testing.T, name, email string) (*sql.DB, *auth.Service, http.Handler, *stage1Caller, string, string) {
	t.Helper()
	db := openMigratedDB(t, filepath.Join(t.TempDir(), "pantry.db"))
	setupDir := t.TempDir()
	svc := auth.NewService(db, setupDir)
	site := stage1Site(t)
	handler, _ := setupOnDB(t, db, nil, WithStage1(svc, site))
	owner := &stage1Caller{t: t, h: handler}
	if rec := owner.call(http.MethodGet, "/setup", nil, app.EntryLAN, nil); rec.Code != http.StatusOK {
		t.Fatalf("setup page %d", rec.Code)
	}
	code, err := auth.ReadSetupCode(setupDir)
	if err != nil {
		t.Fatal(err)
	}
	if rec := owner.call(http.MethodPost, "/setup", map[string]string{
		"setupCode": code, "name": name, "email": email, "password": "correct horse",
	}, app.EntryLAN, nil); rec.Code != http.StatusOK {
		t.Fatalf("setup %d %s", rec.Code, rec.Body.String())
	}
	login := owner.call(http.MethodPost, "/api/auth/login", map[string]string{"email": email, "password": "correct horse"}, app.EntryPublic, nil)
	if login.Code != http.StatusOK {
		t.Fatalf("login %d %s", login.Code, login.Body.String())
	}
	owner.cookie = cookieFrom(t, login, site.SessionCookie().Name)
	var session auth.SessionView
	if err := json.Unmarshal(login.Body.Bytes(), &session); err != nil || session.User == nil || session.ActiveHouseholdID == nil {
		t.Fatalf("session %s", login.Body.String())
	}
	owner.house = *session.ActiveHouseholdID
	return db, svc, handler, owner, owner.house, session.User.ID
}

func execSQL(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

func auditCount(t *testing.T, db *sql.DB, action string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action = ?`, action).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func assertExportZip(t *testing.T, rec *httptest.ResponseRecorder, want string, forbidden []string) {
	t.Helper()
	if rec.Code != http.StatusOK || !strings.Contains(rec.Header().Get("Content-Type"), "application/zip") || !strings.Contains(rec.Header().Get("Content-Disposition"), "stockbot-export.zip") {
		t.Fatalf("zip %d %s %s", rec.Code, rec.Header().Get("Content-Type"), rec.Body.String())
	}
	body := rec.Body.Bytes()
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]string{}
	var order []string
	for _, file := range zr.File {
		order = append(order, file.Name)
		rc, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		raw, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		names[file.Name] = string(raw)
	}
	if strings.Join(order, ",") != "stockbot-export.json,items.csv,shopping-list.csv" {
		t.Fatalf("zip entries %v", order)
	}
	if !strings.Contains(names["stockbot-export.json"], want) || !strings.Contains(names["stockbot-export.json"], `"schemaVersion":1`) {
		t.Fatalf("json %s", names["stockbot-export.json"])
	}
	joined := names["stockbot-export.json"] + names["items.csv"] + names["shopping-list.csv"]
	for _, secret := range forbidden {
		if strings.Contains(joined, secret) {
			t.Fatalf("zip contains %q", secret)
		}
	}
}
