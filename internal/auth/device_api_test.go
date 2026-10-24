package auth

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type hintLog struct {
	items []string
}

func (h *hintLog) PublishHint(_, kind string) {
	h.items = append(h.items, kind)
}

func bootDeviceAPI(t *testing.T) (context.Context, *Service, string, string) {
	t.Helper()
	db := openAuthDB(t)
	dir := t.TempDir()
	svc := NewService(db, dir)
	ctx := context.Background()
	if err := svc.Boot(ctx); err != nil {
		t.Fatal(err)
	}
	code, err := ReadSetupCode(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.RedeemSetup(ctx, code, "Nia North", "nia@example.test", "correct horse", ""); err != nil {
		t.Fatal(err)
	}
	user, err := svc.Authenticate(ctx, "nia@example.test", "correct horse")
	if err != nil {
		t.Fatal(err)
	}
	var house string
	if err := db.QueryRow(`SELECT id FROM households WHERE is_primary = 1`).Scan(&house); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE households SET name = ? WHERE id = ?`, "North Cupboard", house); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { svc.SetClock(wallClock) })
	return ctx, svc, user.ID, house
}

func TestDeviceAPILimitsMatchTheContract(t *testing.T) {
	if maxAttempts != 5 || pairIPMax != 10 || scanLimit != 600 {
		t.Fatalf("attempts %d ip %d scans %d", maxAttempts, pairIPMax, scanLimit)
	}
	if pairLife != 10*time.Minute || onlineWindow != 300*time.Second || tokenGrace != 24*time.Hour || tokenRotate != 30*24*time.Hour {
		t.Fatalf("windows pair %s online %s grace %s rotate %s", pairLife, onlineWindow, tokenGrace, tokenRotate)
	}
	if pairIPWindow != 10*time.Minute || scanWindow != time.Minute {
		t.Fatalf("limit windows %s %s", pairIPWindow, scanWindow)
	}
}

func TestPairingIsSingleUseAndExpires(t *testing.T) {
	ctx, svc, userID, house := bootDeviceAPI(t)
	hints := &hintLog{}
	svc.SetHints(hints)
	start := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	svc.SetClock(func() time.Time { return start })

	pair, err := svc.StartPairing(ctx, userID, house)
	if err != nil {
		t.Fatal(err)
	}
	if pair.Code == "" || pair.BarcodeValue != pair.Code || pair.QRValue != pair.Code || pair.QRPayload != pair.Code {
		t.Fatalf("pairing %#v", pair)
	}
	if _, well, check := NormalizePairingCode(pair.Code); !well || !check {
		t.Fatalf("issued code %q", pair.Code)
	}
	status, err := svc.PairingStatus(ctx, userID, house, pair.PairingID)
	if err != nil || status.Status != "pending" {
		t.Fatalf("pending %#v %v", status, err)
	}
	other := newID()
	if _, err := svc.db.Exec(`
		INSERT INTO households (id, name, created_at, channel_key, is_primary)
		VALUES (?, 'South Shelf', ?, 'abcdef0123456789abcdef0123456789', 0)`, other, stamp(start)); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Exec(`
		INSERT INTO memberships (household_id, user_id, role, created_at)
		VALUES (?, ?, 'owner', ?)`, other, userID, stamp(start)); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.PairingStatus(ctx, userID, other, pair.PairingID); err == nil {
		t.Fatal("foreign household saw the pairing")
	}

	paired, err := svc.CompletePairing(ctx, "  "+strings.ToLower(pair.Code)+"\n", "Counter", "pi_hub", "1.2.3", "boot-a")
	if err != nil || !strings.HasPrefix(paired.Token, "sbd_") || paired.HouseholdID != house || !paired.Hub {
		t.Fatalf("paired %#v %v", paired, err)
	}
	if _, err := svc.CompletePairing(ctx, pair.Code, "Counter", "", "", ""); err == nil {
		t.Fatal("pairing code worked twice")
	}
	done, err := svc.PairingStatus(ctx, userID, house, pair.PairingID)
	if err != nil || done.Status != "complete" || done.DeviceID == nil || *done.DeviceID != paired.DeviceID {
		t.Fatalf("complete %#v %v", done, err)
	}
	var boot string
	if err := svc.db.QueryRow(`SELECT boot_id FROM devices WHERE id = ? AND household_id = ?`, paired.DeviceID, house).Scan(&boot); err != nil || boot != "boot-a" {
		t.Fatalf("boot %q %v", boot, err)
	}
	if len(hints.items) == 0 || hints.items[0] != "devices" {
		t.Fatalf("hints %v", hints.items)
	}

	expiring, err := svc.StartPairing(ctx, userID, house)
	if err != nil {
		t.Fatal(err)
	}
	svc.SetClock(func() time.Time { return start.Add(10 * time.Minute) })
	expectCode(t, take2(svc.CompletePairing(ctx, expiring.Code, "Counter", "", "", "")), CodeNotFound)
	expired, err := svc.PairingStatus(ctx, userID, house, expiring.PairingID)
	if err != nil || expired.Status != "expired" {
		t.Fatalf("expired %#v %v", expired, err)
	}
	for i := 1; i < 5; i++ {
		expectCode(t, take2(svc.CompletePairing(ctx, expiring.Code, "Counter", "", "", "")), CodeNotFound)
	}
	expectCode(t, take2(svc.CompletePairing(ctx, expiring.Code, "Counter", "", "", "")), CodeRateLimited)

	for i := 0; i < 10; i++ {
		if err := svc.NotePairIP("198.51.100.8"); err != nil {
			t.Fatal(err)
		}
	}
	expectCode(t, svc.BlockPairIP("198.51.100.8"), CodeRateLimited)
	if err := svc.BlockPairIP("198.51.100.9"); err != nil {
		t.Fatal(err)
	}
}

func TestBatchScanIsIdempotentInOneTransaction(t *testing.T) {
	ctx, svc, userID, house := bootDeviceAPI(t)
	fixed := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	svc.SetClock(func() time.Time { return fixed })
	pair, err := svc.StartPairing(ctx, userID, house)
	if err != nil {
		t.Fatal(err)
	}
	paired, err := svc.CompletePairing(ctx, pair.Code, "Counter", "pi_hub", "1.0.0", "")
	if err != nil {
		t.Fatal(err)
	}
	principal, err := svc.AuthenticateDevice(ctx, paired.Token)
	if err != nil {
		t.Fatal(err)
	}
	var looked []string
	svc.SetBarcodeLookup(func(_ context.Context, householdID, scanID, barcode string) {
		if householdID != house {
			t.Errorf("lookup household %s", householdID)
		}
		var n int
		if err := svc.db.QueryRow(`SELECT COUNT(*) FROM scan_entries WHERE id = ? AND household_id = ?`, scanID, house).Scan(&n); err != nil || n != 1 {
			t.Errorf("lookup scan %s count %d err %v", scanID, n, err)
		}
		looked = append(looked, barcode)
	})

	early := fixed.Add(-10 * time.Minute)
	age := int64(90_000)
	first, err := svc.IngestScans(ctx, principal, []ScanInput{
		{ID: "later", Barcode: "12345670", Direction: "stock_in", HasBarcode: true, HasScannedAt: true, ScannedAt: fixed},
		{ID: "earlier", Barcode: " 1234567 ", Direction: "stock_out", HasBarcode: true, HasScannedAt: true, ScannedAt: early},
		{ID: "aged", Barcode: "ABC-NOT-A-GTIN", Direction: "stock_in", HasBarcode: true, AgeMs: &age},
		{ID: "approx", Barcode: "7654321", HasBarcode: true},
		{ID: "pair", Barcode: "SBP-0123-ABCD-G", HasBarcode: true},
		{ID: "bad-time", Barcode: "1111111", HasBarcode: true, ScannedAtInvalid: true},
	}, 2)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"later": "created", "earlier": "created", "aged": "created", "approx": "created", "pair": "rejected", "bad-time": "rejected"}
	for _, result := range first {
		if want[result.ID] != result.Status {
			t.Fatalf("result %#v", result)
		}
	}
	again, err := svc.IngestScans(ctx, principal, []ScanInput{
		{ID: "later", Barcode: "12345670", Direction: "stock_in", HasBarcode: true, HasScannedAt: true, ScannedAt: fixed},
		{ID: "earlier", Barcode: "9999999", Direction: "stock_in", HasBarcode: true},
		{ID: "fresh", Barcode: "12345670", Direction: "stock_out", HasBarcode: true},
	}, 4)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, result := range again {
		got[result.ID] = result.Status
	}
	if got["later"] != "duplicate" || got["earlier"] != "conflict" || got["fresh"] != "created" {
		t.Fatalf("retry %#v", again)
	}
	var rows int
	if err := svc.db.QueryRow(`SELECT COUNT(*) FROM scan_entries WHERE household_id = ? AND device_id = ? AND client_scan_id = 'later'`, house, principal.DeviceID).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("later rows %d %v", rows, err)
	}
	var barcode, direction string
	var units, approx, group int
	if err := svc.db.QueryRow(`
		SELECT barcode, COALESCE(direction, ''), unit_count, time_approx, scan_group
		FROM scan_entries WHERE household_id = ? AND client_scan_id = 'earlier'`, house).Scan(&barcode, &direction, &units, &approx, &group); err != nil {
		t.Fatal(err)
	}
	if barcode != "1234567" || direction != "stock_out" || units != 1 || approx != 0 || group != 1 {
		t.Fatalf("earlier stored %s %s %d approx %d group %d", barcode, direction, units, approx, group)
	}
	var laterGroup, agedApprox int
	var agedAt string
	if err := svc.db.QueryRow(`SELECT scan_group FROM scan_entries WHERE household_id = ? AND client_scan_id = 'later'`, house).Scan(&laterGroup); err != nil || laterGroup != 2 {
		t.Fatalf("later group %d %v", laterGroup, err)
	}
	if err := svc.db.QueryRow(`SELECT scanned_at, time_approx FROM scan_entries WHERE household_id = ? AND client_scan_id = 'aged'`, house).Scan(&agedAt, &agedApprox); err != nil {
		t.Fatal(err)
	}
	if agedApprox != 0 || agedAt != stamp(fixed.Add(-90*time.Second)) {
		t.Fatalf("aged %s approx %d", agedAt, agedApprox)
	}
	var approxBit int
	if err := svc.db.QueryRow(`SELECT time_approx, scanned_at FROM scan_entries WHERE household_id = ? AND client_scan_id = 'approx'`, house).Scan(&approxBit, &agedAt); err != nil || approxBit != 1 || agedAt != stamp(fixed) {
		t.Fatalf("approx %d at %s %v", approxBit, agedAt, err)
	}
	var moves int
	if err := svc.db.QueryRow(`SELECT COUNT(*) FROM stock_moves WHERE household_id = ?`, house).Scan(&moves); err != nil || moves != 0 {
		t.Fatalf("stock moves %d %v", moves, err)
	}
	var dropped int
	if err := svc.db.QueryRow(`SELECT dropped_total FROM devices WHERE id = ? AND household_id = ?`, principal.DeviceID, house).Scan(&dropped); err != nil || dropped != 6 {
		t.Fatalf("dropped %d %v", dropped, err)
	}
	var alerts int
	if err := svc.db.QueryRow(`SELECT COUNT(*) FROM operator_alerts WHERE household_id = ? AND kind = 'scans_dropped'`, house).Scan(&alerts); err != nil || alerts != 1 {
		t.Fatalf("alerts %d %v", alerts, err)
	}
	if strings.Join(looked, ",") != "1234567,12345670,7654321,12345670" {
		t.Fatalf("lookups %v", looked)
	}

	before := 0
	_ = svc.db.QueryRow(`SELECT COUNT(*) FROM scan_entries WHERE household_id = ?`, house).Scan(&before)
	tooMany := make([]ScanInput, 51)
	for i := range tooMany {
		tooMany[i] = ScanInput{ID: "n", Barcode: "1", HasBarcode: true}
	}
	expectCode(t, take2(svc.IngestScans(ctx, principal, tooMany, 0)), CodeValidation)
	var after int
	if err := svc.db.QueryRow(`SELECT COUNT(*) FROM scan_entries WHERE household_id = ?`, house).Scan(&after); err != nil || after != before {
		t.Fatalf("51-item batch wrote %d -> %d", before, after)
	}
	for i := 0; i < 600; i++ {
		svc.Limiter().Hit(fixed, "scan:"+principal.DeviceID, 600, time.Minute)
	}
	expectCode(t, take2(svc.IngestScans(ctx, principal, []ScanInput{{ID: "over-limit", Barcode: "2222222", HasBarcode: true}}, 0)), CodeRateLimited)
	var over int
	if err := svc.db.QueryRow(`SELECT COUNT(*) FROM scan_entries WHERE household_id = ? AND client_scan_id = 'over-limit'`, house).Scan(&over); err != nil || over != 0 {
		t.Fatalf("rate limit wrote %d", over)
	}
}

func TestDeviceOnlineSweeperAndRevoke(t *testing.T) {
	ctx, svc, userID, house := bootDeviceAPI(t)
	hints := &hintLog{}
	svc.SetHints(hints)
	start := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	now := start
	svc.SetClock(func() time.Time { return now })
	pair, err := svc.StartPairing(ctx, userID, house)
	if err != nil {
		t.Fatal(err)
	}
	paired, err := svc.CompletePairing(ctx, pair.Code, "Counter", "pi_hub", "9.9.9", "boot-1")
	if err != nil {
		t.Fatal(err)
	}
	principal, err := svc.AuthenticateDevice(ctx, paired.Token)
	if err != nil {
		t.Fatal(err)
	}
	rotate, err := svc.Heartbeat(ctx, principal, Heartbeat{Version: "9.9.9", BootID: "boot-1", UptimeS: 12, Queued: 7, ClockSynced: true})
	if err != nil || rotate {
		t.Fatalf("rotate %v %v", rotate, err)
	}
	if !deviceOnline(t, svc, userID, house, paired.DeviceID, 7) {
		t.Fatal("hub was offline after heartbeat")
	}
	now = start.Add(299 * time.Second)
	if !deviceOnline(t, svc, userID, house, paired.DeviceID, 7) {
		t.Fatal("hub went offline before 300s")
	}
	if flipped := svc.SweepOffline(); len(flipped) != 0 {
		t.Fatalf("sweeper early %v", flipped)
	}
	now = start.Add(300 * time.Second)
	if deviceOnline(t, svc, userID, house, paired.DeviceID, 7) {
		t.Fatal("hub stayed online at 300s")
	}
	if flipped := svc.SweepOffline(); len(flipped) != 1 || flipped[0] != paired.DeviceID {
		t.Fatalf("sweeper %v", flipped)
	}
	if !containsHint(hints.items, "devices") {
		t.Fatalf("hints %v", hints.items)
	}
	hints.items = nil
	now = start.Add(301 * time.Second)
	if _, err := svc.Heartbeat(ctx, principal, Heartbeat{Version: "9.9.9", Queued: 7}); err != nil {
		t.Fatal(err)
	}
	if !deviceOnline(t, svc, userID, house, paired.DeviceID, 7) || !containsHint(hints.items, "devices") {
		t.Fatalf("reconnect hints %v", hints.items)
	}

	fresh := NewService(svc.db, t.TempDir())
	fresh.SetClock(func() time.Time { return now })
	if !deviceOnline(t, fresh, userID, house, paired.DeviceID, 7) {
		t.Fatal("queued depth did not survive restart")
	}

	var before string
	if err := svc.db.QueryRow(`SELECT channel_key FROM households WHERE id = ?`, house).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RenameDevice(ctx, userID, house, paired.DeviceID, "Front door"); err != nil {
		t.Fatal(err)
	}
	next, err := svc.RotateDeviceToken(ctx, paired.Token)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AuthenticateDevice(ctx, paired.Token); err != nil {
		t.Fatal("old token died before the new one was used")
	}
	if _, err := svc.AuthenticateDevice(ctx, next.Token); err != nil {
		t.Fatal(err)
	}
	expectCode(t, take2(svc.AuthenticateDevice(ctx, paired.Token)), CodeUnauthorized)
	now = now.Add(30 * 24 * time.Hour)
	current, err := svc.AuthenticateDevice(ctx, next.Token)
	if err != nil {
		t.Fatal(err)
	}
	rotate, err = svc.Heartbeat(ctx, current, Heartbeat{Queued: 7})
	if err != nil || !rotate {
		t.Fatalf("30 day rotate %v %v", rotate, err)
	}
	if err := svc.RevokeDevice(ctx, userID, house, paired.DeviceID); err != nil {
		t.Fatal(err)
	}
	expectCode(t, take2(svc.AuthenticateDevice(ctx, next.Token)), CodeDeviceRevoked)
	var after string
	if err := svc.db.QueryRow(`SELECT channel_key FROM households WHERE id = ?`, house).Scan(&after); err != nil || after == before || after == "" {
		t.Fatalf("channel key %q -> %q", before, after)
	}
	revoked := NewService(svc.db, t.TempDir())
	revoked.SetClock(func() time.Time { return now })
	if deviceOnline(t, revoked, userID, house, paired.DeviceID, 7) {
		t.Fatal("revoked hub stayed online")
	}
	if queuedOf(t, revoked, userID, house, paired.DeviceID) != 7 {
		t.Fatal("revoked hub lost its queue depth")
	}
	var directID string
	if err := svc.db.QueryRow(`SELECT id FROM devices WHERE kind = 'direct' AND household_id = ?`, house).Scan(&directID); err != nil {
		t.Fatal(err)
	}
	expectCode(t, take2(svc.RenameDevice(ctx, userID, house, directID, "Nope")), CodeNotFound)
	expectCode(t, svc.RevokeDevice(ctx, userID, house, directID), CodeNotFound)

	var actions string
	rows, err := svc.db.Query(`SELECT action FROM audit_log WHERE household_id = ?`, house)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	seen := map[string]bool{}
	for rows.Next() {
		var action string
		if err := rows.Scan(&action); err != nil {
			t.Fatal(err)
		}
		seen[action] = true
		actions += action + " "
	}
	for _, action := range []string{"device.paired", "device.renamed", "device.revoked", "device.token_rotated"} {
		if !seen[action] {
			t.Fatalf("missing %s in %s", action, actions)
		}
	}
	blob := auditBlob(t, svc, house)
	if strings.Contains(blob, "12345670") || strings.Contains(blob, paired.Token) || strings.Contains(blob, next.Token) {
		t.Fatal("audit stored a barcode or token")
	}
}

func TestDeviceSurvivesPasswordRetirementAndMemberRemoval(t *testing.T) {
	ctx, svc, owner, house := bootDeviceAPI(t)
	hash, err := bcrypt.GenerateFromPassword([]byte("shared-secret"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Exec(`INSERT INTO household_password (household_id, login_name, pw_hash, state, session_key) VALUES (?, 'pantry', ?, 'on', 'c2VjcmV0')`, house, string(hash)); err != nil {
		t.Fatal(err)
	}
	member := newID()
	if _, err := svc.db.Exec(`
		INSERT INTO users (id, name, email_norm, pw_hash, idp, operator, created_at)
		VALUES (?, 'Member South', 'member@example.test', ?, 'local', 0, ?)`, member, string(hash), stamp(time.Now())); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Exec(`
		INSERT INTO memberships (household_id, user_id, role, created_at)
		VALUES (?, ?, 'member', ?)`, house, member, stamp(time.Now())); err != nil {
		t.Fatal(err)
	}
	pair, err := svc.StartPairing(ctx, member, house)
	if err != nil {
		t.Fatal(err)
	}
	paired, err := svc.CompletePairing(ctx, pair.Code, "Counter", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.RemoveMember(ctx, owner, house, member); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AuthenticateDevice(ctx, paired.Token); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RetireHouseholdPassword(ctx, owner, house); err != nil {
		t.Fatal(err)
	}
	principal, err := svc.AuthenticateDevice(ctx, paired.Token)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Heartbeat(ctx, principal, Heartbeat{Queued: 1}); err != nil {
		t.Fatal(err)
	}
}

func TestOperatorAlertsNameTheHouseholdOnly(t *testing.T) {
	ctx, svc, userID, house := bootDeviceAPI(t)
	var notes []OperatorNote
	svc.SetOperatorPush(func(_ context.Context, note OperatorNote) {
		notes = append(notes, note)
	})
	if err := svc.RecordOperatorAlert(ctx, house, "busy"); err != nil {
		t.Fatal(err)
	}
	if err := svc.RecordOperatorAlert(ctx, house, "busy"); err != nil {
		t.Fatal(err)
	}
	if err := svc.RecordOperatorAlert(ctx, house, "scans_dropped"); err != nil {
		t.Fatal(err)
	}
	if err := svc.RecordOperatorAlert(ctx, house, "scans_dropped"); err != nil {
		t.Fatal(err)
	}
	if err := svc.RecordOperatorAlert(ctx, house, "barcode-990000000099"); err != nil {
		t.Fatal(err)
	}
	var storedName string
	if err := svc.db.QueryRow(`SELECT name FROM households WHERE id = ?`, house).Scan(&storedName); err != nil {
		t.Fatal(err)
	}
	if storedName != "North Cupboard" {
		t.Fatalf("stored household name %q", storedName)
	}
	list, err := svc.ListOperatorAlerts(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(list)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if !strings.Contains(text, "North Cupboard") || !strings.Contains(text, `"kind":"busy"`) || !strings.Contains(text, `"kind":"scans_dropped"`) {
		t.Fatalf("alerts %s", text)
	}
	if strings.Contains(text, "990000000099") || strings.Contains(text, "ZZ-FAKE-ITEM-9") || strings.Contains(text, "barcode-") {
		t.Fatalf("alert leaked contents %s", text)
	}
	var busy, dropped int
	for _, alert := range list {
		switch alert.Kind {
		case "busy":
			busy++
		case "scans_dropped":
			dropped++
		}
		if alert.HouseholdID != house || alert.HouseholdName != "North Cupboard" {
			t.Fatalf("alert %#v", alert)
		}
	}
	if busy != 2 || dropped != 1 {
		t.Fatalf("counts busy %d dropped %d", busy, dropped)
	}
	noteRaw, _ := json.Marshal(notes)
	if !strings.Contains(string(noteRaw), "North Cupboard") || strings.Contains(string(noteRaw), "990000000099") || strings.Contains(string(noteRaw), "ZZ-FAKE-ITEM-9") {
		t.Fatalf("push %s", noteRaw)
	}
	for _, note := range notes {
		if strings.Contains(note.Body, "990000000099") || note.Kind == "" || note.Title == "" {
			t.Fatalf("note %#v", note)
		}
	}
	member := newID()
	hash, err := bcrypt.GenerateFromPassword([]byte("shared-secret"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Exec(`
		INSERT INTO users (id, name, email_norm, pw_hash, idp, operator, created_at)
		VALUES (?, 'Member South', 'viewer@example.test', ?, 'local', 0, ?)`, member, string(hash), stamp(time.Now())); err != nil {
		t.Fatal(err)
	}
	expectCode(t, take2(svc.ListOperatorAlerts(ctx, member)), CodeNotFound)

	ticks := make(chan time.Time, 1)
	done := make(chan struct{})
	go func() {
		svc.RunSweeper(ctx, ticks)
		close(done)
	}()
	ticks <- time.Now()
	close(ticks)
	<-done
}

func deviceOnline(t *testing.T, svc *Service, userID, house, deviceID string, queued int) bool {
	t.Helper()
	return queuedOf(t, svc, userID, house, deviceID) == queued && onlineOf(t, svc, userID, house, deviceID)
}

func onlineOf(t *testing.T, svc *Service, userID, house, deviceID string) bool {
	t.Helper()
	for _, device := range listOrFail(t, svc, userID, house) {
		if device.ID == deviceID {
			return device.Online
		}
	}
	t.Fatalf("missing device %s", deviceID)
	return false
}

func queuedOf(t *testing.T, svc *Service, userID, house, deviceID string) int {
	t.Helper()
	for _, device := range listOrFail(t, svc, userID, house) {
		if device.ID == deviceID {
			if device.Queued == nil || device.QueuedScans == nil || *device.Queued != *device.QueuedScans {
				t.Fatalf("queue %#v", device)
			}
			return *device.Queued
		}
	}
	t.Fatalf("missing device %s", deviceID)
	return 0
}

func listOrFail(t *testing.T, svc *Service, userID, house string) []DeviceView {
	t.Helper()
	list, err := svc.ListDevices(context.Background(), userID, house)
	if err != nil {
		t.Fatal(err)
	}
	return list
}

func containsHint(items []string, kind string) bool {
	for _, item := range items {
		if item == kind {
			return true
		}
	}
	return false
}

func auditBlob(t *testing.T, svc *Service, house string) string {
	t.Helper()
	rows, err := svc.db.Query(`
		SELECT actor_kind, actor_id, action, target_kind, target_id
		FROM audit_log WHERE household_id = ?`, house)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var b strings.Builder
	for rows.Next() {
		var actorKind, actorID, action, targetKind, targetID string
		if err := rows.Scan(&actorKind, &actorID, &action, &targetKind, &targetID); err != nil {
			t.Fatal(err)
		}
		b.WriteString(strings.Join([]string{actorKind, actorID, action, targetKind, targetID}, " "))
	}
	return b.String()
}
