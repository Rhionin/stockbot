package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/Rhionin/stockbot/internal/tenant"
)

func (c *Catalog) buildRemovalPreview(ctx context.Context, scope tenant.Scope, row storeRow) (RemovalPreview, error) {
	defaultID, err := c.readDefaultStoreID(ctx, scope)
	if err != nil {
		return RemovalPreview{}, err
	}
	isDefault := defaultID != nil && *defaultID == row.id
	isLinked := row.linked != nil

	var runOverrides, usualChoices, personChoices, checkedItems, tripsKept int
	// RunOverrides counts open (unpurchased) lines with this run store only.
	// Delete still clears run_store_id on purchased lines; preview does not count them.
	if err := c.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM shopping_list_items
		WHERE household_id = ? AND run_store_id = ? AND purchased_at IS NULL`,
		scope.HouseholdID(), row.id).Scan(&runOverrides); err != nil {
		return RemovalPreview{}, fmt.Errorf("could not preview store removal: %w", err)
	}
	if err := c.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM product_stores
		WHERE household_id = ? AND store_id = ? AND is_usual = 1`,
		scope.HouseholdID(), row.id).Scan(&usualChoices); err != nil {
		return RemovalPreview{}, fmt.Errorf("could not preview store removal: %w", err)
	}
	if err := c.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM product_stores WHERE household_id = ? AND store_id = ?`,
		scope.HouseholdID(), row.id).Scan(&personChoices); err != nil {
		return RemovalPreview{}, fmt.Errorf("could not preview store removal: %w", err)
	}
	if err := c.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM store_availability
		WHERE household_id = ? AND store_id = ? AND status_expires_at > ?`,
		scope.HouseholdID(), row.id, c.now()).Scan(&checkedItems); err != nil {
		return RemovalPreview{}, fmt.Errorf("could not preview store removal: %w", err)
	}
	if err := c.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM store_trips WHERE household_id = ? AND store_id = ?`,
		scope.HouseholdID(), row.id).Scan(&tripsKept); err != nil {
		return RemovalPreview{}, fmt.Errorf("could not preview store removal: %w", err)
	}

	lines, err := c.loadOpenLines(ctx, scope)
	if err != nil {
		return RemovalPreview{}, err
	}
	stores, err := c.loadStoreRows(ctx, scope)
	if err != nil {
		return RemovalPreview{}, err
	}
	usualByProduct, err := c.loadUsualStores(ctx, scope)
	if err != nil {
		return RemovalPreview{}, err
	}
	excluded, err := c.loadExcluded(ctx, scope)
	if err != nil {
		return RemovalPreview{}, err
	}

	openLines := 0
	nextCounts := map[string]int{}
	var nullNext int
	for _, line := range lines {
		current := effectiveStore(line, stores, usualByProduct, excluded, defaultID)
		if current != row.id {
			continue
		}
		openLines++
		next := nextStoreAfterRemoval(line, row.id, stores, usualByProduct, excluded, defaultID)
		if next == "" {
			nullNext++
		} else {
			nextCounts[next]++
		}
	}
	var openLinesNext []OpenLinesNext
	if nullNext > 0 {
		openLinesNext = append(openLinesNext, OpenLinesNext{StoreID: nil, Count: nullNext})
	}
	for _, st := range stores {
		if st.id == row.id {
			continue
		}
		if n := nextCounts[st.id]; n > 0 {
			id := st.id
			openLinesNext = append(openLinesNext, OpenLinesNext{StoreID: &id, Count: n})
		}
	}
	if openLinesNext == nil {
		openLinesNext = []OpenLinesNext{}
	}

	return RemovalPreview{
		StoreID:       row.id,
		OpenLines:     openLines,
		OpenLinesNext: openLinesNext,
		RunOverrides:  runOverrides,
		UsualChoices:  usualChoices,
		PersonChoices: personChoices,
		CheckedItems:  checkedItems,
		TripsKept:     tripsKept,
		IsDefault:     isDefault,
		IsLinked:      isLinked,
	}, nil
}

type openLine struct {
	id         string
	productID  string
	runStoreID *string
}

func (c *Catalog) loadOpenLines(ctx context.Context, scope tenant.Scope) ([]openLine, error) {
	rows, err := c.db.QueryContext(ctx, `
		SELECT sli.id, i.product_id, sli.run_store_id
		FROM shopping_list_items sli
		JOIN items i ON i.id = sli.item_id AND i.household_id = sli.household_id
		WHERE sli.household_id = ? AND sli.purchased_at IS NULL`, scope.HouseholdID())
	if err != nil {
		return nil, fmt.Errorf("could not preview store removal: %w", err)
	}
	defer rows.Close()
	var out []openLine
	for rows.Next() {
		var line openLine
		var run sql.NullString
		if err := rows.Scan(&line.id, &line.productID, &run); err != nil {
			return nil, err
		}
		line.runStoreID = nullStr(run)
		out = append(out, line)
	}
	return out, rows.Err()
}

func (c *Catalog) loadStoreRows(ctx context.Context, scope tenant.Scope) ([]storeRow, error) {
	rows, err := c.db.QueryContext(ctx, `
		SELECT id, name, name_key, kind, linked_provider, location_id, location_chain, position
		FROM stores WHERE household_id = ? ORDER BY position`, scope.HouseholdID())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []storeRow
	for rows.Next() {
		var row storeRow
		var linked, locID, locChain sql.NullString
		if err := rows.Scan(&row.id, &row.name, &row.nameKey, &row.kind, &linked, &locID, &locChain, &row.position); err != nil {
			return nil, err
		}
		row.linked = nullStr(linked)
		row.locationID = nullStr(locID)
		row.locationChain = nullStr(locChain)
		out = append(out, row)
	}
	return out, rows.Err()
}

func (c *Catalog) loadUsualStores(ctx context.Context, scope tenant.Scope) (map[string]string, error) {
	rows, err := c.db.QueryContext(ctx, `
		SELECT product_id, store_id FROM product_stores
		WHERE household_id = ? AND is_usual = 1`, scope.HouseholdID())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var productID, storeID string
		if err := rows.Scan(&productID, &storeID); err != nil {
			return nil, err
		}
		out[productID] = storeID
	}
	return out, rows.Err()
}

func (c *Catalog) loadExcluded(ctx context.Context, scope tenant.Scope) (map[string]map[string]bool, error) {
	rows, err := c.db.QueryContext(ctx, `
		SELECT product_id, store_id FROM product_stores
		WHERE household_id = ? AND state = 'excluded'`, scope.HouseholdID())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]map[string]bool{}
	for rows.Next() {
		var productID, storeID string
		if err := rows.Scan(&productID, &storeID); err != nil {
			return nil, err
		}
		if out[productID] == nil {
			out[productID] = map[string]bool{}
		}
		out[productID][storeID] = true
	}
	return out, rows.Err()
}

func effectiveStore(line openLine, stores []storeRow, usual map[string]string, excluded map[string]map[string]bool, defaultID *string) string {
	if line.runStoreID != nil {
		return *line.runStoreID
	}
	return usualStore(line.productID, stores, usual, excluded, defaultID)
}

func nextStoreAfterRemoval(line openLine, removed string, stores []storeRow, usual map[string]string, excluded map[string]map[string]bool, defaultID *string) string {
	st := storesWithout(stores, removed)
	def := defaultWithout(defaultID, removed)
	return usualStore(line.productID, st, usual, excluded, def)
}

func usualStore(productID string, stores []storeRow, usual map[string]string, excluded map[string]map[string]bool, defaultID *string) string {
	if id, ok := usual[productID]; ok && !isExcluded(excluded, productID, id) && storeExists(stores, id) {
		return id
	}
	if defaultID != nil && !isExcluded(excluded, productID, *defaultID) && storeExists(stores, *defaultID) {
		return *defaultID
	}
	return ""
}

func storesWithout(stores []storeRow, drop string) []storeRow {
	out := make([]storeRow, 0, len(stores))
	for _, st := range stores {
		if st.id != drop {
			out = append(out, st)
		}
	}
	return out
}

func defaultWithout(defaultID *string, removed string) *string {
	if defaultID == nil || *defaultID == removed {
		return nil
	}
	return defaultID
}

func isExcluded(excluded map[string]map[string]bool, productID, storeID string) bool {
	if excluded[productID] == nil {
		return false
	}
	return excluded[productID][storeID]
}

func storeExists(stores []storeRow, id string) bool {
	for _, st := range stores {
		if st.id == id {
			return true
		}
	}
	return false
}

func previewEqual(a, b RemovalPreview) bool {
	if a.OpenLines != b.OpenLines || a.RunOverrides != b.RunOverrides ||
		a.UsualChoices != b.UsualChoices || a.PersonChoices != b.PersonChoices ||
		a.CheckedItems != b.CheckedItems || a.TripsKept != b.TripsKept ||
		a.IsDefault != b.IsDefault || a.IsLinked != b.IsLinked {
		return false
	}
	if len(a.OpenLinesNext) != len(b.OpenLinesNext) {
		return false
	}
	for i := range a.OpenLinesNext {
		if a.OpenLinesNext[i].Count != b.OpenLinesNext[i].Count {
			return false
		}
		if !ptrEqual(a.OpenLinesNext[i].StoreID, b.OpenLinesNext[i].StoreID) {
			return false
		}
	}
	return true
}
