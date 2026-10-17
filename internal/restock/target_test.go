package restock

import (
	"strings"
	"testing"
)

func months(n float64) Target { return Target{Kind: KindMonths, Value: n} }
func amount(n float64) Target { return Target{Kind: KindAmount, Value: n} }

func TestResolveMostSpecificWins(t *testing.T) {
	household := months(3)
	snacks := CategoryTarget{ID: "snacks", Name: "Snacks & sweets", Target: months(2)}
	item := amount(6)
	group := months(1)

	got := Resolve(Input{Household: household})
	if got.Level != LevelHousehold || got.Target != household {
		t.Fatalf("household: %+v", got)
	}
	if _, _, ok := UseAction(got); ok {
		t.Fatal("household default has no broader target")
	}

	got = Resolve(Input{Categories: []CategoryTarget{snacks}, Household: household})
	if got.Level != LevelCategory || got.Name != "Snacks & sweets" || got.Target != months(2) {
		t.Fatalf("category: %+v", got)
	}
	if got.Next == nil || got.Next.Level != LevelHousehold {
		t.Fatalf("next: %+v", got.Next)
	}

	got = Resolve(Input{Group: &group, GroupName: "Salad kits", Categories: []CategoryTarget{snacks}, Household: household})
	if got.Level != LevelGroup || got.Name != "Salad kits" {
		t.Fatalf("group: %+v", got)
	}

	got = Resolve(Input{Item: &item, Group: &group, GroupName: "Salad kits", Categories: []CategoryTarget{snacks}, Household: household})
	if got.Level != LevelItem || got.Target != item {
		t.Fatalf("item: %+v", got)
	}
	label, hint, ok := UseAction(got)
	if !ok || label != "Use group target" || !strings.Contains(hint, "Salad kits") {
		t.Fatalf("use: %q %q %v", label, hint, ok)
	}
}

func TestHigherCategoryWinsAndZeroLoses(t *testing.T) {
	produce := CategoryTarget{ID: "produce", Name: "Produce", Target: months(0)}
	spices := CategoryTarget{ID: "spices", Name: "Spices & seasonings", Target: amount(1)}
	got := Resolve(Input{
		Categories: []CategoryTarget{produce, spices},
		Household:  months(3),
	})
	if got.Level != LevelCategory || got.Name != "Spices & seasonings" || got.Target != amount(1) {
		t.Fatalf("higher: %+v", got)
	}
	if len(got.Shadowed) != 1 || got.Shadowed[0].Name != "Produce" {
		t.Fatalf("shadowed: %+v", got.Shadowed)
	}
	reason := WontChangeReason(got, "")
	if reason != "From Spices & seasonings, the higher of its two categories" {
		t.Fatalf("reason: %q", reason)
	}

	// A non-zero category beats a household of 0.
	got = Resolve(Input{Categories: []CategoryTarget{spices}, Household: months(0)})
	if got.Target.Value == 0 {
		t.Fatalf("zero household hid a category: %+v", got)
	}
}

func TestMonthsBeatAmountWhenBothPositive(t *testing.T) {
	got := Higher(amount(6), months(2))
	if got != months(2) {
		t.Fatalf("months should beat a fixed amount, got %+v", got)
	}
	got = Higher(months(1), months(3))
	if got != months(3) {
		t.Fatalf("larger window: %+v", got)
	}
	got = Higher(amount(0), amount(1))
	if got != amount(1) {
		t.Fatalf("zero loses: %+v", got)
	}
}

func TestCopyAvoidsProgrammingTerms(t *testing.T) {
	item := amount(6)
	group := months(1)
	in := Input{
		Item:       &item,
		Group:      &group,
		GroupName:  "Salad kits",
		Categories: []CategoryTarget{{ID: "s", Name: "Snacks & sweets", Target: months(2)}},
		Household:  months(3),
	}
	res := Resolve(in)
	texts := []string{
		SourceLabel(res),
		ValuePhrase(res.Target, false),
		ValuePhrase(amount(1), true),
		ValuePhrase(months(0), false),
		QuietLine(res),
		QuietLine(Resolve(Input{Categories: []CategoryTarget{{Name: "Produce", Target: months(0)}}, Household: months(3)})),
		RowSource(res),
		WontChangeReason(res, "Salad kits"),
		DontRestockDetail(Resolve(Input{Categories: in.Categories, Household: in.Household})),
	}
	label, hint, _ := UseAction(res)
	texts = append(texts, label, hint)
	for _, cell := range Chain(in, res, "bags") {
		texts = append(texts, cell.Label, cell.Value)
	}
	banned := []string{"inherit", "override", "precedence"}
	for _, text := range texts {
		lower := strings.ToLower(text)
		for _, word := range banned {
			if strings.Contains(lower, word) {
				t.Errorf("%q contains %q", text, word)
			}
		}
	}
	if QuietLine(Resolve(Input{Household: months(3)})) != "" {
		t.Fatal("household months should stay quiet on a collapsed row")
	}
	quiet := QuietLine(res)
	if quiet != "Keep 6 · set on this item" {
		t.Fatalf("quiet: %q", quiet)
	}
}

