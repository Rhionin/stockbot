package group

import (
	"reflect"
	"testing"

	"github.com/Rhionin/stockbot/internal/product"
)

func goalMember(id, dim string, onHand int, noRestock bool) Member {
	m := Member{ProductID: id, Name: id, OnHand: onHand, NoRestock: noRestock, Dimension: dim}
	if dim != "" {
		v := 100.0
		m.NetBase = &v
	}
	return m
}

func blockerIDs(blockers []Blocker) []string {
	out := []string{}
	for _, b := range blockers {
		out = append(out, b.ProductID+":"+b.Reason)
	}
	return out
}

func TestAmountAvailability(t *testing.T) {
	mass, vol := product.DimensionMass, product.DimensionVolume
	cases := []struct {
		name     string
		members  []Member
		want     bool
		wantDim  string
		blockers []string
	}{
		{name: "no members", want: false, blockers: []string{}},
		{name: "all weight", members: []Member{goalMember("a", mass, 0, false), goalMember("b", mass, 0, false)}, want: true, wantDim: mass, blockers: []string{}},
		{name: "all volume", members: []Member{goalMember("a", vol, 0, false)}, want: true, wantDim: vol, blockers: []string{}},
		{name: "unsized member", members: []Member{goalMember("a", vol, 0, false), goalMember("b", "", 0, false)}, blockers: []string{"b:size_missing"}},
		{name: "mixed names the smaller set", members: []Member{goalMember("a", mass, 0, false), goalMember("b", mass, 0, false), goalMember("c", vol, 0, false)}, blockers: []string{"c:units_mismatch"}},
		{name: "mixed weight smaller", members: []Member{goalMember("a", mass, 0, false), goalMember("b", vol, 0, false), goalMember("c", vol, 0, false)}, blockers: []string{"a:units_mismatch"}},
		{name: "both problems list no-size first", members: []Member{goalMember("a", mass, 0, false), goalMember("b", vol, 0, false), goalMember("c", vol, 0, false), goalMember("d", "", 0, false)}, blockers: []string{"d:size_missing", "a:units_mismatch"}},
		{name: "empty unsized don't-restock member doesn't block", members: []Member{goalMember("a", vol, 0, false), goalMember("b", "", 0, true)}, want: true, wantDim: vol, blockers: []string{}},
		{name: "unsized don't-restock member with stock blocks", members: []Member{goalMember("a", vol, 0, false), goalMember("b", "", 2, true)}, blockers: []string{"b:size_missing"}},
		{name: "every member don't restock and empty", members: []Member{goalMember("a", vol, 0, true)}, blockers: []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, dim, blockers := AmountAvailability(tc.members)
			if got != tc.want || dim != tc.wantDim || !reflect.DeepEqual(blockerIDs(blockers), tc.blockers) {
				t.Fatalf("got %v %q %v, want %v %q %v", got, dim, blockerIDs(blockers), tc.want, tc.wantDim, tc.blockers)
			}
		})
	}
}

func TestAmountStatus(t *testing.T) {
	mass, vol := product.DimensionMass, product.DimensionVolume
	status, blockers := AmountStatus(mass, []Member{goalMember("a", mass, 0, false), goalMember("b", vol, 0, false), goalMember("c", "", 0, false)})
	if status != StatusSizeMissing || !reflect.DeepEqual(blockerIDs(blockers), []string{"c:size_missing", "b:units_mismatch"}) {
		t.Fatalf("got %s %v", status, blockerIDs(blockers))
	}
	status, blockers = AmountStatus(mass, []Member{goalMember("a", mass, 0, false), goalMember("b", vol, 0, false), goalMember("c", vol, 0, false)})
	if status != StatusUnitsMismatch || !reflect.DeepEqual(blockerIDs(blockers), []string{"b:units_mismatch", "c:units_mismatch"}) {
		t.Fatalf("saved-goal mismatch lists every member sized the other way: %s %v", status, blockerIDs(blockers))
	}
	if status, _ := AmountStatus(mass, []Member{goalMember("a", mass, 0, false), goalMember("b", "", 0, true)}); status != StatusOK {
		t.Fatalf("empty don't-restock member: %s", status)
	}
}

func TestBuyItems(t *testing.T) {
	members := []Member{goalMember("a", "", 1, false), goalMember("b", "", 0, true)}
	if buy, _ := BuyCount(BuyTarget{Items: 2}, members, "a", UsageRate{}); buy != 1 {
		t.Fatalf("buy = %d", buy)
	}
	members[1].OnHand = 2
	if buy, _ := BuyCount(BuyTarget{Items: 2}, members, "a", UsageRate{}); buy != 0 {
		t.Fatalf("a don't-restock member's stock counts: buy = %d", buy)
	}
}
