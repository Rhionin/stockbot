package scan

import (
	"encoding/json"
	"math"
	"math/rand/v2"
	"os"
	"testing"
	"time"
)

func TestParseStockedAt(t *testing.T) {
	got, err := parseStockedAt("2026-10-17 03:13:52.933086471 +0000 UTC m=-86399.996836517")
	if err != nil {
		t.Fatal(err)
	}
	if got.IsZero() || got.UTC().Hour() != 3 {
		t.Fatalf("parsed = %v", got)
	}
}

func TestRankLinkCandidatesSuffixStillQualifies(t *testing.T) {
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

	if len(got) != 2 {
		t.Fatalf("suggestions = %#v, want the two suffix hits", got)
	}
	if got[0].ProductID != "prod-pears" || got[0].OnHand != 3 {
		t.Fatalf("first = %#v, want Sliced Pears (higher name score)", got[0])
	}
	if got[1].ProductID != "prod-crackers" {
		t.Fatalf("second = %#v, want the suffix with no shared words", got[1])
	}
}

func TestSourCreamDoesNotMatchCondensedCreamSoup(t *testing.T) {
	two := rankLinkCandidates("111111111111", "Sour Cream", "Dairy", "", "", "prod-sour", []linkCandidate{
		{ItemID: "item-soup", ProductID: "prod-soup", Name: "Condensed Cream Soup", Category: "Dairy", OnHand: 1},
	})
	if len(two) != 0 {
		t.Fatalf("two-product suggestions = %#v, want none", two)
	}

	cands := []linkCandidate{
		{ItemID: "item-soup", ProductID: "prod-soup", Name: "Condensed Cream Soup", OnHand: 1},
	}
	for i := 0; i < 8; i++ {
		cands = append(cands, linkCandidate{
			ItemID:    "item-cream",
			ProductID: "prod-cream",
			Name:      "Cream Item",
			OnHand:    0,
		})
		cands[len(cands)-1].ItemID = cands[len(cands)-1].ItemID + string(rune('a'+i))
		cands[len(cands)-1].ProductID = cands[len(cands)-1].ProductID + string(rune('a'+i))
		cands[len(cands)-1].Name = "Whipped Cream Side " + string(rune('a'+i))
	}
	common := rankLinkCandidates("111111111111", "Sour Cream", "Dairy", "", "", "prod-sour", cands)
	if len(common) != 0 {
		t.Fatalf("cream-heavy suggestions = %#v, want none", common)
	}
}

func TestKnownBrandStrippedFromStockedName(t *testing.T) {
	stocked := linkCandidate{ItemID: "item-pear", ProductID: "prod-pear", Name: "Daisy Pears", OnHand: 1}
	without := rankLinkCandidates("222", "Pears", "", "", "", "prod-scan", []linkCandidate{stocked})
	if len(without) != 0 {
		t.Fatalf("unstripped Daisy Pears = %#v, want none", without)
	}
	stocked.Brand = "Daisy"
	got := rankLinkCandidates("222", "Pears", "", "", "", "prod-scan", []linkCandidate{stocked})
	if len(got) != 1 || got[0].ProductID != "prod-pear" {
		t.Fatalf("recorded Daisy suggestions = %#v, want Daisy Pears", got)
	}
}

func TestTillamookCheddarClearsTheThreshold(t *testing.T) {
	scanned := "Tillamook Sharp Cheddar"
	stocked := linkCandidate{ItemID: "item-cheese", ProductID: "prod-cheese", Name: "Sharp Cheddar Cheese", OnHand: 1}
	got := rankLinkCandidates("223", scanned, "", "", "", "prod-scan", []linkCandidate{stocked})
	if len(got) != 1 || got[0].ProductID != "prod-cheese" {
		t.Fatalf("suggestions = %#v, want Sharp Cheddar Cheese", got)
	}
	own := rankLinkCandidates("223", scanned, "", "", "Tillamook, Darigold", "prod-scan", []linkCandidate{stocked})
	if len(own) != 1 || own[0].ProductID != "prod-cheese" {
		t.Fatalf("own brand suggestions = %#v, want Sharp Cheddar Cheese", own)
	}
}

