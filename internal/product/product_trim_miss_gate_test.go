package product

import (
	"context"
	"testing"
)

// The confirmed-miss row is keyed by the trimmed barcode, and the miss gate
// reads the trimmed key: a differently padded rescan must not fan out again.
func TestLookupService_TrimmedBarcodeKeysMissGate(t *testing.T) {
	db := setupTestDB(t)
	catalog := NewCatalog(db)
	scope := primaryScope(t, db)
	ctx := context.Background()
	const upc = "036000291452"
	upstreamCalls := 0
	service := &LookupService{
		Catalog: catalog,
		Upstream: mockUpstream{fn: func(ctx context.Context, barcode string) FanOutResult {
			upstreamCalls++
			return FanOutResult{Outcome: FanOutConfirmedMiss}
		}},
	}
	if _, err := service.Lookup(ctx, scope, " "+upc+"\r\n"); err != nil {
		t.Fatal(err)
	}
	if upstreamCalls != 1 {
		t.Fatalf("first lookup upstream calls = %d, want 1", upstreamCalls)
	}
	if miss, err := catalog.GetBarcodeMiss(ctx, upc); err != nil || miss == nil {
		t.Fatalf("miss row under trimmed key = %v err=%v, want present", miss, err)
	}
	if miss, _ := catalog.GetBarcodeMiss(ctx, " "+upc+"\r\n"); miss != nil {
		t.Fatal("miss row stored under the padded key")
	}
	if _, err := service.Lookup(ctx, scope, "\t"+upc+" "); err != nil {
		t.Fatal(err)
	}
	if upstreamCalls != 1 {
		t.Fatalf("differently padded rescan fanned out again: upstream calls = %d, want 1", upstreamCalls)
	}
}
