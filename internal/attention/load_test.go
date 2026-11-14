package attention

import (
	"context"
	"testing"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/auth"
)

func TestLoadMarksAndSendFailures(t *testing.T) {
	db, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if err := app.RunMigrations(db); err != nil {
		t.Fatal(err)
	}
	scope, err := auth.PrimaryScope(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	hid := scope.HouseholdID()
	if _, err := db.Exec(`INSERT INTO attention_marks (household_id, line_key, flagged_trip) VALUES (?, 'item:x', 1)`, hid); err != nil {
		t.Fatal(err)
	}
	marks, err := LoadMarks(context.Background(), db, scope)
	if err != nil || marks["item:x"].FlaggedTrip != 1 {
		t.Fatalf("marks %#v err %v", marks, err)
	}
	if _, err := db.Exec(`INSERT INTO cart_line_results (household_id, provider_id, item_id, status, reason, attempted_at) VALUES (?, 'kroger', 'item-y', 'failed', 'timeout', '2026-01-01T00:00:00Z')`, hid); err != nil {
		t.Fatal(err)
	}
	fails, err := LoadSendFailures(context.Background(), db, scope)
	if err != nil || fails["item-y"].Detail != "timeout" {
		t.Fatalf("fails %#v err %v", fails, err)
	}
}