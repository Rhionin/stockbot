package restock

import (
	"context"
	"testing"
)

// Deleting an item is the only way its item target goes away. Wiping stock
// is covered from the HTTP API. There is no second delete-item route; the
// item row is the item.
func TestDeletingTheItemRemovesItsTarget(t *testing.T) {
	targets, db := newTestTargets(t)
	ctx := context.Background()
	scope := primaryScope(t, db)
	insertProduct(t, db, "salt", "Salt")
	insertProduct(t, db, "pepper", "Pepper")
	hid := scope.HouseholdID()
	if _, err := db.Exec(`
		INSERT INTO items (id, user_id, household_id, product_id) VALUES
			('item-salt', ?, ?, 'salt'),
			('item-pepper', ?, ?, 'pepper')`, hid, hid, hid, hid); err != nil {
		t.Fatal(err)
	}
	if err := targets.SetItem(ctx, scope, "item-salt", Target{Kind: KindAmount, Value: 4}); err != nil {
		t.Fatal(err)
	}
	if err := targets.SetItem(ctx, scope, "item-pepper", Target{Kind: KindMonths, Value: 2}); err != nil {
		t.Fatal(err)
	}
	if err := targets.SetHousehold(ctx, scope, Target{Kind: KindMonths, Value: 6}); err != nil {
		t.Fatal(err)
	}

	if err := DeleteItem(ctx, scope, db, "item-salt"); err != nil {
		t.Fatalf("setup: deleting the item: %v", err)
	}

	t.Run("the deleted item's target is gone", func(t *testing.T) {
		got, err := targets.ItemTarget(ctx, scope, "item-salt")
		if err != nil {
			t.Fatal(err)
		}
		if got != nil {
			t.Fatalf("deleting the item left its restock target %+v; that target should be gone", *got)
		}
	})

	t.Run("another item's target stays", func(t *testing.T) {
		got, err := targets.ItemTarget(ctx, scope, "item-pepper")
		if err != nil {
			t.Fatal(err)
		}
		if got == nil || got.Kind != KindMonths || got.Value != 2 {
			t.Fatalf("pepper target = %+v", got)
		}
	})

	t.Run("the household default stays", func(t *testing.T) {
		snap, err := targets.Snapshot(ctx, scope)
		if err != nil {
			t.Fatal(err)
		}
		if snap.Household.Kind != KindMonths || snap.Household.Value != 6 {
			t.Fatalf("household target = %+v", snap.Household)
		}
	})
}
