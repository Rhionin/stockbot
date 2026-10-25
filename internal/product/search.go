package product

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/Rhionin/stockbot/internal/tenant"
)

// ErrQueryTooShort means the search query has fewer than two characters.
var ErrQueryTooShort = errors.New("query_too_short")

// SearchHit is one row from GET /api/products/search.
type SearchHit struct {
	ProductID string  `json:"productId"`
	ItemID    *string `json:"itemId"`
	Name      string  `json:"name"`
	Brand     string  `json:"brand,omitempty"`
	Size      string  `json:"size,omitempty"`
	Category  string  `json:"category,omitempty"`
	OnHand    int     `json:"onHand"`
}

type searchRow struct {
	hit     SearchHit
	hasItem bool
}

// SearchProducts finds products visible to the household by name or brand.
// Household items (an items row exists) are ordered before catalog-only matches.
func (r *Catalog) SearchProducts(ctx context.Context, scope tenant.Scope, query string, limit int) ([]SearchHit, error) {
	if err := scope.Require(); err != nil {
		return nil, err
	}
	query = strings.TrimSpace(query)
	if utf8.RuneCountInString(query) < 2 {
		return nil, ErrQueryTooShort
	}
	if limit <= 0 {
		limit = 20
	}
	if limit > 50 {
		limit = 50
	}
	needle := "%" + escapeLike(strings.ToLower(query)) + "%"
	householdID := scope.HouseholdID()
	display := DisplayNameSQL("p")
	brand := BrandSQL("p")

	rows, err := r.db.QueryContext(ctx, `
		SELECT
			p.id,
			i.id,
			`+display+`,
			`+brand+`,
			COALESCE(p.category, ''),
			p.net_base_value,
			p.net_dimension,
			p.pack_count,
			(
				SELECT COUNT(*) FROM item_instances inst
				WHERE inst.household_id = ? AND inst.item_id = i.id AND inst.removed_at IS NULL
			) AS on_hand
		FROM items i
		JOIN products p ON p.id = i.product_id
		WHERE i.household_id = ?
		  AND (
		    LOWER(`+display+`) LIKE ? ESCAPE '\'
		    OR LOWER(`+brand+`) LIKE ? ESCAPE '\'
		  )`,
		householdID, householdID, householdID, householdID, householdID, needle, householdID, needle,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var hits []searchRow
	seen := map[string]struct{}{}
	for rows.Next() {
		row, productID, err := scanSearchRow(rows, true)
		if err != nil {
			return nil, err
		}
		seen[productID] = struct{}{}
		hits = append(hits, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	catalogRows, err := r.db.QueryContext(ctx, `
		SELECT
			p.id,
			`+display+`,
			`+brand+`,
			COALESCE(p.category, ''),
			p.net_base_value,
			p.net_dimension,
			p.pack_count
		FROM products p
		WHERE (
		    LOWER(`+display+`) LIKE ? ESCAPE '\'
		    OR LOWER(`+brand+`) LIKE ? ESCAPE '\'
		  )
		  AND p.id NOT IN (
		    SELECT product_id FROM items WHERE household_id = ?
		  )
		  AND (
		    COALESCE(p.source, '') != ?
		    OR EXISTS (
		      SELECT 1 FROM household_product_overrides o2
		      WHERE o2.product_id = p.id AND o2.household_id = ?
		    )
		  )`,
		householdID, householdID, householdID, needle, householdID, needle, householdID, SourceUser, householdID,
	)
	if err != nil {
		return nil, err
	}
	defer catalogRows.Close()

	for catalogRows.Next() {
		row, productID, err := scanSearchRow(catalogRows, false)
		if err != nil {
			return nil, err
		}
		if _, ok := seen[productID]; ok {
			continue
		}
		seen[productID] = struct{}{}
		hits = append(hits, row)
	}
	if err := catalogRows.Err(); err != nil {
		return nil, err
	}

	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].hasItem != hits[j].hasItem {
			return hits[i].hasItem
		}
		if hits[i].hit.Name != hits[j].hit.Name {
			return hits[i].hit.Name < hits[j].hit.Name
		}
		return hits[i].hit.ProductID < hits[j].hit.ProductID
	})
	if len(hits) > limit {
		hits = hits[:limit]
	}
	out := make([]SearchHit, len(hits))
	for i, h := range hits {
		out[i] = h.hit
	}
	return out, nil
}

func scanSearchRow(rows *sql.Rows, hasItem bool) (searchRow, string, error) {
	var productID, name, brand, category string
	var netBase sql.NullFloat64
	var netDim sql.NullString
	var pack sql.NullInt64
	var itemID string
	var onHand int
	if hasItem {
		if err := rows.Scan(&productID, &itemID, &name, &brand, &category, &netBase, &netDim, &pack, &onHand); err != nil {
			return searchRow{}, "", err
		}
	} else {
		if err := rows.Scan(&productID, &name, &brand, &category, &netBase, &netDim, &pack); err != nil {
			return searchRow{}, "", err
		}
	}
	prod := productFromSizeColumns(productID, category, netBase, netDim, pack)
	hit := SearchHit{
		ProductID: productID,
		Name:      name,
		Brand:     strings.TrimSpace(brand),
		Size:      formatProductSize(prod),
		Category:  category,
		OnHand:    onHand,
	}
	if hasItem {
		hit.ItemID = &itemID
	}
	return searchRow{hit: hit, hasItem: hasItem}, productID, nil
}

func productFromSizeColumns(productID, category string, netBase sql.NullFloat64, netDim sql.NullString, pack sql.NullInt64) Product {
	prod := Product{ID: productID, Category: category}
	if netBase.Valid {
		v := netBase.Float64
		prod.NetBaseValue = &v
	}
	if netDim.Valid {
		prod.NetDimension = netDim.String
	}
	if pack.Valid {
		p := int(pack.Int64)
		prod.PackCount = &p
	}
	return prod
}

func escapeLike(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "%", "\\%")
	s = strings.ReplaceAll(s, "_", "\\_")
	return s
}

func formatProductSize(prod Product) string {
	if prod.NetBaseValue == nil || prod.NetDimension == "" {
		return ""
	}
	pack := 0
	if prod.PackCount != nil {
		pack = *prod.PackCount
	}
	return FormatQuantity(*prod.NetBaseValue, prod.NetDimension, pack)
}