func TestOwnBrandWinsOverALongerKnownPhrase(t *testing.T) {
	stocked := linkCandidate{ItemID: "item-pear", ProductID: "prod-pear", Name: "Pears", Brand: "Valley Reserve", OnHand: 1}
	longer := rankLinkCandidates("333", "Valley Reserve Pears", "Fruit", "", "Valley Reserve", "prod-scan", []linkCandidate{stocked})
	if len(longer) != 1 {
		t.Fatalf("longer own brand suggestions = %#v, want one", longer)
	}
	shorter := rankLinkCandidates("333", "Valley Reserve Pears", "Fruit", "", "Valley", "prod-scan", []linkCandidate{stocked})
	if len(shorter) != 0 {
		t.Fatalf("shorter own brand suggestions = %#v, want none", shorter)
	}
}

func TestLongestBrandPhraseWins(t *testing.T) {
	got := rankLinkCandidates("334", "Pears", "", "", "", "prod-scan", []linkCandidate{
		{ItemID: "item-valley", ProductID: "prod-valley", Name: "Snack Cakes", Brand: "Valley", OnHand: 0},
		{ItemID: "item-reserve", ProductID: "prod-reserve", Name: "Trail Nuts", Brand: "Valley Reserve", OnHand: 0},
		{ItemID: "item-pears", ProductID: "prod-pears", Name: "Valley Reserve Pears", OnHand: 1},
		{ItemID: "item-other", ProductID: "prod-other", Name: "Pear Candy", OnHand: 1},
	})
	if len(got) != 1 || got[0].ProductID != "prod-pears" {
		t.Fatalf("suggestions = %#v, want Valley Reserve Pears only", got)
	}
}

func TestLinkSuggestCapAndRecency(t *testing.T) {
	const scanned = "123456789"
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var cands []linkCandidate
	for i := 1; i <= 9; i++ {
		// i=1 is oldest and has the longest suffix. Recency, not suffix length, decides.
		suffixLen := 10 - i
		cands = append(cands, linkCandidate{
			ItemID:    "item",
			ProductID: "prod",
			Name:      "Filler Snack",
			OnHand:    1,
			StockedAt: base.Add(time.Duration(i) * time.Hour),
			Barcodes:  []string{scanned[len(scanned)-suffixLen:]},
		})
		cands[i-1].ItemID = "item-" + string(rune('0'+i))
		cands[i-1].ProductID = "prod-" + string(rune('0'+i))
		cands[i-1].Name = "Filler Snack " + string(rune('0'+i))
	}
	got := rankLinkCandidates(scanned, "Nothing Alike", "", "", "", "prod-scan", cands)
	if len(got) != linkSuggestLimit {
		t.Fatalf("len = %d, want %d; suggestions = %#v", len(got), linkSuggestLimit, got)
	}
	if got[0].ProductID != "prod-9" {
		t.Fatalf("first = %#v, want the newest stock, not the longest suffix", got[0])
	}
	for _, row := range got {
		if row.ProductID == "prod-1" {
			t.Fatalf("oldest row kept: %#v", got)
		}
	}
	foundSecondOldest := false
	for _, row := range got {
		if row.ProductID == "prod-2" {
			foundSecondOldest = true
		}
	}
	if !foundSecondOldest {
		t.Fatalf("second-oldest missing: %#v", got)
	}
}

func TestLinkTieBreakOrder(t *testing.T) {
	older := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	newer := older.Add(48 * time.Hour)

	supported := rankLinkCandidates("900", "Pea Soup", "Soup", "", "", "scan", []linkCandidate{
		{ItemID: "new", ProductID: "prod-new", Name: "Soup Pea", OnHand: 1, StockedAt: newer},
		{ItemID: "old", ProductID: "prod-old", Name: "Pea Soup", Category: "Soup", OnHand: 1, StockedAt: older},
	})
	if len(supported) != 2 || supported[0].ProductID != "prod-old" {
		t.Fatalf("support tie-break = %#v, want the shared category first", supported)
	}

	recent := rankLinkCandidates("901", "Pea Soup", "", "", "", "scan", []linkCandidate{
		{ItemID: "old", ProductID: "prod-old", Name: "Pea Soup", OnHand: 1, StockedAt: older},
		{ItemID: "new", ProductID: "prod-new", Name: "Soup, Pea", OnHand: 1, StockedAt: newer},
	})
	if len(recent) != 2 || recent[0].ProductID != "prod-new" {
		t.Fatalf("recency tie-break = %#v, want the newer stock first", recent)
	}

	fillers := []linkCandidate{
		{ItemID: "exact", ProductID: "prod-exact", Name: "Mint", OnHand: 1, StockedAt: older},
		{ItemID: "close", ProductID: "prod-close", Name: "Mint Za", Category: "Herb", OnHand: 1, StockedAt: newer},
	}
	for i := 0; i < 8; i++ {
		fillers = append(fillers, linkCandidate{
			ItemID: "za", ProductID: "za", Name: "Za Side", OnHand: 0,
		})
		fillers[len(fillers)-1].ItemID = "za-" + string(rune('a'+i))
		fillers[len(fillers)-1].ProductID = "za-" + string(rune('a'+i))
		fillers[len(fillers)-1].Name = "Za Side " + string(rune('a'+i))
	}
	// List the lower score first so a missing score key keeps that order.
	swapped := append([]linkCandidate{fillers[1], fillers[0]}, fillers[2:]...)
	higher := rankLinkCandidates("902", "Mint", "Herb", "", "", "scan", swapped)
	if len(higher) < 2 || higher[0].ProductID != "prod-exact" {
		t.Fatalf("score order = %#v, want Mint ahead of the newer supported Mint Za", higher)
	}
}

