package server

import (
	"context"
	"testing"

	"github.com/Rhionin/stockbot/internal/auth"
)

func TestTripBoundaryForNewCartFromSkips(t *testing.T) {
	db := setupTestDB(t)
	scope, err := auth.PrimaryScope(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	hid := scope.HouseholdID()
	mustExecTest(t, db, `INSERT INTO staged_cart_skips (household_id, user_id, item_id, expires_trip) VALUES (?, ?, 'item-boundary', 1)`, hid, hid)
	mustExecTest(t, db, `INSERT INTO staged_cart_skips (household_id, user_id, item_id, expires_trip) VALUES (?, ?, 'item-later', 3)`, hid, hid)
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	out, err := tripBoundaryForNewCart(context.Background(), scope, 2, tx)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 {
		t.Fatalf("boundary changes %d", len(out))
	}
	if out[0].LineKey != "item:item-boundary" || out[0].Cause != "new_trip" {
		t.Fatalf("change %#v", out[0])
	}
	if out[0].Effect != string(tripBoundaryEffectAdded) {
		t.Fatalf("effect %q", out[0].Effect)
	}
}