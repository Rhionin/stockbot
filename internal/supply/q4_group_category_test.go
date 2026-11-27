package supply

import (
	"context"
	"testing"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/restock"
)

func TestRestockGroupedItemSkipsCategoryInGroup(t *testing.T) {
	household := restock.Target{Kind: restock.KindMonths, Value: 3}
	snacks := restock.CategoryTarget{ID: "snacks", Name: "Snacks", Target: restock.Target{Kind: restock.KindMonths, Value: 1}}
	group := restock.Target{Kind: restock.KindMonths, Value: 2}

	got := restock.Resolve(restock.Input{
		Group:      &group,
		GroupName:  "Salad kits",
		Categories: []restock.CategoryTarget{snacks},
		Household:  household,
	})
	if got.Level != restock.LevelGroup || got.Target != group {
		t.Fatalf("group resolution: %+v", got)
	}
	if got.Next == nil || got.Next.Level != restock.LevelHousehold || got.Next.Target != household {
		t.Fatalf("next after group: %+v", got.Next)
	}

	ungrouped := restock.Resolve(restock.Input{
		GroupName:  "Only in a group",
		Categories: []restock.CategoryTarget{snacks},
		Household:  household,
	})
	if ungrouped.Level != restock.LevelHousehold || ungrouped.Target != household {
		t.Fatalf("group membership skips category: %+v", ungrouped)
	}
}

func TestPlanGroupIgnoresCategoryTarget(t *testing.T) {
	db, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if err := app.RunMigrations(db); err != nil {
		t.Fatal(err)
	}
	scope := scopeOf(t, db)
	hid := scope.HouseholdID()
	ctx := context.Background()

	if _, err := db.Exec(`INSERT INTO household_settings (household_id, key, value) VALUES (?, 'supply_target_kind', 'months'), (?, 'supply_target_value', '3')`, hid, hid); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO categories (id, household_id, name, name_key) VALUES ('cat-snacks', ?, 'Snacks', 'snacks')`, hid); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO category_targets (household_id, category_id, kind, value) VALUES (?, 'cat-snacks', 'months', 1)`, hid); err != nil {
		t.Fatal(err)
	}
	for i, prod := range []string{"prod-a", "prod-b"} {
		item := []string{"item-a", "item-b"}[i]
		if _, err := db.Exec(`INSERT INTO products (id, name, source) VALUES (?, 'P', 'external')`, prod); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO items (id, user_id, product_id, household_id) VALUES (?, ?, ?, ?)`, item, hid, prod, hid); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO item_categories (item_id, household_id, category_id, source) VALUES (?, ?, 'cat-snacks', 'user')`, item, hid); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO product_groups (id, user_id, household_id, name, name_key, rule) VALUES ('grp-cat', ?, ?, 'Beans', 'beans', 'same_as_ran_out')`, hid, hid); err != nil {
		t.Fatal(err)
	}
	for _, prod := range []string{"prod-a", "prod-b"} {
		if _, err := db.Exec(`INSERT INTO product_group_members (group_id, product_id, household_id) VALUES ('grp-cat', ?, ?)`, prod, hid); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO household_settings (household_id, key, value) VALUES (?, 'onboarding_started_at', '2020-01-01T00:00:00Z')`, hid); err != nil {
		t.Fatal(err)
	}

	snap, err := restock.Load(ctx, scope, db)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range snap.Items {
		if item.GroupID == "" {
			continue
		}
		if item.Level != restock.LevelHousehold {
			t.Fatalf("%s level %q, want household", item.ItemID, item.Level)
		}
		if item.Target.Value != 3 {
			t.Fatalf("%s target %+v, want household months 3", item.ItemID, item.Target)
		}
	}
}