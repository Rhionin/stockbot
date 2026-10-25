package product_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/inventory"
	"github.com/Rhionin/stockbot/internal/product"
)

func TestSearchFindsHouseholdBarcodelessItem(t *testing.T) {
	conn, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := app.RunMigrations(conn); err != nil {
		t.Fatalf("migrations: %v", err)
	}
	catalog := product.NewCatalog(conn)
	pantry := inventory.NewPantry(conn)
	ctx := context.Background()
	scope := scopeOf(t, catalog)
	created, err := catalog.CreateTypedProduct(ctx, scope, product.Product{Name: "Homemade jam"})
	if err != nil {
		t.Fatalf("CreateTypedProduct: %v", err)
	}
	if _, err := pantry.GetOrCreateItem(ctx, scope, created.ID); err != nil {
		t.Fatalf("GetOrCreateItem: %v", err)
	}
	hits, err := catalog.SearchProducts(ctx, scope, "jam", 20)
	if err != nil {
		t.Fatalf("SearchProducts: %v", err)
	}
	if len(hits) != 1 || hits[0].Name != "Homemade jam" || hits[0].OnHand != 0 {
		t.Fatalf("hits = %+v", hits)
	}
}

func TestSearchTrimsQuery(t *testing.T) {
	conn, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := app.RunMigrations(conn); err != nil {
		t.Fatalf("migrations: %v", err)
	}
	catalog := product.NewCatalog(conn)
	pantry := inventory.NewPantry(conn)
	ctx := context.Background()
	scope := scopeOf(t, catalog)
	created, err := catalog.CreateTypedProduct(ctx, scope, product.Product{Name: "Trim jam"})
	if err != nil {
		t.Fatalf("CreateTypedProduct: %v", err)
	}
	if _, err := pantry.GetOrCreateItem(ctx, scope, created.ID); err != nil {
		t.Fatalf("GetOrCreateItem: %v", err)
	}
	hits, err := catalog.SearchProducts(ctx, scope, "  trim  ", 20)
	if err != nil {
		t.Fatalf("SearchProducts: %v", err)
	}
	if len(hits) != 1 || hits[0].Name != "Trim jam" {
		t.Fatalf("hits = %+v", hits)
	}
}

func TestSearchQueryTooShort(t *testing.T) {
	conn, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := app.RunMigrations(conn); err != nil {
		t.Fatalf("migrations: %v", err)
	}
	catalog := product.NewCatalog(conn)
	ctx := context.Background()
	scope := scopeOf(t, catalog)
	_, err = catalog.SearchProducts(ctx, scope, "j", 20)
	if !errors.Is(err, product.ErrQueryTooShort) {
		t.Fatalf("err = %v", err)
	}
}

func TestSearchCatalogProductWithoutHouseholdItem(t *testing.T) {
	conn, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := app.RunMigrations(conn); err != nil {
		t.Fatalf("migrations: %v", err)
	}
	catalog := product.NewCatalog(conn)
	ctx := context.Background()
	scope := scopeOf(t, catalog)
	const globalID = "0190aaaa-bbbb-7000-8000-0000000000c1"
	if err := catalog.CreateProduct(ctx, product.Product{
		ID:     globalID,
		Name:   "Catalog oats",
		Source: product.SourceExternal,
	}); err != nil {
		t.Fatalf("CreateProduct: %v", err)
	}
	if err := catalog.UpsertBarcodeMapping(ctx, "2001000000012", globalID, "global", ""); err != nil {
		t.Fatalf("barcode: %v", err)
	}
	hits, err := catalog.SearchProducts(ctx, scope, "oats", 20)
	if err != nil {
		t.Fatalf("SearchProducts: %v", err)
	}
	if len(hits) != 1 || hits[0].ProductID != globalID || hits[0].ItemID != "" || hits[0].OnHand != 0 {
		t.Fatalf("hits = %+v", hits)
	}
}

func TestSearchOrdersHouseholdItemBeforeCatalog(t *testing.T) {
	conn, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := app.RunMigrations(conn); err != nil {
		t.Fatalf("migrations: %v", err)
	}
	catalog := product.NewCatalog(conn)
	pantry := inventory.NewPantry(conn)
	ctx := context.Background()
	scope := scopeOf(t, catalog)
	const globalID = "0190aaaa-bbbb-7000-8000-0000000000c2"
	if err := catalog.CreateProduct(ctx, product.Product{
		ID:     globalID,
		Name:   "Shared rice",
		Source: product.SourceExternal,
	}); err != nil {
		t.Fatalf("CreateProduct: %v", err)
	}
	typed, err := catalog.CreateTypedProduct(ctx, scope, product.Product{Name: "Shared rice"})
	if err != nil {
		t.Fatalf("CreateTypedProduct: %v", err)
	}
	if _, err := pantry.GetOrCreateItem(ctx, scope, typed.ID); err != nil {
		t.Fatalf("GetOrCreateItem: %v", err)
	}
	hits, err := catalog.SearchProducts(ctx, scope, "rice", 20)
	if err != nil {
		t.Fatalf("SearchProducts: %v", err)
	}
	if len(hits) < 2 {
		t.Fatalf("hits = %+v", hits)
	}
	if hits[0].ProductID != typed.ID || hits[0].ItemID == "" {
		t.Fatalf("household item should be first: %+v", hits)
	}
	foundCatalog := false
	for _, hit := range hits {
		if hit.ProductID == globalID {
			foundCatalog = true
		}
	}
	if !foundCatalog {
		t.Fatalf("missing catalog match: %+v", hits)
	}
}

