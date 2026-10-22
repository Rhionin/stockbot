package syncsnap

import (
	"context"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/app"
	_ "modernc.org/sqlite"
)

func TestBuildEmptyHousehold(t *testing.T) {
	conn, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := app.RunMigrations(conn); err != nil {
		t.Fatal(err)
	}
	when := time.Date(2026, 10, 10, 1, 2, 3, 0, time.UTC)
	snap, err := Build(context.Background(), conn, when)
	if err != nil {
		t.Fatal(err)
	}
	if !snap.GeneratedAt.Equal(when) || snap.Products == nil || snap.Groups == nil || snap.ShoppingList == nil {
		t.Fatalf("snap = %+v", snap)
	}
	if len(snap.Products) != 0 || len(snap.Moves) != 0 {
		t.Fatalf("expected an empty pantry, got %+v", snap)
	}
}

func TestBuildReadsBrandFromTheProductName(t *testing.T) {
	conn, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := app.RunMigrations(conn); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`
		INSERT INTO products (id, name) VALUES ('p1', 'Hunt''s Tomato Paste'), ('p2', 'Tomato Paste');
		INSERT INTO product_groups (id, user_id, name, name_key, rule) VALUES ('g1', 'user-1', 'Tomato Paste', 'tomato paste', 'favorite');
		INSERT INTO product_group_members (product_id, group_id) VALUES ('p1', 'g1'), ('p2', 'g1');
	`); err != nil {
		t.Fatal(err)
	}
	snap, err := Build(context.Background(), conn, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, p := range snap.Products {
		got[p.ID] = p.Brand
	}
	if got["p1"] != "Hunt's" || got["p2"] != "" {
		t.Fatalf("brands = %#v", got)
	}
}

func TestBuildPrefersItemTargets(t *testing.T) {
	conn, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := app.RunMigrations(conn); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`
		INSERT INTO products (id, name) VALUES ('chips', 'Tortilla chips'), ('beans', 'Green beans');
		INSERT INTO items (id, user_id, product_id) VALUES
			('item-chips', 'user-1', 'chips'),
			('item-beans', 'user-1', 'beans');
		INSERT INTO products (id, name) VALUES ('salt', 'Salt');
		INSERT INTO items (id, user_id, product_id) VALUES ('item-salt', 'user-1', 'salt');
		INSERT INTO categories (id, household_id, name, name_key) VALUES ('cat-salt', 'user-1', 'Spices', 'spices');
		INSERT INTO category_targets (household_id, category_id, kind, value) VALUES ('user-1', 'cat-salt', 'months', 0);
		INSERT INTO item_categories (household_id, item_id, category_id, source) VALUES ('user-1', 'item-salt', 'cat-salt', 'user');
		INSERT INTO item_targets (household_id, item_id, kind, value) VALUES
			('user-1', 'item-chips', 'amount', 4.6),
			('user-1', 'item-beans', 'months', 0);
	`); err != nil {
		t.Fatal(err)
	}
	snap, err := Build(context.Background(), conn, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]Product{}
	for _, product := range snap.Products {
		byID[product.ID] = product
	}
	chips := byID["chips"]
	if chips.TargetQuantity == nil || *chips.TargetQuantity != 5 || chips.NoRestock {
		t.Fatalf("amount target: %+v", chips)
	}
	beans := byID["beans"]
	if !beans.NoRestock || beans.TargetQuantity != nil {
		t.Fatalf("zero target: %+v", beans)
	}
	salt := byID["salt"]
	if !salt.NoRestock {
		t.Fatalf("category zero resolves to noRestock: %+v", salt)
	}
}

func TestBuildSkipsShelvedItems(t *testing.T) {
	conn, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := app.RunMigrations(conn); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`
		INSERT INTO products (id, name) VALUES ('milk', 'Milk'), ('bread', 'Bread');
		INSERT INTO items (id, user_id, product_id, target_quantity) VALUES
			('item-milk', 'user-1', 'milk', 4),
			('item-bread', 'user-1', 'bread', 2);
		INSERT INTO item_targets (household_id, item_id, kind, value) VALUES
			('user-1', 'item-milk', 'amount', 4),
			('user-1', 'item-bread', 'amount', 2);
		INSERT INTO item_instances (id, item_id, stock_in_at) VALUES
			('inst-milk', 'item-milk', '2026-01-01'),
			('inst-milk-out', 'item-milk', '2026-01-02'),
			('inst-bread', 'item-bread', '2026-01-01');
		UPDATE item_instances SET removed_at = '2026-01-03', removal_reason = 'consumed' WHERE id = 'inst-milk-out';
		INSERT INTO stock_moves (id, item_id, product_id, direction, quantity, at, source) VALUES
			('move-milk', 'item-milk', 'milk', 'in', 1, '2026-01-01', 'scan'),
			('move-bread', 'item-bread', 'bread', 'in', 1, '2026-01-01', 'scan');
		INSERT INTO shopping_list_items (id, user_id, item_id, quantity, source) VALUES
			('shop-milk', 'user-1', 'item-milk', 1, 'manual'),
			('shop-bread', 'user-1', 'item-bread', 1, 'manual');
		UPDATE items SET shelved = 1 WHERE id = 'item-milk';
	`); err != nil {
		t.Fatal(err)
	}

	snap, err := Build(context.Background(), conn, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]Product{}
	for _, product := range snap.Products {
		byID[product.ID] = product
	}
	if _, ok := byID["milk"]; !ok {
		t.Fatal("shelving removed the product from the catalog")
	}
	milk := byID["milk"]
	if milk.TargetQuantity != nil || milk.NoRestock {
		t.Fatalf("shelved item still carries a restock target: %+v", milk)
	}
	bread := byID["bread"]
	if bread.TargetQuantity == nil || *bread.TargetQuantity != 2 || bread.NoRestock {
		t.Fatalf("unshelved item: %+v", bread)
	}
	for _, inst := range snap.Instances {
		if inst.ProductID == "milk" {
			t.Fatalf("shelved instance: %+v", snap.Instances)
		}
	}
	if len(snap.Instances) != 1 || snap.Instances[0].ProductID != "bread" {
		t.Fatalf("instances: %+v", snap.Instances)
	}
	for _, rm := range snap.Removals {
		if rm.ProductID == "milk" {
			t.Fatalf("shelved removal: %+v", snap.Removals)
		}
	}
	for _, move := range snap.Moves {
		if move.ProductID == "milk" {
			t.Fatalf("shelved move: %+v", snap.Moves)
		}
	}
	if len(snap.Moves) != 1 || snap.Moves[0].ProductID != "bread" {
		t.Fatalf("moves: %+v", snap.Moves)
	}
	for _, line := range snap.ShoppingList {
		if line.ProductID == "milk" {
			t.Fatalf("shelved shopping line: %+v", snap.ShoppingList)
		}
	}
	if len(snap.ShoppingList) != 1 || snap.ShoppingList[0].ProductID != "bread" {
		t.Fatalf("shopping: %+v", snap.ShoppingList)
	}
}
