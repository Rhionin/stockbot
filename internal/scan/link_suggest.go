package scan

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"unicode"
)

// linkSuggestLimit keeps the confirm row to a short list. Several real
// matches (two sizes of the same food) stay visible; the gate below is what
// drops a weak one.
const linkSuggestLimit = 8

// linkExtraRoom is how many extra distinctive words the longer name may add
// and still be the same food. One word ("lite") needs nothing else. Two words
// need the same brand, category, or group. Three or more are not offered.
const linkExtraRoom = 1

// LinkSuggestion is a household item a scanned barcode might already be.
type LinkSuggestion struct {
	ItemID    string `json:"itemId"`
	ProductID string `json:"productId"`
	Name      string `json:"name"`
	OnHand    int    `json:"onHand"`
}

// NoItemError is a stock-out of a product this household does not stock.
// Suggestions are ranked for the caller to choose. The scan is not linked
// or committed until that choice arrives.
type NoItemError struct {
	Suggestions []LinkSuggestion
}

func (e *NoItemError) Error() string {
	return "Not in your pantry"
}

type linkCandidate struct {
	ItemID    string
	ProductID string
	Name      string
	Category  string
	GroupID   string
	Brand     string
	OnHand    int
	Barcodes  []string
}

type linkQuery interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// suggestLinks ranks household items the scanned barcode might belong to.
// A barcode that is a suffix of the scan outranks a name resemblance.
func (r *Queue) suggestLinks(ctx context.Context, q linkQuery, scanEntry *ScanEntry) ([]LinkSuggestion, error) {
	scannedName := ""
	scannedCategory := ""
	scannedGroup := ""
	scannedBrand := ""
	excludeProductID := ""
	if scanEntry.ProductID != nil {
		excludeProductID = *scanEntry.ProductID
		err := q.QueryRowContext(ctx, `
			SELECT p.name, COALESCE(p.category, ''),
				COALESCE((SELECT m.group_id FROM product_group_members m WHERE m.product_id = p.id), ''),
				COALESCE(p.brand, '')
			FROM products p WHERE p.id = ?`, excludeProductID).Scan(&scannedName, &scannedCategory, &scannedGroup, &scannedBrand)
		if err != nil && err != sql.ErrNoRows {
			return nil, fmt.Errorf("scanned product name: %w", err)
		}
	}

	rows, err := q.QueryContext(ctx, `
		SELECT i.id, p.id, p.name, COALESCE(p.category, ''),
			COALESCE((SELECT m.group_id FROM product_group_members m WHERE m.product_id = p.id), ''),
			COALESCE(p.brand, ''),
			(SELECT COUNT(*) FROM item_instances ii
			 WHERE ii.item_id = i.id AND ii.removed_at IS NULL),
			COALESCE((
				SELECT group_concat(b.barcode, char(31))
				FROM barcodes b
				WHERE b.product_id = p.id
				  AND (
				        (b.source = 'global' AND b.user_id = '')
				     OR (b.source = 'user_override' AND b.user_id = i.user_id)
				  )
			), '')
		FROM items i
		JOIN products p ON p.id = i.product_id
		WHERE i.user_id = ?`,
		scanEntry.UserID,
	)
	if err != nil {
		return nil, fmt.Errorf("list link candidates: %w", err)
	}
	defer rows.Close()

	var candidates []linkCandidate
	for rows.Next() {
		var c linkCandidate
		var codes string
		if err := rows.Scan(&c.ItemID, &c.ProductID, &c.Name, &c.Category, &c.GroupID, &c.Brand, &c.OnHand, &codes); err != nil {
			return nil, fmt.Errorf("list link candidates: %w", err)
		}
		if codes != "" {
			c.Barcodes = strings.Split(codes, "\x1f")
		}
		candidates = append(candidates, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list link candidates: %w", err)
	}

	return rankLinkCandidates(scanEntry.Barcode, scannedName, scannedCategory, scannedGroup, scannedBrand, excludeProductID, candidates), nil
}

// rankLinkCandidates puts the longest suffix barcode first, then closer food names.
// A suffix of any length outranks a name-only resemblance, and a longer suffix
// outranks a shorter one, so a truncated code beats an accidental last digit.
func rankLinkCandidates(scannedBarcode, scannedName, scannedCategory, scannedGroup, scannedBrand, excludeProductID string, candidates []linkCandidate) []LinkSuggestion {
	scannedBarcode = strings.TrimSpace(scannedBarcode)
	generic := genericWordSet(candidates)
	knownBrands := recordedBrandPhrases(candidates)
	scanned := profileLink(scannedName, scannedCategory, scannedGroup, scannedBrand, generic, knownBrands)
	type scored struct {
		suggestion LinkSuggestion
		suffix     int
		nameScore  float64
	}
	var ranked []scored
	for _, c := range candidates {
		if c.OnHand <= 0 || c.ProductID == excludeProductID {
			continue
		}
		suffix := longestSuffix(scannedBarcode, c.Barcodes)
		other := profileLink(c.Name, c.Category, c.GroupID, c.Brand, generic, knownBrands)
		score := foodDice(scanned, other)
		if suffix == 0 && !plausibleLink(scanned, other) {
			continue
		}
		ranked = append(ranked, scored{
			suggestion: LinkSuggestion{
				ItemID:    c.ItemID,
				ProductID: c.ProductID,
				Name:      c.Name,
				OnHand:    c.OnHand,
			},
			suffix:    suffix,
			nameScore: score,
		})
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].suffix != ranked[j].suffix {
			return ranked[i].suffix > ranked[j].suffix
		}
		if ranked[i].nameScore != ranked[j].nameScore {
			return ranked[i].nameScore > ranked[j].nameScore
		}
		return ranked[i].suggestion.Name < ranked[j].suggestion.Name
	})
	if len(ranked) > linkSuggestLimit {
		ranked = ranked[:linkSuggestLimit]
	}
	out := make([]LinkSuggestion, len(ranked))
	for i, row := range ranked {
		out[i] = row.suggestion
	}
	return out
}

