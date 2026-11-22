package product

import (
	"context"
	"testing"
	"time"
)

func TestGetBarcodeMiss_PaddedLegacyMissRow(t *testing.T) {
	db := setupTestDB(t)
	catalog := NewCatalog(db)
	ctx := context.Background()
	const upc = "036000291452"
	padded := "\t" + upc + " \r\n"
	stamp := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := catalog.RecordBarcodeMiss(ctx, padded, stamp); err != nil {
		t.Fatal(err)
	}
	got, err := catalog.GetBarcodeMiss(ctx, upc)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || !got.Equal(stamp) {
		t.Fatalf("GetBarcodeMiss(%q) = %v, want %v", upc, got, stamp)
	}
}

func TestLookupService_PaddedLegacyMissRowSkipsUpstream(t *testing.T) {
	db := setupTestDB(t)
	catalog := NewCatalog(db)
	scope := primaryScope(t, db)
	ctx := context.Background()
	const upc = "036000291452"
	padded := " " + upc + "\t\r\n"
	upstreamCalls := 0
	service := &LookupService{
		Catalog: catalog,
		Upstream: mockUpstream{fn: func(ctx context.Context, barcode string) FanOutResult {
			upstreamCalls++
			return FanOutResult{Outcome: FanOutConfirmedMiss}
		}},
	}
	if err := catalog.RecordBarcodeMiss(ctx, padded, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Lookup(ctx, scope, upc); err != nil {
		t.Fatal(err)
	}
	if upstreamCalls != 0 {
		t.Fatalf("upstream calls = %d, want 0 (padded legacy miss row)", upstreamCalls)
	}
}
