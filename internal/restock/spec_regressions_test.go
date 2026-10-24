package restock

import (
	"context"
	"strings"
	"testing"
)

// Deleting an item is the only way its item target goes away. Wiping stock
// is covered from the HTTP API. There is no second delete-item route; the
// item row is the item.
func TestDeletingTheItemRemovesItsTarget(t *testing.T) {
	targets, db := newTestTargets(t)
	ctx := context.Background()
	insertProduct(t, db, "salt", "Salt")
	insertProduct(t, db, "pepper", "Pepper")
	if _, err := db.Exec(`
		INSERT INTO items (id, user_id, product_id) VALUES
			('item-salt', 'user-1', 'salt'),
			('item-pepper', 'user-1', 'pepper')`); err != nil {
		t.Fatal(err)
	}
	if err := targets.SetItem(ctx, "item-salt", Target{Kind: KindAmount, Value: 4}); err != nil {
		t.Fatal(err)
	}
	if err := targets.SetItem(ctx, "item-pepper", Target{Kind: KindMonths, Value: 2}); err != nil {
		t.Fatal(err)
	}
	if err := targets.SetHousehold(ctx, Target{Kind: KindMonths, Value: 6}); err != nil {
		t.Fatal(err)
	}

	if err := DeleteItem(ctx, db, "item-salt"); err != nil {
		t.Fatalf("setup: deleting the item: %v", err)
	}

	t.Run("the deleted item's target is gone", func(t *testing.T) {
		got, err := targets.ItemTarget(ctx, "item-salt")
		if err != nil {
			t.Fatal(err)
		}
		if got != nil {
			t.Fatalf("deleting the item left its restock target %+v; that target should be gone", *got)
		}
	})

	t.Run("another item's target stays", func(t *testing.T) {
		got, err := targets.ItemTarget(ctx, "item-pepper")
		if err != nil {
			t.Fatal(err)
		}
		if got == nil || got.Kind != KindMonths || got.Value != 2 {
			t.Fatalf("pepper target = %+v", got)
		}
	})

	t.Run("the household default stays", func(t *testing.T) {
		snap, err := targets.Snapshot(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if snap.Household.Kind != KindMonths || snap.Household.Value != 6 {
			t.Fatalf("household target = %+v", snap.Household)
		}
	})
}

func TestDeleteItemReportsAClosedDatabase(t *testing.T) {
	_, db := newTestTargets(t)
	db.Close()
	err := DeleteItem(context.Background(), db, "item-sample")
	if err == nil || !strings.Contains(err.Error(), "could not delete the item") {
		t.Fatalf("err = %v", err)
	}
}
