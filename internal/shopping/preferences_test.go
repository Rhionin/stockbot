package shopping_test

import (
	"context"
	"testing"

	"github.com/Rhionin/stockbot/internal/shopping"
)

func TestDealRoundTrip(t *testing.T) {
	deps := newTestStore(t)
	ctx := context.Background()
	scope := scopeOf(t, deps.db)
	kr := createTestItem(t, deps, ctx, "prod-kr-deal", "Kroger Cut Green Beans")
	price := 79
	regular := 125
	if err := deps.shopping.SaveDeal(ctx, scope, shopping.Deal{
		ItemID: kr, PriceCents: &price, RegularPriceCents: &regular, Label: "Weekly ad",
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	price = 69
	if err := deps.shopping.SaveDeal(ctx, scope, shopping.Deal{
		ItemID: kr, PriceCents: &price, Label: "Weekly ad",
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	deals, err := deps.shopping.ListDeals(ctx, scope)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(deals) != 1 || deals[0].PriceCents == nil || *deals[0].PriceCents != 69 || deals[0].RegularPriceCents != nil {
		t.Fatalf("deals = %+v", deals)
	}
	if deals[0].Source != shopping.DealSourceRecorded {
		t.Fatalf("source = %q", deals[0].Source)
	}
	if err := deps.shopping.DeleteDeal(ctx, scope, kr); err != nil {
		t.Fatalf("delete: %v", err)
	}
	deals, err = deps.shopping.ListDeals(ctx, scope)
	if err != nil {
		t.Fatalf("list after delete: %v", err)
	}
	if len(deals) != 0 {
		t.Fatalf("deals after delete = %+v", deals)
	}
}
