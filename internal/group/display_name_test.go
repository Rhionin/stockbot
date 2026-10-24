package group

import (
	"context"
	"strings"
	"testing"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/Rhionin/stockbot/internal/product"
	"github.com/Rhionin/stockbot/internal/tenant"
)

func TestHintAndSeedUseTheHouseholdName(t *testing.T) {
	db, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := app.RunMigrations(db); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	primary, err := auth.PrimaryScope(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	const cedar = "0190bbbb-cccc-7000-8000-0000000000e5"
	if _, err := db.Exec(`
		INSERT INTO households (id, name, restock_mode, created_at, channel_key, is_primary)
		VALUES (?, 'Household Cedar', '', '2026-02-02T00:00:00Z', 'abcdef0123456789abcdef0123456789', 0)`, cedar); err != nil {
		t.Fatal(err)
	}
	other, err := tenant.New(cedar)
	if err != nil {
		t.Fatal(err)
	}
	catalog := product.NewCatalog(db)
	const typed = "cedar-sentinel-q9p4w cereal"
	created, err := catalog.CreateTypedProduct(ctx, other, product.Product{
		ID: "typed-group", Name: typed, UnitOfMeasure: "unit", Category: "Pantry",
	})
	if err != nil {
		t.Fatal(err)
	}
	groups := NewGroups(db)
	made, err := groups.Create(ctx, other, "Cedar staples", []string{created.ID}, nil)
	if err != nil {
		t.Fatal(err)
	}
	loaded, memberOf, err := loadHintGroups(ctx, other, db)
	if err != nil {
		t.Fatal(err)
	}
	if memberOf[created.ID] != made.ID {
		t.Fatalf("member group %q", memberOf[created.ID])
	}
	found := false
	for _, hintGroup := range loaded {
		for _, name := range hintGroup.memberNames {
			if name == typed {
				found = true
			}
			if strings.Contains(name, typed) && hintGroup.id != made.ID {
				t.Fatalf("unexpected group %s", hintGroup.id)
			}
		}
	}
	if !found {
		t.Fatalf("hint groups missed %q %+v", typed, loaded)
	}
	primaryHints, _, err := loadHintGroups(ctx, primary, db)
	if err != nil {
		t.Fatal(err)
	}
	for _, hintGroup := range primaryHints {
		for _, name := range hintGroup.memberNames {
			if strings.Contains(name, "cedar-sentinel") {
				t.Fatalf("primary hint leaked %q", name)
			}
		}
	}

	seeded, err := loadSeedProducts(ctx, primary, db)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range seeded {
		if strings.Contains(item.name, "cedar-sentinel") || item.id == created.ID {
			t.Fatalf("primary seed loaded %+v", item)
		}
	}
	ownerSeed, err := loadSeedProducts(ctx, other, db)
	if err != nil {
		t.Fatal(err)
	}
	seen := false
	for _, item := range ownerSeed {
		if item.id == created.ID && item.name == typed {
			seen = true
		}
	}
	if !seen {
		t.Fatalf("owner seed %+v", ownerSeed)
	}
}
