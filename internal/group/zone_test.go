package group

import (
	"context"
	"testing"
	"time"
)

func TestGroupLastEventsFollowTheLaterInstantAcrossDST(t *testing.T) {
	loc, err := time.LoadLocation("America/Denver")
	if err != nil {
		t.Fatal(err)
	}
	later := time.Date(2026, 11, 1, 0, 30, 0, 0, loc)
	earlier := time.Date(2026, 11, 1, 6, 0, 0, 0, time.UTC)
	if !later.After(earlier) || !later.IsDST() {
		t.Fatalf("dst fixture later=%s earlier=%s", later, earlier)
	}

	groups, catalog, db := newTestGroups(t)
	ctx := context.Background()
	scope := scopeOf(t, db)
	mustProduct(t, catalog, "p1", "Oats")
	if _, err := db.Exec(`INSERT INTO items (id, household_id, user_id, product_id) VALUES ('item', ?, ?, 'p1')`, scope.HouseholdID(), scope.HouseholdID()); err != nil {
		t.Fatal(err)
	}
	for i, at := range []time.Time{earlier, later} {
		if _, err := db.Exec(`INSERT INTO consumption_events (id, household_id, item_id, consumed_at) VALUES (?, ?, 'item', ?)`, at.Format(time.RFC3339), scope.HouseholdID(), at); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO stock_in_events (household_id, id, product_id, at) VALUES (?, ?, 'p1', ?)`, scope.HouseholdID(), i+1, at); err != nil {
			t.Fatal(err)
		}
	}
	created, err := groups.Create(ctx, scope, "Oats", []string{"p1"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := groups.Get(ctx, scope, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Members) != 1 ||
		!got.Members[0].LastConsumedAt.Equal(later) ||
		!got.Members[0].LastStockedAt.Equal(later) {
		t.Fatalf("consumed %s stocked %s", got.Members[0].LastConsumedAt, got.Members[0].LastStockedAt)
	}
}
