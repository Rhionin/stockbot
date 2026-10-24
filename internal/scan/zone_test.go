package scan_test

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestStockOutTakesTheEarlierExpiryAcrossDST(t *testing.T) {
	loc, err := time.LoadLocation("America/Denver")
	if err != nil {
		t.Fatal(err)
	}
	later := time.Date(2026, 11, 1, 0, 30, 0, 0, loc)
	earlier := time.Date(2026, 11, 1, 6, 0, 0, 0, time.UTC)
	if !later.After(earlier) || !later.IsDST() {
		t.Fatalf("dst fixture later=%s earlier=%s", later, earlier)
	}

	queue, db, scope := newTestQueue(t)
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`INSERT INTO products (id, name) VALUES ('prod', 'Milk')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO items (id, user_id, product_id, household_id) VALUES ('item', ?, 'prod', ?)`,
		scope.HouseholdID(), scope.HouseholdID()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO item_instances (id, item_id, household_id, stock_in_at, expires_at) VALUES
			('early', 'item', ?, ?, ?),
			('late', 'item', ?, ?, ?)`,
		scope.HouseholdID(), earlier, earlier, scope.HouseholdID(), earlier, later); err != nil {
		t.Fatal(err)
	}
	if err := queue.StockOut(context.Background(), scope, "item", later); err != nil {
		t.Fatal(err)
	}
	var id, raw string
	if err := db.QueryRow(`
		SELECT id, CAST(removed_at AS TEXT) FROM item_instances WHERE removed_at IS NOT NULL`).Scan(&id, &raw); err != nil {
		t.Fatal(err)
	}
	if id != "early" {
		t.Fatalf("removed %s, want the earlier UTC expiry", id)
	}
	if !strings.Contains(raw, "+0000 UTC") || strings.Contains(raw, "MDT") {
		t.Fatalf("removed_at = %s", raw)
	}
}
