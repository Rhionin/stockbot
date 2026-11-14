package trip_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/Rhionin/stockbot/internal/shopping"
	"github.com/Rhionin/stockbot/internal/trip"
)

func TestFaultAfterAdvanceSeqRollsBackBeforeLapsedZeroDelete(t *testing.T) {
	conn, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := app.RunMigrations(conn); err != nil {
		t.Fatal(err)
	}
	scope, err := auth.PrimaryScope(context.Background(), conn)
	if err != nil {
		t.Fatal(err)
	}
	hid := scope.HouseholdID()
	if _, err := conn.Exec(`INSERT INTO products (id, name, category) VALUES ('p', 'P', 'c')`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO items (id, user_id, household_id, product_id) VALUES ('item-zadv', ?, ?, 'p')`, hid, hid); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO shopping_list_items (id, household_id, user_id, item_id, quantity, source, created_at)
		VALUES ('line-zadv', ?, ?, 'item-zadv', 1, 'auto', datetime('now'))`, hid, hid); err != nil {
		t.Fatal(err)
	}
	closed := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	tx, err := conn.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := trip.AdvanceTripForNewCart(context.Background(), tx, hid, closed); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO shopping_list_entry_adjustments (household_id, entry_id, provider_id, quantity, expires_trip)
		VALUES (?, 'line-zadv', 'kroger', 0, 2)`, hid); err != nil {
		t.Fatal(err)
	}
	removed := closed.Add(time.Hour).Format(time.RFC3339Nano)
	if _, err := conn.Exec(`INSERT INTO staged_cart_skips (household_id, user_id, item_id, expires_trip, removed_at)
		VALUES (?, ?, 'item-other', 9, ?)`, hid, hid, removed); err != nil {
		t.Fatal(err)
	}
	var seqBefore int
	if err := conn.QueryRow(`SELECT trip_seq FROM households WHERE id = ?`, hid).Scan(&seqBefore); err != nil {
		t.Fatal(err)
	}
	restore := trip.SetFaultAfterAdvanceSeqForTest(func() error {
		return errors.New("injected fault before lapsed zero delete")
	})
	t.Cleanup(restore)
	tx, err = conn.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	err = trip.AdvanceTripForNewCart(context.Background(), tx, hid, closed.Add(2*time.Hour))
	if err == nil {
		t.Fatal("expected fault")
	}
	_ = tx.Rollback()
	var seqAfter int
	if err := conn.QueryRow(`SELECT trip_seq FROM households WHERE id = ?`, hid).Scan(&seqAfter); err != nil {
		t.Fatal(err)
	}
	if seqAfter != seqBefore {
		t.Fatalf("trip_seq %d -> %d", seqBefore, seqAfter)
	}
	var adjCount int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM shopping_list_entry_adjustments WHERE entry_id = 'line-zadv'`).Scan(&adjCount); err != nil {
		t.Fatal(err)
	}
	if adjCount != 1 {
		t.Fatalf("zero adjustment rows %d want 1", adjCount)
	}
}

func TestClearLapsedZeroAdjustmentsWritesNewTripBoundary(t *testing.T) {
	conn, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := app.RunMigrations(conn); err != nil {
		t.Fatal(err)
	}
	scope, err := auth.PrimaryScope(context.Background(), conn)
	if err != nil {
		t.Fatal(err)
	}
	hid := scope.HouseholdID()
	_ = scope
	if _, err := conn.Exec(`INSERT INTO products (id, name, category) VALUES ('pb', 'PB', 'c')`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO items (id, user_id, household_id, product_id) VALUES ('item-bd', ?, ?, 'pb')`, hid, hid); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO shopping_list_items (id, household_id, user_id, item_id, quantity, source, created_at)
		VALUES ('line-bd', ?, ?, 'item-bd', 1, 'auto', datetime('now'))`, hid, hid); err != nil {
		t.Fatal(err)
	}
	var tripSeq int
	if err := conn.QueryRow(`SELECT trip_seq FROM households WHERE id = ?`, hid).Scan(&tripSeq); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO shopping_list_entry_adjustments (household_id, entry_id, provider_id, quantity, expires_trip)
		VALUES (?, 'line-bd', 'kroger', 0, ?)`, hid, tripSeq); err != nil {
		t.Fatal(err)
	}
	closed := time.Date(2026, 11, 14, 15, 0, 0, 0, time.UTC)
	removed := closed.Format(time.RFC3339Nano)
	if _, err := conn.Exec(`INSERT INTO staged_cart_skips (household_id, user_id, item_id, expires_trip, removed_at)
		VALUES (?, ?, 'item-x', 5, ?)`, hid, hid, removed); err != nil {
		t.Fatal(err)
	}
	tx, err := conn.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := trip.AdvanceTripForNewCart(context.Background(), tx, hid, closed); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	lineKey := shopping.ItemLineKey("item-bd")
	var n int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM list_line_changes WHERE household_id = ? AND line_key = ? AND cause = 'new_trip'`,
		hid, lineKey).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("new_trip boundary rows %d", n)
	}
	var adj int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM shopping_list_entry_adjustments WHERE entry_id = 'line-bd'`).Scan(&adj); err != nil {
		t.Fatal(err)
	}
	if adj != 0 {
		t.Fatalf("lapsed zero not deleted, rows %d", adj)
	}
}