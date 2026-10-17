package restock

import (
	"sort"
	"strings"
)

// StarterCategories are the household categories seeded from Open Food Facts.
// A household can rename or delete any of them.
var StarterCategories = []string{
	"Produce",
	"Breads & bakery",
	"Spices & seasonings",
	"Canned & jarred",
	"Beverages",
	"Snacks & sweets",
	"Dairy & eggs",
	"Frozen",
	"Pasta, rice & grains",
	"Breakfast cereals",
	"Condiments & sauces",
	"Oils & vinegars",
	"Baking",
	"Meat & seafood",
	"Legumes & nuts",
	"Spreads & sweeteners",
}

// TaxonomyVersion changes when the Open Food Facts map changes, so a household
// is classified again.
const TaxonomyVersion = "231-v1"

// Match is one suggested or taxonomy category for an item.
type Match struct {
	Category string
	Reason   string
	Source   string // off, suggested
	Checked  bool
	// FreshProduce is a produce match that is safe to treat as fresh.
	Fresh bool
}

// Classify reads Open Food Facts tags, a barcode, and a name.
// Taxonomy matches (the versioned map, walked up parents) come back with
// Source off. Food groups, names, and loose-produce codes come back as
// suggestions. Nothing here writes a membership.
func Classify(name string, tags, foodGroups []string, barcode string) (matches []Match, notSuggested []string) {
	tags = cleanTags(tags)
	foodGroups = cleanTags(foodGroups)
	closure := map[string]bool{}
	for _, tag := range tags {
		for _, node := range ancestors(tag) {
			closure[node] = true
		}
	}
	blocked := produceBlocked(closure, name)
	seen := map[string]bool{}
	add := func(match Match) {
		if match.Category == "" || seen[match.Category] {
			return
		}
		seen[match.Category] = true
		matches = append(matches, match)
	}

	for node := range closure {
		category := nodeCategory[node]
		if category == "" {
			continue
		}
		if category == "Produce" && blocked {
			continue
		}
		add(Match{
			Category: category,
			Reason:   "Open Food Facts: " + tagLabel(node),
			Source:   "off",
			Checked:  true,
			Fresh:    category == "Produce",
		})
	}

	if !seen["Produce"] && (closure["en:fruits"] || closure["en:vegetables"] || foodGroupHas(foodGroups, "en:fruits-and-vegetables")) {
		if blocked {
			notSuggested = append(notSuggested, name)
		} else {
			label := "Fruits"
			if closure["en:vegetables"] && !closure["en:fruits"] {
				label = "Vegetables"
			}
			add(Match{
				Category: "Produce",
				Reason:   "Open Food Facts: " + label + ", but not fresh. Check it if they are.",
				Source:   "suggested",
				Checked:  false,
			})
		}
	}

	for _, group := range foodGroups {
		category := foodGroupCategory[group]
		if category == "" || category == "Produce" {
			continue
		}
		add(Match{
			Category: category,
			Reason:   "Open Food Facts group: " + tagLabel(group),
			Source:   "suggested",
			Checked:  true,
		})
	}

	if plu, ok := LooseProduce(barcode); ok && !seen["Produce"] && !blocked {
		add(Match{
			Category: "Produce",
			Reason:   "Loose-produce code (PLU " + plu + ")",
			Source:   "suggested",
			Checked:  true,
			Fresh:    true,
		})
	}

	for category, words := range nameKeywords {
		if seen[category] {
			continue
		}
		if category == "Produce" && blocked {
			continue
		}
		if word, ok := nameHit(name, words); ok {
			reason := "Name looks like " + word + "."
			if len(tags) == 0 {
				reason += " No Open Food Facts tags."
			}
			add(Match{
				Category: category,
				Reason:   reason,
				Source:   "suggested",
				Checked:  false,
			})
		}
	}
	sort.Slice(matches, func(i, j int) bool {
		return categoryOrder(matches[i].Category) < categoryOrder(matches[j].Category)
	})
	return matches, notSuggested
}

func categoryOrder(name string) int {
	for i, category := range StarterCategories {
		if category == name {
			return i
		}
	}
	return len(StarterCategories)
}

