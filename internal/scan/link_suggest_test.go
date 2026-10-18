package scan

import (
	"encoding/json"
	"fmt"
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

func TestBarcodeEndingMatchesEitherSide(t *testing.T) {
	const longCode = "011110828385" // last 8 are 10828385; last 7 are 0828385
	const shortCode = "10828385"
	const seven = "0828385"
	const name = "Nothing Alike"

	longScan := rankLinkCandidates(longCode, name, "", "", "", "scan", []linkCandidate{
		{ItemID: "name", ProductID: "prod-name", Name: name, OnHand: 1},
		{ItemID: "seven", ProductID: "prod-seven", Name: "Also Wrong", OnHand: 1, Barcodes: []string{seven}},
		{ItemID: "short", ProductID: "prod-short", Name: "Wrong Food", OnHand: 1, Barcodes: []string{shortCode}},
	})
	if len(longScan) != 2 || longScan[0].ProductID != "prod-short" || longScan[1].ProductID != "prod-name" {
		t.Fatalf("long scan = %#v, want the 8-digit stored code ahead of the name", longScan)
	}

	shortScan := rankLinkCandidates(shortCode, name, "", "", "", "scan", []linkCandidate{
		{ItemID: "name", ProductID: "prod-name", Name: name, OnHand: 1},
		{ItemID: "long", ProductID: "prod-long", Name: "Wrong Food", OnHand: 1, Barcodes: []string{longCode}},
		{ItemID: "seven-scan", ProductID: "prod-seven-scan", Name: "Seven Stored", OnHand: 1, Barcodes: []string{"0000000" + seven}},
	})
	if len(shortScan) != 2 || shortScan[0].ProductID != "prod-long" || shortScan[1].ProductID != "prod-name" {
		t.Fatalf("short scan = %#v, want the longer stored code ahead of the name", shortScan)
	}

	// A 7-digit scan is not an ending match against a longer stored code.
	sevenScan := rankLinkCandidates(seven, name, "", "", "", "scan", []linkCandidate{
		{ItemID: "long", ProductID: "prod-long", Name: "Wrong Food", OnHand: 1, Barcodes: []string{longCode}},
	})
	if len(sevenScan) != 0 {
		t.Fatalf("7-digit scan = %#v, want none", sevenScan)
	}

	shared := rankLinkCandidates("111108283855", "Other Name", "", "", "", "scan", []linkCandidate{
		{ItemID: "full", ProductID: "prod-full", Name: "Wrong Food", OnHand: 1, Barcodes: []string{"222208283855"}},
	})
	if len(shared) != 0 {
		t.Fatalf("shared last 8 of two full codes = %#v, want none", shared)
	}

	const exact = "123456789012"
	full := rankLinkCandidates(exact, "Other Name", "", "", "", "scan", []linkCandidate{
		{ItemID: "name", ProductID: "prod-name", Name: "Other Name", OnHand: 1},
		{ItemID: "exact", ProductID: "prod-exact", Name: "Wrong Food", OnHand: 1, Barcodes: []string{exact}},
	})
	if len(full) != 2 || full[0].ProductID != "prod-exact" || full[1].ProductID != "prod-name" {
		t.Fatalf("full-length code = %#v, want the exact code ahead of the name", full)
	}

	nines := rankLinkCandidates("0000000099999999", "Other Name", "", "", "", "scan", []linkCandidate{
		{ItemID: "nine", ProductID: "prod-nine", Name: "Wrong Food", OnHand: 1, Barcodes: []string{"99999999"}},
	})
	if len(nines) != 1 || nines[0].ProductID != "prod-nine" {
		t.Fatalf("digit 9 code = %#v, want the 8 nines", nines)
	}

	nonDigit := rankLinkCandidates("01111082838X", "Other Name", "", "", "", "scan", []linkCandidate{
		{ItemID: "bad", ProductID: "prod-bad", Name: "Wrong Food", OnHand: 1, Barcodes: []string{"1082838X", "not-a-code", "", "   ", "１０８２８３８５"}},
	})
	if len(nonDigit) != 0 {
		t.Fatalf("non-digit stored codes = %#v, want none", nonDigit)
	}

	// Only the shorter code has to be digits. The longer stored code may
	// carry letters in front of an 8-digit ending.
	prefixed := rankLinkCandidates(shortCode, "Other Name", "", "", "", "scan", []linkCandidate{
		{ItemID: "name", ProductID: "prod-name", Name: "Other Name", OnHand: 1},
		{ItemID: "prefixed", ProductID: "prod-prefixed", Name: "Wrong Food", OnHand: 1, Barcodes: []string{"AB011110828385"}},
	})
	if len(prefixed) != 2 || prefixed[0].ProductID != "prod-prefixed" || prefixed[1].ProductID != "prod-name" {
		t.Fatalf("letter-prefixed stored code = %#v, want it ahead of the name", prefixed)
	}
}

func TestBarcodeRowsOrderByEndingLength(t *testing.T) {
	const scanned = "011110828385" // endings: 12 full, 9 = 110828385, 8 = 10828385
	// Shortest first. A sort that drops the ending length keeps that order.
	got := rankLinkCandidates(scanned, "Nothing Alike", "", "", "", "scan", []linkCandidate{
		{ItemID: "eight", ProductID: "prod-eight", Name: "Wrong Eight", OnHand: 1, Barcodes: []string{"10828385"}},
		{ItemID: "nine", ProductID: "prod-nine", Name: "Wrong Nine", OnHand: 1, Barcodes: []string{"110828385"}},
		{ItemID: "full", ProductID: "prod-full", Name: "Wrong Full", OnHand: 1, Barcodes: []string{scanned}},
		{ItemID: "name", ProductID: "prod-name", Name: "Nothing Alike", OnHand: 1},
	})
	if len(got) != 4 {
		t.Fatalf("suggestions = %#v, want three endings and the name", got)
	}
	want := []string{"prod-full", "prod-nine", "prod-eight", "prod-name"}
	for i, id := range want {
		if got[i].ProductID != id {
			t.Fatalf("order = %s %s %s %s, want %v", got[0].ProductID, got[1].ProductID, got[2].ProductID, got[3].ProductID, want)
		}
	}
}

func TestBarcodeSuffixNeedsEightDigitsAndRanksFirst(t *testing.T) {
	const scanned = "011110828385" // last 8: 10828385, last 7: 0828385
	got := rankLinkCandidates(scanned, "Nothing Alike", "", "", "", "prod-off", []linkCandidate{
		{ItemID: "item-name", ProductID: "prod-name", Name: "Nothing Alike", OnHand: 2, Barcodes: []string{"999000111222"}},
		{ItemID: "item-seven", ProductID: "prod-seven", Name: "Also Wrong", OnHand: 3, Barcodes: []string{"0828385"}},
		{ItemID: "item-short", ProductID: "prod-short", Name: "Short Code", OnHand: 1, Barcodes: []string{"828385"}},
		{ItemID: "item-eight", ProductID: "prod-eight", Name: "Wrong Food", OnHand: 4, Barcodes: []string{"10828385"}},
		{ItemID: "item-empty", ProductID: "prod-empty", Name: "Wrong Food Empty", OnHand: 0, Barcodes: []string{"10828385"}},
		{ItemID: "item-self", ProductID: "prod-off", Name: "Nothing Alike", OnHand: 1, Barcodes: []string{scanned}},
	})
	if len(got) != 2 {
		t.Fatalf("suggestions = %#v, want the 8-digit suffix and the name match", got)
	}
	if got[0].ProductID != "prod-eight" || got[0].OnHand != 4 {
		t.Fatalf("first = %#v, want the 8-digit suffix ahead of the name", got[0])
	}
	if got[1].ProductID != "prod-name" {
		t.Fatalf("second = %#v, want the perfect name match", got[1])
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
	stocked := linkCandidate{ItemID: "item-cheese", ProductID: "prod-cheese", Name: "Sharp Cheddar", OnHand: 1}
	got := rankLinkCandidates("223", scanned, "", "", "", "prod-scan", []linkCandidate{stocked})
	if len(got) != 0 {
		t.Fatalf("without a recorded brand = %#v, want none", got)
	}
	stocked.Brand = "Tillamook"
	own := rankLinkCandidates("223", scanned, "", "", "Tillamook, Darigold", "prod-scan", []linkCandidate{stocked})
	if len(own) != 1 || own[0].Name != "Sharp Cheddar" {
		t.Fatalf("own brand suggestions = %#v, want Sharp Cheddar", own)
	}
}

func TestBrandStrippedFromBothNames(t *testing.T) {
	stocked := linkCandidate{ItemID: "item-valley", ProductID: "prod-valley", Name: "Valley Pears", OnHand: 1}
	plain := rankLinkCandidates("224", "Daisy Pears", "", "", "", "scan", []linkCandidate{stocked})
	if len(plain) != 0 {
		t.Fatalf("without brands = %#v, want none", plain)
	}
	stocked.Brand = "Valley"
	got := rankLinkCandidates("224", "Daisy Pears", "", "", "Daisy", "scan", []linkCandidate{stocked})
	if len(got) != 1 || got[0].ProductID != "prod-valley" {
		t.Fatalf("both brands stripped = %#v, want Valley Pears", got)
	}
}

func TestScannedBrandStrippedFromUnbrandedStock(t *testing.T) {
	got := rankLinkCandidates("227", "Pears", "", "", "Daisy", "scan", []linkCandidate{
		{ItemID: "item-daisy", ProductID: "prod-daisy", Name: "Daisy Pears", OnHand: 1},
	})
	if len(got) != 1 || got[0].Name != "Daisy Pears" {
		t.Fatalf("scanned brand suggestions = %#v, want Daisy Pears", got)
	}
}

func TestBrandAloneDoesNotQualify(t *testing.T) {
	got := rankLinkCandidates("225", "Daisy Pears", "Snacks", "grp-snack", "Daisy", "scan", []linkCandidate{
		{ItemID: "item-chips", ProductID: "prod-chips", Name: "Daisy Chips", Brand: "Daisy", Category: "Snacks", GroupID: "grp-snack", OnHand: 1},
	})
	if len(got) != 0 {
		t.Fatalf("brand-only suggestions = %#v, want none", got)
	}
}

func TestSharedGroupDoesNotLiftABelowThresholdPair(t *testing.T) {
	got := rankLinkCandidates("226", "Turkey Stock", "Soup", "grp-stock", "", "scan", []linkCandidate{
		{ItemID: "item-veg", ProductID: "prod-veg", Name: "Vegetable Stock", Category: "Soup", GroupID: "grp-stock", OnHand: 1},
	})
	if len(got) != 0 {
		t.Fatalf("shared group suggestions = %#v, want none", got)
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
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var cands []linkCandidate
	for i := 1; i <= 9; i++ {
		// Equal name scores. A 7-digit code is not a suffix qualifier.
		cands = append(cands, linkCandidate{
			ItemID:    "item-" + string(rune('0'+i)),
			ProductID: "prod-" + string(rune('0'+i)),
			Name:      "Pea Soup",
			OnHand:    1,
			StockedAt: base.Add(time.Duration(i) * time.Hour),
			Barcodes:  []string{"1234567"},
		})
	}
	got := rankLinkCandidates("000000000000", "Soup Pea", "", "", "", "prod-scan", cands)
	if len(got) != linkSuggestLimit {
		t.Fatalf("len = %d, want %d; suggestions = %#v", len(got), linkSuggestLimit, got)
	}
	if got[0].ProductID != "prod-9" {
		t.Fatalf("first = %#v, want the newest stock", got[0])
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

	// Later name is listed first. A dropped name key would keep that order.
	when := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	named := rankLinkCandidates("903", "Pea Soup", "", "", "", "scan", []linkCandidate{
		{ItemID: "later", ProductID: "prod-later", Name: "Soup Pea", OnHand: 1, StockedAt: when},
		{ItemID: "earlier", ProductID: "prod-earlier", Name: "Pea Soup", OnHand: 1, StockedAt: when},
	})
	if len(named) != 2 || named[0].Name != "Pea Soup" {
		t.Fatalf("name tie-break = %#v, want Pea Soup first", named)
	}

	// Shared brand only. The supported row is older and listed second.
	branded := rankLinkCandidates("904", "Pea Soup", "", "", "Daisy", "scan", []linkCandidate{
		{ItemID: "new", ProductID: "prod-new", Name: "Soup Pea", OnHand: 1, StockedAt: newer},
		{ItemID: "old", ProductID: "prod-old", Name: "Pea Soup", Brand: "Daisy", OnHand: 1, StockedAt: older},
	})
	if len(branded) != 2 || branded[0].ProductID != "prod-old" {
		t.Fatalf("brand tie-break = %#v, want the shared brand first", branded)
	}

	// Shared group only. Categories and brands differ, so only the group key orders them.
	grouped := rankLinkCandidates("905", "Pea Soup", "Canned", "grp-soup", "", "scan", []linkCandidate{
		{ItemID: "new", ProductID: "prod-new", Name: "Soup Pea", Category: "Soup", OnHand: 1, StockedAt: newer},
		{ItemID: "old", ProductID: "prod-old", Name: "Pea Soup", Category: "Fruit", GroupID: "grp-soup", OnHand: 1, StockedAt: older},
	})
	if len(grouped) != 2 || grouped[0].ProductID != "prod-old" {
		t.Fatalf("group tie-break = %#v, want the shared group first", grouped)
	}

	// The scanned name has no brand field. Daisy is known from another product,
	// so the stripped phrase is what makes the older row the supported tie.
	applied := rankLinkCandidates("906", "Daisy Pea Soup", "", "", "", "scan", []linkCandidate{
		{ItemID: "known", ProductID: "prod-known", Name: "Snack Cakes", Brand: "Daisy", OnHand: 0},
		{ItemID: "new", ProductID: "prod-new", Name: "Soup Pea", OnHand: 1, StockedAt: newer},
		{ItemID: "old", ProductID: "prod-old", Name: "Pea Soup", Brand: "Daisy", OnHand: 1, StockedAt: older},
	})
	if len(applied) != 2 || applied[0].ProductID != "prod-old" {
		t.Fatalf("stripped-brand tie-break = %#v, want the Daisy row first", applied)
	}
}

func TestIdenticalSurfaceNamesMatchWhenTokensAreEmpty(t *testing.T) {
	if Score(Tokens("16 oz"), Tokens("16 oz"), nil) != 0 {
		t.Fatal("empty token sets still score 0")
	}
	if Score(Tokens("16 oz"), Tokens("12 oz"), nil) != 0 {
		t.Fatal("different empty token sets still score 0")
	}
	different := rankLinkCandidates("12", "16 oz", "", "", "", "scan", []linkCandidate{
		{ItemID: "ale", ProductID: "prod-ale", Name: "Ginger Ale 12 oz", OnHand: 1},
		{ItemID: "oz", ProductID: "prod-oz", Name: "12 oz", OnHand: 1},
		{ItemID: "kale", ProductID: "prod-kale", Name: "Kale", OnHand: 1},
	})
	if len(different) != 0 {
		t.Fatalf("different empty names = %#v, want none", different)
	}
	same := rankLinkCandidates("16", "16 oz", "", "", "", "scan", []linkCandidate{
		{ItemID: "glued", ProductID: "prod-glued", Name: "16oz", OnHand: 1},
		{ItemID: "space", ProductID: "prod-space", Name: "16  OZ", OnHand: 1},
		{ItemID: "pad", ProductID: "prod-pad", Name: "  16 oz  ", OnHand: 1},
		{ItemID: "kale", ProductID: "prod-kale", Name: "Kale", OnHand: 1},
	})
	if len(same) != 3 || same[0].ProductID != "prod-pad" || same[1].ProductID != "prod-space" || same[2].ProductID != "prod-glued" {
		t.Fatalf("identical surface names = %#v, want the spaced and glued size labels", same)
	}

	upper := rankLinkCandidates("16", "16 oz", "", "", "", "scan", []linkCandidate{
		{ItemID: "upper", ProductID: "prod-upper", Name: "16OZ", OnHand: 1},
		{ItemID: "kale", ProductID: "prod-kale", Name: "Kale", OnHand: 1},
	})
	if len(upper) != 1 || upper[0].ProductID != "prod-upper" {
		t.Fatalf("16OZ = %#v, want it to match 16 oz", upper)
	}

	floz := rankLinkCandidates("16", "16 floz", "", "", "", "scan", []linkCandidate{
		{ItemID: "glued", ProductID: "prod-floz", Name: "16floz", OnHand: 1},
		{ItemID: "oz", ProductID: "prod-oz", Name: "16 oz", OnHand: 1},
	})
	if len(floz) != 1 || floz[0].ProductID != "prod-floz" {
		t.Fatalf("16floz = %#v, want 16 floz and not 16 oz", floz)
	}

	ounces := rankLinkCandidates("16", "16 ounces", "", "", "", "scan", []linkCandidate{
		{ItemID: "glued", ProductID: "prod-ounces", Name: "16ounces", OnHand: 1},
		{ItemID: "oz", ProductID: "prod-oz", Name: "16 oz", OnHand: 1},
	})
	if len(ounces) != 0 {
		t.Fatalf("16ounces = %#v, want no match for 16 ounces or 16 oz", ounces)
	}
}

func TestIdenticalRowScoresExactlyOne(t *testing.T) {
	// Zero weights would make the overlap ratio 0. An identical row still scores 1.
	got := Score([]string{"soup", "kale"}, []string{"kale", "soup"}, map[string]float64{"kale": 0, "soup": 0})
	if got != 1 {
		t.Fatalf("identical row score = %v, want 1", got)
	}
}

func TestIdenticalNamesMatchInAOneProductHousehold(t *testing.T) {
	weights := BuildWeights([]string{"Kale"})
	if Score(Tokens("Kale"), Tokens("Kale"), weights) != 1 {
		t.Fatalf("identical row score = %v, want 1", Score(Tokens("Kale"), Tokens("Kale"), weights))
	}
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
	under := Score([]string{"shared"}, []string{"shared", "extra"}, map[string]float64{"shared": 0.6999, "extra": 1 - 0.6999})
	if !(under < LinkScoreThreshold && under > 0.699 && !scoreMatches(under) && scoreWithin(under, 0.699)) {
		t.Fatalf("0.6999 score = %v, want a miss that a 0.699 gate would accept", under)
	}

	exact := Score([]string{"shared"}, []string{"shared", "extra"}, map[string]float64{"shared": 0.70, "extra": 1 - 0.70})
	if !scoreMatches(exact) {
		t.Fatalf("0.70 score %v should match", exact)
	}

	over := Score([]string{"shared"}, []string{"shared", "extra"}, map[string]float64{"shared": 0.7001, "extra": 1 - 0.7001})
	if !scoreMatches(over) {
		t.Fatalf("0.7001 score %v should match", over)
	}

	hair := LinkScoreThreshold - 5e-10
	close := Score([]string{"shared"}, []string{"shared", "extra"}, map[string]float64{"shared": hair, "extra": 1 - hair})
	if !scoreMatches(close) {
		t.Fatalf("T-5e-10 score %v should match", close)
	}
	if !scoreWithin(LinkScoreThreshold-5e-10, LinkScoreThreshold) {
		t.Fatal("T-5e-10 should pass the epsilon")
	}
	if scoreWithin(LinkScoreThreshold-2e-9, LinkScoreThreshold) {
		t.Fatal("two billionths under the threshold should fail")
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
}

func TestLongerNameIsTheDenominator(t *testing.T) {
	// shared/longer = 0.75/2 = 0.375, under 0.70.
	// shared/shorter = 0.75/0.75 = 1, which would match.
	score := Score([]string{"shared"}, []string{"shared", "extra"}, map[string]float64{"shared": 0.75, "extra": 1.25})
	if scoreMatches(score) || math.Abs(score-0.375) > 1e-12 {
		t.Fatalf("longer denominator score = %v, want 0.375 and no match", score)
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
	if !sameSet(wordSet(Tokens("Cafe\u0301")), wordSet(Tokens("Cafe"))) {
		t.Fatalf("Cafe combining mark = %#v, Cafe = %#v", Tokens("Cafe\u0301"), Tokens("Cafe"))
	}
	if !sameSet(wordSet(Tokens("foo'bar")), wordSet([]string{"foobar"})) {
		t.Fatalf("foo'bar = %#v", Tokens("foo'bar"))
	}
	nonTail := Tokens("Milk Non")
	if !sameSet(wordSet(nonTail), wordSet([]string{"milk", "non"})) {
		t.Fatalf("trailing non = %#v", nonTail)
	}

	stems := []struct{ in, want string }{
		{"tomatoes", "tomato"},
		{"pies", "pies"},
		{"gas", "gas"},
		{"as", "as"},
		{"cats", "cat"},
		{"bass", "bass"},
		{"loaves", "loaf"},
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
			if math.Abs(got-row.Score) > 1e-9 {
				t.Fatalf("%s %q vs %q score = %v, fixture %v", group, row.A, row.B, got, row.Score)
			}
			want := row.MatchesAtT["0.70"]
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

	column := doc.ByThreshold["0.70"]
	if column.MustNot != "9/9" || column.MustMatch != "4/4" || column.Border != "0/2" || column.Likely != "0/6" || column.Total != 4 {
		t.Fatalf("0.70 column = %+v", column)
	}
	if mustNot != mustNotN || column.MustNot != fmtCount(mustNot, mustNotN) {
		t.Fatalf("must_not rejected %d/%d, fixture %s", mustNot, mustNotN, column.MustNot)
	}
	if mustMatch != mustN || column.MustMatch != fmtCount(mustMatch, mustN) {
		t.Fatalf("must_match %d/%d, fixture %s", mustMatch, mustN, column.MustMatch)
	}
	if column.Border != fmtCount(borderMatch, borderN) {
		t.Fatalf("borderline matched %d/%d, fixture %s", borderMatch, borderN, column.Border)
	}
	if column.Likely != fmtCount(likelyMatch, likelyN) {
		t.Fatalf("likely matched %d/%d, fixture %s", likelyMatch, likelyN, column.Likely)
	}

	total := 0
	for i := 0; i < len(names); i++ {
		for j := i + 1; j < len(names); j++ {
			if scoreMatches(Score(Tokens(names[i]), Tokens(names[j]), weights)) {
				total++
			}
		}
	}
	if total != column.Total {
		t.Fatalf("matching pairs = %d, fixture %d", total, column.Total)
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
			if c.ID == "E3c-empty-raw-identical" {
				if got != 0 {
					t.Fatalf("empty token score = %v", got)
				}
				ranked := rankSpec(c.Household, c.Scanned)
				found := false
				for _, row := range ranked {
					if row.Name == c.Candidate {
						found = true
					}
				}
				if !found {
					t.Fatalf("identical surface name %q was not suggested", c.Candidate)
				}
			}
			if c.Expect == "no suggestion" {
				if got != 0 {
					t.Fatalf("empty score = %v", got)
				}
				ranked := rankSpec(c.Household, c.Scanned)
				for _, row := range ranked {
					if row.Name == c.Candidate {
						t.Fatalf("suggested %q", row.Name)
					}
				}
			}
		})
	}
}

func fmtCount(matched, total int) string {
	return fmt.Sprintf("%d/%d", matched, total)
}

func rankSpec(household []string, scanned string) []LinkSuggestion {
	var cands []linkCandidate
	for i, name := range household {
		cands = append(cands, linkCandidate{
			ItemID: "item", ProductID: "prod-" + string(rune('a'+i)), Name: name, OnHand: 1,
		})
	}
	return rankLinkCandidates("100", scanned, "", "", "", "scan", cands)
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
