package product_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Rhionin/stockbot/internal/product"
	"github.com/Rhionin/stockbot/internal/tenant"
)

func TestTypedNameGuards(t *testing.T) {
	if got := product.DisplayNameSQL(""); !strings.Contains(got, "p.id") || !strings.Contains(got, "p.name") {
		t.Fatal(got)
	}
	catalog := newTestCatalog(t)
	scope := scopeOf(t, catalog)
	ctx := context.Background()
	if err := catalog.SaveDisplayName(ctx, scope, "prod-guard", "  "); err == nil {
		t.Fatal("blank name")
	}
	if _, err := catalog.CreateTypedProduct(ctx, scope, product.Product{Name: " "}); err == nil {
		t.Fatal("blank typed name")
	}
	if err := catalog.UpsertHouseholdBarcode(ctx, scope, " ", "prod-guard"); err == nil {
		t.Fatal("blank barcode")
	}

	typed, err := catalog.CreateTypedProduct(ctx, scope, product.Product{Name: "private oats"})
	if err != nil {
		t.Fatal(err)
	}
	if err := catalog.UpsertBarcodeMapping(ctx, "0001112223334", typed.ID, "global", ""); err != nil {
		t.Fatal(err)
	}
	other, err := tenant.New("0190bbbb-cccc-7000-8000-0000000000e5")
	if err != nil {
		t.Fatal(err)
	}
	got, err := catalog.LookupBarcode(ctx, other, "0001112223334")
	if err != nil || got != nil {
		t.Fatalf("other household saw %v (%v)", got, err)
	}
}