func TestChainStrikesBroaderLevels(t *testing.T) {
	snacks := CategoryTarget{ID: "snacks", Name: "Snacks & sweets", Target: months(2)}
	in := Input{Categories: []CategoryTarget{snacks}, Household: months(3)}
	cells := Chain(in, Resolve(in), "bags")
	if !cells[2].Win || cells[2].Value != "2 mo" {
		t.Fatalf("category cell: %+v", cells[2])
	}
	if !cells[3].Struck || cells[3].Value != "3 mo" {
		t.Fatalf("default cell: %+v", cells[3])
	}
	if cells[0].Value != "—" || cells[0].Struck {
		t.Fatalf("empty item cell: %+v", cells[0])
	}
}

func TestPreviewStopsOnlyItemsThatWouldChange(t *testing.T) {
	produceID := "produce"
	household := months(3)
	lemons := amount(6)
	group := months(1)
	spices := amount(1)
	subjects := []Subject{
		{ItemID: "ba", Name: "Bananas", Input: Input{Household: household}},
		{ItemID: "le", Name: "Lemons", Input: Input{Item: &lemons, Household: household}},
		{ItemID: "sm", Name: "Spring mix kit", GroupName: "Salad kits", Input: Input{Group: &group, GroupName: "Salad kits", Household: household}},
		{ItemID: "ga", Name: "Garlic", Input: Input{Household: household, Categories: []CategoryTarget{
			{ID: "spices", Name: "Spices & seasonings", Target: spices},
		}}},
	}
	preview := BuildPreview("Produce", months(0), subjects, func(in Input) Input {
		return WithCategoryTarget(in, produceID, "Produce", months(0))
	})
	if preview.Title != "Stop restocking Produce?" || preview.Confirm != "Stop restocking 1" {
		t.Fatalf("preview chrome: %+v", preview)
	}
	if len(preview.Stop) != 1 || preview.Stop[0].Name != "Bananas" || preview.Stop[0].Before != "3 mo" || preview.Stop[0].After != "0" {
		t.Fatalf("stop: %+v", preview.Stop)
	}
	if len(preview.Keep) != 3 {
		t.Fatalf("keep: %+v", preview.Keep)
	}
	byName := map[string]PreviewLine{}
	for _, line := range preview.Keep {
		byName[line.Name] = line
	}
	if byName["Lemons"].Reason != "Set on this item" || byName["Lemons"].After != "Keep 6" {
		t.Fatalf("lemons: %+v", byName["Lemons"])
	}
	if byName["Spring mix kit"].Reason != "From Salad kits group" {
		t.Fatalf("spring: %+v", byName["Spring mix kit"])
	}
	if byName["Garlic"].Reason != "From Spices & seasonings, the higher of its two categories" || byName["Garlic"].After != "Keep 1" {
		t.Fatalf("garlic: %+v", byName["Garlic"])
	}
}

