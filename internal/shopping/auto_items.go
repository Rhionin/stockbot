package shopping

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// PlannedLine is one supply line ready to persist.
// Manual lines are echoed and left untouched. Auto lines are upserted by item.
type PlannedLine struct {
	ProductID string
	ItemID    string
	GroupKey  string
	Quantity  int
	Note      string
	Manual    bool
}

// ShelfMember is one pantry item in a product group.
type ShelfMember struct {
	ItemID   string
	GroupKey string
}

// SavePlannedLines writes one supply snapshot into the staged cart.
// Untouched auto rows are refreshed. A line the owner changed (touched) stays.
// Auto rows the owner removed stay removed. Manual rows are neither deleted nor resized.
func (s *Store) SavePlannedLines(ctx context.Context, userID string, lines []PlannedLine, members []ShelfMember) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("could not begin shopping list update: %w", err)
	}
	defer tx.Rollback()

	membersOf := map[string]map[string]struct{}{}
	for _, member := range members {
		if member.GroupKey == "" {
			continue
		}
		if membersOf[member.GroupKey] == nil {
			membersOf[member.GroupKey] = map[string]struct{}{}
		}
		membersOf[member.GroupKey][member.ItemID] = struct{}{}
	}

	touched, touchedGroups, err := listTouchedAutoItems(ctx, tx, userID)
	if err != nil {
		return err
	}
	skipped, skippedGroups, err := listSkippedItems(ctx, tx, userID)
	if err != nil {
		return err
	}
	groupOf := map[string]string{}
	for _, member := range members {
		if member.GroupKey == "" {
			continue
		}
		groupOf[member.ItemID] = member.GroupKey
	}
	for itemID := range touched {
		if key := groupOf[itemID]; key != "" {
			touchedGroups[key] = struct{}{}
		}
	}

	keep := map[string]struct{}{}
	for _, line := range lines {
		if line.Manual || line.Quantity < 1 || line.ItemID == "" {
			continue
		}
		itemID := line.ItemID
		key := line.GroupKey
		if key == "" {
			key = groupOf[itemID]
		}
		if groupSkipped(skipped, skippedGroups, key, itemID, membersOf) {
			continue
		}
		if _, held := touched[itemID]; held {
			continue
		}
		if key != "" {
			if _, held := touchedGroups[key]; held {
				continue
			}
		}
		if err := upsertAutoLine(ctx, tx, userID, itemID, line.GroupKey, line.Quantity, line.Note); err != nil {
			return err
		}
		keep[itemID] = struct{}{}
	}
	if err := deleteUnplannedAuto(ctx, tx, userID, keep); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("could not save shopping list updates: %w", err)
	}
	return nil
}

