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
	if _, err := db.Exec(`
		INSERT INTO products (id, name) VALUES ('prod', 'Oats');
		INSERT INTO items (id, user_id, product_id) VALUES ('item', 'user-1', 'prod');
		INSERT INTO shopping_list_items (id, user_id, item_id, quantity, source)
		VALUES ('line', 'user-1', 'item', 1, 'manual')`); err != nil {
		t.Fatal(err)
	}
	if err := NewStore(db).MarkPurchased(context.Background(), "line"); err != nil {
		t.Fatal(err)
	}
	var raw string
	if err := db.QueryRow(`SELECT CAST(purchased_at AS TEXT) FROM shopping_list_items WHERE id = 'line'`).Scan(&raw); err != nil {
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
	const stored = "2026-10-03 18:12:53.123456789 +0000 UTC m=+12.5"
	want := time.Date(2026, 10, 3, 18, 12, 53, 123456789, time.UTC)
	if _, err := db.Exec(`
		INSERT INTO products (id, name) VALUES ('prod', 'Oats');
		INSERT INTO items (id, user_id, product_id) VALUES ('item', 'user-1', 'prod');
		INSERT INTO shopping_list_items (id, user_id, item_id, quantity, source, purchased_at)
		VALUES ('line', 'user-1', 'item', 1, 'manual', ?)`, stored); err != nil {
		t.Fatal(err)
	}
	got, err := NewStore(db).GetItemByID(context.Background(), "line")
	if err != nil {
		t.Fatal(err)
	}
	if got.PurchasedAt == nil || !got.PurchasedAt.Equal(want) {
		t.Fatalf("purchased_at = %v", got.PurchasedAt)
	}
}
