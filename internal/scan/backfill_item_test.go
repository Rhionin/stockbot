package scan

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/supply"
	"github.com/Rhionin/stockbot/internal/tenant"
)

func TestBackfillItemAtZeroOnHand(t *testing.T) {
	queue, db, scope := newTestQueueInternal(t)
	ctx := context.Background()
	at := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	itemID, productID := seedBackfillItem(t, db, scope, "item-zero", 0, 0)

	got, err := queue.BackfillItem(ctx, scope, itemID, at)
	if err != nil {
		t.Fatal(err)
	}
	if got.ItemID != itemID || got.Units != 1 || got.StockInMoveID == "" || got.StockOutMoveID == "" {
		t.Fatalf("result = %+v", got)
	}
	_ = productID
	var onHand int
	if err := db.QueryRow(`SELECT COUNT(*) FROM item_instances WHERE item_id = ? AND removed_at IS NULL`, itemID).Scan(&onHand); err != nil {
		t.Fatal(err)
	}
	if onHand != 0 {
		t.Fatalf("on hand = %d", onHand)
	}
	var consumption int
	if err := db.QueryRow(`SELECT COUNT(*) FROM consumption_events WHERE item_id = ?`, itemID).Scan(&consumption); err != nil {
		t.Fatal(err)
	}
	if consumption != 1 {
		t.Fatalf("consumption events = %d", consumption)
	}
}

func TestBackfillItemUnshelvesShelvedItem(t *testing.T) {
	queue, db, scope := newTestQueueInternal(t)
	ctx := context.Background()
	at := time.Date(2026, 10, 25, 13, 0, 0, 0, time.UTC)
	itemID, _ := seedBackfillItem(t, db, scope, "item-shelved", 1, 0)

	if _, err := queue.BackfillItem(ctx, scope, itemID, at); err != nil {
		t.Fatal(err)
	}
	var shelved int
	if err := db.QueryRow(`SELECT shelved FROM items WHERE id = ?`, itemID).Scan(&shelved); err != nil {
		t.Fatal(err)
	}
	if shelved != 0 {
		t.Fatalf("shelved = %d", shelved)
	}
}

func TestBackfillItemRejectsWhenOnHand(t *testing.T) {
	queue, db, scope := newTestQueueInternal(t)
	ctx := context.Background()
	at := time.Now().UTC()
	itemID, _ := seedBackfillItem(t, db, scope, "item-full", 0, 0)
	if _, err := db.Exec(`INSERT INTO item_instances (id, item_id, household_id, stock_in_at) VALUES ('inst-1', ?, ?, ?)`,
		itemID, scope.HouseholdID(), at); err != nil {
		t.Fatal(err)
	}

	_, err := queue.BackfillItem(ctx, scope, itemID, at)
	var onHandErr *UnitsOnHandError
	if !errors.As(err, &onHandErr) || onHandErr.OnHand != 1 {
		t.Fatalf("err = %v", err)
	}
}

func TestBackfillItemMissingItem(t *testing.T) {
	queue, _, scope := newTestQueueInternal(t)
	_, err := queue.BackfillItem(context.Background(), scope, "missing-item", time.Now().UTC())
	if !errors.Is(err, ErrItemNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestBackfillItemSkipsConsumptionDuringOpening(t *testing.T) {
	queue, db, scope := newTestQueueInternal(t)
	queue.Supply = supply.Open(db)
	ctx := context.Background()
	at := time.Date(2026, 10, 25, 14, 0, 0, 0, time.UTC)
	itemID, _ := seedBackfillItem(t, db, scope, "item-opening", 0, 0)

	if _, err := queue.BackfillItem(ctx, scope, itemID, at); err != nil {
		t.Fatal(err)
	}
	var consumption int
	if err := db.QueryRow(`SELECT COUNT(*) FROM consumption_events WHERE item_id = ?`, itemID).Scan(&consumption); err != nil {
		t.Fatal(err)
	}
	if consumption != 0 {
		t.Fatalf("consumption events = %d, want none during opening", consumption)
	}
}

func seedBackfillItem(t *testing.T, db *sql.DB, scope tenant.Scope, itemID string, shelved int, instances int) (string, string) {
	t.Helper()
	productID := "prod-" + itemID
	if _, err := db.Exec(`INSERT INTO products (id, name, category, unit_of_measure) VALUES (?, 'Oats', 'Pantry', 'unit')`, productID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO items (id, user_id, product_id, household_id, shelved) VALUES (?, ?, ?, ?, ?)`,
		itemID, scope.HouseholdID(), productID, scope.HouseholdID(), shelved); err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC()
	for i := 0; i < instances; i++ {
		if _, err := db.Exec(`INSERT INTO item_instances (id, item_id, household_id, stock_in_at) VALUES (?, ?, ?, ?)`,
			fmt.Sprintf("%s-inst-%d", itemID, i), itemID, scope.HouseholdID(), at); err != nil {
			t.Fatal(err)
		}
	}
	return itemID, productID
}