func longestSuffix(scanned string, barcodes []string) int {
	best := 0
	for _, code := range barcodes {
		code = strings.TrimSpace(code)
		if code == "" || len(code) > len(scanned) {
			continue
		}
		if strings.HasSuffix(scanned, code) && len(code) > best {
			best = len(code)
		}
	}
	return best
}

// linkProfile is the part of a product name that can justify a one-tap link.
// Food words have the brand, generic foods, sizes, and qualifiers taken out.
type linkProfile struct {
	food     []string
	brand    string
	category string
	groupID  string
}

// plausibleLink is true when the shorter food name is contained in the longer
// one. A single shared generic word such as "cream" never qualifies: those
// words are not food tokens. The same brand, category, or group can confirm
// a name that is two words longer. None of those signals is enough alone.
func plausibleLink(a, b linkProfile) bool {
	shared, extra, contained := foodOverlap(a.food, b.food)
	if !contained {
		return false
	}
	if extra <= linkExtraRoom || (shared >= 2 && extra <= linkExtraRoom+1) {
		return true
	}
	return extra == linkExtraRoom+1 && linkSupported(a, b)
}

// linkSupported confirms a near name. It cannot create a suggestion when the
// food words do not already overlap.
func linkSupported(a, b linkProfile) bool {
	if a.brand != "" && a.brand == b.brand {
		return true
	}
	if a.category != "" && a.category == b.category {
		return true
	}
	return a.groupID != "" && a.groupID == b.groupID
}

func foodDice(a, b linkProfile) float64 {
	shared, _, contained := foodOverlap(a.food, b.food)
	if !contained && shared == 0 {
		return 0
	}
	if len(a.food) == 0 || len(b.food) == 0 {
		return 0
	}
	return 2 * float64(shared) / float64(len(a.food)+len(b.food))
}

