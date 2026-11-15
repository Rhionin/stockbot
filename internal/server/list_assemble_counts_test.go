package server

import (
	"testing"

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

func TestCountW3ShortNowStatusDistinctLineKeys(t *testing.T) {
	adj := 1
	items := []ShoppingListStatusEntryResponse{{
		ShoppingListEntryResponse: ShoppingListEntryResponse{
			ItemID: "i1", LineKey: shopping.ItemLineKey("i1"), Quantity: 2, Adjustment: &adj,
		},
		Name: "A",
	}, {
		ShoppingListEntryResponse: ShoppingListEntryResponse{
			ItemID: "i2", LineKey: shopping.ItemLineKey("i1"), Quantity: 3, Adjustment: &adj,
		},
		Name: "B",
	}}
	if n := countW3ShortNowStatus(items); n != 1 {
		t.Fatalf("shortNow %d want 1", n)
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