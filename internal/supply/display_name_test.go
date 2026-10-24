package supply

import (
	"context"
	"testing"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/product"
	"github.com/Rhionin/stockbot/internal/tenant"
)

func TestLoadFactsUsesTheHouseholdName(t *testing.T) {
	db, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)
	if err := app.RunMigrations(db); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	primary := scopeOf(t, db)
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
	created, err := catalog.CreateTypedProduct(ctx, other, product.Product{ID: "typed-supply", Name: typed, UnitOfMeasure: "unit"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO items (id, user_id, product_id, household_id) VALUES ('item-typed', ?, ?, ?)`,
		other.HouseholdID(), created.ID, other.HouseholdID()); err != nil {
		t.Fatal(err)
	}
	if err := catalog.CreateProduct(ctx, product.Product{ID: "public-supply", Name: "Public oats fixture", Source: product.SourceExternal}); err != nil {
		t.Fatal(err)
	}
	if err := catalog.SaveDisplayName(ctx, primary, "public-supply", "alder-sentinel-k7m2q oats"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO items (id, user_id, product_id, household_id) VALUES ('item-public', ?, 'public-supply', ?)`,
		primary.HouseholdID(), primary.HouseholdID()); err != nil {
		t.Fatal(err)
	}

	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	ownerFacts, err := loadFacts(ctx, other, tx, Phase{})
	if err != nil {
		t.Fatal(err)
	}
	if len(ownerFacts) != 1 || ownerFacts[0].Name != typed {
		t.Fatalf("owner facts %+v", ownerFacts)
	}
	otherFacts, err := loadFacts(ctx, primary, tx, Phase{})
	if err != nil {
		t.Fatal(err)
	}
	for _, fact := range otherFacts {
		if fact.Name == typed || fact.Product == ProductID(created.ID) {
			t.Fatalf("primary fact leaked %+v", fact)
		}
		if fact.Product == "public-supply" && fact.Name != "alder-sentinel-k7m2q oats" {
			t.Fatalf("primary public name %q", fact.Name)
		}
	}
}
