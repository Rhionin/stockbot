package supply

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/Rhionin/stockbot/internal/tenant"
)

const (
	cedarHouseholdID = "0190bbbb-cccc-7000-8000-0000000000e5"
	cedarChannelKey  = "0123456789abcdef0123456789abcdef"
	cedarCreatedAt   = "2026-01-15T12:00:00Z"
)

func scopeOf(t *testing.T, db *sql.DB) tenant.Scope {
	t.Helper()
	scope, err := auth.PrimaryScope(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	return scope
}

func TestSupplyRejectsZeroScope(t *testing.T) {
	db, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := app.RunMigrations(db); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	var zero tenant.Scope
	svc := Open(db)
	if _, err := svc.Plan(ctx, zero, time.Now()); !errors.Is(err, tenant.ErrMissingHousehold) {
		t.Fatalf("Plan: %v", err)
	}
	if _, err := svc.Override(ctx, zero, "prod-cedar"); !errors.Is(err, tenant.ErrMissingHousehold) {
		t.Fatalf("Override: %v", err)
	}
	if err := svc.SetOverride(ctx, zero, "prod-cedar", Override{}); !errors.Is(err, tenant.ErrMissingHousehold) {
		t.Fatalf("SetOverride: %v", err)
	}
	if err := svc.Wipe(ctx, zero, WipePhrase); !errors.Is(err, tenant.ErrMissingHousehold) {
		t.Fatalf("Wipe: %v", err)
	}
}

func TestSupplyOverrideHidesAnotherHousehold(t *testing.T) {
	db, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := app.RunMigrations(db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO households (id, name, restock_mode, created_at, channel_key, is_primary, legacy_user_id)
		VALUES (?, 'Household Cedar', '', ?, ?, 0, '')`,
		cedarHouseholdID, cedarCreatedAt, cedarChannelKey); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(
		`INSERT INTO supply_overrides (household_id, product_id, window_months) VALUES (?, 'prod-cedar', 4)`,
		cedarHouseholdID,
	); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	primary := scopeOf(t, db)
	svc := Open(db)
	got, err := svc.Override(ctx, primary, "prod-cedar")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got.Window(); ok {
		t.Fatal("household A read Cedar's supply override")
	}
	if _, ok := got.Quantity(); ok {
		t.Fatal("household A read Cedar's supply override")
	}
	window, err := Window(6)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.SetOverride(ctx, primary, "prod-cedar", window); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetOverride(ctx, primary, "prod-cedar", Override{}); err != nil {
		t.Fatal(err)
	}

	var household string
	var months int
	if err := db.QueryRow(
		`SELECT household_id, window_months FROM supply_overrides WHERE product_id = ? AND household_id = ?`,
		"prod-cedar", cedarHouseholdID,
	).Scan(&household, &months); err != nil {
		t.Fatal(err)
	}
	if household != cedarHouseholdID || months != 4 {
		t.Fatalf("Cedar's override changed: household %s months %d", household, months)
	}

	cedar, err := tenant.New(cedarHouseholdID)
	if err != nil {
		t.Fatal(err)
	}
	owned, err := svc.Override(ctx, cedar, "prod-cedar")
	if err != nil {
		t.Fatal(err)
	}
	gotMonths, ok := owned.Window()
	if !ok || gotMonths != 4 {
		t.Fatalf("Cedar could not read its override: %+v", owned)
	}
}
