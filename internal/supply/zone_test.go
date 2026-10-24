package supply

import (
	"context"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/app"
)

func TestPlanFollowsTheLaterEventAcrossDST(t *testing.T) {
	loc, err := time.LoadLocation("America/Denver")
	if err != nil {
		t.Fatal(err)
	}
	later := time.Date(2026, 11, 1, 0, 30, 0, 0, loc)
	earlier := time.Date(2026, 11, 1, 6, 0, 0, 0, time.UTC)
	between := time.Date(2026, 11, 1, 6, 15, 0, 0, time.UTC)
	if !later.After(between) || !between.After(earlier) || !later.IsDST() {
		t.Fatalf("dst fixture later=%s between=%s earlier=%s", later, between, earlier)
	}

	db, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)
	if err := app.RunMigrations(db); err != nil {
		t.Fatal(err)
	}
	scope := scopeOf(t, db)
	household := scope.HouseholdID()
	if _, err := db.Exec(`
		INSERT INTO products (id, name, net_base_value, net_dimension, net_size_origin) VALUES
			('cons-night', 'Night Oats', 400, 'mass', 'manual'),
			('cons-day', 'Day Oats', 400, 'mass', 'manual'),
			('stock-night', 'Night Rice', 400, 'mass', 'manual'),
			('stock-day', 'Day Rice', 400, 'mass', 'manual');
		INSERT INTO items (id, user_id, product_id, household_id) VALUES
			('i-cons-night', ?, 'cons-night', ?),
			('i-cons-day', ?, 'cons-day', ?),
			('i-stock-night', ?, 'stock-night', ?),
			('i-stock-day', ?, 'stock-day', ?);
		INSERT INTO product_groups (id, user_id, household_id, name, name_key, rule, quantity_base_value, quantity_dimension) VALUES
			('g-cons', ?, ?, 'Oats', 'oats', 'same_as_ran_out', 400, 'mass'),
			('g-stock', ?, ?, 'Rice', 'rice', 'same_as_ran_out', 400, 'mass');
		INSERT INTO product_group_members (product_id, group_id, household_id) VALUES
			('cons-night', 'g-cons', ?), ('cons-day', 'g-cons', ?),
			('stock-night', 'g-stock', ?), ('stock-day', 'g-stock', ?);
		INSERT INTO app_settings (key, value) VALUES ('onboarding_started_at', '2020-01-01T00:00:00Z')`,
		household, household, household, household, household, household, household, household,
		household, household, household, household,
		household, household, household, household); err != nil {
		t.Fatal(err)
	}
	consume := []struct {
		id, item string
		at       time.Time
	}{
		{"c1", "i-cons-night", earlier},
		{"c2", "i-cons-night", later},
		{"c3", "i-cons-day", between},
	}
	for _, row := range consume {
		if _, err := db.Exec(`INSERT INTO consumption_events (id, household_id, item_id, consumed_at) VALUES (?, ?, ?, ?)`, row.id, household, row.item, row.at); err != nil {
			t.Fatal(err)
		}
	}
	stock := []struct {
		product string
		at      time.Time
	}{
		{"stock-night", earlier},
		{"stock-night", later},
		{"stock-day", between},
	}
	for i, row := range stock {
		if _, err := db.Exec(`INSERT INTO stock_in_events (household_id, id, product_id, at) VALUES (?, ?, ?, ?)`, household, i+1, row.product, row.at); err != nil {
			t.Fatal(err)
		}
	}

	lines, err := Open(db).Plan(context.Background(), scope, later.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, line := range lines {
		got[string(line.Product)] = line.Note
	}
	if got["cons-night"] == "" || got["stock-night"] == "" || got["cons-day"] != "" || got["stock-day"] != "" {
		t.Fatalf("lines = %#v", lines)
	}
}
