// Package trip tracks household shopping trips for one-trip staged-cart skips.
package trip

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/Rhionin/stockbot/internal/tenant"
)

const (
	clusterGap      = 5 * time.Minute
	clusterClose    = 5 * time.Minute
	timeLayout      = "2006-01-02T15:04:05.999999999Z"
	timeLayoutAlt   = "2006-01-02 15:04:05.999999999 -0700 MST"
	timeLayoutShort = "2006-01-02 15:04:05"

	// CloseCauseNewCart is set when the household starts a new cart.
	CloseCauseNewCart = "new_cart"
	// CloseCausePutAway is set when a mass stock-in cluster closes a trip.
	CloseCausePutAway = "put_away"
)

type stockInPoint struct {
	productID string
	at        time.Time
}

type cluster struct {
	startedAt time.Time
	endedAt   time.Time
	products  map[string]struct{}
}

// MaintainSkips purges expired skips and records any put-away clusters ready to close.
// It may advance trip_seq when rules allow. Call from list maintenance paths and stock-in commit.
func MaintainSkips(ctx context.Context, scope tenant.Scope, tx *sql.Tx, now time.Time) error {
	if err := scope.Require(); err != nil {
		return err
	}
	household := scope.HouseholdID()
	if err := PurgeExpiredSkips(ctx, tx, household); err != nil {
		return err
	}
	return closeReadyPutAwayClusters(ctx, scope, tx, now)
}

// PurgeExpiredSkips deletes skips the household has outlived at the current trip_seq.
func PurgeExpiredSkips(ctx context.Context, tx *sql.Tx, householdID string) error {
	_, err := tx.ExecContext(ctx, `
		DELETE FROM staged_cart_skips
		WHERE household_id = ?
		  AND expires_trip <= (SELECT trip_seq FROM households WHERE id = ?)`,
		householdID, householdID)
	if err != nil {
		return fmt.Errorf("could not clear expired skips: %w", err)
	}
	return nil
}

// AdvanceTripForNewCart advances trip_seq after a new cart, subject to no-empty-trip rules.
func AdvanceTripForNewCart(ctx context.Context, tx *sql.Tx, householdID string, now time.Time) error {
	return advanceTripBoundary(ctx, tx, householdID, CloseCauseNewCart, now)
}

// MaybeCloseMassStockIn is kept for call-site compatibility; it runs full skip maintenance.
func MaybeCloseMassStockIn(ctx context.Context, scope tenant.Scope, tx *sql.Tx, now time.Time) error {
	return MaintainSkips(ctx, scope, tx, now)
}

func closeReadyPutAwayClusters(ctx context.Context, scope tenant.Scope, tx *sql.Tx, now time.Time) error {
	household := scope.HouseholdID()
	if err := scope.Require(); err != nil {
		return err
	}
	var pending int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM scan_entries
		WHERE household_id = ? AND direction = 'stock_in' AND status = 'pending'`,
		household).Scan(&pending); err != nil {
		return fmt.Errorf("could not read pending stock-in scans: %w", err)
	}
	if pending > 0 {
		return nil
	}
	k, err := householdClusterMin(ctx, tx, household)
	if err != nil {
		return err
	}
	since, err := scanSinceLastCluster(ctx, tx, household)
	if err != nil {
		return err
	}
	points, err := scanStockInPointsSince(ctx, tx, household, since)
	if err != nil {
		return err
	}
	clusters := allClusters(points)
	advancedRun := false
	for _, c := range clusters {
		if len(c.products) < k {
			continue
		}
		if now.Before(c.endedAt.Add(clusterClose)) {
			continue
		}
		started := c.startedAt.UTC().Format(timeLayout)
		var exists int
		if err := tx.QueryRowContext(ctx, `
			SELECT 1 FROM stock_in_clusters WHERE household_id = ? AND started_at = ?`,
			household, started).Scan(&exists); err == nil {
			continue
		} else if err != sql.ErrNoRows {
			return fmt.Errorf("could not read stock-in cluster: %w", err)
		}
		tripSeq, err := currentTripSeq(ctx, tx, household)
		if err != nil {
			return err
		}
		if !advancedRun {
			advanced, err := tryAdvanceTrip(ctx, tx, household, CloseCausePutAway, now)
			if err != nil {
				return err
			}
			if advanced {
				advancedRun = true
				tripSeq, err = currentTripSeq(ctx, tx, household)
				if err != nil {
					return err
				}
			}
		}
		closedAt := now.UTC().Format(time.RFC3339Nano)
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO stock_in_clusters (household_id, started_at, closed_at, product_count, k_used, trip_seq)
			VALUES (?, ?, ?, ?, ?, ?)`,
			household, started, closedAt, len(c.products), k, tripSeq,
		); err != nil {
			return fmt.Errorf("could not record stock-in cluster: %w", err)
		}
	}
	return nil
}