// LooseProduce reports a PLU or a variable-weight barcode.
func LooseProduce(barcode string) (string, bool) {
	barcode = strings.TrimSpace(barcode)
	if barcode == "" || !digits(barcode) {
		return "", false
	}
	switch len(barcode) {
	case 4, 5:
		return barcode, true
	case 12:
		if barcode[0] == '2' {
			return barcode, true
		}
	case 13:
		if barcode[0] == '2' && barcode[1] >= '0' && barcode[1] <= '9' {
			return barcode, true
		}
	}
	return "", false
}

func ancestors(tag string) []string {
	seen := map[string]bool{}
	var out []string
	var walk func(string)
	walk = func(node string) {
		if node == "" || seen[node] {
			return
		}
		seen[node] = true
		out = append(out, node)
		for _, parent := range offParents[node] {
			walk(parent)
		}
	}
	walk(tag)
	return out
}

func produceBlocked(closure map[string]bool, name string) bool {
	for node := range closure {
		if produceBlockers[node] {
			return true
		}
	}
	lower := strings.ToLower(name)
	for _, word := range []string{"canned", "frozen", "dried", "crushed", "puree", "sauce", "paste"} {
		if strings.Contains(lower, word) {
			return true
		}
	}
	return false
}

func foodGroupHas(groups []string, want string) bool {
	for _, group := range groups {
		if group == want {
			return true
		}
	}
	return false
}

func nameHit(name string, words []string) (string, bool) {
	lower := strings.ToLower(name)
	for _, word := range words {
		if strings.Contains(lower, word) {
			return word, true
		}
	}
	return "", false
}

func cleanTags(tags []string) []string {
	out := make([]string, 0, len(tags))
	seen := map[string]bool{}
	for _, tag := range tags {
		tag = strings.ToLower(strings.TrimSpace(tag))
		if tag == "" || tag == "en:undefined" || seen[tag] {
			continue
		}
		seen[tag] = true
		out = append(out, tag)
	}
	return out
}

func tagLabel(tag string) string {
	tag = strings.TrimPrefix(tag, "en:")
	tag = strings.ReplaceAll(tag, "-", " ")
	if tag == "" {
		return tag
	}
	return strings.ToUpper(tag[:1]) + tag[1:]
}

