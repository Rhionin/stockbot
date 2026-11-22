package product

import (
	"context"
	"testing"
)

// A local-only (FNSKU/ASIN) hit must not schedule the upstream refresh; a
// product-barcode hit on the same product still does (positive control, so
// the refresher stub is proven wired).
func TestLookupService_LocalOnlyHitDoesNotScheduleRefresh(t *testing.T) {
	db := setupTestDB(t)
	catalog := NewCatalog(db)
	scope := primaryScope(t, db)
	ctx := context.Background()
	if err := catalog.CreateProduct(ctx, Product{ID: "ext-prod", Name: "External", Source: SourceExternal}); err != nil {
		t.Fatal(err)
	}
	const fnsku, upc = "X002MB5N7L", "987654321098"
	for _, bc := range []string{fnsku, upc} {
		if err := catalog.UpsertBarcodeMapping(ctx, bc, "ext-prod", "global", ""); err != nil {
			t.Fatal(err)
		}
	}
	var scheduled []string
	upstreamCalls := 0
	service := &LookupService{
		Catalog: catalog,
		Upstream: mockUpstream{fn: func(ctx context.Context, barcode string) FanOutResult {
			upstreamCalls++
			return FanOutResult{Outcome: FanOutUnresolved}
		}},
		Refresher: mockRefresher{scheduleFn: func(ctx context.Context, productID string) {
			scheduled = append(scheduled, productID)
		}},
	}

	res, err := service.Lookup(ctx, scope, fnsku)
	if err != nil || !res.IsFound() {
		t.Fatalf("local-only lookup: found=%v err=%v", res.IsFound(), err)
	}
	if len(scheduled) != 0 {
		t.Fatalf("local-only hit scheduled refresh for %v, want none", scheduled)
	}
	if upstreamCalls != 0 {
		t.Fatalf("upstream calls = %d, want 0", upstreamCalls)
	}

	res, err = service.Lookup(ctx, scope, upc)
	if err != nil || !res.IsFound() {
		t.Fatalf("product lookup: found=%v err=%v", res.IsFound(), err)
	}
	if len(scheduled) != 1 || scheduled[0] != "ext-prod" {
		t.Fatalf("product hit scheduled = %v, want [ext-prod]", scheduled)
	}
}
