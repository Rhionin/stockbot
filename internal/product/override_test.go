package product_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/Rhionin/stockbot/internal/product"
	"github.com/Rhionin/stockbot/internal/tenant"
)

func TestTypedNamesStayInsideTheHousehold(t *testing.T) {
	db := openProductDB(t)
	catalog := product.NewCatalog(db)
	ctx := context.Background()
	primary := mustPrimary(t, db)
	other := insertHousehold(t, db, "0190bbbb-cccc-7000-8000-0000000000e5", "Household Cedar")

	const publicName = "Public oats fixture"
	err := catalog.CreateProduct(ctx, product.Product{
		ID:     "0190cccc-dddd-7000-8000-0000000000f6",
		Name:   publicName,
		Source: product.SourceExternal,
	})
	if err != nil {
		t.Fatal(err)
	}
	const renamed = "alder-sentinel-k7m2q oats"
	if err := catalog.SaveDisplayName(ctx, primary, "0190cccc-dddd-7000-8000-0000000000f6", renamed); err != nil {
		t.Fatal(err)
	}
	if err := catalog.SaveDisplayName(ctx, tenant.Scope{}, "0190cccc-dddd-7000-8000-0000000000f6", "nope"); !errors.Is(err, tenant.ErrMissingHousehold) {
		t.Fatalf("zero scope: %v", err)
	}

	global, err := catalog.GetProductByID(ctx, "0190cccc-dddd-7000-8000-0000000000f6")
	if err != nil || global == nil || global.Name != publicName {
		t.Fatalf("global name %v err %v", global, err)
	}
	seen, err := catalog.ProductFor(ctx, primary, global.ID)
	if err != nil || seen == nil || seen.Name != renamed || !seen.NameOverridden {
		t.Fatalf("owner view %+v err %v", seen, err)
	}
	otherView, err := catalog.ProductFor(ctx, other, global.ID)
	if err != nil || otherView == nil || otherView.Name != publicName || otherView.NameOverridden {
		t.Fatalf("other household saw %+v err %v", otherView, err)
	}
	assertCatalogHasNoText(t, db, renamed)

	const typed = "cedar-sentinel-q9p4w cereal"
	created, err := catalog.CreateTypedProduct(ctx, other, product.Product{
		ID:       "0190dddd-eeee-7000-8000-0000000000a7",
		Name:     typed,
		Category: "Household Cedar",
	})
	if err != nil {
		t.Fatal(err)
	}
	stored, err := catalog.GetProductByID(ctx, created.ID)
	if err != nil || stored == nil || stored.Name != "" || stored.Source != product.SourceUser {
		t.Fatalf("typed product leaked onto the catalog row: %+v err %v", stored, err)
	}
	owner, err := catalog.ProductFor(ctx, other, created.ID)
	if err != nil || owner == nil || owner.Name != typed {
		t.Fatalf("owner typed view %+v err %v", owner, err)
	}
	hidden, err := catalog.ProductFor(ctx, primary, created.ID)
	if err != nil || hidden != nil {
		t.Fatalf("primary saw typed product %+v err %v", hidden, err)
	}
	assertCatalogHasNoText(t, db, typed)

	const barcode = "2001000000012"
	if err := catalog.UpsertBarcodeMapping(ctx, barcode, global.ID, "global", ""); err != nil {
		t.Fatal(err)
	}
	if err := catalog.UpsertHouseholdBarcode(ctx, other, barcode, created.ID); err != nil {
		t.Fatal(err)
	}
	gotOther, err := catalog.LookupBarcode(ctx, other, barcode)
	if err != nil || gotOther == nil || gotOther.ID != created.ID || gotOther.Name != typed {
		t.Fatalf("owner barcode %+v err %v", gotOther, err)
	}
	gotPrimary, err := catalog.LookupBarcode(ctx, primary, barcode)
	if err != nil || gotPrimary == nil || gotPrimary.ID != global.ID || gotPrimary.Name != renamed {
		t.Fatalf("other barcode must stay on the public product with that household's name: %+v err %v", gotPrimary, err)
	}
	if strings.Contains(gotPrimary.Name, "cedar-sentinel") {
		t.Fatalf("public lookup returned a typed name %q", gotPrimary.Name)
	}
}

func assertCatalogHasNoText(t *testing.T, db *sql.DB, text string) {
	t.Helper()
	checks := []string{
		`SELECT name FROM products`,
		`SELECT categories_tags || ' ' || food_groups_tags FROM product_off_tags`,
		`SELECT barcode FROM barcode_misses`,
	}
	for _, query := range checks {
		rows, err := db.Query(query)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var value string
			if err := rows.Scan(&value); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			if strings.Contains(value, text) {
				rows.Close()
				t.Fatalf("%s contains %q (%q)", query, text, value)
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
	}
}

func openProductDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := app.RunMigrations(db); err != nil {
		t.Fatal(err)
	}
	return db
}

func mustPrimary(t *testing.T, db *sql.DB) tenant.Scope {
	t.Helper()
	scope, err := auth.PrimaryScope(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	return scope
}

func insertHousehold(t *testing.T, db *sql.DB, id, name string) tenant.Scope {
	t.Helper()
	_, err := db.Exec(`
		INSERT INTO households (id, name, restock_mode, created_at, channel_key, is_primary)
		VALUES (?, ?, '', '2026-02-02T00:00:00Z', 'abcdef0123456789abcdef0123456789', 0)`, id, name)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := tenant.New(id)
	if err != nil {
		t.Fatal(err)
	}
	return scope
}
