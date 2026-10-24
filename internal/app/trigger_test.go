package app_test

import (
	"context"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/Rhionin/stockbot/internal/history"
)

// Raising a stock-in on a shelved item has to put the item back. The old
// trigger did that. The Go path is inventory.UnshelveItem inside the quantity edit.
func TestRaisingAShelvedStockInPutsTheItemBack(t *testing.T) {
	conn, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := app.RunMigrations(conn); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	scope, err := auth.PrimaryScope(ctx, conn)
	if err != nil {
		t.Fatal(err)
	}
	household := scope.HouseholdID()
	at := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	if _, err := conn.Exec(`INSERT INTO products (id, name) VALUES ('prod-held', 'Held oats')`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO items (id, user_id, product_id, household_id, shelved) VALUES ('item-held', ?, 'prod-held', ?, 1)`, household, household); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO stock_moves (id, item_id, product_id, direction, quantity, at, source, household_id) VALUES ('move-held', 'item-held', 'prod-held', 'in', 1, ?, 'manual', ?)`, at, household); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO item_instances (id, item_id, stock_in_at, stock_move_id, household_id) VALUES ('inst-held', 'item-held', ?, 'move-held', ?)`, at, household); err != nil {
		t.Fatal(err)
	}

	if _, err := history.NewJournal(conn).SetQuantity(ctx, scope, "move-held", 2, at); err != nil {
		t.Fatal(err)
	}
	var shelved int
	if err := conn.QueryRow(`SELECT shelved FROM items WHERE id = 'item-held'`).Scan(&shelved); err != nil {
		t.Fatal(err)
	}
	if shelved != 0 {
		t.Fatalf("shelved = %d, want 0", shelved)
	}
	var quantity int
	if err := conn.QueryRow(`SELECT quantity FROM stock_moves WHERE id = 'move-held'`).Scan(&quantity); err != nil {
		t.Fatal(err)
	}
	if quantity != 2 {
		t.Fatalf("quantity = %d, want 2", quantity)
	}
}
