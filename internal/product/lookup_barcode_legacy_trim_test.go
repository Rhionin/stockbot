package product

import (
	"context"
	"testing"
)

func TestLookupBarcode_PaddedLegacyBarcodeRow(t *testing.T) {
	db := setupTestDB(t)
	catalog := NewCatalog(db)
	scope := primaryScope(t, db)
	ctx := context.Background()
	const upc = "036000291452"
	padded := " " + upc + "\t\r\n"
	if err := catalog.CreateProduct(ctx, Product{ID: "prod-legacy-pad", Name: "Legacy Pad", Category: "Test"}); err != nil {
		t.Fatal(err)
	}
	if err := catalog.UpsertBarcodeMapping(ctx, padded, "prod-legacy-pad", "global", ""); err != nil {
		t.Fatal(err)
	}
	summary, err := catalog.LookupBarcode(ctx, scope, upc)
	if err != nil {
		t.Fatal(err)
	}
	if summary == nil || summary.ID != "prod-legacy-pad" {
		t.Fatalf("LookupBarcode(%q) = %+v, want prod-legacy-pad", upc, summary)
	}
}
