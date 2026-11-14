package attention

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/Rhionin/stockbot/internal/cart"
	"github.com/Rhionin/stockbot/internal/shopping"
	"github.com/Rhionin/stockbot/internal/supply"
	"github.com/Rhionin/stockbot/internal/tenant"
)

// SendResultRow is one loaded cart_line_results row for attention evaluation.
type SendResultRow struct {
	ItemID      string
	Status      string
	Reason      string
	AttemptedAt string
	EntryID     string
}

// LoadSendResults loads failed, unknown, and skipped rows for one provider since cutoff.
func LoadSendResults(ctx context.Context, db *sql.DB, scope tenant.Scope, providerID string, cutoff time.Time) ([]SendResultRow, error) {
	if providerID == "" {
		return nil, nil
	}
	if err := scope.Require(); err != nil {
		return nil, err
	}
	cutoffStr := cutoff.UTC().Format(time.RFC3339)
	rows, err := db.QueryContext(ctx, `
		SELECT item_id, COALESCE(entry_id,''), status, COALESCE(reason,''), attempted_at
		FROM cart_line_results
		WHERE household_id = ? AND provider_id = ?
		  AND attempted_at >= ?
		  AND status IN ('failed','unknown','skipped')`,
		scope.HouseholdID(), providerID, cutoffStr)
	if err != nil {
		return nil, fmt.Errorf("could not load send results: %w", err)
	}
	defer rows.Close()
	var out []SendResultRow
	for rows.Next() {
		var r SendResultRow
		if err := rows.Scan(&r.ItemID, &r.EntryID, &r.Status, &r.Reason, &r.AttemptedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// LoadActiveZero returns line keys with an active person-set next-send 0 (#679 §5, §4.1).
func LoadActiveZero(ctx context.Context, db *sql.DB, scope tenant.Scope) (map[string]struct{}, error) {
	if err := scope.Require(); err != nil {
		return nil, err
	}
	// quantity = 0 ∧ ActiveAdjustmentSQL zero branch (expires_trip null or > trip_seq).
	query := `
		SELECT DISTINCT CASE WHEN COALESCE(i.group_id,'') <> '' THEN 'group:' || i.group_id ELSE 'item:' || i.item_id END
		FROM shopping_list_entry_adjustments a
		JOIN shopping_list_items i ON i.id = a.entry_id AND i.household_id = a.household_id
		JOIN households h ON h.id = a.household_id
		WHERE a.household_id = ? AND a.quantity = 0 AND i.purchased_at IS NULL
		  AND ` + shopping.ActiveAdjustmentSQL
	rows, err := db.QueryContext(ctx, query, scope.HouseholdID())
	if err != nil {
		return nil, fmt.Errorf("could not load active zero keys: %w", err)
	}
	defer rows.Close()
	out := map[string]struct{}{}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, err
		}
		out[key] = struct{}{}
	}
	return out, rows.Err()
}

// LoadItemProductGroups maps item id → group id for group membership (send key step 3).
func LoadItemProductGroups(ctx context.Context, db *sql.DB, scope tenant.Scope, itemIDs []string) (map[string]string, error) {
	if len(itemIDs) == 0 {
		return map[string]string{}, nil
	}
	if err := scope.Require(); err != nil {
		return nil, err
	}
	placeholders := sqlPlaceholders(len(itemIDs))
	query := `
		SELECT i.id, m.group_id
		FROM items i
		JOIN product_group_members m ON m.product_id = i.product_id AND m.household_id = i.household_id
		WHERE i.household_id = ? AND i.id IN (` + placeholders + `)`
	args := make([]any, 0, 1+len(itemIDs))
	args = append(args, scope.HouseholdID())
	for _, id := range itemIDs {
		args = append(args, id)
	}
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("could not load item groups: %w", err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var itemID, groupID string
		if err := rows.Scan(&itemID, &groupID); err != nil {
			return nil, err
		}
		out[itemID] = groupID
	}
	return out, rows.Err()
}

func sqlPlaceholders(n int) string {
	if n <= 0 {
		return ""
	}
	s := "?"
	for i := 1; i < n; i++ {
		s += ",?"
	}
	return s
}

// MapSendResultsByLineKey groups send rows under line keys and merges members (§6.2).
func MapSendResultsByLineKey(
	rows []SendResultRow,
	stagedByItem map[string]StagedRow,
	planViews map[string]supply.LinePlanView,
	itemProductGroup map[string]string,
) map[string][]SendResultRow {
	byLine := map[string][]SendResultRow{}
	for _, row := range rows {
		key := LineKeyForItemSend(row.ItemID, stagedByItem, planViews, itemProductGroup)
		byLine[key] = append(byLine[key], row)
	}
	return byLine
}

// CutoffForSendRetention is cart.SendResultRetention before now (UTC).
func CutoffForSendRetention(now time.Time) time.Time {
	return now.UTC().Add(-cart.SendResultRetention)
}