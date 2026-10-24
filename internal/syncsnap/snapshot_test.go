package syncsnap

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/Rhionin/stockbot/internal/tenant"
	_ "modernc.org/sqlite"
)

func openTestDB(t *testing.T) (*sql.DB, tenant.Scope) {
	t.Helper()
	conn, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := app.RunMigrations(conn); err != nil {
		t.Fatal(err)
	}
	scope, err := auth.PrimaryScope(context.Background(), conn)
	if err != nil {
		t.Fatal(err)
	}
	return conn, scope
}

func TestBuildEmptyHousehold(t *testing.T) {
	conn, scope := openTestDB(t)
	when := time.Date(2026, 10, 10, 1, 2, 3, 0, time.UTC)
	snap, err := Build(context.Background(), scope, conn, when)
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
	conn, scope := openTestDB(t)
	hid := scope.HouseholdID()
	if _, err := conn.Exec(`INSERT INTO products (id, name) VALUES ('p1', 'Hunt''s Tomato Paste'), ('p2', 'Tomato Paste')`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`
		INSERT INTO product_groups (id, user_id, household_id, name, name_key, rule)
		VALUES ('g1', ?, ?, 'Tomato Paste', 'tomato paste', 'favorite')`, hid, hid); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`
		INSERT INTO product_group_members (product_id, group_id, household_id) VALUES ('p1', 'g1', ?), ('p2', 'g1', ?)`, hid, hid); err != nil {
		t.Fatal(err)
	}
	snap, err := Build(context.Background(), scope, conn, time.Now())
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
	conn, scope := openTestDB(t)
	hid := scope.HouseholdID()
	if _, err := conn.Exec(`INSERT INTO products (id, name) VALUES ('chips', 'Tortilla chips'), ('beans', 'Green beans'), ('salt', 'Salt')`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`
		INSERT INTO items (id, user_id, household_id, product_id) VALUES
			('item-chips', ?, ?, 'chips'),
			('item-beans', ?, ?, 'beans'),
			('item-salt', ?, ?, 'salt')`, hid, hid, hid, hid, hid, hid); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`
		INSERT INTO categories (id, household_id, name, name_key) VALUES ('cat-salt', ?, 'Spices', 'spices')`, hid); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`
		INSERT INTO category_targets (household_id, category_id, kind, value) VALUES (?, 'cat-salt', 'months', 0)`, hid); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`
		INSERT INTO item_categories (household_id, item_id, category_id, source) VALUES (?, 'item-salt', 'cat-salt', 'user')`, hid); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`
		INSERT INTO item_targets (household_id, item_id, kind, value) VALUES
			(?, 'item-chips', 'amount', 4.6),
			(?, 'item-beans', 'months', 0)`, hid, hid); err != nil {
		t.Fatal(err)
	}
	snap, err := Build(context.Background(), scope, conn, time.Now())
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
	conn, scope := openTestDB(t)
	hid := scope.HouseholdID()
	if _, err := conn.Exec(`INSERT INTO products (id, name) VALUES ('milk', 'Milk'), ('bread', 'Bread')`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`
		INSERT INTO items (id, user_id, household_id, product_id, target_quantity) VALUES
			('item-milk', ?, ?, 'milk', 4),
			('item-bread', ?, ?, 'bread', 2)`, hid, hid, hid, hid); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`
		INSERT INTO item_targets (household_id, item_id, kind, value) VALUES
			(?, 'item-milk', 'amount', 4),
			(?, 'item-bread', 'amount', 2)`, hid, hid); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`
		INSERT INTO item_instances (id, household_id, item_id, stock_in_at) VALUES
			('inst-milk', ?, 'item-milk', '2026-01-01'),
			('inst-milk-out', ?, 'item-milk', '2026-01-02'),
			('inst-bread', ?, 'item-bread', '2026-01-01')`, hid, hid, hid); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`UPDATE item_instances SET removed_at = '2026-01-03', removal_reason = 'consumed' WHERE household_id = ? AND id = 'inst-milk-out'`, hid); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`
		INSERT INTO stock_moves (id, household_id, item_id, product_id, direction, quantity, at, source) VALUES
			('move-milk', ?, 'item-milk', 'milk', 'in', 1, '2026-01-01', 'scan'),
			('move-bread', ?, 'item-bread', 'bread', 'in', 1, '2026-01-01', 'scan')`, hid, hid); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`
		INSERT INTO shopping_list_items (id, user_id, household_id, item_id, quantity, source) VALUES
			('shop-milk', ?, ?, 'item-milk', 1, 'manual'),
			('shop-bread', ?, ?, 'item-bread', 1, 'manual')`, hid, hid, hid, hid); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`UPDATE items SET shelved = 1 WHERE household_id = ? AND id = 'item-milk'`, hid); err != nil {
		t.Fatal(err)
	}

	snap, err := Build(context.Background(), scope, conn, time.Now())
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

func TestSnapshotRowsStayInTheirHousehold(t *testing.T) {
	conn, home := openTestDB(t)
	const cedarID = "0190bbbb-cccc-7000-8000-0000000000e5"
	if _, err := conn.Exec(`
		INSERT INTO households (id, name, created_at, channel_key, is_primary)
		VALUES (?, 'Household Cedar', '2026-02-02T00:00:00Z', 'abcdef0123456789abcdef0123456789', 0)`, cedarID); err != nil {
		t.Fatal(err)
	}
	cedar, err := tenant.New(cedarID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO products (id, name) VALUES ('cedar-oats', 'Cedar oats')`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`
		INSERT INTO items (id, user_id, household_id, product_id) VALUES ('item-cedar', ?, ?, 'cedar-oats')`, cedarID, cedarID); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`
		INSERT INTO product_groups (id, user_id, household_id, name, name_key, rule)
		VALUES ('g-cedar', ?, ?, 'Cedar oats', 'cedar oats', 'favorite')`, cedarID, cedarID); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`
		INSERT INTO shopping_list_items (id, user_id, household_id, item_id, quantity, source)
		VALUES ('shop-cedar', ?, ?, 'item-cedar', 2, 'manual')`, cedarID, cedarID); err != nil {
		t.Fatal(err)
	}

	homeSnap, err := Build(context.Background(), home, conn, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, group := range homeSnap.Groups {
		if group.ID == "g-cedar" || group.Name == "Cedar oats" {
			t.Fatalf("primary snapshot included cedar's group %+v", group)
		}
	}
	for _, line := range homeSnap.ShoppingList {
		if line.ID == "shop-cedar" || line.ProductID == "cedar-oats" {
			t.Fatalf("primary snapshot included cedar's shopping line %+v", line)
		}
	}
	cedarSnap, err := Build(context.Background(), cedar, conn, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var foundGroup, foundLine bool
	for _, group := range cedarSnap.Groups {
		if group.ID == "g-cedar" && group.Name == "Cedar oats" {
			foundGroup = true
		}
	}
	for _, line := range cedarSnap.ShoppingList {
		if line.ProductID == "cedar-oats" && line.Quantity == 2 {
			foundLine = true
		}
	}
	if !foundGroup || !foundLine {
		t.Fatalf("cedar snapshot groups=%+v shopping=%+v", cedarSnap.Groups, cedarSnap.ShoppingList)
	}
	again, err := Build(context.Background(), home, conn, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Groups) != len(homeSnap.Groups) || len(again.ShoppingList) != len(homeSnap.ShoppingList) {
		t.Fatalf("reading the primary household changed cedar's rows: groups %d shopping %d", len(again.Groups), len(again.ShoppingList))
	}
}
