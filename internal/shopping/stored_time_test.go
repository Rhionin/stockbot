package shopping

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/app"
)

func TestMarkPurchasedStoresCleanUTC(t *testing.T) {
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
	if _, err := db.Exec(`INSERT INTO products (id, name) VALUES ('prod', 'Oats')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO items (id, user_id, product_id, household_id) VALUES ('item', ?, 'prod', ?)`, household, household); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO shopping_list_items (id, household_id, user_id, item_id, quantity, source)
		VALUES ('line', ?, ?, 'item', 1, 'manual')`, household, household); err != nil {
		t.Fatal(err)
	}
	if err := NewStore(db).MarkPurchased(context.Background(), scope, "line"); err != nil {
		t.Fatal(err)
	}
	var raw string
	if err := db.QueryRow(`SELECT CAST(purchased_at AS TEXT) FROM shopping_list_items WHERE id = ? AND household_id = ?`, "line", household).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(raw, "+0000 UTC") || strings.Contains(raw, "m=") {
		t.Fatalf("purchased_at = %q", raw)
	}
}

func TestSuffixedPurchasedAtReadsBack(t *testing.T) {
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
	const stored = "2026-10-03 18:12:53.123456789 +0000 UTC m=+12.5"
	want := time.Date(2026, 10, 3, 18, 12, 53, 123456789, time.UTC)
	if _, err := db.Exec(`INSERT INTO products (id, name) VALUES ('prod', 'Oats')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO items (id, user_id, product_id, household_id) VALUES ('item', ?, 'prod', ?)`, household, household); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO shopping_list_items (id, household_id, user_id, item_id, quantity, source, purchased_at)
		VALUES ('line', ?, ?, 'item', 1, 'manual', ?)`, household, household, stored); err != nil {
		t.Fatal(err)
	}
	got, err := NewStore(db).GetItemByID(context.Background(), scope, "line")
	if err != nil {
		t.Fatal(err)
	}
	if got.PurchasedAt == nil || !got.PurchasedAt.Equal(want) {
		t.Fatalf("purchased_at = %v", got.PurchasedAt)
	}
}
