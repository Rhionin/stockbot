package scan

import "testing"

func TestRankLinkCandidatesSuffixBeforeName(t *testing.T) {
	const scanned = "011110828385"
	got := rankLinkCandidates(scanned, "Lite Pear Slices In Pear Juice", "", "", "prod-off", []linkCandidate{
		{ItemID: "item-tuna", ProductID: "prod-tuna", Name: "Canned Tuna", OnHand: 1, Barcodes: []string{"491"}},
		{ItemID: "item-beans", ProductID: "prod-beans", Name: "Black Beans", OnHand: 2, Barcodes: []string{"1552"}},
		{ItemID: "item-slices", ProductID: "prod-slices", Name: "Pear Slices", OnHand: 2, Barcodes: []string{"999000111222"}},
		{ItemID: "item-crackers", ProductID: "prod-crackers", Name: "Unrelated Crackers", OnHand: 4, Barcodes: []string{"8385"}},
		{ItemID: "item-pears", ProductID: "prod-pears", Name: "Sliced Pears", OnHand: 3, Barcodes: []string{"828385"}},
		{ItemID: "item-middle", ProductID: "prod-middle", Name: "Middle Match", OnHand: 1, Barcodes: []string{"110828"}},
		{ItemID: "item-empty", ProductID: "prod-empty", Name: "Sliced Pears Empty", OnHand: 0, Barcodes: []string{"828385"}},
		{ItemID: "item-self", ProductID: "prod-off", Name: "Lite Pear Slices In Pear Juice", OnHand: 1, Barcodes: []string{scanned}},
	})

	if len(got) != 3 {
		t.Fatalf("suggestions = %#v, want 3", got)
	}
	if got[0].ProductID != "prod-pears" || got[0].OnHand != 3 {
		t.Fatalf("first = %#v, want Sliced Pears with 3 on hand", got[0])
	}
	if got[1].ProductID != "prod-crackers" {
		t.Fatalf("second = %#v, want the shorter suffix ahead of the name match", got[1])
	}
	if got[2].ProductID != "prod-slices" {
		t.Fatalf("third = %#v, want the name match after every suffix", got[2])
	}
}
