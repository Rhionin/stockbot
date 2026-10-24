package inventory_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/inventory"
	"github.com/Rhionin/stockbot/internal/product"
)

func TestAddInstanceUnshelvesTheItem(t *testing.T) {
	pantry, catalog, db := newTestPantry(t)
	ctx := context.Background()
	if err := catalog.CreateProduct(ctx, product.Product{ID: "prod-sample", Name: "Sample oats"}); err != nil {
		t.Fatal(err)
	}
	item, err := pantry.GetOrCreateItem(ctx, "user-1", "prod-sample")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE items SET shelved = 1 WHERE id = ?`, item.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pantry.AddInstance(ctx, inventory.ItemInstance{
		ItemID:    item.ID,
		StockInAt: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatal(err)
	}
	var shelved int
	if err := db.QueryRow(`SELECT shelved FROM items WHERE id = ?`, item.ID).Scan(&shelved); err != nil {
		t.Fatal(err)
	}
	if shelved != 0 {
		t.Fatalf("shelved = %d", shelved)
	}
}

func TestUnshelveItemReportsAClosedDatabase(t *testing.T) {
	_, _, db := newTestPantry(t)
	db.Close()
	err := inventory.UnshelveItem(context.Background(), db, "item-sample")
	if err == nil || !strings.Contains(err.Error(), "could not put the item back on the shelf") {
		t.Fatalf("err = %v", err)
	}
}
