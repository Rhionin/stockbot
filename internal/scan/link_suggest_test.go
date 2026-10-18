package scan

import "testing"

func TestRankLinkCandidatesSuffixBeforeName(t *testing.T) {
	const scanned = "011110828385"
	got := rankLinkCandidates(scanned, "Lite Pear Slices In Pear Juice", "", "", "", "prod-off", []linkCandidate{
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

// An empty household marks nothing generic. Sour Cream still must not match
// Condensed Cream Soup: "sour" is not in the soup. Matching here would mean a
// leading word was stripped as a brand with no recorded brand.
func TestSourCreamDoesNotMatchCondensedCreamSoupWhenNothingIsGeneric(t *testing.T) {
	got := rankLinkCandidates("111111111111", "Sour Cream", "Dairy", "", "", "prod-sour", []linkCandidate{
		{ItemID: "item-soup", ProductID: "prod-soup", Name: "Condensed Cream Soup", Category: "Dairy", OnHand: 1},
	})
	if len(got) != 0 {
		t.Fatalf("suggestions = %#v, want none", got)
	}
}

func TestKnownBrandMatchesTillamookCheddar(t *testing.T) {
	scanned := "Tillamook Sharp Cheddar"
	stocked := linkCandidate{ItemID: "item-cheese", ProductID: "prod-cheese", Name: "Sharp Cheddar Cheese", OnHand: 1}

	without := rankLinkCandidates("222", scanned, "", "", "", "prod-scan", []linkCandidate{stocked})
	if len(without) != 0 {
		t.Fatalf("unrecorded brand suggestions = %#v, want none", without)
	}

	stocked.Brand = "Tillamook"
	got := rankLinkCandidates("222", scanned, "", "", "", "prod-scan", []linkCandidate{stocked})
	if len(got) != 1 || got[0].ProductID != "prod-cheese" {
		t.Fatalf("known brand suggestions = %#v, want Sharp Cheddar Cheese", got)
	}

	own := rankLinkCandidates("222", scanned, "", "", "Tillamook, Darigold", "prod-scan", []linkCandidate{
		{ItemID: "item-cheese", ProductID: "prod-cheese", Name: "Sharp Cheddar Cheese", OnHand: 1},
	})
	if len(own) != 1 || own[0].ProductID != "prod-cheese" {
		t.Fatalf("own brand suggestions = %#v, want Sharp Cheddar Cheese", own)
	}
}

func TestOwnBrandWinsOverALongerKnownPhrase(t *testing.T) {
	scanned := "Valley Reserve Select Extra Pears"
	stocked := linkCandidate{ItemID: "item-pear", ProductID: "prod-pear", Name: "Pears", Category: "Fruit", Brand: "Valley Reserve", OnHand: 1}

	// The longer phrase leaves two extra words, which the shared category confirms.
	longer := rankLinkCandidates("333", scanned, "Fruit", "", "Valley Reserve", "prod-scan", []linkCandidate{stocked})
	if len(longer) != 1 {
		t.Fatalf("longer own brand suggestions = %#v, want one", longer)
	}
	// The product's own brand is only "Valley", so the longer recorded phrase
	// is not stripped and three extra words remain.
	shorter := rankLinkCandidates("333", scanned, "Fruit", "", "Valley", "prod-scan", []linkCandidate{stocked})
	if len(shorter) != 0 {
		t.Fatalf("shorter own brand suggestions = %#v, want none", shorter)
	}
}

func TestWordIsGenericBoundaries(t *testing.T) {
	tests := []struct {
		name  string
		count int
		n     int
		want  bool
	}{
		{name: "289 products, 9 names", count: 9, n: 289, want: true},
		{name: "289 products, 8 names", count: 8, n: 289, want: false},
		{name: "100 products, floor of 5", count: 5, n: 100, want: true},
		{name: "100 products, 4 is below the floor", count: 4, n: 100, want: false},
		{name: "166 products thresholds at 5", count: 5, n: 166, want: true},
		{name: "167 products, 5 is short", count: 5, n: 167, want: false},
		{name: "167 products thresholds at 6", count: 6, n: 167, want: true},
		{name: "empty pantry", count: 1, n: 0, want: false},
		{name: "four names cannot be generic", count: 4, n: 4, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := wordIsGeneric(tt.count, tt.n); got != tt.want {
				t.Fatalf("wordIsGeneric(%d, %d) = %v, want %v", tt.count, tt.n, got, tt.want)
			}
		})
	}
}

func TestHouseholdWordCountsARepeatedWordOnce(t *testing.T) {
	n, counts := householdWordCounts([]linkCandidate{
		{Name: "Soup Soup"},
		{Name: "Tomato Soup", OnHand: 0},
	})
	if n != 2 {
		t.Fatalf("n = %d, want 2 including the zero-stock row", n)
	}
	if counts["soup"] != 2 {
		t.Fatalf("soup count = %d, want 2", counts["soup"])
	}
}

func TestStemFood(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{in: "tomatoes", want: "tomato"},
		{in: "tomato", want: "tomato"},
		{in: "potatoes", want: "potato"},
		{in: "potato", want: "potato"},
		{in: "eggs", want: "egg"},
		{in: "egg", want: "egg"},
		{in: "berries", want: "berry"},
		{in: "peaches", want: "peach"},
		{in: "dishes", want: "dish"},
		{in: "boxes", want: "box"},
		{in: "glass", want: "glass"},
		{in: "swiss", want: "swiss"},
		{in: "slices", want: "slice"},
		{in: "pears", want: "pear"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := stemFood(tt.in); got != tt.want {
				t.Fatalf("stemFood(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestPluralNamesMatch(t *testing.T) {
	tests := []struct {
		scanned string
		stocked string
	}{
		{scanned: "Tomatoes", stocked: "Tomato"},
		{scanned: "Potatoes", stocked: "Potato"},
		{scanned: "Eggs", stocked: "Egg"},
	}
	for _, tt := range tests {
		t.Run(tt.scanned, func(t *testing.T) {
			got := rankLinkCandidates("444", tt.scanned, "", "", "", "prod-scan", []linkCandidate{
				{ItemID: "item", ProductID: "prod", Name: tt.stocked, OnHand: 1},
			})
			if len(got) != 1 || got[0].Name != tt.stocked {
				t.Fatalf("suggestions = %#v, want %s", got, tt.stocked)
			}
		})
	}
}
