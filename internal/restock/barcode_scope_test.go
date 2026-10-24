package restock

import (
	"context"
	"testing"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/product"
	"github.com/Rhionin/stockbot/internal/tenant"
)

func TestSecondaryHouseholdSeesGlobalBarcodes(t *testing.T) {
	db, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := app.RunMigrations(db); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	primary := primaryScope(t, db)
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
	if err := catalog.CreateProduct(ctx, product.Product{ID: "public-code", Name: "Public oats fixture", Source: product.SourceExternal}); err != nil {
		t.Fatal(err)
	}
	const globalCode = "0001112223334"
	const overrideCode = "0001112223335"
	if err := catalog.UpsertBarcodeMapping(ctx, globalCode, "public-code", "global", ""); err != nil {
		t.Fatal(err)
	}
	if err := catalog.UpsertHouseholdBarcode(ctx, other, overrideCode, "public-code"); err != nil {
		t.Fatal(err)
	}
	codes, err := loadBarcodes(ctx, other, db)
	if err != nil {
		t.Fatal(err)
	}
	if codes["public-code"] != overrideCode && codes["public-code"] != globalCode {
		t.Fatalf("cedar barcodes %v", codes)
	}
	if codes["public-code"] != overrideCode {
		t.Fatalf("user override should win, got %q", codes["public-code"])
	}
	primaryCodes, err := loadBarcodes(ctx, primary, db)
	if err != nil {
		t.Fatal(err)
	}
	if primaryCodes["public-code"] != globalCode {
		t.Fatalf("primary should keep the global code, got %q (%v)", primaryCodes["public-code"], primaryCodes)
	}
}