func upsertAutoLine(ctx context.Context, tx *sql.Tx, userID, itemID, groupID string, quantity int, note string) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT id, quantity, note, purchased_at, CAST(created_at AS TEXT) FROM shopping_list_items
		WHERE user_id = ? AND item_id = ? AND source = 'auto'
		ORDER BY created_at ASC, rowid ASC`, userID, itemID)
	if err != nil {
		return fmt.Errorf("could not load shopping list item %q: %w", itemID, err)
	}
	type autoRow struct {
		id, note string
		qty      int
		bought   bool
		added    time.Time
	}
	var existing []autoRow
	for rows.Next() {
		var row autoRow
		var purchasedAt sql.NullTime
		var addedRaw string
		if err := rows.Scan(&row.id, &row.qty, &row.note, &purchasedAt, &addedRaw); err != nil {
			rows.Close()
			return fmt.Errorf("could not read shopping list item %q: %w", itemID, err)
		}
		row.bought = purchasedAt.Valid
		added, err := parseStoredTime(addedRaw)
		if err != nil {
			rows.Close()
			return fmt.Errorf("could not read shopping list item %q: %w", itemID, err)
		}
		row.added = added
		existing = append(existing, row)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("could not read shopping list item %q: %w", itemID, err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("could not read shopping list item %q: %w", itemID, err)
	}

	stockIns, err := stockInTimes(ctx, tx, itemID)
	if err != nil {
		return err
	}
	var openID string
	var extras []string
	alreadyBought := false
	for _, row := range existing {
		if row.bought {
			// A bought line is finished once the product has been stocked in
			// at or after the line was added. The tick time does not matter.
			// A finished line must not hold back the next need.
			if stockInFinishesLine(stockIns, row.added) {
				continue
			}
			if row.qty == quantity && row.note == note {
				alreadyBought = true
			}
			continue
		}
		if openID == "" {
			openID = row.id
			continue
		}
		extras = append(extras, row.id)
	}

	var storedGroup any
	if groupID != "" {
		storedGroup = groupID
	}
	if openID == "" {
		// A checked-off row for this exact line stays checked off. A different
		// plan is a new unpurchased line, so the purchase is not rewritten.
		if alreadyBought {
			return nil
		}
		_, err := tx.ExecContext(ctx, `
			INSERT INTO shopping_list_items (id, user_id, item_id, quantity, source, note, created_at, group_id)
			VALUES (?, ?, ?, ?, 'auto', ?, ?, ?)`,
			uuid.NewString(), userID, itemID, quantity, note, time.Now().UTC(), storedGroup,
		)
		if err != nil {
			return fmt.Errorf("could not add shopping list item %q: %w", itemID, err)
		}
		return nil
	}

	_, err = tx.ExecContext(ctx, `
		UPDATE shopping_list_items
		SET quantity = ?,
		    note = ?,
		    group_id = ?
		WHERE id = ?`,
		quantity, note, storedGroup, openID,
	)
	if err != nil {
		return fmt.Errorf("could not refresh shopping list item %q: %w", itemID, err)
	}
	for _, extra := range extras {
		if _, err := tx.ExecContext(ctx, `DELETE FROM shopping_list_items WHERE id = ?`, extra); err != nil {
			return fmt.Errorf("could not refresh shopping list item %q: %w", itemID, err)
		}
	}
	return nil
}

func listTouchedAutoItems(ctx context.Context, tx *sql.Tx, userID string) (map[string]struct{}, map[string]struct{}, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT item_id, COALESCE(group_id, '') FROM shopping_list_items
		WHERE user_id = ? AND source = 'auto' AND touched = 1 AND purchased_at IS NULL`,
		userID)
	if err != nil {
		return nil, nil, fmt.Errorf("could not load shopping list edits: %w", err)
	}
	defer rows.Close()
	touched := map[string]struct{}{}
	groups := map[string]struct{}{}
	for rows.Next() {
		var itemID, groupID string
		if err := rows.Scan(&itemID, &groupID); err != nil {
			return nil, nil, fmt.Errorf("could not read shopping list edit: %w", err)
		}
		touched[itemID] = struct{}{}
		if groupID != "" {
			groups[groupID] = struct{}{}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("could not read shopping list edits: %w", err)
	}
	return touched, groups, nil
}

func listSkippedItems(ctx context.Context, tx *sql.Tx, userID string) (map[string]struct{}, map[string]struct{}, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT item_id, COALESCE(group_id, '') FROM staged_cart_skips WHERE user_id = ?`, userID)
	if err != nil {
		return nil, nil, fmt.Errorf("could not load removed shopping list items: %w", err)
	}
	defer rows.Close()
	skipped := map[string]struct{}{}
	groups := map[string]struct{}{}
	for rows.Next() {
		var itemID, groupID string
		if err := rows.Scan(&itemID, &groupID); err != nil {
			return nil, nil, fmt.Errorf("could not read removed shopping list item: %w", err)
		}
		skipped[itemID] = struct{}{}
		if groupID != "" {
			groups[groupID] = struct{}{}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("could not read removed shopping list items: %w", err)
	}
	return skipped, groups, nil
}

func groupSkipped(skipped, skippedGroups map[string]struct{}, key, itemID string, membersOf map[string]map[string]struct{}) bool {
	if _, ok := skipped[itemID]; ok {
		return true
	}
	if key == "" {
		return false
	}
	if _, ok := skippedGroups[key]; ok {
		return true
	}
	for member := range membersOf[key] {
		if _, ok := skipped[member]; ok {
			return true
		}
	}
	return false
}

func deleteUnplannedAuto(ctx context.Context, tx *sql.Tx, userID string, keep map[string]struct{}) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT id, item_id, touched, purchased_at, CAST(created_at AS TEXT) FROM shopping_list_items
		WHERE user_id = ? AND source = 'auto'`, userID)
	if err != nil {
		return fmt.Errorf("could not load derived shopping list items: %w", err)
	}
	type candidate struct {
		id, itemID string
		touched    int
		bought     bool
		added      time.Time
	}
	var candidates []candidate
	for rows.Next() {
		var row candidate
		var purchasedAt sql.NullTime
		var addedRaw string
		if err := rows.Scan(&row.id, &row.itemID, &row.touched, &purchasedAt, &addedRaw); err != nil {
			rows.Close()
			return fmt.Errorf("could not read derived shopping list item: %w", err)
		}
		row.bought = purchasedAt.Valid
		if row.bought {
			added, err := parseStoredTime(addedRaw)
			if err != nil {
				rows.Close()
				return fmt.Errorf("could not read derived shopping list item: %w", err)
			}
			row.added = added
		}
		candidates = append(candidates, row)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("could not read derived shopping list items: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("could not read derived shopping list items: %w", err)
	}
	// The list is fully read before any row is deleted, so one fill removes
	// every finished purchase.
	stocked := map[string][]time.Time{}
	var drop []string
	for _, row := range candidates {
		if row.bought {
			// Target changes and undo keep a purchase while nothing has been
			// stocked in since the line was added. Once that stock-in exists,
			// master deletes the row the same way it deletes any other
			// unplanned auto line.
			times, ok := stocked[row.itemID]
			if !ok {
				times, err = stockInTimes(ctx, tx, row.itemID)
				if err != nil {
					return err
				}
				stocked[row.itemID] = times
			}
			if stockInFinishesLine(times, row.added) {
				drop = append(drop, row.id)
			}
			continue
		}
		if row.touched != 0 {
			continue
		}
		if _, ok := keep[row.itemID]; ok {
			continue
		}
		drop = append(drop, row.id)
	}
	for _, id := range drop {
		if err := deleteShoppingRow(ctx, tx, id); err != nil {
			return err
		}
	}
	return nil
}

