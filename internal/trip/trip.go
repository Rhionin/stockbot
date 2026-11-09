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
	minProducts     = 5
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
	return nil
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

// MaybeCloseMassStockIn advances trip_seq when a qualifying put-away cluster has closed.
// now is the commit clock; only the cluster containing the latest stock-in is considered.
func MaybeCloseMassStockIn(ctx context.Context, scope tenant.Scope, tx *sql.Tx, now time.Time) error {
	if err := scope.Require(); err != nil {
		return err
	}
	household := scope.HouseholdID()
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

	points, err := scanStockInPoints(ctx, tx, household)
	if err != nil {
		return err
	}
	closed := clusterForLast(points)
	if closed == nil {
		return nil
	}
	if len(closed.products) < minProducts {
		return nil
	}
	if now.Before(closed.endedAt.Add(clusterClose)) {
		return nil
	}
	started := closed.startedAt.UTC().Format(timeLayout)
	res, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO stock_in_trip_closures (household_id, cluster_started_at)
		VALUES (?, ?)`, household, started)
	if err != nil {
		return fmt.Errorf("could not record mass stock-in trip: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("could not record mass stock-in trip: %w", err)
	}
	if n == 0 {
		return nil
	}
	return AdvanceSeq(ctx, tx, household, CloseCausePutAway, now)
}

func scanStockInPoints(ctx context.Context, tx *sql.Tx, household string) ([]stockInPoint, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT DISTINCT m.product_id, m.at
		FROM stock_moves m
		WHERE m.household_id = ?
		  AND m.direction = 'in'
		  AND m.source = 'scan'
		  AND m.tracked = 1
		ORDER BY m.at`, household)
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

func clusterForLast(points []stockInPoint) *cluster {
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
	g := groups[len(groups)-1]
	c := cluster{products: map[string]struct{}{}}
	for _, p := range g {
		if c.startedAt.IsZero() || p.at.Before(c.startedAt) {
			c.startedAt = p.at
		}
		if p.at.After(c.endedAt) {
			c.endedAt = p.at
		}
		c.products[p.productID] = struct{}{}
	}
	return &c
}

func parseTime(raw string) (time.Time, error) {
	if i := strings.IndexAny(raw, "Z"); i > 0 {
		raw = raw[:i] + "Z"
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, timeLayoutAlt, timeLayoutShort} {
		if t, err := time.Parse(layout, raw); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognized time %q", raw)
}
