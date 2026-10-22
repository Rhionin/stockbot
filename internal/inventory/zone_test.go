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
	if _, err := db.Exec(`
		INSERT INTO products (id, name) VALUES ('prod', 'Milk');
		INSERT INTO items (id, user_id, product_id) VALUES ('item', 'user-1', 'prod');
		INSERT INTO item_instances (id, item_id, stock_in_at, expires_at) VALUES
			('late', 'item', ?, ?),
			('early', 'item', ?, ?)`,
		earlier, later, earlier, earlier); err != nil {
		t.Fatal(err)
	}
	got, err := pantry.ListItemInstances(context.Background(), "item")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "early" || got[1].ID != "late" {
		t.Fatalf("%+v", got)
	}
}