func TestEmptyCleanedNameMakesNoSuggestion(t *testing.T) {
	got := rankLinkCandidates("12", "16 oz", "", "", "", "scan", []linkCandidate{
		{ItemID: "ale", ProductID: "prod-ale", Name: "Ginger Ale 12 oz", OnHand: 1},
		{ItemID: "oz", ProductID: "prod-oz", Name: "12 oz", OnHand: 1},
		{ItemID: "kale", ProductID: "prod-kale", Name: "Kale", OnHand: 1},
	})
	if len(got) != 0 {
		t.Fatalf("empty scanned name suggestions = %#v, want none", got)
	}
	same := rankLinkCandidates("16", "16 oz", "", "", "", "scan", []linkCandidate{
		{ItemID: "oz", ProductID: "prod-oz", Name: "16 oz", OnHand: 1},
		{ItemID: "kale", ProductID: "prod-kale", Name: "Kale", OnHand: 1},
	})
	if len(same) != 0 {
		t.Fatalf("identical empty names suggestions = %#v, want none", same)
	}
}

func TestIdenticalNamesMatchInAOneProductHousehold(t *testing.T) {
	got := rankLinkCandidates("444", "Kale", "", "", "", "scan", []linkCandidate{
		{ItemID: "kale", ProductID: "prod-kale", Name: "Kale", OnHand: 1},
	})
	if len(got) != 1 || got[0].Name != "Kale" {
		t.Fatalf("suggestions = %#v, want Kale", got)
	}
}

func TestBuildWeightsSmoothingAndUnseenWords(t *testing.T) {
	one := BuildWeights([]string{"Kale"})
	if math.IsNaN(one["kale"]) || math.IsInf(one["kale"], 0) {
		t.Fatalf("N=1 weight = %v", one["kale"])
	}
	if math.Abs(one["kale"]-math.Log(2)) > 1e-12 {
		t.Fatalf("N=1 weight = %v, want ln(2)", one["kale"])
	}
	if math.Abs(one[""]-math.Log(2)) > 1e-12 {
		t.Fatalf("unseen weight = %v, want ln(2)", one[""])
	}

	names := []string{"Green Apple", "Green Pear", "Green Kale"}
	weights := BuildWeights(names)
	if weights["green"] <= 0 {
		t.Fatalf("word in every name weight = %v, want a small positive", weights["green"])
	}
	if weights["green"] >= weights["apple"] {
		t.Fatalf("everywhere word %v should weigh less than a rare word %v", weights["green"], weights["apple"])
	}

	repeated := BuildWeights([]string{"Soup Soup", "Tomato Soup"})
	if _, ok := repeated["soup"]; !ok {
		t.Fatal("soup missing")
	}
	// Both names contain soup once each after the set fold, so df stays 2.
	want := math.Log(3.0 / 2.0)
	if math.Abs(repeated["soup"]-want) > 1e-12 {
		t.Fatalf("soup weight = %v, want %v", repeated["soup"], want)
	}

	score := Score(Tokens("Kale Chips"), Tokens("Kale"), one)
	if math.IsNaN(score) || math.IsInf(score, 0) {
		t.Fatalf("unseen word score = %v", score)
	}
	if math.Abs(score-0.5) > 1e-12 {
		t.Fatalf("unseen word score = %v, want 0.5", score)
	}
}