func stockInTimes(ctx context.Context, tx *sql.Tx, itemID string) ([]time.Time, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT CAST(at AS TEXT) FROM stock_in_events
		WHERE product_id = (SELECT product_id FROM items WHERE id = ?)`, itemID)
	if err != nil {
		return nil, fmt.Errorf("could not read stock-in events: %w", err)
	}
	defer rows.Close()
	var times []time.Time
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, fmt.Errorf("could not read stock-in events: %w", err)
		}
		at, err := parseStoredTime(raw)
		if err != nil {
			return nil, fmt.Errorf("could not read stock-in events: %w", err)
		}
		times = append(times, at)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("could not read stock-in events: %w", err)
	}
	return times, nil
}

// stockInFinishesLine is true when a stock-in is at or after the moment the
// line was added. Equal instants count. The tick time is not the boundary.
func stockInFinishesLine(stockIns []time.Time, added time.Time) bool {
	for _, at := range stockIns {
		if !at.Before(added) {
			return true
		}
	}
	return false
}

// parseStoredTime reads a sqlite time string. The driver writes time.Time.String,
// which appends " m=+…" while a monotonic reading is present, and it keeps the
// zone the value was stored in. Those strings do not sort in time order.
func parseStoredTime(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if i := strings.Index(raw, " m="); i >= 0 {
		raw = raw[:i]
	}
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05.999999999 -0700 MST",
		"2006-01-02 15:04:05 -0700 MST",
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05",
	}
	var last error
	for _, layout := range layouts {
		parsed, err := time.Parse(layout, raw)
		if err == nil {
			return parsed, nil
		}
		last = err
	}
	return time.Time{}, last
}

// EndTrip drops checked-off auto rows. Starting a new cart ends the
// protection those rows had during a target change, so the next list can
// plan the item again.
func (s *Store) EndTrip(ctx context.Context, userID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("could not start a new cart: %w", err)
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `
		SELECT id FROM shopping_list_items
		WHERE user_id = ? AND source = 'auto' AND purchased_at IS NOT NULL`, userID)
	if err != nil {
		return fmt.Errorf("could not start a new cart: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return fmt.Errorf("could not start a new cart: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("could not start a new cart: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("could not start a new cart: %w", err)
	}
	for _, id := range ids {
		if err := deleteShoppingRow(ctx, tx, id); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("could not start a new cart: %w", err)
	}
	return nil
}

func deleteShoppingRow(ctx context.Context, tx *sql.Tx, id string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM shopping_list_entry_adjustments WHERE entry_id = ?`, id); err != nil {
		return fmt.Errorf("could not clear shopping list item: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM shopping_list_items WHERE id = ?`, id); err != nil {
		return fmt.Errorf("could not clear shopping list item: %w", err)
	}
	return nil
}
