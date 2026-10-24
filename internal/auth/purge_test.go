package auth

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/app"
)

type recordKick struct {
	mu   sync.Mutex
	hits []string
}

func (r *recordKick) Kick(householdID, userID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.hits = append(r.hits, householdID+"|"+userID)
}

func TestPurgeDueOverwritesHouseholdRowsAndRetiredPasswords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pantry.db")
	db, reader := openPurgeDB(t, path)
	svc := NewService(db, t.TempDir())
	when := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	svc.SetClock(func() time.Time { return when })
	ctx := context.Background()

	const (
		dueID  = "0190aaaa-bbbb-7000-8000-0000000000d1"
		keepID = "0190aaaa-bbbb-7000-8000-0000000000d2"
		passID = "0190aaaa-bbbb-7000-8000-0000000000d3"
	)
	sentinels := []string{
		"due-pantry-name-q7", "due-item-label-q7", "due-device-label-q7",
		"due-hash-q7", "due-session-q7", "due-channel-q7", "due-barcode-q7", "due-setting-q7",
		"pass-hash-q7", "pass-session-q7", "sbd_dueq7",
	}
	mustExec(t, db, `INSERT INTO households (id, name, restock_mode, created_at, deleted_at, purge_after, channel_key, is_primary)
		VALUES (?, 'due-pantry-name-q7', '', '2026-01-01T00:00:00Z', '2026-05-01T00:00:00Z', '2026-05-31T00:00:00Z', 'due-channel-q7', 0)`, dueID)
	mustExec(t, db, `INSERT INTO households (id, name, restock_mode, created_at, purge_after, channel_key, is_primary)
		VALUES (?, 'keep-pantry-name-q7', '', '2026-01-01T00:00:00Z', '2026-07-01T00:00:00Z', 'keep-channel-q7', 0)`, keepID)
	mustExec(t, db, `INSERT INTO households (id, name, restock_mode, created_at, channel_key, is_primary)
		VALUES (?, 'pass-pantry-name-q7', '', '2026-01-01T00:00:00Z', 'pass-channel-q7', 0)`, passID)
	mustExec(t, db, `INSERT INTO products (id, name) VALUES ('p-due', 'catalog-keep-q7')`)
	mustExec(t, db, `INSERT INTO household_product_overrides (household_id, product_id, display_name, updated_at)
		VALUES (?, 'p-due', 'due-item-label-q7', '2026-01-01T00:00:00Z')`, dueID)
	mustExec(t, db, `INSERT INTO items (id, user_id, product_id, household_id) VALUES ('item-due', 'acct', 'p-due', ?)`, dueID)
	mustExec(t, db, `INSERT INTO barcodes (barcode, product_id, source, user_id, household_id)
		VALUES ('due-barcode-q7', 'p-due', 'user_override', 'acct', ?)`, dueID)
	mustExec(t, db, `INSERT INTO devices (id, household_id, kind, label, created_at)
		VALUES ('dev-due', ?, 'direct', 'due-device-label-q7', '2026-01-01T00:00:00Z')`, dueID)
	mustExec(t, db, `INSERT INTO device_tokens (id, device_id, household_id, token_hash, prefix, created_at)
		VALUES ('tok-due', 'dev-due', ?, 'sbd-hash-q7', 'sbd_dueq7', '2026-01-01T00:00:00Z')`, dueID)
	mustExec(t, db, `INSERT INTO household_password (household_id, pw_hash, state, session_key, restore_until)
		VALUES (?, 'due-hash-q7', 'off', 'due-session-q7', '2026-05-01T00:00:00Z')`, dueID)
	mustExec(t, db, `INSERT INTO household_password (household_id, pw_hash, state, session_key, restore_until)
		VALUES (?, 'pass-hash-q7', 'off', 'pass-session-q7', '2026-05-01T00:00:00Z')`, passID)
	mustExec(t, db, `INSERT INTO household_password (household_id, pw_hash, state, session_key, restore_until)
		VALUES (?, 'live-hash-q7', 'on', 'live-session-q7', '2026-05-01T00:00:00Z')`, keepID)
	for i := 0; i < 1001; i++ {
		mustExec(t, db, `INSERT INTO household_settings (household_id, key, value) VALUES (?, ?, 'due-setting-q7')`, dueID, settingKey(i))
	}
	mustExec(t, db, `INSERT INTO household_settings (household_id, key, value) VALUES (?, 'kept', 'keep-setting-q7')`, keepID)

	result, err := svc.PurgeDue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !result.SecureDelete {
		t.Fatal("secure_delete was off")
	}
	var flag int
	if err := db.QueryRow(`PRAGMA secure_delete`).Scan(&flag); err != nil || flag != 1 {
		t.Fatalf("pragma %d %v", flag, err)
	}
	if result.Households != 1 || result.Largest != purgeBatch || result.Largest > 1000 || result.Batches < 2 || result.Passwords < 2 {
		t.Fatalf("result %+v", result)
	}
	var left int
	if err := db.QueryRow(`SELECT COUNT(*) FROM households WHERE id = ?`, dueID).Scan(&left); err != nil || left != 0 {
		t.Fatalf("due household %d %v", left, err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM household_settings WHERE household_id = ?`, dueID).Scan(&left); err != nil || left != 0 {
		t.Fatalf("due settings %d %v", left, err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM household_settings WHERE household_id = ?`, keepID).Scan(&left); err != nil || left != 1 {
		t.Fatalf("kept settings %d %v", left, err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM items WHERE household_id = ?`, dueID).Scan(&left); err != nil || left != 0 {
		t.Fatalf("due items %d %v", left, err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM device_tokens WHERE household_id = ?`, dueID).Scan(&left); err != nil || left != 0 {
		t.Fatalf("due tokens %d %v", left, err)
	}
	var hash, sessionKey sql.NullString
	if err := db.QueryRow(`SELECT pw_hash, session_key FROM household_password WHERE household_id = ?`, passID).Scan(&hash, &sessionKey); err != nil {
		t.Fatal(err)
	}
	if hash.Valid || sessionKey.Valid {
		t.Fatalf("retired password remained hash=%v key=%v", hash, sessionKey)
	}
	if err := db.QueryRow(`SELECT pw_hash FROM household_password WHERE household_id = ?`, keepID).Scan(&hash); err != nil || hash.String != "live-hash-q7" {
		t.Fatalf("live password changed %v %v", hash, err)
	}
	var auditAction, auditTarget string
	if err := db.QueryRow(`SELECT action, target_id FROM audit_log WHERE household_id = ? AND action = 'household.purged'`, dueID).Scan(&auditAction, &auditTarget); err != nil {
		t.Fatal(err)
	}
	if auditTarget != dueID {
		t.Fatalf("audit target %s", auditTarget)
	}
	var auditBlob string
	if err := db.QueryRow(`SELECT COALESCE(GROUP_CONCAT(action || target_id || COALESCE(actor_id, '')), '') FROM audit_log`).Scan(&auditBlob); err != nil {
		t.Fatal(err)
	}
	for _, secret := range sentinels {
		if strings.Contains(auditBlob, secret) {
			t.Fatalf("audit kept %q", secret)
		}
	}
	var passAudits int
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action = 'household_password.purged' AND household_id = ?`, passID).Scan(&passAudits); err != nil || passAudits != 1 {
		t.Fatalf("password audit %d %v", passAudits, err)
	}
	assertNoHouseholdRows(t, db, dueID)
	if err := db.QueryRow(`SELECT COUNT(*) FROM products WHERE name = 'catalog-keep-q7'`).Scan(&left); err != nil || left != 1 {
		t.Fatalf("shared product removed %d", left)
	}
	db.Close()
	reader.Close()
	file := readDBFiles(t, path)
	for _, secret := range sentinels {
		if bytesContains(file, secret) {
			t.Fatalf("database file still contains %q", secret)
		}
	}
}

func TestDeleteHouseholdThenAccountDelete(t *testing.T) {
	db := openAuthDB(t)
	svc := NewService(db, t.TempDir())
	ctx := context.Background()
	when := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	svc.SetClock(func() time.Time { return when })
	if err := svc.RedeemSetup(ctx, mustSetup(t, svc), "Nia North", "nia@pantry.test", "correct horse", ""); err != nil {
		t.Fatal(err)
	}
	var adaID string
	if err := db.QueryRow(`SELECT id FROM users WHERE email_norm = 'nia@pantry.test'`).Scan(&adaID); err != nil {
		t.Fatal(err)
	}
	primary, err := svc.Primary(ctx)
	if err != nil {
		t.Fatal(err)
	}
	house := primary.HouseholdID()
	kicks := &recordKick{}
	svc.SetLive(kicks)

	if _, err := db.Exec(`UPDATE households SET name = 'north-pantry-z9', channel_key = 'channel-before-q7' WHERE id = ?`, house); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO push_subscriptions (household_id, endpoint, p256dh, auth, user_id)
		VALUES (?, 'https://push.test/nia', 'p256', 'authb', ?)`, house, adaID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO devices (id, household_id, kind, label, created_at)
		VALUES ('dev-1', ?, 'direct', 'scanner-label-q7', ?)`, house, stamp(when)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO device_tokens (id, device_id, household_id, token_hash, prefix, created_at)
		VALUES ('tok-1', 'dev-1', ?, 'sbd-hash-q7', 'sbd_testq7', ?)`, house, stamp(when)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO invites (id, kind, household_id, code_hash, expires_at)
		VALUES ('inv-1', 'join_household', ?, 'invite-hash-q7', ?)`, house, stamp(when.Add(24*time.Hour))); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO sessions (id_hash, user_id, kind, origin, active_household_id, created_at, last_seen_at, expires_at)
		VALUES ('sess-1', ?, 'cookie', 'personal', ?, ?, ?, ?)`, adaID, house, stamp(when), stamp(when), stamp(when.Add(time.Hour))); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO sessions (id_hash, kind, origin, active_household_id, created_at, last_seen_at, expires_at)
		VALUES ('sess-lan', 'cookie', 'household_password', ?, ?, ?, ?)`, house, stamp(when), stamp(when), stamp(when.Add(time.Hour))); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO reset_links (id, user_id, code_hash, expires_at) VALUES ('rst-1', ?, 'reset-hash', ?)`, adaID, stamp(when.Add(time.Hour))); err != nil {
		t.Fatal(err)
	}

	err = svc.DeleteAccount(ctx, adaID, "nia@pantry.test")
	expectCode(t, err, CodeLastOwner)
	if err.Error() != LastOwnerSentence("north-pantry-z9") {
		t.Fatalf("last owner %v", err)
	}
	expectCode(t, take2(svc.DeleteHousehold(ctx, adaID, house, "wrong")), CodeConfirm)
	deleted, err := svc.DeleteHousehold(ctx, adaID, house, "  North-Pantry-Z9 ")
	if err != nil {
		t.Fatal(err)
	}
	if deleted.Message != SentenceDeleteHouse || deleted.DeletedAt == nil {
		t.Fatalf("deleted %+v", deleted)
	}
	var channel, active, revoked, cancelled string
	var pushes, tokens int
	if err := db.QueryRow(`SELECT channel_key FROM households WHERE id = ?`, house).Scan(&channel); err != nil {
		t.Fatal(err)
	}
	if channel == "channel-before-q7" || channel == "" {
		t.Fatalf("channel key %q", channel)
	}
	if err := db.QueryRow(`SELECT active_household_id, COALESCE(revoked_at, '') FROM sessions WHERE id_hash = 'sess-1'`).Scan(&active, &revoked); err != nil {
		t.Fatal(err)
	}
	if active != "" || revoked != "" {
		t.Fatalf("personal session active=%q revoked=%q", active, revoked)
	}
	if err := db.QueryRow(`SELECT COALESCE(revoked_at, '') FROM sessions WHERE id_hash = 'sess-lan'`).Scan(&revoked); err != nil || revoked == "" {
		t.Fatalf("lan session revoked %q %v", revoked, err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM push_subscriptions WHERE household_id = ?`, house).Scan(&pushes); err != nil || pushes != 0 {
		t.Fatalf("pushes %d %v", pushes, err)
	}
	if err := db.QueryRow(`SELECT COALESCE(revoked_at, '') FROM devices WHERE id = 'dev-1'`).Scan(&revoked); err != nil || revoked == "" {
		t.Fatalf("device %q %v", revoked, err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM device_tokens WHERE household_id = ?`, house).Scan(&tokens); err != nil || tokens != 1 {
		t.Fatalf("token rows %d", tokens)
	}
	if err := db.QueryRow(`SELECT COALESCE(cancelled_at, '') FROM invites WHERE id = 'inv-1'`).Scan(&cancelled); err != nil || cancelled == "" {
		t.Fatalf("invite %q %v", cancelled, err)
	}
	foundUser, foundHousehold := false, false
	for _, hit := range kicks.hits {
		if hit == house+"|"+adaID {
			foundUser = true
		}
		if hit == house+"|" {
			foundHousehold = true
		}
	}
	if !foundUser || !foundHousehold {
		t.Fatalf("kicks %v", kicks.hits)
	}
	gone, err := svc.HouseholdGone(ctx, house)
	if err != nil || !gone {
		t.Fatalf("gone %v %v", gone, err)
	}
	if err := svc.ExportAllowed(ctx, adaID, house); err != nil {
		t.Fatal(err)
	}

	restored, err := svc.RestoreHousehold(ctx, adaID, house)
	if err != nil || restored.DeletedAt != nil {
		t.Fatalf("restore %+v %v", restored, err)
	}
	if err := db.QueryRow(`SELECT COALESCE(revoked_at, '') FROM devices WHERE id = 'dev-1'`).Scan(&revoked); err != nil || revoked == "" {
		t.Fatal("restore un-revoked the device")
	}
	var audits int
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action = 'household.restored' AND household_id = ?`, house).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("restored audit %d %v", audits, err)
	}

	if _, err := svc.DeleteHousehold(ctx, adaID, house, "north-pantry-z9"); err != nil {
		t.Fatal(err)
	}
	when = when.Add(31 * 24 * time.Hour)
	expectCode(t, take2(svc.RestoreHousehold(ctx, adaID, house)), CodeNotFound)
	expectCode(t, svc.ExportAllowed(ctx, adaID, house), CodeNotFound)
	when = when.Add(-20 * 24 * time.Hour)
	expectCode(t, svc.DeleteAccount(ctx, adaID, "nope@pantry.test"), CodeConfirm)
	if err := svc.DeleteAccount(ctx, adaID, " Nia@Pantry.Test "); err != nil {
		t.Fatal(err)
	}
	var users int
	if err := db.QueryRow(`SELECT COUNT(*) FROM users WHERE id = ?`, adaID).Scan(&users); err != nil || users != 0 {
		t.Fatalf("user remained %d", users)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM memberships WHERE user_id = ?`, adaID).Scan(&users); err != nil || users != 0 {
		t.Fatalf("memberships %d", users)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM reset_links WHERE user_id = ?`, adaID).Scan(&users); err != nil || users != 0 {
		t.Fatalf("resets %d", users)
	}
	sum := sha256.Sum256([]byte(adaID))
	pseudo := "deleted:" + hex.EncodeToString(sum[:8])
	var actor string
	if err := db.QueryRow(`SELECT actor_id FROM audit_log WHERE action = 'account.deleted'`).Scan(&actor); err != nil || actor != pseudo {
		t.Fatalf("account audit actor %q want %q (%v)", actor, pseudo, err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE actor_id = ?`, adaID).Scan(&users); err != nil || users != 0 {
		t.Fatalf("raw actor id remained %d", users)
	}
	expectCode(t, take2(svc.RestoreHousehold(ctx, adaID, house)), CodeNotFound)
	when = when.Add(21 * 24 * time.Hour)
	result, err := svc.PurgeDue(ctx)
	if err != nil || result.Households < 1 || !result.SecureDelete || result.Largest > purgeBatch {
		t.Fatalf("purge %+v %v", result, err)
	}
	var householdLeft int
	if err := db.QueryRow(`SELECT COUNT(*) FROM households WHERE id = ?`, house).Scan(&householdLeft); err != nil || householdLeft != 0 {
		t.Fatalf("household remained %d", householdLeft)
	}
}

