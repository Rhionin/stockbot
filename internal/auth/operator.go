package auth

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// OperatorHouseholdSummary is one household row on the operator admin list.
type OperatorHouseholdSummary struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	IsPrimary       bool   `json:"isPrimary"`
	RestockMode     string `json:"restockMode"`
	CreatedAt       string `json:"createdAt"`
	DeletedAt       string `json:"deletedAt,omitempty"`
	PurgeAfter      string `json:"purgeAfter,omitempty"`
	Members         []any  `json:"members"`
	MemberCount     int    `json:"memberCount"`
	ProductCount    int    `json:"productCount"`
	ItemsOnHand     int    `json:"itemsOnHand"`
	PendingScans    int    `json:"pendingScans"`
	ScannersOnline  int    `json:"scannersOnline"`
	ScannersOffline int    `json:"scannersOffline"`
	// ScansThisWeek counts physical scans received in the rolling seven-day window
	// ending at the service clock. Device scans come from device_scan_receipts; web
	// and API scans use unit_count on scan_entries that have no device receipts.
	ScansThisWeek    int    `json:"scansThisWeek"`
	LastActivityAt   string `json:"lastActivityAt,omitempty"`
}

// ListAllHouseholdsForOperator returns every household in the product for the operator account.
func (s *Service) ListAllHouseholdsForOperator(ctx context.Context) ([]OperatorHouseholdSummary, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			h.id,
			h.name,
			h.is_primary,
			COALESCE(NULLIF(h.restock_mode, ''), 'auto'),
			h.created_at,
			COALESCE(h.deleted_at, ''),
			COALESCE(h.purge_after, ''),
			(SELECT COUNT(*) FROM memberships m WHERE m.household_id = h.id),
			(SELECT COUNT(DISTINCT i.product_id) FROM items i WHERE i.household_id = h.id),
			(SELECT COUNT(*) FROM item_instances inst
			 WHERE inst.household_id = h.id AND inst.removed_at IS NULL),
			(SELECT COUNT(*) FROM scan_entries se
			 WHERE se.household_id = h.id AND se.status = 'pending'),
			COALESCE((SELECT MAX(ts) FROM (
				SELECT sm.at AS ts FROM stock_moves sm WHERE sm.household_id = h.id
				UNION ALL
				SELECT se.scanned_at AS ts FROM scan_entries se WHERE se.household_id = h.id
			)), '')
		FROM households h
		ORDER BY h.is_primary DESC, h.created_at ASC, h.id ASC`)
	if err != nil {
		return nil, AsError(err)
	}
	defer rows.Close()

	out := []OperatorHouseholdSummary{}
	for rows.Next() {
		var row OperatorHouseholdSummary
		var primary int
		var deleted, purge, lastActivity string
		if err := rows.Scan(
			&row.ID, &row.Name, &primary, &row.RestockMode, &row.CreatedAt,
			&deleted, &purge, &row.MemberCount, &row.ProductCount, &row.ItemsOnHand,
			&row.PendingScans, &lastActivity,
		); err != nil {
			return nil, AsError(err)
		}
		row.IsPrimary = primary != 0
		row.DeletedAt = strings.TrimSpace(deleted)
		row.PurgeAfter = strings.TrimSpace(purge)
		row.LastActivityAt = strings.TrimSpace(lastActivity)
		row.Members = []any{}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, AsError(err)
	}
	if len(out) == 0 {
		return out, nil
	}
	if err := s.attachOperatorDeviceCounts(ctx, out); err != nil {
		return nil, err
	}
	if err := s.attachOperatorScansThisWeek(ctx, out); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Service) attachOperatorDeviceCounts(ctx context.Context, households []OperatorHouseholdSummary) error {
	rows, err := s.db.QueryContext(ctx, `
		SELECT household_id, id, kind, COALESCE(last_seen_at, ''), COALESCE(revoked_at, '')
		FROM devices`)
	if err != nil {
		return AsError(err)
	}
	defer rows.Close()
	now := s.clock()
	online := map[string]int{}
	offline := map[string]int{}
	for rows.Next() {
		var houseID, deviceID, kind, seen, revoked string
		if err := rows.Scan(&houseID, &deviceID, &kind, &seen, &revoked); err != nil {
			return AsError(err)
		}
		if strings.TrimSpace(revoked) != "" {
			continue
		}
		if s.deviceOnlineAt(now, deviceID, kind, seen) {
			online[houseID]++
		} else {
			offline[houseID]++
		}
	}
	if err := rows.Err(); err != nil {
		return AsError(err)
	}
	for i := range households {
		households[i].ScannersOnline = online[households[i].ID]
		households[i].ScannersOffline = offline[households[i].ID]
	}
	return nil
}

func (s *Service) attachOperatorScansThisWeek(ctx context.Context, households []OperatorHouseholdSummary) error {
	now := s.clock()
	cutoff := stamp(now.Add(-7 * 24 * time.Hour))
	counts := map[string]int{}

	receiptRows, err := s.db.QueryContext(ctx, `
		SELECT household_id, COUNT(*)
		FROM device_scan_receipts
		WHERE scanned_at >= ?
		GROUP BY household_id`, cutoff)
	if err != nil {
		return AsError(err)
	}
	defer receiptRows.Close()
	for receiptRows.Next() {
		var houseID string
		var n int
		if err := receiptRows.Scan(&houseID, &n); err != nil {
			return AsError(err)
		}
		counts[houseID] += n
	}
	if err := receiptRows.Err(); err != nil {
		return AsError(err)
	}

	webRows, err := s.db.QueryContext(ctx, `
		SELECT e.household_id, COALESCE(SUM(e.unit_count), 0)
		FROM scan_entries e
		WHERE e.scanned_at >= ?
		  AND NOT EXISTS (
		    SELECT 1 FROM device_scan_receipts r
		    WHERE r.household_id = e.household_id AND r.scan_entry_id = e.id
		  )
		GROUP BY e.household_id`, cutoff)
	if err != nil {
		return AsError(err)
	}
	defer webRows.Close()
	for webRows.Next() {
		var houseID string
		var n int
		if err := webRows.Scan(&houseID, &n); err != nil {
			return AsError(err)
		}
		counts[houseID] += n
	}
	if err := webRows.Err(); err != nil {
		return AsError(err)
	}

	for i := range households {
		households[i].ScansThisWeek = counts[households[i].ID]
	}
	return nil
}

// OperatorForbidden is returned when a signed-in user who is not the operator calls an operator route.
func OperatorForbidden() *Error {
	return fail(CodeForbiddenOperator, SentenceForbiddenOperator)
}

// IsOperator reports whether userID is the product operator account.
func (s *Service) IsOperator(ctx context.Context, userID string) (bool, error) {
	var op int
	err := s.db.QueryRowContext(ctx, `SELECT operator FROM users WHERE id = ? AND deleted_at IS NULL`, userID).Scan(&op)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, AsError(err)
	}
	return op == 1, nil
}
