package restock

import (
	"context"
	"errors"
	"testing"
)

func TestSetItemRefusesGroupedProductInGroup(t *testing.T) {
	ctx := context.Background()
	targets, db := newTestTargets(t)
	scope := primaryScope(t, db)
	insertProduct(t, db, "prod-beans", "Cut Green Beans")
	insertProduct(t, db, "prod-rice", "Rice")
	for _, stmt := range []string{
		`INSERT INTO items (id, user_id, product_id) VALUES ('item-beans', 'user-1', 'prod-beans')`,
		`INSERT INTO items (id, user_id, product_id) VALUES ('item-rice', 'user-1', 'prod-rice')`,
		`INSERT INTO product_groups (id, user_id, name, name_key, rule) VALUES ('g-beans', 'user-1', 'Green beans', 'green beans', 'same_as_ran_out')`,
		`INSERT INTO product_group_members (product_id, group_id) VALUES ('prod-beans', 'g-beans')`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}

	for _, target := range []Target{{Kind: KindAmount, Value: 2}, {Kind: KindMonths, Value: 1}} {
		err := targets.SetItem(ctx, scope, "item-beans", target)
		var inGroup *InGroupError
		if !errors.As(err, &inGroup) || inGroup.GroupID != "g-beans" || inGroup.GroupName != "Green beans" {
			t.Fatalf("%v: got %v, want in_group", target, err)
		}
		stored, err := targets.ItemTarget(ctx, scope, "item-beans")
		if err != nil || stored != nil {
			t.Fatalf("%v: a target was written: %v %v", target, stored, err)
		}
	}

	if err := targets.SetItem(ctx, scope, "item-beans", Target{Kind: KindMonths, Value: 0}); err != nil {
		t.Fatalf("don't restock: %v", err)
	}
	if stored, _ := targets.ItemTarget(ctx, scope, "item-beans"); stored == nil || stored.Value != 0 {
		t.Fatalf("don't restock not stored: %v", stored)
	}
	if err := targets.SetItem(ctx, scope, "item-rice", Target{Kind: KindAmount, Value: 2}); err != nil {
		t.Fatalf("ungrouped: %v", err)
	}
	if stored, _ := targets.ItemTarget(ctx, scope, "item-rice"); stored == nil || stored.Value != 2 {
		t.Fatalf("ungrouped not stored: %v", stored)
	}
}

func TestSetItemInGroupIsScopedToTheHousehold(t *testing.T) {
	ctx := context.Background()
	targets, db := newTestTargets(t)
	primary := primaryScope(t, db)
	cedarScope(t, db)
	insertProduct(t, db, "prod-beans", "Cut Green Beans")
	for _, stmt := range []string{
		`INSERT INTO items (id, user_id, product_id) VALUES ('item-beans', 'user-1', 'prod-beans')`,
		`INSERT INTO product_groups (id, household_id, user_id, name, name_key, rule) VALUES ('g-cedar', '` + cedarHouseholdID + `', 'user-1', 'Green beans', 'green beans', 'same_as_ran_out')`,
		`INSERT INTO product_group_members (household_id, product_id, group_id) VALUES ('` + cedarHouseholdID + `', 'prod-beans', 'g-cedar')`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	if err := targets.SetItem(ctx, primary, "item-beans", Target{Kind: KindAmount, Value: 2}); err != nil {
		t.Fatalf("another household's group blocked this one: %v", err)
	}
}