func digits(value string) bool {
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// nodeCategory maps an Open Food Facts node to a starter category.
// A product matches when its tag or any parent is in this map.
var nodeCategory = map[string]string{
	"en:fresh-fruits":             "Produce",
	"en:fresh-vegetables":         "Produce",
	"en:fresh-plant-based-foods":  "Produce",
	"en:breads":                   "Breads & bakery",
	"en:viennoiseries":            "Breads & bakery",
	"en:cakes":                    "Breads & bakery",
	"en:tortillas":                "Breads & bakery",
	"en:spices":                   "Spices & seasonings",
	"en:herbs":                    "Spices & seasonings",
	"en:herbs-and-spices":         "Spices & seasonings",
	"en:seasonings":               "Spices & seasonings",
	"en:salts":                    "Spices & seasonings",
	"en:canned-foods":             "Canned & jarred",
	"en:canned-plant-based-foods": "Canned & jarred",
	"en:canned-soups":             "Canned & jarred",
	"en:canned-meals":             "Canned & jarred",
	"en:beverages":                "Beverages",
	"en:beverages-and-beverages-preparations": "Beverages",
	"en:dehydrated-beverages":                 "Beverages",
	"en:snacks":                               "Snacks & sweets",
	"en:sweet-snacks":                         "Snacks & sweets",
	"en:salty-snacks":                         "Snacks & sweets",
	"en:confectioneries":                      "Snacks & sweets",
	"en:dairies":                              "Dairy & eggs",
	"en:milks":                                "Dairy & eggs",
	"en:cheeses":                              "Dairy & eggs",
	"en:eggs":                                 "Dairy & eggs",
	"en:frozen-foods":                         "Frozen",
	"en:pastas":                               "Pasta, rice & grains",
	"en:rices":                                "Pasta, rice & grains",
	"en:breakfast-cereals":                    "Breakfast cereals",
	"en:sauces":                               "Condiments & sauces",
	"en:condiments":                           "Condiments & sauces",
	"en:vegetable-oils":                       "Oils & vinegars",
	"en:oils":                                 "Oils & vinegars",
	"en:vinegars":                             "Oils & vinegars",
	"en:baking-decorations":                   "Baking",
	"en:flours":                               "Baking",
	"en:meats":                                "Meat & seafood",
	"en:seafood":                              "Meat & seafood",
	"en:fishes":                               "Meat & seafood",
	"en:legumes":                              "Legumes & nuts",
	"en:nuts":                                 "Legumes & nuts",
	"en:nut-butters":                          "Legumes & nuts",
	"en:spreads":                              "Spreads & sweeteners",
	"en:sweeteners":                           "Spreads & sweeteners",
	"en:honeys":                               "Spreads & sweeteners",
}

// offParents is the slice of the Open Food Facts taxonomy this map walks.
// Specific tags roll up to the nodes in nodeCategory.
var offParents = map[string][]string{
	"en:fresh-fruits":             {"en:fruits", "en:fresh-plant-based-foods"},
	"en:fresh-vegetables":         {"en:vegetables", "en:fresh-plant-based-foods"},
	"en:raspberries":              {"en:fresh-fruits"},
	"en:spaghetti":                {"en:pastas"},
	"en:lasagna":                  {"en:pastas"},
	"en:farfalle":                 {"en:pastas"},
	"en:pastas":                   {"en:cereals-and-their-products"},
	"en:rices":                    {"en:cereals-and-their-products"},
	"en:breads":                   {"en:cereals-and-their-products"},
	"en:tortillas":                {"en:breads"},
	"en:canned-tomatoes":          {"en:canned-plant-based-foods", "en:tomatoes"},
	"en:canned-chickpeas":         {"en:canned-plant-based-foods", "en:chickpeas", "en:legumes"},
	"en:canned-soups":             {"en:canned-foods", "en:soups"},
	"en:canned-meals":             {"en:canned-foods"},
	"en:canned-fruits":            {"en:canned-foods", "en:fruits"},
	"en:canned-plant-based-foods": {"en:canned-foods"},
	"en:canola-oils":              {"en:vegetable-oils"},
	"en:vegetable-oils":           {"en:oils"},
	"en:parmesan":                 {"en:cheeses"},
	"en:cheeses":                  {"en:dairies"},
	"en:milks":                    {"en:dairies"},
	"en:peanut-butters":           {"en:nut-butters", "en:spreads"},
	"en:nut-butters":              {"en:legumes", "en:spreads"},
	"en:black-peppers":            {"en:spices"},
	"en:paprika":                  {"en:spices"},
	"en:cumins":                   {"en:spices"},
	"en:salts":                    {"en:seasonings"},
	"en:spices":                   {"en:herbs-and-spices"},
	"en:herbs":                    {"en:herbs-and-spices"},
	"en:frozen-foods":             {"en:plant-based-foods"},
	"en:dried-fruits":             {"en:dried-products", "en:fruits"},
	"en:tomato-sauces":            {"en:sauces"},
}

var produceBlockers = map[string]bool{
	"en:canned-foods":             true,
	"en:canned-plant-based-foods": true,
	"en:canned-soups":             true,
	"en:canned-meals":             true,
	"en:canned-fruits":            true,
	"en:frozen-foods":             true,
	"en:dried-products":           true,
	"en:dried-fruits":             true,
	"en:dried-vegetables":         true,
	"en:sauces":                   true,
	"en:tomato-sauces":            true,
}

var foodGroupCategory = map[string]string{
	"en:sugary-snacks":         "Snacks & sweets",
	"en:salty-snacks":          "Snacks & sweets",
	"en:beverages":             "Beverages",
	"en:cereals-and-potatoes":  "Pasta, rice & grains",
	"en:fats-and-sauces":       "Condiments & sauces",
	"en:milk-and-yogurt":       "Dairy & eggs",
	"en:cheese":                "Dairy & eggs",
	"en:fruits-and-vegetables": "Produce",
}

var nameKeywords = map[string][]string{
	"Spices & seasonings": {"cumin", "paprika", "seasoning", "vanilla", "cinnamon", "peppercorn"},
	"Produce":             {"cilantro", "banana", "spinach", "lettuce", "avocado"},
	"Baking":              {"baking powder", "baking soda", "flour"},
	"Beverages":           {"oat milk", "coffee", "tea"},
}