func advanceTripBoundary(ctx context.Context, tx *sql.Tx, householdID, cause string, now time.Time) error {
	advanced, err := tryAdvanceTrip(ctx, tx, householdID, cause, now)
	if err != nil {
		return err
	}
	if !advanced {
		return nil
	}
	return nil
}

func tryAdvanceTrip(ctx context.Context, tx *sql.Tx, householdID, cause string, now time.Time) (bool, error) {
	if householdID == "" {
		return false, fmt.Errorf("missing household")
	}
	var closedAt sql.NullString
	if err := tx.QueryRowContext(ctx,
		`SELECT trip_closed_at FROM households WHERE id = ?`, householdID).Scan(&closedAt); err != nil {
		return false, fmt.Errorf("could not read household trip: %w", err)
	}
	if closedAt.Valid && closedAt.String != "" {
		ok, err := hasTripActivitySince(ctx, tx, householdID, closedAt.String)
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}
	}
	if err := AdvanceSeq(ctx, tx, householdID, cause, now); err != nil {
		return false, err
	}
	return true, nil
}

func hasTripActivitySince(ctx context.Context, tx *sql.Tx, householdID, since string) (bool, error) {
	var n int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM staged_cart_skips
		WHERE household_id = ? AND COALESCE(removed_at, '') > ?`,
		householdID, since).Scan(&n); err != nil {
		return false, err
	}
	if n > 0 {
		return true, nil
	}
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM shopping_list_entry_adjustments
		WHERE household_id = ? AND expires_trip IS NOT NULL AND COALESCE(set_at, '') > ?`,
		householdID, since).Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}

