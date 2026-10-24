package scan_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/scan"
	"github.com/Rhionin/stockbot/internal/tenant"
)

func TestScanEntriesStayInsideTheHousehold(t *testing.T) {
	queue, db, primary := newTestQueue(t)
	ctx := context.Background()
	other := insertCedarHousehold(t, db)

	direction := scan.StockIn
	created, err := queue.CreateScanEntry(ctx, primary, scan.ScanEntry{
		UserID:    "scanner",
		Barcode:   "111222333444",
		ScannedAt: time.Now().UTC(),
		Direction: &direction,
		UnitCount: 1,
		Status:    scan.Pending,
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := queue.CreateScanEntry(ctx, tenant.Scope{}, scan.ScanEntry{
		Barcode: "999", UnitCount: 1,
	}); !errors.Is(err, tenant.ErrMissingHousehold) {
		t.Fatalf("zero scope create: %v", err)
	}
	if _, err := queue.GetScanEntry(ctx, tenant.Scope{}, created.ID); !errors.Is(err, tenant.ErrMissingHousehold) {
		t.Fatalf("zero scope get: %v", err)
	}
	if _, err := queue.ListScanEntries(ctx, tenant.Scope{}, ""); !errors.Is(err, tenant.ErrMissingHousehold) {
		t.Fatalf("zero scope list: %v", err)
	}
	if err := queue.UpdateScanEntry(ctx, tenant.Scope{}, created.ID, &direction, nil, nil, nil, nil); !errors.Is(err, tenant.ErrMissingHousehold) {
		t.Fatalf("zero scope update: %v", err)
	}
	if _, err := queue.CountOnHand(ctx, tenant.Scope{}, "missing"); !errors.Is(err, tenant.ErrMissingHousehold) {
		t.Fatalf("zero scope count: %v", err)
	}

	got, err := queue.GetScanEntry(ctx, other, created.ID)
	if err != nil || got != nil {
		t.Fatalf("other household saw %+v err %v", got, err)
	}
	listed, err := queue.ListScanEntries(ctx, other, "")
	if err != nil || len(listed) != 0 {
		t.Fatalf("other list %+v err %v", listed, err)
	}
	cancelled := scan.Cancelled
	if err := queue.UpdateScanEntry(ctx, other, created.ID, nil, nil, nil, nil, &cancelled); err == nil {
		t.Fatal("other household cancelled the scan")
	}
	if err := queue.BatchUpdateScanEntries(ctx, other, []string{created.ID}, &direction, nil, nil); err != nil {
		t.Fatal(err)
	}
	again, err := queue.CreateScanEntry(ctx, other, scan.ScanEntry{
		UserID:    "scanner",
		Barcode:   "111222333444",
		ScannedAt: time.Now().UTC(),
		Direction: &direction,
		UnitCount: 1,
		Status:    scan.Pending,
	})
	if err != nil {
		t.Fatal(err)
	}
	if again.ID == created.ID || again.UnitCount != 1 {
		t.Fatalf("other household merged into %+v", again)
	}

	owner, err := queue.GetScanEntry(ctx, primary, created.ID)
	if err != nil || owner == nil {
		t.Fatalf("owner read %+v err %v", owner, err)
	}
	if owner.UnitCount != 1 || owner.Status != scan.Pending || owner.UserID != "scanner" {
		t.Fatalf("owner entry changed %+v", owner)
	}
	var storedHousehold, storedUser string
	if err := db.QueryRow(`SELECT household_id, user_id FROM scan_entries WHERE id = ?`, created.ID).Scan(&storedHousehold, &storedUser); err != nil {
		t.Fatal(err)
	}
	if storedHousehold != primary.HouseholdID() || storedUser != "scanner" {
		t.Fatalf("stored household %s user %s", storedHousehold, storedUser)
	}
	owners, err := queue.ListScanEntries(ctx, primary, scan.Pending)
	if err != nil {
		t.Fatal(err)
	}
	if len(owners) != 1 || owners[0].ID != created.ID {
		t.Fatalf("owner list %+v", owners)
	}
}

func insertCedarHousehold(t *testing.T, db *sql.DB) tenant.Scope {
	t.Helper()
	const id = "0190bbbb-cccc-7000-8000-0000000000e5"
	_, err := db.Exec(`
		INSERT INTO households (id, name, restock_mode, created_at, channel_key, is_primary)
		VALUES (?, 'Household Cedar', '', '2026-02-02T00:00:00Z', 'abcdef0123456789abcdef0123456789', 0)`, id)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := tenant.New(id)
	if err != nil {
		t.Fatal(err)
	}
	return scope
}