// foodOverlap reports how many food words are shared and how many the longer
// name adds. contained is true when every food word of the shorter name
// appears in the longer one.
func foodOverlap(a, b []string) (shared, extra int, contained bool) {
	if len(a) == 0 || len(b) == 0 {
		return 0, 0, false
	}
	inB := make(map[string]struct{}, len(b))
	for _, tok := range b {
		inB[tok] = struct{}{}
	}
	n := 0
	for _, tok := range a {
		if _, ok := inB[tok]; ok {
			n++
		}
	}
	shorter, longer := len(a), len(b)
	if shorter > longer {
		shorter, longer = longer, shorter
	}
	return n, longer - n, n == shorter
}

func profileLink(name, category, groupID, brand string, generic map[string]struct{}, known [][]string) linkProfile {
	content := contentTokens(name)
	rest, applied := stripLeadingBrand(content, brandPhrases(brand), known)
	seen := make(map[string]struct{}, len(rest))
	var food []string
	for _, tok := range rest {
		if _, skip := generic[tok]; skip {
			continue
		}
		if _, ok := seen[tok]; ok {
			continue
		}
		seen[tok] = struct{}{}
		food = append(food, tok)
	}
	key := brandKey(brand)
	if key == "" {
		key = strings.Join(applied, " ")
	}
	return linkProfile{
		food:     food,
		brand:    key,
		category: strings.ToLower(strings.TrimSpace(category)),
		groupID:  strings.TrimSpace(groupID),
	}
}

// stripLeadingBrand drops a leading brand phrase. The product's own brand
// wins when it is a prefix, even if a longer recorded brand also fits.
// A leading word is never a brand just because another word follows it:
// "Sour Cream" stays sour and cream when no brand was recorded, so it cannot
// match "Condensed Cream Soup" on cream alone.
func stripLeadingBrand(content []string, own, known [][]string) (rest, applied []string) {
	if phrase, ok := longestLeadingPhrase(content, own); ok {
		return content[len(phrase):], phrase
	}
	if phrase, ok := longestLeadingPhrase(content, known); ok {
		return content[len(phrase):], phrase
	}
	return content, nil
}

func longestLeadingPhrase(content []string, phrases [][]string) ([]string, bool) {
	var best []string
	for _, phrase := range phrases {
		if len(phrase) == 0 || len(phrase) > len(content) || len(phrase) <= len(best) {
			continue
		}
		if !hasPrefixTokens(content, phrase) {
			continue
		}
		best = phrase
	}
	return best, len(best) > 0
}

func hasPrefixTokens(content, phrase []string) bool {
	for i, tok := range phrase {
		if content[i] != tok {
			return false
		}
	}
	return true
}

// brandPhrases splits a recorded brand on commas. Open Food Facts stores
// several brands in one field ("Tillamook, Darigold").
func brandPhrases(brand string) [][]string {
	var phrases [][]string
	for _, part := range strings.Split(brand, ",") {
		toks := contentTokens(part)
		if len(toks) == 0 {
			continue
		}
		phrases = append(phrases, toks)
	}
	return phrases
}

func brandKey(brand string) string {
	phrases := brandPhrases(brand)
	if len(phrases) == 0 {
		return ""
	}
	return strings.Join(phrases[0], " ")
}

func recordedBrandPhrases(candidates []linkCandidate) [][]string {
	seen := map[string]struct{}{}
	var phrases [][]string
	for _, c := range candidates {
		for _, phrase := range brandPhrases(c.Brand) {
			key := strings.Join(phrase, "\x00")
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			phrases = append(phrases, phrase)
		}
	}
	return phrases
}

// GenericWordMinCount is the fewest household names that can mark a word
// generic. Below five names nothing is generic: 3% of a four-product pantry
// is less than one name, and at 100 products 3% is only three, so the floor
// is still what decides.
const GenericWordMinCount = 5

