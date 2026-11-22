package product

import (
	"context"
	"testing"

	"github.com/Rhionin/stockbot/internal/barcode"
)

func TestLookupService_LocalOnlySkipsUpstreamAndMisses(t *testing.T) {
	db := setupTestDB(t)
	catalog := NewCatalog(db)
	scope := primaryScope(t, db)
	upstreamCalls := 0
	service := &LookupService{
		Catalog: catalog,
		Upstream: mockUpstream{fn: func(ctx context.Context, barcode string) FanOutResult {
			upstreamCalls++
			return FanOutResult{
				Outcome: FanOutHit,
				Product: &ProductSummary{ID: "off-would-hit", Name: "Upstream", Category: "Food"},
				Source:  ExternalSourceOpenFoodFacts,
			}
		}},
	}
	const fnsku = "X002MB5N7L"
	result, err := service.Lookup(context.Background(), scope, fnsku)
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if result.IsFound() {
		t.Fatal("expected local miss")
	}
	if upstreamCalls != 0 {
		t.Fatalf("upstream calls = %d, want 0", upstreamCalls)
	}
	miss, err := catalog.GetBarcodeMiss(context.Background(), fnsku)
	if err != nil {
		t.Fatalf("GetBarcodeMiss: %v", err)
	}
	if miss != nil {
		t.Fatal("barcode_misses row written for local-only miss")
	}
	if barcode.Classify(fnsku) != barcode.LookupLocalOnly {
		t.Fatalf("classify = %v, want local-only", barcode.Classify(fnsku))
	}
}

func TestLookupService_LocalOnlyResolvesHouseholdMapping(t *testing.T) {
	db := setupTestDB(t)
	catalog := NewCatalog(db)
	scope := primaryScope(t, db)
	const fnsku = "X002MB5N7L"
	if err := catalog.CreateProduct(context.Background(), Product{ID: "prod-fnsku", Name: "Mapped", Category: "Test"}); err != nil {
		t.Fatal(err)
	}
	if err := catalog.UpsertBarcodeMapping(context.Background(), fnsku, "prod-fnsku", "global", ""); err != nil {
		t.Fatal(err)
	}
	upstreamCalls := 0
	service := &LookupService{
		Catalog: catalog,
		Upstream: mockUpstream{fn: func(ctx context.Context, barcode string) FanOutResult {
			upstreamCalls++
			return FanOutResult{Outcome: FanOutUnresolved}
		}},
	}
	result, err := service.Lookup(context.Background(), scope, fnsku)
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if !result.IsFound() || result.Product.ID != "prod-fnsku" {
		t.Fatalf("result = %+v", result)
	}
	if upstreamCalls != 0 {
		t.Fatalf("upstream calls = %d, want 0", upstreamCalls)
	}
}