package server

import (
	"testing"

	"github.com/Rhionin/stockbot/internal/inventory"
	"github.com/Rhionin/stockbot/internal/supply"
)

func TestPlannedAutoLineSkipsManualAndZeroBuy(t *testing.T) {
	item := inventory.Item{ID: "item-a", ProductID: "prod-a"}
	lines := []supply.Line{
		{Product: supply.ProductID("prod-a"), Buy: 0, Source: supply.SourceManual},
		{Product: supply.ProductID("prod-b"), Buy: 2, Source: supply.SourceManual},
	}
	if _, ok := plannedAutoLine(lines, item); ok {
		t.Fatal("manual and zero buy should not match")
	}
	lines = []supply.Line{
		{Product: supply.ProductID("prod-a"), Buy: 3, Note: "need", Source: supply.SourceReplace},
	}
	got, ok := plannedAutoLine(lines, item)
	if !ok || got.Quantity != 3 || got.Note != "need" {
		t.Fatalf("planned line: %#v ok=%v", got, ok)
	}
}
