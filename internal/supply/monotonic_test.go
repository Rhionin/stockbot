package supply

import (
	"context"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/Rhionin/stockbot/internal/app"
)

// A stock-out used to store time.Now().String(), monotonic clock and all.
// Group detail and the shopping plan both read that text back.
func TestGroupUsageAndPlanReadMonotonicConsumption(t *testing.T) {
	conn, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	conn.SetMaxOpenConns(1)
	if err := app.RunMigrations(conn); err != nil {
		t.Fatal(err)
	}
	scope := scopeOf(t, conn)
	household := scope.HouseholdID()
	if _, err := conn.Exec(`INSERT INTO products (id, name, unit_of_measure) VALUES ('prod-rice', 'Rice', 'bag')`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO items (id, user_id, product_id, household_id) VALUES ('item-rice', ?, 'prod-rice', ?)`, household, household); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO product_groups (id, user_id, household_id, name, name_key, rule) VALUES ('group-rice', ?, ?, 'Rice', 'rice', 'same_as_ran_out')`, household, household); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO product_group_members (product_id, group_id, household_id) VALUES ('prod-rice', 'group-rice', ?)`, household); err != nil {
		t.Fatal(err)
	}
	started := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	if _, err := conn.Exec(
		`INSERT INTO app_settings (key, value) VALUES ('onboarding_started_at', ?)`,
		started.Format(time.RFC3339),
	); err != nil {
		t.Fatal(err)
	}
	const consumed = "2026-10-17 18:55:19.032067103 +0000 UTC m=+12.3"
	if _, err := conn.Exec(
		`INSERT INTO consumption_events (id, household_id, item_id, consumed_at) VALUES ('ce-rice', ?, 'item-rice', ?)`,
		household, consumed,
	); err != nil {
		t.Fatal(err)
	}

	svc := Open(conn)
	now := time.Date(2026, 10, 17, 20, 0, 0, 0, time.UTC)
	ctx := context.Background()
	if _, err := svc.GroupUsage(ctx, scope, "group-rice", now); err != nil {
		t.Fatalf("GroupUsage: %v", err)
	}
	if _, err := svc.Plan(ctx, scope, now); err != nil {
		t.Fatalf("Plan: %v", err)
	}
}