func TestScoreThresholdBoundaries(t *testing.T) {
	under := Score([]string{"shared"}, []string{"shared", "extra"}, map[string]float64{"shared": 0.6489, "extra": 0.3511})
	if under >= LinkScoreThreshold-LinkScoreEpsilon {
		t.Fatalf("score %v should sit under the threshold", under)
	}
	if scoreMatches(under) {
		t.Fatal("0.6489 must not match")
	}

	over := Score([]string{"shared"}, []string{"shared", "extra"}, map[string]float64{"shared": 0.6501, "extra": 0.3499})
	if !scoreMatches(over) {
		t.Fatalf("score %v should match", over)
	}

	exact := Score([]string{"shared"}, []string{"shared", "extra"}, map[string]float64{"shared": LinkScoreThreshold, "extra": 1 - LinkScoreThreshold})
	if !scoreMatches(exact) {
		t.Fatalf("exact threshold score %v should match", exact)
	}

	// Equal log-weights make a 2/5 overlap print as 0.3999…, which a bare >= 0.4 rejects.
	ratioWeights := BuildWeights([]string{"Lime Mint Sage Dill Basil", "Lime Mint", "Sage Dill Basil"})
	ratio := Score(Tokens("Lime Mint"), Tokens("Lime Mint Sage Dill Basil"), ratioWeights)
	if ratio >= 0.4 {
		t.Fatalf("2/5 float = %v, want it to print under 0.4", ratio)
	}
	if !scoreWithin(ratio, 0.4) {
		t.Fatalf("2/5 float = %v should pass a 0.4 gate that uses the epsilon", ratio)
	}
	if !scoreWithin(LinkScoreThreshold-1e-12, LinkScoreThreshold) {
		t.Fatal("a hair under the threshold should pass")
	}
	if scoreWithin(LinkScoreThreshold-2e-9, LinkScoreThreshold) {
		t.Fatal("two billionths under the threshold should fail")
	}
}

func TestScoreNeverNaNOrInf(t *testing.T) {
	fixtures := []struct {
		a, b    []string
		weights map[string]float64
	}{
		{nil, nil, nil},
		{[]string{}, []string{"kale"}, nil},
		{[]string{"kale"}, []string{"kale"}, nil},
		{[]string{"kale"}, []string{"kale", "kale"}, map[string]float64{"kale": 0}},
		{[]string{"a"}, []string{"b"}, map[string]float64{"": math.NaN()}},
		{[]string{"a"}, []string{"b"}, map[string]float64{"a": math.Inf(1), "b": math.Inf(-1)}},
		{[]string{"a"}, []string{"a", "b"}, map[string]float64{"a": math.NaN(), "b": math.NaN()}},
	}
	for i, tt := range fixtures {
		got := Score(tt.a, tt.b, tt.weights)
		if math.IsNaN(got) || math.IsInf(got, 0) {
			t.Fatalf("fixture %d score = %v", i, got)
		}
	}
}

func TestCommonWordDeltaBeatsRareWordDelta(t *testing.T) {
	names := []string{"Sour Pickle", "Dragon Fruit"}
	for i := 0; i < 20; i++ {
		names = append(names, "Cream Item "+string(rune('a'+i)))
	}
	weights := BuildWeights(names)
	common := Score(Tokens("Sour Cream"), Tokens("Sour"), weights)
	rare := Score(Tokens("Sour Dragon"), Tokens("Sour"), weights)
	if math.IsNaN(common) || math.IsNaN(rare) || common >= 1 || rare >= 1 || common <= rare {
		t.Fatalf("common %v, rare %v", common, rare)
	}
}

