package scan

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/history"
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

func TestBackfillItemHistoryListsOutBeforeIn(t *testing.T) {
	queue, db, scope := newTestQueueInternal(t)
	ctx := context.Background()
	at := time.Date(2026, 10, 26, 8, 0, 0, 0, time.UTC)
	itemID, _ := seedBackfillItem(t, db, scope, "item-order", 0, 0)

	result, err := queue.BackfillItem(ctx, scope, itemID, at)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE stock_moves SET id = 'bf-in' WHERE id = ?`, result.StockInMoveID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE stock_moves SET id = 'bf-out' WHERE id = ?`, result.StockOutMoveID); err != nil {
		t.Fatal(err)
	}

	journal := history.NewJournal(db)
	view, err := journal.Product(ctx, scope, itemID, at.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Moves) != 2 || view.Moves[0].ID != "bf-out" || view.Moves[0].Direction != "out" ||
		view.Moves[1].ID != "bf-in" || view.Moves[1].Direction != "in" || view.Moves[1].Source != "backfill" {
		t.Fatalf("moves = %+v", view.Moves)
	}
}

func TestBackfillItemReplenishesAfterUse(t *testing.T) {
	queue, db, scope := newTestQueueInternal(t)
	queue.Supply = supply.Open(db)
	ctx := context.Background()
	started := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, err := db.Exec(`INSERT INTO household_settings (household_id, key, value) VALUES (?, 'onboarding_started_at', ?)`,
		scope.HouseholdID(), started.Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC)
	itemID, productID := seedBackfillItem(t, db, scope, "item-repl", 0, 0)

	if _, err := queue.BackfillItem(ctx, scope, itemID, at); err != nil {
		t.Fatal(err)
	}
	lines, err := queue.Supply.Plan(ctx, scope, at.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, line := range lines {
		if line.Product == supply.ProductID(productID) {
			if line.Buy != 1 || line.Source != supply.SourceReplace {
				t.Fatalf("line = %+v, want buy 1 replace", line)
			}
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("no replenish line for %s in %+v", productID, lines)
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

func TestCommitBackfillTxZeroOnHand(t *testing.T) {
	queue, db, scope := newTestQueueInternal(t)
	ctx := context.Background()
	at := time.Date(2026, 10, 27, 10, 0, 0, 0, time.UTC)
	itemID, productID := seedBackfillItem(t, db, scope, "scan-bf", 0, 0)
	_ = itemID
	dir := StockOut
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	entry, err := queue.CreateScanEntryTx(ctx, scope, tx, ScanEntry{
		UserID: scope.HouseholdID(), Barcode: "4000000000999", ScannedAt: at, Direction: &dir, UnitCount: 1, ProductID: &productID,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := queue.CommitBackfillTx(ctx, scope, tx, entry)
	if err != nil {
		t.Fatal(err)
	}
	if result.Units != 1 || result.StockInMoveID == "" || result.StockOutMoveID == "" {
		t.Fatalf("result %+v", result)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var onHand int
	if err := db.QueryRow(`SELECT COUNT(*) FROM item_instances WHERE item_id = ? AND removed_at IS NULL`, itemID).Scan(&onHand); err != nil || onHand != 0 {
		t.Fatalf("on hand %d err %v", onHand, err)
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