// AdvanceSeq bumps the household trip counter by one inside an existing transaction
// and records when and why the prior trip closed.
func AdvanceSeq(ctx context.Context, tx *sql.Tx, householdID, cause string, closedAt time.Time) error {
	if householdID == "" {
		return fmt.Errorf("missing household")
	}
	if cause != CloseCauseNewCart && cause != CloseCausePutAway {
		return fmt.Errorf("unknown trip close cause")
	}
	closed := closedAt.UTC().Format(time.RFC3339Nano)
	res, err := tx.ExecContext(ctx,
		`UPDATE households SET trip_seq = trip_seq + 1, trip_closed_at = ?, trip_closed_cause = ? WHERE id = ?`,
		closed, cause, householdID)
	if err != nil {
		return fmt.Errorf("could not advance trip: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("could not advance trip: %w", err)
	}
	if n != 1 {
		return fmt.Errorf("household not found")
	}
	_, err = tx.ExecContext(ctx, `
		DELETE FROM list_line_changes
		WHERE household_id = ? AND trip_seq < (SELECT trip_seq FROM households WHERE id = ?)`,
		householdID, householdID)
	if err != nil {
		return fmt.Errorf("could not clear stale list changes: %w", err)
	}
	return nil
}

func currentTripSeq(ctx context.Context, tx *sql.Tx, household string) (int, error) {
	var seq int
	if err := tx.QueryRowContext(ctx, `SELECT trip_seq FROM households WHERE id = ?`, household).Scan(&seq); err != nil {
		return 0, fmt.Errorf("could not read trip_seq: %w", err)
	}
	return seq, nil
}

func householdClusterMin(ctx context.Context, tx *sql.Tx, household string) (int, error) {
	var k int
	if err := tx.QueryRowContext(ctx, `SELECT stock_in_cluster_min FROM households WHERE id = ?`, household).Scan(&k); err != nil {
		return 0, fmt.Errorf("could not read stock-in cluster minimum: %w", err)
	}
	return k, nil
}

func scanSinceLastCluster(ctx context.Context, tx *sql.Tx, household string) (time.Time, error) {
	var closed sql.NullString
	err := tx.QueryRowContext(ctx, `
		SELECT closed_at FROM stock_in_clusters
		WHERE household_id = ? AND closed_at IS NOT NULL
		ORDER BY closed_at DESC LIMIT 1`, household).Scan(&closed)
	if err == sql.ErrNoRows {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("could not read last cluster: %w", err)
	}
	if !closed.Valid {
		return time.Time{}, nil
	}
	t, err := parseTime(closed.String)
	if err != nil {
		return time.Time{}, err
	}
	return t, nil
}

func scanStockInPointsSince(ctx context.Context, tx *sql.Tx, household string, since time.Time) ([]stockInPoint, error) {
	query := `
		SELECT DISTINCT m.product_id, m.at
		FROM stock_moves m
		WHERE m.household_id = ?
		  AND m.direction = 'in'
		  AND m.source = 'scan'
		  AND m.tracked = 1`
	args := []any{household}
	if !since.IsZero() {
		query += ` AND m.at >= ?`
		args = append(args, since.UTC().Format(timeLayout))
	}
	query += ` ORDER BY m.at`
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("could not read stock-in cluster: %w", err)
	}
	defer rows.Close()
	var out []stockInPoint
	for rows.Next() {
		var productID, atRaw string
		if err := rows.Scan(&productID, &atRaw); err != nil {
			return nil, fmt.Errorf("could not read stock-in cluster: %w", err)
		}
		at, err := parseTime(atRaw)
		if err != nil {
			return nil, fmt.Errorf("could not read stock-in time: %w", err)
		}
		out = append(out, stockInPoint{productID: productID, at: at})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("could not read stock-in cluster: %w", err)
	}
	return out, nil
}

func allClusters(points []stockInPoint) []*cluster {
	if len(points) == 0 {
		return nil
	}
	var groups [][]stockInPoint
	cur := []stockInPoint{points[0]}
	for i := 1; i < len(points); i++ {
		if points[i].at.Sub(points[i-1].at) > clusterGap {
			groups = append(groups, cur)
			cur = []stockInPoint{points[i]}
			continue
		}
		cur = append(cur, points[i])
	}
	groups = append(groups, cur)
	out := make([]*cluster, 0, len(groups))
	for _, g := range groups {
		c := &cluster{products: map[string]struct{}{}}
		for _, p := range g {
			if c.startedAt.IsZero() || p.at.Before(c.startedAt) {
				c.startedAt = p.at
			}
			if p.at.After(c.endedAt) {
				c.endedAt = p.at
			}
			c.products[p.productID] = struct{}{}
		}
		out = append(out, c)
	}
	return out
}

func parseTime(raw string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, timeLayoutAlt, timeLayoutShort} {
		if t, err := time.Parse(layout, raw); err == nil {
			return t, nil
		}
	}
	if i := strings.LastIndex(raw, " "); i > 0 {
		if t, err := time.ParseInLocation(timeLayoutShort, strings.TrimSpace(raw[:i]), time.UTC); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognized time %q", raw)
}