func TestTokensFoldCompoundsPluralsAndDiacritics(t *testing.T) {
	nonFat := Tokens("Non-Fat Milk")
	nonfat := Tokens("Nonfat Milk")
	nonSpace := Tokens("Non Fat Milk")
	if !sameSet(wordSet(nonFat), wordSet(nonfat)) || !sameSet(wordSet(nonFat), wordSet(nonSpace)) {
		t.Fatalf("non-fat forms = %#v %#v %#v", nonFat, nonfat, nonSpace)
	}
	if !sameSet(wordSet(Tokens("Crème")), wordSet(Tokens("Creme"))) {
		t.Fatalf("Crème = %#v, Creme = %#v", Tokens("Crème"), Tokens("Creme"))
	}
	if !sameSet(wordSet(Tokens("Annie's")), wordSet(Tokens("Annies"))) {
		t.Fatalf("Annie's = %#v, Annies = %#v", Tokens("Annie's"), Tokens("Annies"))
	}
	low := Tokens("Low-Fat Yogurt")
	closed := Tokens("Lowfat Yogurt")
	if sameSet(wordSet(low), wordSet(closed)) {
		t.Fatalf("low-fat should stay two words, got %#v and %#v", low, closed)
	}

	stems := []struct{ in, want string }{
		{"tomatoes", "tomato"},
		{"tomato", "tomato"},
		{"potatoes", "potato"},
		{"potato", "potato"},
		{"eggs", "egg"},
		{"egg", "egg"},
		{"berries", "berry"},
		{"cookies", "cookie"},
		{"brownies", "brownie"},
		{"ponies", "pony"},
		{"peaches", "peach"},
		{"dishes", "dish"},
		{"boxes", "box"},
		{"glass", "glass"},
		{"swiss", "swiss"},
		{"slices", "slice"},
		{"pears", "pear"},
		{"leaves", "leaf"},
		{"leaf", "leaf"},
	}
	for _, tt := range stems {
		if got := stemFood(tt.in); got != tt.want {
			t.Errorf("stemFood(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestPluralNamesMatch(t *testing.T) {
	tests := []struct{ scanned, stocked string }{
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

func TestScoreSumsAreDeterministic(t *testing.T) {
	names := []string{"Basil Fig Plum", "Plum Fig Basil Kiwi", "Kiwi Lime", "Lime Date"}
	scanned := Tokens("Basil Fig Plum")
	candidate := Tokens("Plum Fig Basil Kiwi")
	baseWeights := BuildWeights(names)
	base := Score(scanned, candidate, baseWeights)
	if math.IsNaN(base) || math.IsInf(base, 0) {
		t.Fatalf("base score = %v", base)
	}
	for i := 0; i < 1000; i++ {
		shuffledNames := append([]string(nil), names...)
		rand.Shuffle(len(shuffledNames), func(a, b int) {
			shuffledNames[a], shuffledNames[b] = shuffledNames[b], shuffledNames[a]
		})
		left := append([]string(nil), scanned...)
		right := append([]string(nil), candidate...)
		rand.Shuffle(len(left), func(a, b int) { left[a], left[b] = left[b], left[a] })
		rand.Shuffle(len(right), func(a, b int) { right[a], right[b] = right[b], right[a] })
		// A repeated word must not change the set score.
		if i%2 == 0 {
			left = append(left, left...)
		}
		got := Score(left, right, BuildWeights(shuffledNames))
		if got != base || math.IsNaN(got) || math.IsInf(got, 0) {
			t.Fatalf("run %d score = %v, want %v", i, got, base)
		}
	}
}

type synthPair struct {
	A          string          `json:"a"`
	B          string          `json:"b"`
	Score      float64         `json:"score"`
	Kind       string          `json:"kind"`
	MatchesAtT map[string]bool `json:"matches_at_T"`
}

func TestSyntheticPantryAtThreshold(t *testing.T) {
	pantry := loadPantry(t)
	names := make([]string, len(pantry))
	for i, p := range pantry {
		names[i] = p.Name
	}
	weights := BuildWeights(names)

	var doc struct {
		Groups struct {
			MustNot map[string][]synthPair `json:"must_not_match"`
			Likely  map[string][]synthPair `json:"likely_duplicates"`
			Edge    map[string][]synthPair `json:"edge"`
			Must    []synthPair            `json:"must_match"`
			Border  []synthPair            `json:"borderline"`
		} `json:"groups"`
		ByThreshold map[string]struct {
			MustNot   string `json:"must_not_correctly_rejected"`
			MustMatch string `json:"must_match"`
			Border    string `json:"borderline_matched"`
			Likely    string `json:"likely_matched"`
			Total     int    `json:"total_pairs"`
		} `json:"by_threshold"`
	}
	loadJSON(t, "testdata/expected.json", &doc)

	check := func(group string, rows []synthPair) (rejected, matched int) {
		t.Helper()
		for _, row := range rows {
			got := Score(Tokens(row.A), Tokens(row.B), weights)
			if math.IsNaN(got) || math.IsInf(got, 0) {
				t.Fatalf("%s %q vs %q score = %v", group, row.A, row.B, got)
			}
			if math.Abs(got-row.Score) > 1e-4 {
				t.Fatalf("%s %q vs %q score = %v, fixture %v", group, row.A, row.B, got, row.Score)
			}
			want := row.MatchesAtT["0.65"]
			if scoreMatches(got) != want {
				t.Fatalf("%s %q vs %q match = %v, want %v (score %v)", group, row.A, row.B, scoreMatches(got), want, got)
			}
			if want {
				matched++
			} else {
				rejected++
			}
		}
		return rejected, matched
	}

	var mustNot, mustMatch, borderMatch, likelyMatch int
	var mustNotN, mustN, borderN, likelyN int
	for _, rows := range doc.Groups.MustNot {
		rej, ok := check("must_not", rows)
		mustNot += rej
		mustNotN += rej + ok
	}
	_, mustMatch = check("must_match", doc.Groups.Must)
	mustN = len(doc.Groups.Must)
	_, borderMatch = check("borderline", doc.Groups.Border)
	borderN = len(doc.Groups.Border)
	for _, rows := range doc.Groups.Likely {
		_, ok := check("likely", rows)
		likelyMatch += ok
		likelyN += len(rows)
	}
	for _, rows := range doc.Groups.Edge {
		check("edge", rows)
	}

	if mustNot != mustNotN || mustNotN != 9 {
		t.Fatalf("must_not rejected %d/%d, want 9/9", mustNot, mustNotN)
	}
	if mustMatch != mustN || mustN != 4 {
		t.Fatalf("must_match %d/%d, want 4/4", mustMatch, mustN)
	}
	if borderMatch != 0 || borderN != 2 {
		t.Fatalf("borderline matched %d/%d, want 0/2", borderMatch, borderN)
	}
	if likelyMatch != 1 || likelyN != 6 {
		t.Fatalf("likely matched %d/%d, want 1/6", likelyMatch, likelyN)
	}

	total := 0
	for i := 0; i < len(names); i++ {
		for j := i + 1; j < len(names); j++ {
			if scoreMatches(Score(Tokens(names[i]), Tokens(names[j]), weights)) {
				total++
			}
		}
	}
	if total != 5 {
		t.Fatalf("matching pairs = %d, want 5", total)
	}
	got := doc.ByThreshold["0.65"]
	if got.MustNot != "9/9" || got.MustMatch != "4/4" || got.Border != "0/2" || got.Likely != "1/6" || got.Total != 5 {
		t.Fatalf("fixture 0.65 column = %+v", got)
	}
}

func TestSpecEdgeCases(t *testing.T) {
	var cases []struct {
		ID             string   `json:"id"`
		Household      []string `json:"household"`
		Scanned        string   `json:"scanned"`
		Candidate      string   `json:"candidate"`
		ExpectedScore  *float64 `json:"expected_score"`
		Expect         string   `json:"expect"`
		ReferenceScore float64  `json:"reference_score"`
	}
	loadJSON(t, "testdata/spec-cases.json", &cases)
	if len(cases) < 10 {
		t.Fatalf("spec cases = %d", len(cases))
	}
	for _, c := range cases {
		t.Run(c.ID, func(t *testing.T) {
			weights := BuildWeights(c.Household)
			got := Score(Tokens(c.Scanned), Tokens(c.Candidate), weights)
			if math.IsNaN(got) || math.IsInf(got, 0) {
				t.Fatalf("score = %v", got)
			}
			want := c.ReferenceScore
			if c.ExpectedScore != nil {
				want = *c.ExpectedScore
			}
			if c.ID != "E8-order" && math.Abs(got-want) > 1e-9 {
				t.Fatalf("score = %v, want %v (%s)", got, want, c.Expect)
			}
			if c.ID == "E7a-float-0.4" {
				if got >= 0.4 || !scoreWithin(got, 0.4) {
					t.Fatalf("2/5-style score = %v", got)
				}
			}
			if c.Expect == "no suggestion" || c.ID == "E3c-empty-raw-identical" {
				if got != 0 {
					t.Fatalf("empty score = %v", got)
				}
				var cands []linkCandidate
				for i, name := range c.Household {
					cands = append(cands, linkCandidate{
						ItemID: "item", ProductID: "prod-" + string(rune('a'+i)), Name: name, OnHand: 1,
					})
				}
				ranked := rankLinkCandidates("100", c.Scanned, "", "", "", "scan", cands)
				for _, row := range ranked {
					if row.Name == c.Candidate {
						t.Fatalf("suggested %q", row.Name)
					}
				}
			}
		})
	}
}

type pantryRow struct {
	Name string `json:"name"`
}

func loadPantry(t *testing.T) []pantryRow {
	t.Helper()
	var rows []pantryRow
	loadJSON(t, "testdata/pantry.json", &rows)
	if len(rows) != 95 {
		t.Fatalf("pantry len = %d", len(rows))
	}
	return rows
}

func loadJSON(t *testing.T, path string, dest any) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if err := json.Unmarshal(raw, dest); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
}
