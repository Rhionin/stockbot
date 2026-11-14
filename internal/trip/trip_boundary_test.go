package trip_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/Rhionin/stockbot/internal/trip"
)

func TestTripAdvanceWritesBoundaryChanges(t *testing.T) {
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
	if _, err := db.Exec(`INSERT INTO list_line_changes (household_id, line_key, effect, cause, at, trip_seq)
		VALUES (?, 'item:old', 'added', 'taken_out', '2026-01-01T00:00:00Z', 0)`, hid); err != nil {
		t.Fatal(err)
	}
	adv := &trip.TripAdvance{DB: db, Now: func() time.Time { return time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC) }}
	toQty := 2
	if err := adv.Advance(context.Background(), scope, trip.CloseCauseNewCart, []trip.BoundaryChange{{
		LineKey: "item:item-new-trip",
		Effect:  "added",
		Cause:   "new_trip",
		ToQty:   &toQty,
	}}); err != nil {
		t.Fatal(err)
	}
	var seq int
	if err := db.QueryRow(`SELECT trip_seq FROM households WHERE id = ?`, hid).Scan(&seq); err != nil || seq != 1 {
		t.Fatalf("trip_seq %d", seq)
	}
	var stale int
	if err := db.QueryRow(`SELECT COUNT(*) FROM list_line_changes WHERE household_id = ? AND trip_seq < ?`, hid, seq).Scan(&stale); err != nil {
		t.Fatal(err)
	}
	if stale != 0 {
		t.Fatalf("stale rows %d", stale)
	}
	var effect string
	if err := db.QueryRow(`SELECT effect FROM list_line_changes WHERE household_id = ? AND line_key = ?`,
		hid, "item:item-new-trip").Scan(&effect); err != nil || effect != "added" {
		t.Fatalf("boundary row %q err %v", effect, err)
	}
}

func TestTripAdvanceFaultRollsBack(t *testing.T) {
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
	before := 1
	if err := db.QueryRow(`SELECT COUNT(*) FROM list_line_changes WHERE household_id = ?`, hid).Scan(&before); err != nil {
		t.Fatal(err)
	}
	adv := &trip.TripAdvance{
		DB: db,
		FaultAfterDeleteChanges: func() error { return errors.New("injected") },
	}
	err = adv.Advance(context.Background(), scope, trip.CloseCausePutAway, []trip.BoundaryChange{{
		LineKey: "item:x", Effect: "added", Cause: "new_trip",
	}})
	if err == nil {
		t.Fatal("expected fault")
	}
	var seq int
	if err := db.QueryRow(`SELECT trip_seq FROM households WHERE id = ?`, hid).Scan(&seq); err != nil || seq != 0 {
		t.Fatalf("trip_seq %d want 0", seq)
	}
	var after int
	if err := db.QueryRow(`SELECT COUNT(*) FROM list_line_changes WHERE household_id = ?`, hid).Scan(&after); err != nil || after != before {
		t.Fatalf("count %d want %d", after, before)
	}
}