func TestSearchIncludesFormattedSize(t *testing.T) {
	conn, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := app.RunMigrations(conn); err != nil {
		t.Fatalf("migrations: %v", err)
	}
	catalog := product.NewCatalog(conn)
	ctx := context.Background()
	scope := scopeOf(t, catalog)
	const globalID = "0190aaaa-bbbb-7000-8000-0000000000c3"
	prod := product.Product{ID: globalID, Name: "Sparkling water", Source: product.SourceExternal}
	parsed, ok := product.ParseNetSize("16 fl oz")
	if !ok {
		t.Fatal("ParseNetSize")
	}
	prod.ApplyParsedSize(parsed)
	if err := catalog.CreateProduct(ctx, prod); err != nil {
		t.Fatalf("CreateProduct: %v", err)
	}
	hits, err := catalog.SearchProducts(ctx, scope, "spark", 20)
	if err != nil {
		t.Fatalf("SearchProducts: %v", err)
	}
	if len(hits) != 1 || hits[0].Size == "" {
		t.Fatalf("hits = %+v", hits)
	}
}

func TestSearchLimitCapsAtFifty(t *testing.T) {
	conn, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := app.RunMigrations(conn); err != nil {
		t.Fatalf("migrations: %v", err)
	}
	catalog := product.NewCatalog(conn)
	pantry := inventory.NewPantry(conn)
	ctx := context.Background()
	scope := scopeOf(t, catalog)
	for i := 0; i < 55; i++ {
		name := fmt.Sprintf("Cap bean %02d", i)
		created, err := catalog.CreateTypedProduct(ctx, scope, product.Product{Name: name})
		if err != nil {
			t.Fatalf("CreateTypedProduct: %v", err)
		}
		if _, err := pantry.GetOrCreateItem(ctx, scope, created.ID); err != nil {
			t.Fatalf("GetOrCreateItem: %v", err)
		}
	}
	hits, err := catalog.SearchProducts(ctx, scope, "cap", 100)
	if err != nil {
		t.Fatalf("SearchProducts: %v", err)
	}
	if len(hits) != 50 {
		t.Fatalf("len(hits) = %d", len(hits))
	}
}

func TestSearchRespectsLimit(t *testing.T) {
	conn, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := app.RunMigrations(conn); err != nil {
		t.Fatalf("migrations: %v", err)
	}
	catalog := product.NewCatalog(conn)
	pantry := inventory.NewPantry(conn)
	ctx := context.Background()
	scope := scopeOf(t, catalog)
	for _, name := range []string{"Limit bean a", "Limit bean b", "Limit bean c"} {
		created, err := catalog.CreateTypedProduct(ctx, scope, product.Product{Name: name})
		if err != nil {
			t.Fatalf("CreateTypedProduct: %v", err)
		}
		if _, err := pantry.GetOrCreateItem(ctx, scope, created.ID); err != nil {
			t.Fatalf("GetOrCreateItem: %v", err)
		}
	}
	hits, err := catalog.SearchProducts(ctx, scope, "limit", 2)
	if err != nil {
		t.Fatalf("SearchProducts: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("hits = %+v", hits)
	}
}

func TestSearchHidesOtherHouseholdTypedProduct(t *testing.T) {
	conn, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := app.RunMigrations(conn); err != nil {
		t.Fatalf("migrations: %v", err)
	}
	catalog := product.NewCatalog(conn)
	ctx := context.Background()
	scope := scopeOf(t, catalog)
	const cedarID = "0190bbbb-cccc-7000-8000-0000000000e5"
	other, err := catalog.AddHousehold(ctx, cedarID, "Household Cedar")
	if err != nil {
		t.Fatalf("AddHousehold: %v", err)
	}
	typed, err := catalog.CreateTypedProduct(ctx, other, product.Product{Name: "Secret cedar jam"})
	if err != nil {
		t.Fatalf("CreateTypedProduct: %v", err)
	}
	hits, err := catalog.SearchProducts(ctx, scope, "cedar", 20)
	if err != nil {
		t.Fatalf("SearchProducts: %v", err)
	}
	for _, hit := range hits {
		if hit.ProductID == typed.ID {
			t.Fatalf("leaked foreign typed product: %+v", hits)
		}
	}
}

func TestSearchMatchesBrand(t *testing.T) {
	conn, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := app.RunMigrations(conn); err != nil {
		t.Fatalf("migrations: %v", err)
	}
	catalog := product.NewCatalog(conn)
	pantry := inventory.NewPantry(conn)
	ctx := context.Background()
	scope := scopeOf(t, catalog)
	created, err := catalog.CreateTypedProduct(ctx, scope, product.Product{Name: "Crackers"})
	if err != nil {
		t.Fatalf("CreateTypedProduct: %v", err)
	}
	if err := catalog.SaveBrand(ctx, scope, created.ID, "Pantry"); err != nil {
		t.Fatalf("SaveBrand: %v", err)
	}
	if _, err := pantry.GetOrCreateItem(ctx, scope, created.ID); err != nil {
		t.Fatalf("GetOrCreateItem: %v", err)
	}
	hits, err := catalog.SearchProducts(ctx, scope, "pantry", 20)
	if err != nil {
		t.Fatalf("SearchProducts: %v", err)
	}
	if len(hits) != 1 || hits[0].Brand != "Pantry" {
		t.Fatalf("hits = %+v", hits)
	}
}
