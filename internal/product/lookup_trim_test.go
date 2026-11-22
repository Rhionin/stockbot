package product

import (
	"context"
	"sync"
	"testing"
)

func TestLookupService_TrimmedBarcodeForCatalogAndUpstream(t *testing.T) {
	db := setupTestDB(t)
	catalog := NewCatalog(db)
	scope := primaryScope(t, db)
	ctx := context.Background()
	const fnsku = "X002MB5N7L"
	if err := catalog.CreateProduct(ctx, Product{ID: "prod-padded", Name: "Padded", Category: "Test"}); err != nil {
		t.Fatal(err)
	}
	if err := catalog.UpsertBarcodeMapping(ctx, fnsku, "prod-padded", "global", ""); err != nil {
		t.Fatal(err)
	}
	var upstreamBarcodes []string
	var mu sync.Mutex
	service := &LookupService{
		Catalog: catalog,
		Upstream: mockUpstream{fn: func(ctx context.Context, barcode string) FanOutResult {
			mu.Lock()
			upstreamBarcodes = append(upstreamBarcodes, barcode)
			mu.Unlock()
			return FanOutResult{Outcome: FanOutConfirmedMiss}
		}},
	}

	padded := "  " + fnsku + " \r\n"
	res, err := service.Lookup(ctx, scope, padded)
	if err != nil || !res.IsFound() || res.Product.ID != "prod-padded" {
		t.Fatalf("padded local-only lookup = %+v err=%v", res, err)
	}

	const upc = "036000291452"
	paddedUPC := "\t " + upc + "\n"
	res, err = service.Lookup(ctx, scope, paddedUPC)
	if err != nil {
		t.Fatalf("padded product lookup err=%v", err)
	}
	if res.IsFound() {
		t.Fatal("expected upstream miss without local mapping")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(upstreamBarcodes) != 1 || upstreamBarcodes[0] != upc {
		t.Fatalf("upstream barcodes = %v, want [%q]", upstreamBarcodes, upc)
	}
}
