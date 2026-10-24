package auth

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/tenant"
)

func TestPrimaryAndDirectScannerStayOnHouseholdOne(t *testing.T) {
	db := openAuthDB(t)
	ctx := context.Background()

	primary, err := PrimaryScope(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	device, err := DirectScanner(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if device.Scope.HouseholdID() != primary.HouseholdID() {
		t.Fatalf("scanner household %s, primary %s", device.Scope.HouseholdID(), primary.HouseholdID())
	}
	if device.ID == "" {
		t.Fatal("missing direct device id")
	}
	var label, kind string
	if err := db.QueryRow(`SELECT label, kind FROM devices WHERE id = ? AND household_id = ?`, device.ID, primary.HouseholdID()).Scan(&label, &kind); err != nil {
		t.Fatal(err)
	}
	if label != DirectDeviceLabel || kind != "direct" {
		t.Fatalf("device %s %s", kind, label)
	}

	const other = "0190aaaa-bbbb-7000-8000-0000000000c3"
	if _, err := db.Exec(`
		INSERT INTO households (id, name, restock_mode, created_at, channel_key, is_primary)
		VALUES (?, 'Household Cedar', '', '2026-02-02T00:00:00Z', 'abcdef0123456789abcdef0123456789', 0)`, other); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO devices (id, household_id, kind, label, created_at)
		VALUES ('0190aaaa-bbbb-7000-8000-0000000000d4', ?, 'direct', ?, '2026-02-02T00:00:00Z')`, other, DirectDeviceLabel); err != nil {
		t.Fatal(err)
	}

	again, err := DirectScanner(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != device.ID || again.Scope.HouseholdID() != primary.HouseholdID() {
		t.Fatalf("scanner moved to %+v", again)
	}
	if _, err := tenant.New(other); err != nil {
		t.Fatal(err)
	}
}

func TestRejectRetiredHeadlessUser(t *testing.T) {
	db := openAuthDB(t)
	ctx := context.Background()
	if err := RejectRetiredHeadlessUser(ctx, db, ""); err != nil {
		t.Fatal(err)
	}
	if err := RejectRetiredHeadlessUser(ctx, db, "   "); err != nil {
		t.Fatal(err)
	}
	var legacy string
	if err := db.QueryRow(`SELECT legacy_user_id FROM households WHERE is_primary = 1`).Scan(&legacy); err != nil {
		t.Fatal(err)
	}
	if legacy == "" {
		t.Fatal("primary household has no legacy id")
	}
	if err := RejectRetiredHeadlessUser(ctx, db, legacy); err != nil {
		t.Fatal(err)
	}
	if err := RejectRetiredHeadlessUser(ctx, db, " "+legacy+" "); err != nil {
		t.Fatal(err)
	}
	err := RejectRetiredHeadlessUser(ctx, db, "someone-else")
	if err == nil || !strings.Contains(err.Error(), "HEADLESS_USER_ID") {
		t.Fatalf("err %v", err)
	}
}

func TestPrimaryScopeMissingHousehold(t *testing.T) {
	db := openAuthDB(t)
	if _, err := db.Exec(`DELETE FROM devices`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM change_versions`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM households`); err != nil {
		t.Fatal(err)
	}
	_, err := PrimaryScope(context.Background(), db)
	if err == nil {
		t.Fatal("missing household succeeded")
	}
	_, err = DirectScanner(context.Background(), db)
	if err == nil || !strings.Contains(err.Error(), "not set up") {
		t.Fatalf("err %v", err)
	}
}

func TestDirectScannerWithoutSeed(t *testing.T) {
	db := openAuthDB(t)
	if _, err := db.Exec(`DELETE FROM devices`); err != nil {
		t.Fatal(err)
	}
	_, err := DirectScanner(context.Background(), db)
	if err == nil || !errors.Is(err, sql.ErrNoRows) && !strings.Contains(err.Error(), "not set up") {
		t.Fatalf("err %v", err)
	}
}

func openAuthDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := app.RunMigrations(db); err != nil {
		t.Fatal(err)
	}
	return db
}
