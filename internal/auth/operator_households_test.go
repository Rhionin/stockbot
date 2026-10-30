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
			if len(row.Members) != 0 {
				t.Fatalf("members must be empty %#v", row.Members)
			}
		}
		if len(row.Members) != 0 {
			t.Fatalf("household %s members %#v", row.ID, row.Members)
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

func TestListAllHouseholdsForOperatorScannerCounts(t *testing.T) {
	ctx, svc, userID, _ := bootDeviceAPI(t)
	houseA := newID()
	houseB := newID()
	start := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	svc.SetClock(func() time.Time { return start })
	for _, spec := range []struct {
		id   string
		name string
		key  string
	}{
		{houseA, "Alpha", "abcdef0123456789abcdef0123456789af"},
		{houseB, "Beta", "abcdef0123456789abcdef0123456789b2"},
	} {
		if _, err := svc.db.Exec(`
			INSERT INTO households (id, name, restock_mode, created_at, channel_key, is_primary)
			VALUES (?, ?, 'auto', ?, ?, 0)`, spec.id, spec.name, stamp(start), spec.key); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.db.Exec(`INSERT INTO memberships (household_id, user_id, role, created_at) VALUES (?, ?, 'owner', ?)`,
			spec.id, userID, stamp(start)); err != nil {
			t.Fatal(err)
		}
	}

	seedOperatorDevice(t, svc.db, houseA, "dev-on", "esp32_scanner", stamp(start.Add(-time.Minute)), "")
	seedOperatorDevice(t, svc.db, houseA, "dev-off", "esp32_scanner", stamp(start.Add(-onlineWindow)), "")
	seedOperatorDevice(t, svc.db, houseA, "dev-revoked", "box_hub", stamp(start), stamp(start))
	seedOperatorDevice(t, svc.db, houseB, "dev-other-on", "esp32_scanner", stamp(start.Add(-time.Minute)), "")

	list, err := svc.ListAllHouseholdsForOperator(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][2]int{
		houseA: {1, 1},
		houseB: {1, 0},
	}
	for _, row := range list {
		if counts, ok := want[row.ID]; ok {
			if row.ScannersOnline != counts[0] || row.ScannersOffline != counts[1] {
				t.Fatalf("house %s scanners online=%d offline=%d want %v", row.ID, row.ScannersOnline, row.ScannersOffline, counts)
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

func TestListAllHouseholdsForOperatorScansThisWeek(t *testing.T) {
	ctx, svc, userID, primary := bootDeviceAPI(t)
	other := newID()
	now := time.Date(2026, 10, 15, 15, 0, 0, 0, time.UTC)
	svc.SetClock(func() time.Time { return now })
	if _, err := svc.db.Exec(`
		INSERT INTO households (id, name, restock_mode, created_at, channel_key, is_primary)
		VALUES (?, 'West', 'auto', ?, 'abcdef0123456789abcdef0123456789b0', 0)`, other, stamp(now)); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Exec(`INSERT INTO memberships (household_id, user_id, role, created_at) VALUES (?, ?, 'owner', ?)`,
		other, userID, stamp(now)); err != nil {
		t.Fatal(err)
	}

	cutoff := now.Add(-7 * 24 * time.Hour)
	entryRecent := uuid.NewString()
	entryBoundary := uuid.NewString()
	entryOld := uuid.NewString()
	entryMerged := uuid.NewString()
	entryOther := uuid.NewString()

	insertScanEntry(t, svc.db, entryRecent, primary, userID, stamp(now.Add(-time.Hour)), 1, "", "")
	insertScanEntry(t, svc.db, entryBoundary, primary, userID, stamp(cutoff), 1, "", "")
	insertScanEntry(t, svc.db, entryOld, primary, userID, stamp(cutoff.Add(-time.Second)), 1, "", "")
	insertScanEntry(t, svc.db, entryMerged, primary, userID, stamp(now.Add(-2*time.Hour)), 3, "", "")
	insertScanEntry(t, svc.db, entryOther, other, userID, stamp(now.Add(-time.Hour)), 1, "", "")

	deviceID := newID()
	seedOperatorDevice(t, svc.db, primary, deviceID, "esp32_scanner", stamp(now), "")
	for i, clientID := range []string{"rcpt-1", "rcpt-2"} {
		if _, err := svc.db.Exec(`
			INSERT INTO device_scan_receipts (household_id, device_id, client_scan_id, scan_entry_id, barcode, direction, unit_count, scanned_at)
			VALUES (?, ?, ?, ?, '990000000001', 'stock_out', 1, ?)`,
			primary, deviceID, clientID, entryRecent, stamp(now.Add(-time.Duration(i+1)*time.Hour))); err != nil {
			t.Fatal(err)
		}
	}

	list, err := svc.ListAllHouseholdsForOperator(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range list {
		switch row.ID {
		case primary:
			// 2 device receipts + 1 boundary web + 3 merged web (entry has no receipts)
			if row.ScansThisWeek != 6 {
				t.Fatalf("primary scansThisWeek=%d want 6", row.ScansThisWeek)
			}
		case other:
			if row.ScansThisWeek != 1 {
				t.Fatalf("other scansThisWeek=%d want 1", row.ScansThisWeek)
			}
		}
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
	raw, err := json.Marshal(list)
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	for _, needle := range []string{sentinelEmail, sentinelName, sentinelID, "pii-sentinel", "Sentinel Dee"} {
		if strings.Contains(body, needle) {
			t.Fatalf("response leaked %q in %s", needle, body)
		}
	}
	for _, row := range list {
		if len(row.Members) != 0 {
			t.Fatalf("members must be empty got %#v", row.Members)
		}
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

func insertScanEntry(t *testing.T, db *sql.DB, id, house, userID, scannedAt string, unitCount int, deviceID, clientScanID string) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO scan_entries (id, user_id, household_id, device_id, client_scan_id, barcode, scanned_at, direction, unit_count, status)
		VALUES (?, ?, ?, NULLIF(?, ''), NULLIF(?, ''), '990000000002', ?, 'stock_out', ?, 'pending')`,
		id, userID, house, deviceID, clientScanID, scannedAt, unitCount); err != nil {
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