func TestLifecycleEdgesAndPurgeGuards(t *testing.T) {
	if !safeIdent("household_settings") || safeIdent("") || safeIdent("has space") || safeIdent("a-b") {
		t.Fatal("safeIdent")
	}
	db := openAuthDB(t)
	if _, _, err := deleteHouseholdTable(context.Background(), db, "bad name", "x"); err == nil {
		t.Fatal("unsafe table was purged")
	}
	var none *Service
	if result, err := none.PurgeDue(context.Background()); err != nil || result.Households != 0 {
		t.Fatalf("nil purge %+v %v", result, err)
	}
	none.RunPurge(context.Background(), time.Hour)

	svc := NewService(db, t.TempDir())
	ctx := context.Background()
	when := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	svc.SetClock(func() time.Time { return when })
	if err := svc.RedeemSetup(ctx, mustSetup(t, svc), "Nia North", "nia@pantry.test", "correct horse", ""); err != nil {
		t.Fatal(err)
	}
	var userID string
	if err := db.QueryRow(`SELECT id FROM users WHERE email_norm = 'nia@pantry.test'`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	primary, err := svc.Primary(ctx)
	if err != nil {
		t.Fatal(err)
	}
	house := primary.HouseholdID()
	gone, err := svc.HouseholdGone(ctx, "0190aaaa-bbbb-7000-8000-000000000099")
	if err != nil || !gone {
		t.Fatalf("missing household gone=%v %v", gone, err)
	}
	expectCode(t, svc.ExportAllowed(ctx, "no-such-user", house), CodeNotFound)
	expectCode(t, svc.DeleteAccount(ctx, "no-such-user", "nia@pantry.test"), CodeUnauthorized)
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := svc.HouseholdGone(cancelled, house); err == nil {
		t.Fatal("cancelled household lookup succeeded")
	}
	if err := svc.ExportAllowed(cancelled, userID, house); err == nil {
		t.Fatal("cancelled export check succeeded")
	}
	if _, err := svc.DeleteHousehold(cancelled, userID, house, "Primary"); err == nil {
		t.Fatal("cancelled delete succeeded")
	}
	if _, err := svc.RestoreHousehold(cancelled, userID, house); err == nil {
		t.Fatal("cancelled restore succeeded")
	}
	if err := svc.DeleteAccount(cancelled, userID, "nia@pantry.test"); err == nil {
		t.Fatal("cancelled account delete succeeded")
	}
	if _, err := svc.PurgeDue(cancelled); err == nil {
		t.Fatal("cancelled purge succeeded")
	}
	if _, err := svc.DeleteHousehold(ctx, userID, house, "Primary"); err != nil {
		t.Fatal(err)
	}
	expectCode(t, take2(svc.DeleteHousehold(ctx, userID, house, "Primary")), CodeNotFound)
	if _, err := db.Exec(`UPDATE households SET purge_after = 'not-a-stamp' WHERE id = ?`, house); err != nil {
		t.Fatal(err)
	}
	expectCode(t, take2(svc.RestoreHousehold(ctx, userID, house)), CodeNotFound)
	expectCode(t, svc.ExportAllowed(ctx, userID, house), CodeNotFound)
	open, err := svc.restoreOpen(ctx, db, "0190aaaa-bbbb-7000-8000-000000000099")
	if err != nil || open {
		t.Fatalf("missing restore window %v %v", open, err)
	}
	if err := svc.RecordExport(ctx, "", userID, house); err != nil {
		t.Fatal(err)
	}
	var kind string
	if err := db.QueryRow(`SELECT actor_kind FROM audit_log WHERE action = 'export.downloaded' AND household_id = ?`, house).Scan(&kind); err != nil || kind != "account" {
		t.Fatalf("actor kind %q %v", kind, err)
	}
	if _, err := memberIDs(cancelled, db, house); err == nil {
		t.Fatal("cancelled member lookup succeeded")
	}

	stopped, stop := context.WithCancel(context.Background())
	stop()
	done := make(chan struct{})
	go func() {
		svc.RunPurge(stopped, 0)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("pre-cancelled purge did not return")
	}

	ticking, stopTick := context.WithCancel(context.Background())
	ticked := make(chan struct{})
	go func() {
		svc.RunPurge(ticking, 5*time.Millisecond)
		close(ticked)
	}()
	time.Sleep(40 * time.Millisecond)
	stopTick()
	select {
	case <-ticked:
	case <-time.After(2 * time.Second):
		t.Fatal("ticking purge did not stop")
	}
}

func TestRunPurgeStopsWhenCancelled(t *testing.T) {
	db := openAuthDB(t)
	svc := NewService(db, t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		svc.RunPurge(ctx, time.Hour)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("purge loop did not stop")
	}
}

func TestLastOwnerSentenceNamesTheHousehold(t *testing.T) {
	got := LastOwnerSentence("north-pantry-z9")
	want := "You're the only owner of north-pantry-z9. Make someone else an owner or delete the household first."
	if got != want {
		t.Fatalf("sentence %q", got)
	}
}

func mustSetup(t *testing.T, svc *Service) string {
	t.Helper()
	if err := svc.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	code, err := ReadSetupCode(svc.setupDir)
	if err != nil {
		t.Fatal(err)
	}
	return code
}

func openPurgeDB(t *testing.T, path string) (*sql.DB, *sql.DB) {
	t.Helper()
	db, reader, err := app.OpenPantry(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Close()
		reader.Close()
	})
	if err := app.RunMigrations(db); err != nil {
		t.Fatal(err)
	}
	return db, reader
}

func mustExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

func settingKey(i int) string {
	return "k" + strings.Repeat("0", 4-len(itoa(i))) + itoa(i)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [8]byte
	n := len(buf)
	for i > 0 {
		n--
		buf[n] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[n:])
}

func assertNoHouseholdRows(t *testing.T, db *sql.DB, householdID string) {
	t.Helper()
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	for _, table := range tables {
		info, err := db.Query(`PRAGMA table_info(` + table + `)`)
		if err != nil {
			t.Fatal(err)
		}
		has := false
		for info.Next() {
			var cid, notnull, pk int
			var col, ctype string
			var dflt sql.NullString
			if err := info.Scan(&cid, &col, &ctype, &notnull, &dflt, &pk); err != nil {
				info.Close()
				t.Fatal(err)
			}
			if col == "household_id" {
				has = true
			}
		}
		info.Close()
		if !has {
			continue
		}
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE household_id = ?`, householdID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if table == "audit_log" {
			if n != 1 {
				t.Fatalf("audit_log rows = %d, want the purge marker only", n)
			}
			continue
		}
		if n != 0 {
			t.Fatalf("%s still has %d rows", table, n)
		}
	}
}

func readDBFiles(t *testing.T, path string) []byte {
	t.Helper()
	var out []byte
	for _, name := range []string{path, path + "-wal", path + "-shm"} {
		body, err := os.ReadFile(name)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			t.Fatal(err)
		}
		out = append(out, body...)
	}
	return out
}

func bytesContains(body []byte, needle string) bool {
	return strings.Contains(string(body), needle)
}