// GenericWordPercent is the share of household products, as an integer
// percent, at which a word is generic. Compared as
// count*100 >= GenericWordPercent*n so 166 products still threshold at 5
// and 167 steps to 6, with no float multiply.
const GenericWordPercent = 3

func wordIsGeneric(count, n int) bool {
	if n <= 0 || count <= 0 {
		return false
	}
	return count >= GenericWordMinCount && count*100 >= GenericWordPercent*n
}

// householdWordCounts counts distinct household products whose cleaned name
// contains each word. A name that repeats a word counts once. n is every
// household product row, including zero stock. Pending and flagged scans
// are not household rows, so they are not in this set.
func householdWordCounts(candidates []linkCandidate) (n int, counts map[string]int) {
	n = len(candidates)
	counts = make(map[string]int)
	for _, c := range candidates {
		seen := make(map[string]struct{})
		for _, tok := range contentTokens(c.Name) {
			if _, ok := seen[tok]; ok {
				continue
			}
			seen[tok] = struct{}{}
			counts[tok]++
		}
	}
	return n, counts
}

func genericWordSet(candidates []linkCandidate) map[string]struct{} {
	n, counts := householdWordCounts(candidates)
	generic := make(map[string]struct{})
	for word, count := range counts {
		if wordIsGeneric(count, n) {
			generic[word] = struct{}{}
		}
	}
	return generic
}

func contentTokens(name string) []string {
	name = strings.ToLower(name)
	fields := strings.FieldsFunc(name, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	var out []string
	for _, raw := range fields {
		if hasDigit(raw) {
			continue
		}
		token := stemFood(raw)
		if len(token) < 2 || linkSkip(token) {
			continue
		}
		out = append(out, token)
	}
	return out
}

func hasDigit(token string) bool {
	for _, r := range token {
		if unicode.IsDigit(r) {
			return true
		}
	}
	return false
}

func linkSkip(token string) bool {
	if _, ok := linkStopWords[token]; ok {
		return true
	}
	if _, ok := linkQualifiers[token]; ok {
		return true
	}
	_, ok := linkPackageWords[token]
	return ok
}

func stemFood(word string) string {
	switch {
	case len(word) > 4 && strings.HasSuffix(word, "ies"):
		return strings.TrimSuffix(word, "ies") + "y"
	case len(word) > 4 && strings.HasSuffix(word, "oes"):
		return strings.TrimSuffix(word, "oes") + "o"
	case len(word) > 4 && (strings.HasSuffix(word, "ches") || strings.HasSuffix(word, "shes") || strings.HasSuffix(word, "xes")):
		return strings.TrimSuffix(word, "es")
	case len(word) > 3 && strings.HasSuffix(word, "s") && !strings.HasSuffix(word, "ss") && !strings.HasSuffix(word, "us") && !strings.HasSuffix(word, "is"):
		return strings.TrimSuffix(word, "s")
	default:
		return word
	}
}

var linkStopWords = map[string]struct{}{
	"a": {}, "an": {}, "and": {}, "for": {}, "from": {}, "in": {}, "of": {}, "the": {}, "to": {}, "with": {},
}

// linkQualifiers are variety words, not the food. Dropping them lets
// "organic" milk and "lite" pears compare on the food that remains.
var linkQualifiers = map[string]struct{}{
	"organic": {}, "lite": {}, "light": {},
}

var linkPackageWords = map[string]struct{}{
	"oz": {}, "ounce": {}, "fl": {}, "floz": {}, "fluid": {},
	"gram": {}, "ml": {}, "kg": {}, "lb": {}, "lbs": {},
	"gallon": {}, "liter": {}, "litre": {}, "milliliter": {}, "kilogram": {},
	"pound": {}, "pint": {}, "quart": {},
	"can": {}, "jar": {}, "box": {}, "bag": {}, "bottle": {}, "pack": {},
	"ct": {}, "pk": {}, "count": {}, "tub": {}, "carton": {}, "pouch": {},
}
