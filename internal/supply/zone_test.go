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
	if _, err := db.Exec(`
		INSERT INTO products (id, name, net_base_value, net_dimension, net_size_origin) VALUES
			('cons-night', 'Night Oats', 400, 'mass', 'manual'),
			('cons-day', 'Day Oats', 400, 'mass', 'manual'),
			('stock-night', 'Night Rice', 400, 'mass', 'manual'),
			('stock-day', 'Day Rice', 400, 'mass', 'manual');
		INSERT INTO items (id, user_id, product_id) VALUES
			('i-cons-night', 'user-1', 'cons-night'),
			('i-cons-day', 'user-1', 'cons-day'),
			('i-stock-night', 'user-1', 'stock-night'),
			('i-stock-day', 'user-1', 'stock-day');
		INSERT INTO product_groups (id, user_id, name, name_key, rule, quantity_base_value, quantity_dimension) VALUES
			('g-cons', 'user-1', 'Oats', 'oats', 'same_as_ran_out', 400, 'mass'),
			('g-stock', 'user-1', 'Rice', 'rice', 'same_as_ran_out', 400, 'mass');
		INSERT INTO product_group_members (product_id, group_id) VALUES
			('cons-night', 'g-cons'), ('cons-day', 'g-cons'),
			('stock-night', 'g-stock'), ('stock-day', 'g-stock');
		INSERT INTO app_settings (key, value) VALUES ('onboarding_started_at', '2020-01-01T00:00:00Z')`); err != nil {
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
		if _, err := db.Exec(`INSERT INTO consumption_events (id, item_id, consumed_at) VALUES (?, ?, ?)`, row.id, row.item, row.at); err != nil {
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
	for _, row := range stock {
		if _, err := db.Exec(`INSERT INTO stock_in_events (product_id, at) VALUES (?, ?)`, row.product, row.at); err != nil {
			t.Fatal(err)
		}
	}

	lines, err := Open(db).Plan(context.Background(), later.Add(24*time.Hour))
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
