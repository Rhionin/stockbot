package auth

import (
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestListAllHouseholdsForOperatorOrdersWithoutMemberPII(t *testing.T) {
	ctx, svc, userID, primary := bootDeviceAPI(t)
	start := time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)
	svc.SetClock(func() time.Time { return start })

	other := newID()
	if _, err := svc.db.Exec(`
		INSERT INTO households (id, name, restock_mode, created_at, channel_key, is_primary)
		VALUES (?, 'Annex', 'auto', ?, 'abcdef0123456789abcdef0123456789ad', 0)`, other, stamp(start.Add(time.Hour))); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Exec(`INSERT INTO memberships (household_id, user_id, role, created_at) VALUES (?, ?, 'owner', ?)`,
		other, userID, stamp(start.Add(time.Hour))); err != nil {
		t.Fatal(err)
	}

	list, err := svc.ListAllHouseholdsForOperator(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) < 2 || !list[0].IsPrimary || list[0].ID != primary {
		t.Fatalf("order %#v primary %s", list, primary)
	}
	found := false
	for _, row := range list {
		if row.ID == other {
			found = true
		}
	}
	if !found {
		t.Fatal("annex missing")
	}
}

func TestListAllHouseholdsForOperatorMemberCountStillCountsDeletedUserMemberships(t *testing.T) {
	ctx, svc, _, primary := bootDeviceAPI(t)
	start := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	svc.SetClock(func() time.Time { return start })

	deletedUser := newID()
	if _, err := svc.db.Exec(`
		INSERT INTO users (id, name, email_norm, pw_hash, idp, operator, created_at, deleted_at)
		VALUES (?, 'Ghost', 'ghost@pantry.test', 'x', 'local', 0, ?, ?)`,
		deletedUser, stamp(start), stamp(start.Add(time.Hour))); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Exec(`INSERT INTO memberships (household_id, user_id, role, created_at) VALUES (?, ?, 'member', ?)`,
		primary, deletedUser, stamp(start.Add(2*time.Hour))); err != nil {
		t.Fatal(err)
	}

	list, err := svc.ListAllHouseholdsForOperator(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var primaryRow OperatorHouseholdSummary
	for _, row := range list {
		if row.ID == primary {
			primaryRow = row
			break
		}
	}
	if primaryRow.MemberCount != 2 {
		t.Fatalf("memberCount still counts deleted-user memberships got %d want 2", primaryRow.MemberCount)
	}
}

func TestOperatorScannerCountsIsolatedPerHouseholdWithDecoys(t *testing.T) {
	ctx, svc, userID, primary := bootDeviceAPI(t)
	start := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	svc.SetClock(func() time.Time { return start })

	type houseSpec struct {
		id      string
		name    string
		key     string
		created time.Time
		online  int
		offline int
	}
	specs := []houseSpec{
		{id: newID(), name: "Decoy Before", key: "abcdef0123456789abcdef0123456789c1", created: start.Add(time.Hour), online: 1, offline: 0},
		{id: newID(), name: "Target House", key: "abcdef0123456789abcdef0123456789c2", created: start.Add(2 * time.Hour), online: 2, offline: 3},
		{id: newID(), name: "Decoy After", key: "abcdef0123456789abcdef0123456789c3", created: start.Add(3 * time.Hour), online: 0, offline: 4},
	}
	for _, spec := range specs {
		if _, err := svc.db.Exec(`
			INSERT INTO households (id, name, restock_mode, created_at, channel_key, is_primary)
			VALUES (?, ?, 'auto', ?, ?, 0)`, spec.id, spec.name, stamp(spec.created), spec.key); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.db.Exec(`INSERT INTO memberships (household_id, user_id, role, created_at) VALUES (?, ?, 'owner', ?)`,
			spec.id, userID, stamp(spec.created)); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < spec.online; i++ {
			seedOperatorDevice(t, svc.db, spec.id, uuid.NewString(), "esp32_scanner", stamp(start.Add(-time.Minute)), "")
		}
		for i := 0; i < spec.offline; i++ {
			seedOperatorDevice(t, svc.db, spec.id, uuid.NewString(), "esp32_scanner", stamp(start.Add(-onlineWindow)), "")
		}
	}

	list, err := svc.ListAllHouseholdsForOperator(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if list[0].ID != primary {
		t.Fatalf("primary first got %s", list[0].ID)
	}
	want := map[string][2]int{}
	for _, spec := range specs {
		want[spec.id] = [2]int{spec.online, spec.offline}
	}
	for _, row := range list {
		if counts, ok := want[row.ID]; ok {
			if row.ScannersOnline != counts[0] || row.ScannersOffline != counts[1] {
				t.Fatalf("house %s online=%d offline=%d want %v", row.ID, row.ScannersOnline, row.ScannersOffline, counts)
			}
		}
	}
}

func TestDeviceOnlineAtMatchesListDevicesAtOnlineWindow(t *testing.T) {
	ctx, svc, userID, house := bootDeviceAPI(t)
	start := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	svc.SetClock(func() time.Time { return start })
	deviceID := newID()
	seen := stamp(start.Add(-onlineWindow))
	seedOperatorDevice(t, svc.db, house, deviceID, "esp32_scanner", seen, "")

	devices, err := svc.ListDevices(ctx, userID, house)
	if err != nil {
		t.Fatal(err)
	}
	var view DeviceView
	for _, d := range devices {
		if d.ID == deviceID {
			view = d
			break
		}
	}
	if view.Online {
		t.Fatal("expected offline at exactly onlineWindow boundary")
	}
	if svc.deviceOnlineAt(start, deviceID, "esp32_scanner", seen) {
		t.Fatal("deviceOnlineAt disagrees with ListDevices at boundary")
	}
}

func TestDirectDeviceOnlineDespiteStaleLastSeen(t *testing.T) {
	ctx, svc, userID, primary := bootDeviceAPI(t)
	house := newID()
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	svc.SetClock(func() time.Time { return now })
	if _, err := svc.db.Exec(`
		INSERT INTO households (id, name, restock_mode, created_at, channel_key, is_primary)
		VALUES (?, 'Direct only', 'auto', ?, 'abcdef0123456789abcdef0123456789d1', 0)`, house, stamp(now)); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Exec(`INSERT INTO memberships (household_id, user_id, role, created_at) VALUES (?, ?, 'owner', ?)`,
		house, userID, stamp(now)); err != nil {
		t.Fatal(err)
	}
	deviceID := newID()
	seedOperatorDevice(t, svc.db, house, deviceID, "direct", stamp(now.Add(-48*time.Hour)), "")

	devices, err := svc.ListDevices(ctx, userID, house)
	if err != nil {
		t.Fatal(err)
	}
	var view DeviceView
	for _, d := range devices {
		if d.ID == deviceID {
			view = d
			break
		}
	}
	if !view.Online {
		t.Fatal("direct device should be online regardless of last_seen")
	}

	list, err := svc.ListAllHouseholdsForOperator(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range list {
		if row.ID == house && row.ScannersOnline != 1 {
			t.Fatalf("operator scannersOnline=%d want 1", row.ScannersOnline)
		}
	}
	_ = primary
}

func TestPresenceOverridesStaleDBLastSeenForOperatorAndListDevices(t *testing.T) {
	ctx, svc, userID, primary := bootDeviceAPI(t)
	house := newID()
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	svc.SetClock(func() time.Time { return now })
	if _, err := svc.db.Exec(`
		INSERT INTO households (id, name, restock_mode, created_at, channel_key, is_primary)
		VALUES (?, 'Presence house', 'auto', ?, 'abcdef0123456789abcdef0123456789d2', 0)`, house, stamp(now)); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Exec(`INSERT INTO memberships (household_id, user_id, role, created_at) VALUES (?, ?, 'owner', ?)`,
		house, userID, stamp(now)); err != nil {
		t.Fatal(err)
	}
	_, deviceID := pairESP32(t, ctx, svc, userID, house)
	stale := stamp(now.Add(-48 * time.Hour))
	if _, err := svc.db.Exec(`UPDATE devices SET last_seen_at = ? WHERE id = ?`, stale, deviceID); err != nil {
		t.Fatal(err)
	}
	svc.presence.note(deviceID, house, now)

	devices, err := svc.ListDevices(ctx, userID, house)
	if err != nil {
		t.Fatal(err)
	}
	var view DeviceView
	for _, d := range devices {
		if d.ID == deviceID {
			view = d
			break
		}
	}
	if !view.Online {
		t.Fatal("presence should mark device online despite stale DB last_seen")
	}

	list, err := svc.ListAllHouseholdsForOperator(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range list {
		if row.ID == house && row.ScannersOnline != 1 {
			t.Fatalf("operator scannersOnline=%d want 1", row.ScannersOnline)
		}
	}
	_ = primary
}

func TestOperatorScannerCountsExcludeRevokedDevicesPairedAndRevoked(t *testing.T) {
	ctx, svc, userID, primary := bootDeviceAPI(t)
	target := newID()
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	svc.SetClock(func() time.Time { return now })
	if _, err := svc.db.Exec(`
		INSERT INTO households (id, name, restock_mode, created_at, channel_key, is_primary)
		VALUES (?, 'Revoke target', 'auto', ?, 'abcdef0123456789abcdef0123456789f1', 0)`, target, stamp(now)); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Exec(`INSERT INTO memberships (household_id, user_id, role, created_at) VALUES (?, ?, 'owner', ?)`,
		target, userID, stamp(now)); err != nil {
		t.Fatal(err)
	}

	activeOnlinePrincipal, activeOnline := pairESP32(t, ctx, svc, userID, target)
	if _, err := svc.Heartbeat(ctx, activeOnlinePrincipal, Heartbeat{
		Version: "1.0", BootID: "boot-a", UptimeS: 1,
	}); err != nil {
		t.Fatal(err)
	}

	_, activeOffline := pairESP32(t, ctx, svc, userID, target)
	stale := stamp(now.Add(-onlineWindow))
	if _, err := svc.db.Exec(`UPDATE devices SET last_seen_at = ? WHERE id = ?`, stale, activeOffline); err != nil {
		t.Fatal(err)
	}

	revokedOnlinePrincipal, revokedOnline := pairESP32(t, ctx, svc, userID, target)
	if _, err := svc.Heartbeat(ctx, revokedOnlinePrincipal, Heartbeat{
		Version: "1.0", BootID: "boot-r1", UptimeS: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := svc.RevokeDevice(ctx, userID, target, revokedOnline); err != nil {
		t.Fatal(err)
	}

	_, revokedOffline := pairESP32(t, ctx, svc, userID, target)
	if _, err := svc.db.Exec(`UPDATE devices SET last_seen_at = ? WHERE id = ?`, stale, revokedOffline); err != nil {
		t.Fatal(err)
	}
	if err := svc.RevokeDevice(ctx, userID, target, revokedOffline); err != nil {
		t.Fatal(err)
	}

	list, err := svc.ListAllHouseholdsForOperator(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var row OperatorHouseholdSummary
	for _, h := range list {
		if h.ID == target {
			row = h
			break
		}
	}
	if row.ID == "" {
		t.Fatal("target household missing from operator list")
	}
	if row.ScannersOnline != 1 || row.ScannersOffline != 1 {
		t.Fatalf("target scanners online=%d offline=%d want 1/1 (revoked pair excluded)", row.ScannersOnline, row.ScannersOffline)
	}
	_ = primary
	_ = activeOnline
	_ = activeOffline
	_ = revokedOnline
	_ = revokedOffline
}

func TestOperatorScansThisWeekLegacyDeviceIngestRespectsCutoff(t *testing.T) {
	for _, locName := range []string{"UTC", "America/Denver"} {
		loc, err := time.LoadLocation(locName)
		if err != nil {
			t.Fatal(err)
		}
		t.Run(locName, func(t *testing.T) {
			ctx, svc, userID, primary := bootDeviceAPI(t)
			house := newID()
			now := time.Date(2026, 10, 22, 9, 0, 0, 0, loc)
			svc.SetClock(func() time.Time { return now })
			if _, err := svc.db.Exec(`
				INSERT INTO households (id, name, restock_mode, created_at, channel_key, is_primary)
				VALUES (?, 'Legacy ingest', 'auto', ?, 'abcdef0123456789abcdef0123456789f2', 0)`, house, stamp(now)); err != nil {
				t.Fatal(err)
			}
			if _, err := svc.db.Exec(`INSERT INTO memberships (household_id, user_id, role, created_at) VALUES (?, ?, 'owner', ?)`,
				house, userID, stamp(now)); err != nil {
				t.Fatal(err)
			}
			principal, _ := pairESP32(t, ctx, svc, userID, house)
			cutoff := now.Add(-7 * 24 * time.Hour)
			ingestLegacyAt := func(clientID string, at time.Time) {
				t.Helper()
				results, err := svc.IngestScans(ctx, principal, []ScanInput{{
					ID: clientID, Barcode: "4000000000200", Direction: "stock_out", UnitCount: 1,
					HasBarcode: true, HasScannedAt: true, ScannedAt: at,
				}}, 0)
				if err != nil {
					t.Fatal(err)
				}
				if len(results) != 1 || results[0].Status != "created" {
					t.Fatalf("ingest %#v", results)
				}
			}
			ingestLegacyAt("legacy-at-cutoff", cutoff)
			ingestLegacyAt("legacy-before-cutoff", cutoff.Add(-time.Second))
			list, err := svc.ListAllHouseholdsForOperator(ctx)
			if err != nil {
				t.Fatal(err)
			}
			var scans int
			for _, row := range list {
				if row.ID == house {
					scans = row.ScansThisWeek
					break
				}
			}
			if scans != 1 {
				t.Fatalf("legacy path scansThisWeek=%d want 1 (time.Time scanned_at at cutoff only)", scans)
			}
			_ = primary
		})
	}
}

func TestListAllHouseholdsForOperatorResponseHasNoMemberPII(t *testing.T) {
	ctx, svc, _, primary := bootDeviceAPI(t)
	start := time.Date(2026, 10, 20, 8, 0, 0, 0, time.UTC)
	svc.SetClock(func() time.Time { return start })

	sentinelEmail := "pii-sentinel-dee@pantry.test"
	sentinelName := "Sentinel Dee"
	sentinelID := newID()
	if _, err := svc.db.Exec(`
		INSERT INTO users (id, name, email_norm, pw_hash, idp, operator, created_at)
		VALUES (?, ?, ?, 'x', 'local', 0, ?)`,
		sentinelID, sentinelName, sentinelEmail, stamp(start)); err != nil {
		t.Fatal(err)
	}
	other := newID()
	if _, err := svc.db.Exec(`
		INSERT INTO households (id, name, restock_mode, created_at, channel_key, is_primary)
		VALUES (?, 'Sentinel Loft', 'auto', ?, 'abcdef0123456789abcdef0123456789b1', 0)`, other, stamp(start)); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Exec(`INSERT INTO memberships (household_id, user_id, role, created_at) VALUES (?, ?, 'owner', ?)`,
		other, sentinelID, stamp(start)); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Exec(`INSERT INTO memberships (household_id, user_id, role, created_at) VALUES (?, ?, 'member', ?)`,
		primary, sentinelID, stamp(start.Add(time.Hour))); err != nil {
		t.Fatal(err)
	}

	list, err := svc.ListAllHouseholdsForOperator(ctx)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(map[string]any{"households": list})
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	for _, needle := range []string{sentinelEmail, sentinelName, sentinelID, "pii-sentinel", "Sentinel Dee"} {
		if strings.Contains(body, needle) {
			t.Fatalf("response leaked %q in %s", needle, body)
		}
	}
	if strings.Contains(body, `"members"`) {
		t.Fatalf("response must not include members key: %s", body)
	}
}

func seedOperatorDevice(t *testing.T, db *sql.DB, house, deviceID, kind, lastSeen, revoked string) {
	t.Helper()
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, err := db.Exec(`
		INSERT INTO devices (id, household_id, kind, label, created_at, last_seen_at, revoked_at)
		VALUES (?, ?, ?, ?, ?, NULLIF(?, ''), NULLIF(?, ''))`,
		deviceID, house, kind, deviceID, stamp(at), lastSeen, revoked); err != nil {
		t.Fatal(err)
	}
}

func TestRenameHouseholdRejectsNonMemberWithHouseholdNotFound(t *testing.T) {
	ctx, svc, userID, primary := bootDeviceAPI(t)
	expectCode(t, take2(svc.RenameHousehold(ctx, userID, "missing-household", "Nope")), CodeHouseholdNotFound)
	other := newID()
	if _, err := svc.db.Exec(`
		INSERT INTO households (id, name, restock_mode, created_at, channel_key, is_primary)
		VALUES (?, 'Solo', '', ?, 'abcdef0123456789abcdef0123456789ae', 0)`, other, stamp(time.Now())); err != nil {
		t.Fatal(err)
	}
	expectCode(t, take2(svc.RenameHousehold(ctx, userID, other, "Hack")), CodeHouseholdNotFound)
	_ = primary
}
