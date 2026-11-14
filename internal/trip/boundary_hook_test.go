package trip_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/Rhionin/stockbot/internal/trip"
)

func TestTryAdvanceTripRunsBoundaryPlanner(t *testing.T) {
	db, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := app.RunMigrations(db); err != nil {
		t.Fatal(err)
	}
	scope, err := auth.PrimaryScope(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	hid := scope.HouseholdID()
	t.Cleanup(func() { trip.SetBoundaryPlanner(nil) })
	trip.SetBoundaryPlanner(func(ctx context.Context, householdID string, tripSeq int, tx *sql.Tx) ([]trip.BoundaryChange, error) {
		if householdID != hid || tripSeq != 1 {
			t.Fatalf("planner household %s seq %d", householdID, tripSeq)
		}
		one := 1
		return []trip.BoundaryChange{{
			LineKey: "item:item-boundary-hook", Effect: "added", Cause: "new_trip", ToQty: &one,
		}}, nil
	})
	base := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		at := base.Add(time.Duration(i) * time.Minute).Format(time.RFC3339Nano)
		pid := fmt.Sprintf("hook-%d", i)
		if _, err := db.Exec(`INSERT INTO products (id, name, category) VALUES (?, ?, 't')`, pid, pid); err != nil {
			t.Fatal(err)
		}
		item := "item-" + pid
		if _, err := db.Exec(`INSERT INTO items (id, user_id, household_id, product_id) VALUES (?, ?, ?, ?)`, item, hid, hid, pid); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`
			INSERT INTO stock_moves (id, household_id, item_id, product_id, direction, quantity, at, source, tracked, created_at)
			VALUES (?, ?, ?, ?, 'in', 1, ?, 'scan', 1, ?)`, "mv-"+pid, hid, item, pid, at, at); err != nil {
			t.Fatal(err)
		}
	}
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := trip.MaybeCloseMassStockIn(context.Background(), scope, tx, base.Add(9*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var effect string
	if err := db.QueryRow(`SELECT effect FROM list_line_changes WHERE household_id = ? AND line_key = ?`,
		hid, "item:item-boundary-hook").Scan(&effect); err != nil || effect != "added" {
		t.Fatalf("boundary row %q err %v", effect, err)
	}
}

func TestBoundaryPlannerErrorPreventsTripAdvance(t *testing.T) {
	db, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := app.RunMigrations(db); err != nil {
		t.Fatal(err)
	}
	scope, err := auth.PrimaryScope(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	hid := scope.HouseholdID()
	t.Cleanup(func() { trip.SetBoundaryPlanner(nil) })
	trip.SetBoundaryPlanner(func(context.Context, string, int, *sql.Tx) ([]trip.BoundaryChange, error) {
		return nil, errors.New("planner failed")
	})
	base := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		at := base.Add(time.Duration(i) * time.Minute).Format(time.RFC3339Nano)
		pid := fmt.Sprintf("err-%d", i)
		item := "item-" + pid
		if _, err := db.Exec(`INSERT INTO products (id, name, category) VALUES (?, ?, 't')`, pid, pid); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO items (id, user_id, household_id, product_id) VALUES (?, ?, ?, ?)`, item, hid, hid, pid); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`
			INSERT INTO stock_moves (id, household_id, item_id, product_id, direction, quantity, at, source, tracked, created_at)
			VALUES (?, ?, ?, ?, 'in', 1, ?, 'scan', 1, ?)`, "mv-"+pid, hid, item, pid, at, at); err != nil {
			t.Fatal(err)
		}
	}
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	err = trip.MaybeCloseMassStockIn(context.Background(), scope, tx, base.Add(9*time.Minute))
	if err == nil {
		t.Fatal("expected planner failure")
	}
	_ = tx.Rollback()
	var seq int
	if err := db.QueryRow(`SELECT trip_seq FROM households WHERE id = ?`, hid).Scan(&seq); err != nil || seq != 0 {
		t.Fatalf("trip_seq %d", seq)
	}
}