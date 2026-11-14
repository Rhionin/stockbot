package attention

import (
	"testing"

	"github.com/Rhionin/stockbot/internal/shopping"
	"github.com/Rhionin/stockbot/internal/supply"
)

func TestLineKeyForItemSendSteps(t *testing.T) {
	itemID := "item-x"
	itemKey := shopping.ItemLineKey(itemID)
	plan := map[string]supply.LinePlanView{}
	staged := map[string]StagedRow{}
	groups := map[string]string{}

	if got := LineKeyForItemSend(itemID, staged, plan, groups); got != itemKey {
		t.Fatalf("step4 default %q", got)
	}

	staged[itemID] = StagedRow{ItemID: itemID, Qty: 1}
	if got := LineKeyForItemSend(itemID, staged, plan, groups); got != itemKey {
		t.Fatalf("step1 item %q", got)
	}

	staged[itemID] = StagedRow{ItemID: itemID, GroupID: "g1", Qty: 1}
	groupKey := shopping.GroupLineKey("g1")
	if got := LineKeyForItemSend(itemID, staged, plan, groups); got != groupKey {
		t.Fatalf("step1 group %q", got)
	}

	delete(staged, itemID)
	plan[itemKey] = supply.LinePlanView{ItemID: itemID}
	if got := LineKeyForItemSend(itemID, staged, plan, groups); got != itemKey {
		t.Fatalf("step2 %q", got)
	}

	delete(plan, itemKey)
	groups[itemID] = "g1"
	plan[groupKey] = supply.LinePlanView{GroupID: "g1"}
	if got := LineKeyForItemSend(itemID, staged, plan, groups); got != groupKey {
		t.Fatalf("step3 %q", got)
	}
}

func TestMapSendResultsByLineKeyMergesMembers(t *testing.T) {
	rows := []SendResultRow{
		{ItemID: "a", Status: "failed"},
		{ItemID: "b", Status: "failed"},
	}
	staged := map[string]StagedRow{"b": {ItemID: "b", GroupID: "g", Qty: 1}}
	plan := map[string]supply.LinePlanView{shopping.GroupLineKey("g"): {GroupID: "g"}}
	byLine := MapSendResultsByLineKey(rows, staged, plan, nil)
	gKey := shopping.GroupLineKey("g")
	if len(byLine[gKey]) != 1 || byLine[gKey][0].ItemID != "b" {
		t.Fatalf("group %#v", byLine[gKey])
	}
	if len(byLine[shopping.ItemLineKey("a")]) != 1 {
		t.Fatalf("item a %#v", byLine)
	}
}