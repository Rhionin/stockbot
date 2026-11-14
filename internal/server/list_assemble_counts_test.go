package server

import (
	"testing"

	"github.com/Rhionin/stockbot/internal/attention"
	"github.com/Rhionin/stockbot/internal/inventory"
	"github.com/Rhionin/stockbot/internal/product"
	"github.com/Rhionin/stockbot/internal/shopping"
)

func TestCountW3NotOnListSkipsNeedsAttention(t *testing.T) {
	na := &NeedsAttentionResponse{Reason: "attention"}
	off := []OffListRowResponse{
		{ID: "item:a", OffListReason: "removed"},
		{ID: "item:b", OffListReason: "removed", NeedsAttention: na},
	}
	if n := countW3NotOnList(off); n != 1 {
		t.Fatalf("notOnList %d want 1", n)
	}
}

func TestCountW3ShortNowDistinctLineKeys(t *testing.T) {
	adj := 1
	items := []ShoppingListEntryResponse{
		{ItemID: "i1", LineKey: shopping.ItemLineKey("i1"), Quantity: 2, Adjustment: &adj},
		{ItemID: "i2", LineKey: shopping.ItemLineKey("i1"), Quantity: 3, Adjustment: &adj},
	}
	if n := countW3ShortNow(items); n != 1 {
		t.Fatalf("shortNow %d want 1", n)
	}
}

func TestWireAttentionGroupsOmitsEmptyCount(t *testing.T) {
	groups := []attention.AttentionGroup{{
		Reason: "attention", Count: 1, RowKeys: []string{"item:ghost"},
	}}
	dropped := map[string]struct{}{"item:ghost": {}}
	wire := wireAttentionGroups(groups, nil, dropped)
	if len(wire) != 0 {
		t.Fatalf("empty group emitted %#v", wire)
	}
}

func TestListEntryLineDisplayNameFromProduct(t *testing.T) {
	name := listEntryLineDisplayName(ShoppingListEntryResponse{ItemID: "i1"}, inventory.Item{
		Product: &product.ProductSummary{Name: "Hand Added"},
	})
	if name != "Hand Added" {
		t.Fatalf("display name %q", name)
	}
}