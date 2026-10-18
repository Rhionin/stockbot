package scan

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"unicode"
)

// linkNameFloor is how much of two food names must overlap before a stock-out
// offers that item. Suffix barcode matches skip this bar.
const linkNameFloor = 0.34

// linkSuggestLimit keeps the confirm row to a short list.
const linkSuggestLimit = 8

// LinkSuggestion is a household item a scanned barcode might already be.
type LinkSuggestion struct {
	ItemID    string `json:"itemId"`
	ProductID string `json:"productId"`
	Name      string `json:"name"`
	OnHand    int    `json:"onHand"`
}

// NoItemError is a stock-out of a product this household does not stock.
// The scan is left pending. Suggestions are ranked in case a caller offers a link.
type NoItemError struct {
	Suggestions []LinkSuggestion
}

func (e *NoItemError) Error() string {
	return "household does not stock this product"
}

type linkCandidate struct {
	ItemID    string
	ProductID string
	Name      string
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
	excludeProductID := ""
	if scanEntry.ProductID != nil {
		excludeProductID = *scanEntry.ProductID
		err := q.QueryRowContext(ctx, `SELECT name FROM products WHERE id = ?`, excludeProductID).Scan(&scannedName)
		if err != nil && err != sql.ErrNoRows {
			return nil, fmt.Errorf("scanned product name: %w", err)
		}
	}

	rows, err := q.QueryContext(ctx, `
		SELECT i.id, p.id, p.name,
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
		if err := rows.Scan(&c.ItemID, &c.ProductID, &c.Name, &c.OnHand, &codes); err != nil {
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

	return rankLinkCandidates(scanEntry.Barcode, scannedName, excludeProductID, candidates), nil
}

// rankLinkCandidates puts the longest suffix barcode first, then closer food names.
// A suffix of any length outranks a name-only resemblance, and a longer suffix
// outranks a shorter one, so a truncated code beats an accidental last digit.
func rankLinkCandidates(scannedBarcode, scannedName, excludeProductID string, candidates []linkCandidate) []LinkSuggestion {
	scannedBarcode = strings.TrimSpace(scannedBarcode)
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
		score := nameSimilarity(scannedName, c.Name)
		if suffix == 0 && score < linkNameFloor {
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

func nameSimilarity(a, b string) float64 {
	ta := foodTokens(a)
	tb := foodTokens(b)
	if len(ta) == 0 || len(tb) == 0 {
		return 0
	}
	inter := 0
	for token := range ta {
		if _, ok := tb[token]; ok {
			inter++
		}
	}
	return 2 * float64(inter) / float64(len(ta)+len(tb))
}

var linkStopWords = map[string]struct{}{
	"a": {}, "an": {}, "and": {}, "for": {}, "in": {}, "of": {}, "the": {}, "to": {}, "with": {},
}

func foodTokens(name string) map[string]struct{} {
	name = strings.ToLower(name)
	fields := strings.FieldsFunc(name, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	tokens := make(map[string]struct{}, len(fields))
	for _, raw := range fields {
		token := stemFood(raw)
		if len(token) < 2 {
			continue
		}
		if _, stop := linkStopWords[token]; stop {
			continue
		}
		tokens[token] = struct{}{}
	}
	return tokens
}

func stemFood(word string) string {
	switch {
	case strings.HasSuffix(word, "ies") && len(word) > 4:
		word = word[:len(word)-3] + "y"
	case strings.HasSuffix(word, "es") && len(word) > 4:
		word = word[:len(word)-2]
	case strings.HasSuffix(word, "s") && len(word) > 3:
		word = word[:len(word)-1]
	}
	if strings.HasSuffix(word, "ing") && len(word) > 5 {
		word = word[:len(word)-3]
	}
	if strings.HasSuffix(word, "ed") && len(word) > 4 {
		word = word[:len(word)-2]
	}
	return word
}