func TestClassifyTaxonomyProduceAndFallbacks(t *testing.T) {
	matches, excluded := Classify("Canola oil", []string{"en:canola-oils"}, nil, "")
	if len(matches) != 1 || matches[0].Category != "Oils & vinegars" || matches[0].Source != "off" || !matches[0].Checked {
		t.Fatalf("oil: %+v", matches)
	}

	matches, _ = Classify("Garbanzo beans", []string{"en:canned-chickpeas"}, nil, "")
	got := map[string]Match{}
	for _, match := range matches {
		got[match.Category] = match
	}
	if _, ok := got["Canned & jarred"]; !ok {
		t.Fatalf("canned missing: %+v", matches)
	}
	if _, ok := got["Legumes & nuts"]; !ok {
		t.Fatalf("legumes missing: %+v", matches)
	}

	matches, _ = Classify("Grapes", []string{"en:fruits"}, nil, "")
	if len(matches) != 1 || matches[0].Category != "Produce" || matches[0].Checked || matches[0].Source != "suggested" {
		t.Fatalf("grapes: %+v", matches)
	}

	matches, _ = Classify("Raspberries", []string{"en:raspberries"}, nil, "")
	if len(matches) != 1 || matches[0].Category != "Produce" || !matches[0].Fresh || matches[0].Source != "off" {
		t.Fatalf("raspberries: %+v", matches)
	}

	matches, excluded = Classify("Crushed tomatoes", []string{"en:vegetables"}, nil, "")
	for _, match := range matches {
		if match.Category == "Produce" {
			t.Fatalf("crushed tomatoes suggested as produce: %+v", matches)
		}
	}
	if len(excluded) != 1 {
		t.Fatalf("excluded: %v", excluded)
	}

	matches, _ = Classify("Cilantro", nil, nil, "4889")
	if len(matches) != 1 || !strings.Contains(matches[0].Reason, "PLU 4889") || !matches[0].Checked {
		t.Fatalf("plu: %+v", matches)
	}

	matches, _ = Classify("Vanilla extract", []string{"en:undefined"}, nil, "")
	if len(matches) != 1 || matches[0].Category != "Spices & seasonings" || matches[0].Checked {
		t.Fatalf("vanilla: %+v", matches)
	}

	if _, ok := LooseProduce("212345678905"); !ok {
		t.Fatal("UPC prefix 2 should count as loose produce")
	}
	if _, ok := LooseProduce("2012345678901"); !ok {
		t.Fatal("EAN-13 prefix 2 should count as loose produce")
	}
	if _, ok := LooseProduce("0012345678905"); ok {
		t.Fatal("a normal EAN is not loose produce")
	}
	if _, ok := LooseProduce("12ab"); ok {
		t.Fatal("a barcode with letters is not a PLU")
	}

	matches, _ = Classify("Carrots", []string{"en:vegetables"}, nil, "")
	if len(matches) != 1 || matches[0].Checked || !strings.Contains(matches[0].Reason, "Vegetables") {
		t.Fatalf("carrots: %+v", matches)
	}

	matches, excluded = Classify("Canned peaches", []string{"en:raspberries"}, nil, "")
	for _, match := range matches {
		if match.Category == "Produce" {
			t.Fatalf("canned fruit suggested as produce: %+v", matches)
		}
	}
	if len(excluded) != 1 {
		t.Fatalf("canned fruit excluded: %v", excluded)
	}

	matches, _ = Classify("Trail mix", nil, []string{"en:sugary-snacks", "en:fruits-and-vegetables"}, "")
	got = map[string]Match{}
	for _, match := range matches {
		got[match.Category] = match
	}
	if snack, ok := got["Snacks & sweets"]; !ok || !snack.Checked || !strings.Contains(snack.Reason, "Sugary snacks") {
		t.Fatalf("food group: %+v", matches)
	}
	if produce, ok := got["Produce"]; !ok || produce.Checked {
		t.Fatalf("food-group produce: %+v", got["Produce"])
	}

	if len(ancestors("")) != 0 || tagLabel("") != "" {
		t.Fatal("empty tags should stay empty")
	}
}

func TestPreviewOfAPositiveTarget(t *testing.T) {
	snacks := CategoryTarget{ID: "snacks", Name: "Snacks & sweets", Target: months(1)}
	subjects := []Subject{{
		ItemID: "tc", Name: "Tortilla chips",
		Input: Input{Household: months(3), Categories: []CategoryTarget{snacks}},
	}}
	preview := BuildPreview("Snacks & sweets", months(2), subjects, func(in Input) Input {
		return WithCategoryTarget(in, "snacks", "Snacks & sweets", months(2))
	})
	if preview.Title != "Set Snacks & sweets to 2 months?" || preview.Confirm != "Change 1" {
		t.Fatalf("preview: %+v", preview)
	}
	if len(preview.Stop) != 1 || preview.Stop[0].Before != "1 mo" || preview.Stop[0].After != "2 mo" {
		t.Fatalf("stop: %+v", preview.Stop)
	}
	if ValuePhrase(amount(4), true) != "Keep 4 each" {
		t.Fatal("category amounts are per item")
	}
	if Short(Target{Kind: KindAmount, Value: 1.5}, "") != "Keep 1.5" {
		t.Fatal("a fractional amount keeps its decimal")
	}
}

func TestCopyNamesEveryLevel(t *testing.T) {
	if got := Higher(months(4), months(2)); got != months(4) {
		t.Fatalf("larger months: %+v", got)
	}
	if got := Higher(months(2), amount(9)); got != months(2) {
		t.Fatalf("months beat amount: %+v", got)
	}

	group := months(1)
	groupRes := Resolve(Input{Group: &group, GroupName: "Salad kits", Household: months(3)})
	if RowSource(groupRes) != "1 month, from Salad kits group" {
		t.Fatalf("group row: %q", RowSource(groupRes))
	}
	one := Resolve(Input{Categories: []CategoryTarget{{Name: "Snacks & sweets", Target: months(2)}}, Household: months(3)})
	if WontChangeReason(one, "") != "From Snacks & sweets" {
		t.Fatalf("one category: %q", WontChangeReason(one, ""))
	}
	if WontChangeReason(Resolve(Input{Household: months(3)}), "") != "Household default" {
		t.Fatal("household reason")
	}
	several := Input{Categories: []CategoryTarget{
		{Name: "Produce", Target: months(1)},
		{Name: "Snacks & sweets", Target: months(2)},
		{Name: "Frozen", Target: months(0)},
	}, Household: months(3)}
	three := Resolve(several)
	if WontChangeReason(three, "") != "From Snacks & sweets, the higher of its categories" {
		t.Fatalf("three categories: %q", WontChangeReason(three, ""))
	}
	if cells := Chain(several, three, ""); cells[2].Value != "2 mo" {
		t.Fatalf("chain: %+v", cells[2])
	}
}
