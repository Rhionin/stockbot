package inventory_test

import (
	"context"
	"testing"
	"time"
)

func TestInstancesComeBackSoonestExpiryFirstAcrossDST(t *testing.T) {
	loc, err := time.LoadLocation("America/Denver")
	if err != nil {
		t.Fatal(err)
	}
	later := time.Date(2026, 11, 1, 0, 30, 0, 0, loc)
	earlier := time.Date(2026, 11, 1, 6, 0, 0, 0, time.UTC)
	if !later.After(earlier) || !later.IsDST() {
		t.Fatalf("dst fixture later=%s earlier=%s", later, earlier)
	}

	pantry, _, db := newTestPantry(t)
	scope := scopeOf(t, db)
	householdID := scope.HouseholdID()
	if _, err := db.Exec(`INSERT INTO products (id, name) VALUES ('prod', 'Milk')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(
		`INSERT INTO items (id, user_id, product_id, household_id) VALUES ('item', ?, 'prod', ?)`,
		householdID, householdID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO item_instances (id, item_id, stock_in_at, expires_at, household_id) VALUES
			('late', 'item', ?, ?, ?),
			('early', 'item', ?, ?, ?)`,
		earlier, later, householdID,
		earlier, earlier, householdID); err != nil {
		t.Fatal(err)
	}
	got, err := pantry.ListItemInstances(context.Background(), scope, "item")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "early" || got[1].ID != "late" {
		t.Fatalf("%+v", got)
	}
}
