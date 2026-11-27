package restock

import (
	"context"
	"errors"
	"testing"
)

func TestSetItemInGroup(t *testing.T) {
	targets, db := newTestTargets(t)
	ctx := context.Background()
	scope := primaryScope(t, db)
	hid := scope.HouseholdID()

	insertProduct(t, db, "beans", "Beans")
	insertProduct(t, db, "solo", "Solo")
	if _, err := db.Exec(`
		INSERT INTO items (id, user_id, household_id, product_id) VALUES
			('item-beans', ?, ?, 'beans'),
			('item-solo', ?, ?, 'solo')`, hid, hid, hid, hid); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO product_groups (id, household_id, user_id, name, name_key, rule) VALUES
			('grp-beans', ?, ?, 'Cut green beans', 'cut green beans', 'same_as_ran_out')`, hid, hid); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO product_group_members (household_id, group_id, product_id) VALUES (?, 'grp-beans', 'beans')`, hid); err != nil {
		t.Fatal(err)
	}

	err := targets.SetItem(ctx, scope, "item-beans", Target{Kind: KindAmount, Value: 2})
	var ig *InGroupError
	if !errors.As(err, &ig) {
		t.Fatalf("amount>0: %v", err)
	}
	if ig.GroupID != "grp-beans" || ig.GroupName != "Cut green beans" {
		t.Fatalf("error members: %+v", ig)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM item_targets WHERE household_id = ? AND item_id = 'item-beans'`, hid).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("row must be unchanged")
	}

	err = targets.SetItem(ctx, scope, "item-beans", Target{Kind: KindMonths, Value: 3})
	if !errors.As(err, &ig) {
		t.Fatalf("months>0: %v", err)
	}

	if err := targets.SetItem(ctx, scope, "item-beans", Target{Kind: KindMonths, Value: 0}); err != nil {
		t.Fatalf("zero months: %v", err)
	}
	if err := targets.SetItem(ctx, scope, "item-beans", Target{Kind: KindAmount, Value: 0}); err != nil {
		t.Fatalf("zero amount: %v", err)
	}
	got, err := targets.ItemTarget(ctx, scope, "item-beans")
	if err != nil || got == nil || got.Value != 0 {
		t.Fatalf("saved zero target: %#v err=%v", got, err)
	}

	if err := targets.ClearItem(ctx, scope, "item-beans"); err != nil {
		t.Fatal(err)
	}
	got, err = targets.ItemTarget(ctx, scope, "item-beans")
	if err != nil || got != nil {
		t.Fatalf("cleared: %#v", got)
	}

	if err := targets.SetItem(ctx, scope, "item-solo", Target{Kind: KindAmount, Value: 4}); err != nil {
		t.Fatalf("ungrouped: %v", err)
	}

	cedar := cedarScope(t, db)
	if _, err := db.Exec(`
		INSERT INTO items (id, user_id, household_id, product_id) VALUES ('item-beans-cedar', ?, ?, 'beans')`,
		cedar.HouseholdID(), cedar.HouseholdID()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO product_groups (id, household_id, user_id, name, name_key, rule)
		VALUES ('grp-beans-cedar', ?, ?, 'Other beans', 'other beans', 'same_as_ran_out')`, cedar.HouseholdID(), cedar.HouseholdID()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO product_group_members (household_id, group_id, product_id) VALUES (?, 'grp-beans-cedar', 'beans')`, cedar.HouseholdID()); err != nil {
		t.Fatal(err)
	}
	err = targets.SetItem(ctx, scope, "item-beans", Target{Kind: KindMonths, Value: 1})
	if !errors.As(err, &ig) {
		t.Fatalf("scoped lookup: %v", err)
	}
	if ig.GroupID != "grp-beans" || ig.GroupName != "Cut green beans" {
		t.Fatalf("scoped group %+v", ig)
	}

	if _, err := db.Exec(`DELETE FROM product_group_members WHERE household_id = ? AND product_id = 'beans'`, hid); err != nil {
		t.Fatal(err)
	}
	if err := targets.SetItem(ctx, scope, "item-beans", Target{Kind: KindAmount, Value: 2}); err != nil {
		t.Fatalf("primary item must not inherit other-household group: %v", err)
	}
}