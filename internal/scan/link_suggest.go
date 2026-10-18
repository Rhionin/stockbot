package scan

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
	"unicode"
)

// linkSuggestLimit keeps the confirm row to a short list. Several real
// matches (two sizes of the same food) stay visible; the score gate below is
// what drops a weak one.
const linkSuggestLimit = 8

// LinkScoreThreshold is the minimum weighted overlap that becomes a suggestion.
// 0.65 sits just above the highest wrong pair measured on a real pantry
// (0.6489). It is one named constant so a later retune does not rewrite the
// comparison.
//
// Release note: Link suggestions are now much stricter: they're shown only
// when the names closely match, so you'll see fewer wrong suggestions. If an
// item isn't suggested, search for it.
const LinkScoreThreshold = 0.65

// LinkScoreEpsilon keeps a ratio that lands a hair under the threshold, the
// way 2/5 does in float64, on the matching side of the comparison.
const LinkScoreEpsilon = 1e-9

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
	StockedAt time.Time
	Barcodes  []string
}

type linkQuery interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// suggestLinks ranks household items the scanned barcode might belong to.
// A barcode suffix still qualifies when the names do not. Qualified rows are
// ordered by name score, then a shared brand, category, or group, then how
// recently the item was stocked.
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
			COALESCE((SELECT MAX(ii.stock_in_at) FROM item_instances ii WHERE ii.item_id = i.id), i.created_at),
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
		var codes, stockedAt string
		if err := rows.Scan(&c.ItemID, &c.ProductID, &c.Name, &c.Category, &c.GroupID, &c.Brand, &c.OnHand, &stockedAt, &codes); err != nil {
			return nil, fmt.Errorf("list link candidates: %w", err)
		}
		c.StockedAt, err = parseStockedAt(stockedAt)
		if err != nil {
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

// rankLinkCandidates keeps a household item when its name scores at least
// LinkScoreThreshold, or when one of its barcodes is a suffix of the scan.
// Rows are ordered by score descending, then a shared brand, category, or
// group, then the most recently stocked item. A name that cleans to nothing
// is never a name suggestion. At most linkSuggestLimit rows are returned.
func rankLinkCandidates(scannedBarcode, scannedName, scannedCategory, scannedGroup, scannedBrand, excludeProductID string, candidates []linkCandidate) []LinkSuggestion {
	scannedBarcode = strings.TrimSpace(scannedBarcode)
	weights := BuildWeights(candidateNames(candidates))
	knownBrands := recordedBrandPhrases(candidates)
	scanned := profileLink(scannedName, scannedCategory, scannedGroup, scannedBrand, knownBrands)
	type scored struct {
		suggestion LinkSuggestion
		nameScore  float64
		supported  bool
		stockedAt  time.Time
	}
	var ranked []scored
	for _, c := range candidates {
		if c.OnHand <= 0 || c.ProductID == excludeProductID {
			continue
		}
		suffix := longestSuffix(scannedBarcode, c.Barcodes)
		other := profileLink(c.Name, c.Category, c.GroupID, c.Brand, knownBrands)
		score := Score(scanned.food, other.food, weights)
		// An empty cleaned name has nothing to weigh. A suffix still qualifies
		// on its own, because a truncated code is the barcode, not the name.
		nameOK := len(scanned.food) > 0 && len(other.food) > 0 && scoreMatches(score)
		if suffix == 0 && !nameOK {
			continue
		}
		ranked = append(ranked, scored{
			suggestion: LinkSuggestion{
				ItemID:    c.ItemID,
				ProductID: c.ProductID,
				Name:      c.Name,
				OnHand:    c.OnHand,
			},
			nameScore: score,
			supported: linkSupported(scanned, other),
			stockedAt: c.StockedAt,
		})
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].nameScore != ranked[j].nameScore {
			return ranked[i].nameScore > ranked[j].nameScore
		}
		if ranked[i].supported != ranked[j].supported {
			return ranked[i].supported
		}
		if !ranked[i].stockedAt.Equal(ranked[j].stockedAt) {
			return ranked[i].stockedAt.After(ranked[j].stockedAt)
		}
		// Same score, support, and stock time: a stable name keeps the list
		// from depending on query order.
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

// parseStockedAt reads the text SQLite returns for a COALESCE(MAX(stock_in_at))
// expression. A bare datetime column scans into time.Time; this expression does not.
func parseStockedAt(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, nil
	}
	// database/sql's time encoding keeps the monotonic clock in the text form.
	if i := strings.Index(raw, " m="); i >= 0 {
		raw = raw[:i]
	}
	layouts := []string{
		time.RFC3339Nano,
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05-07:00",
		"2006-01-02 15:04:05.999999999 -0700 MST",
		"2006-01-02 15:04:05 -0700 MST",
		"2006-01-02 15:04:05.999999999Z07:00",
		"2006-01-02 15:04:05Z07:00",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05Z",
	}
	for _, layout := range layouts {
		if parsed, err := time.Parse(layout, raw); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, fmt.Errorf("stocked time %q", raw)
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
// Food words have a recorded brand and package sizes taken out. A word is
// never dropped for being common.
type linkProfile struct {
	food     []string
	brand    string
	category string
	groupID  string
}

// linkSupported only orders ties. A shared brand, category, or group never
// puts a name over the score threshold by itself.
func linkSupported(a, b linkProfile) bool {
	if a.brand != "" && a.brand == b.brand {
		return true
	}
	if a.category != "" && a.category == b.category {
		return true
	}
	return a.groupID != "" && a.groupID == b.groupID
}

// Score is the weighted overlap of two cleaned token lists.
//
// Words are a set: a repeated word counts once, and order does not matter.
// The same set scores 1, unless both sets are empty, which scores 0 so an
// empty cleaned name never becomes a suggestion and the ratio is never 0/0.
// Otherwise the score is the weight of the shared words divided by the weight
// of the longer name. Longer means the larger total weight; when the weights
// tie, it is the name with more words. Weights are added in sorted word order
// so the sum does not depend on map or input order.
//
// A result is never NaN or Inf. A zero denominator scores 0.
func Score(a, b []string, weights map[string]float64) float64 {
	as := wordSet(a)
	bs := wordSet(b)
	if len(as) == 0 || len(bs) == 0 {
		return 0
	}
	if sameSet(as, bs) {
		return 1
	}
	wa := tokenWeight(as, weights)
	wb := tokenWeight(bs, weights)
	shared := tokenWeight(sharedSet(as, bs), weights)
	denom := longerWeight(wa, wb, len(as), len(bs))
	if denom == 0 || math.IsNaN(denom) || math.IsInf(denom, 0) {
		return 0
	}
	score := shared / denom
	if math.IsNaN(score) || math.IsInf(score, 0) {
		return 0
	}
	return score
}

// scoreMatches is the suggestion gate. The epsilon keeps a float that is
// mathematically the threshold, but prints a hair under it, on the match side.
func scoreMatches(score float64) bool {
	return scoreWithin(score, LinkScoreThreshold)
}

func scoreWithin(score, threshold float64) bool {
	if math.IsNaN(score) || math.IsInf(score, 0) {
		return false
	}
	return score >= threshold-LinkScoreEpsilon
}

// BuildWeights assigns each cleaned word ln((N+1)/max(df, 1)). N is the
// number of names, including ones that clean to nothing. df is how many of
// those names contain the word; a repeated word in one name counts once.
// A word in every name still gets ln((N+1)/N), and a single name gets ln(2).
//
// The empty-string entry is not a word. It holds ln(N+1), which is the weight
// of a scanned word that no household name contains (df clamped up from 0 to
// 1). Score uses that entry for any word missing from the map.
func BuildWeights(names []string) map[string]float64 {
	n := len(names)
	df := make(map[string]int)
	for _, name := range names {
		for _, tok := range wordSet(contentTokens(name)) {
			df[tok]++
		}
	}
	weights := make(map[string]float64, len(df)+1)
	if n == 0 {
		return weights
	}
	weights[""] = math.Log(float64(n) + 1)
	for word, count := range df {
		if count < 1 {
			count = 1
		}
		weights[word] = math.Log((float64(n) + 1) / float64(count))
	}
	return weights
}

func candidateNames(candidates []linkCandidate) []string {
	names := make([]string, len(candidates))
	for i, c := range candidates {
		names[i] = c.Name
	}
	return names
}

// wordSet is the cleaned words of one name: unique, sorted, and never empty
// string. Sorted order is what makes weight sums deterministic.
func wordSet(tokens []string) []string {
	seen := make(map[string]struct{}, len(tokens))
	out := make([]string, 0, len(tokens))
	for _, tok := range tokens {
		if tok == "" {
			continue
		}
		if _, ok := seen[tok]; ok {
			continue
		}
		seen[tok] = struct{}{}
		out = append(out, tok)
	}
	sort.Strings(out)
	return out
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sharedSet(a, b []string) []string {
	var out []string
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			out = append(out, a[i])
			i++
			j++
		case a[i] < b[j]:
			i++
		default:
			j++
		}
	}
	return out
}

func wordWeight(word string, weights map[string]float64) float64 {
	if weights == nil {
		return 0
	}
	w, ok := weights[word]
	if !ok {
		// Missing word: df clamped to 1, stored under the empty key by BuildWeights.
		w = weights[""]
	}
	if math.IsNaN(w) || math.IsInf(w, 0) || w < 0 {
		return 0
	}
	return w
}

func tokenWeight(tokens []string, weights map[string]float64) float64 {
	var sum float64
	for _, tok := range tokens {
		sum += wordWeight(tok, weights)
	}
	return sum
}

func longerWeight(wa, wb float64, na, nb int) float64 {
	if wa > wb {
		return wa
	}
	if wb > wa {
		return wb
	}
	if na >= nb {
		return wa
	}
	return wb
}

func profileLink(name, category, groupID, brand string, known [][]string) linkProfile {
	content := contentTokens(name)
	rest, applied := stripLeadingBrand(content, brandPhrases(brand), known)
	key := brandKey(brand)
	if key == "" {
		key = strings.Join(applied, " ")
	}
	return linkProfile{
		food:     rest,
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

// Tokens is the cleaned word list Score and BuildWeights share.
func Tokens(name string) []string {
	return contentTokens(name)
}

func contentTokens(name string) []string {
	name = strings.ToLower(name)
	name = stripDiacritics(name)
	// Apostrophes fold possessives (Annie's == Annies). Hyphens stay as
	// separators so "low-fat" is two words; "non" is joined to the next word
	// below, which is what makes "non-fat", "nonfat", and "non fat" one token.
	name = strings.NewReplacer("'", "", "’", "").Replace(name)
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
	return joinCompounds(out)
}

// joinCompounds folds the spaced and hyphenated spelling of "non" into the
// closed form. "non-fat" has already been split into "non" and "fat".
func joinCompounds(tokens []string) []string {
	if len(tokens) == 0 {
		return nil
	}
	var out []string
	for i := 0; i < len(tokens); i++ {
		if tokens[i] == "non" && i+1 < len(tokens) {
			out = append(out, tokens[i]+tokens[i+1])
			i++
			continue
		}
		out = append(out, tokens[i])
	}
	return out
}

func stripDiacritics(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r >= 0x0300 && r <= 0x036F {
			continue
		}
		if base, ok := latinFold[r]; ok {
			b.WriteRune(base)
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

var latinFold = map[rune]rune{
	'à': 'a', 'á': 'a', 'â': 'a', 'ä': 'a', 'ã': 'a', 'å': 'a',
	'è': 'e', 'é': 'e', 'ê': 'e', 'ë': 'e',
	'ì': 'i', 'í': 'i', 'î': 'i', 'ï': 'i',
	'ò': 'o', 'ó': 'o', 'ô': 'o', 'ö': 'o', 'õ': 'o',
	'ù': 'u', 'ú': 'u', 'û': 'u', 'ü': 'u',
	'ý': 'y', 'ÿ': 'y',
	'ñ': 'n', 'ç': 'c',
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
	_, ok := linkPackageWords[token]
	return ok
}

func stemFood(word string) string {
	if repl, ok := irregularStems[word]; ok {
		return repl
	}
	switch {
	case len(word) > 4 && strings.HasSuffix(word, "ies"):
		base := strings.TrimSuffix(word, "ies")
		if iesKeepsE(base) {
			return base + "ie"
		}
		return base + "y"
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

// iesKeepsE is the English -ie plural: -ies after k, or after n when a
// consonant precedes that n. cookies and brownies keep the e; berries and
// ponies do not.
func iesKeepsE(base string) bool {
	if base == "" {
		return false
	}
	last := base[len(base)-1]
	if last == 'k' {
		return true
	}
	if last != 'n' || len(base) < 2 {
		return false
	}
	return isConsonant(base[len(base)-2])
}

func isConsonant(b byte) bool {
	switch b {
	case 'a', 'e', 'i', 'o', 'u':
		return false
	default:
		return true
	}
}

var irregularStems = map[string]string{
	"leaves": "leaf",
	"loaves": "loaf",
}

var linkPackageWords = map[string]struct{}{
	"oz": {}, "ounce": {}, "fl": {}, "floz": {}, "fluid": {},
	"gram": {}, "ml": {}, "kg": {}, "lb": {}, "lbs": {},
	"gallon": {}, "liter": {}, "litre": {}, "milliliter": {}, "kilogram": {},
	"pound": {}, "pint": {}, "quart": {},
	"can": {}, "jar": {}, "box": {}, "bag": {}, "bottle": {}, "pack": {},
	"ct": {}, "pk": {}, "count": {}, "tub": {}, "carton": {}, "pouch": {},
}
