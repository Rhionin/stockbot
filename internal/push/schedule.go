package push

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/Rhionin/stockbot/internal/scan"
	"github.com/Rhionin/stockbot/internal/tenant"
)

// scheduledBatch is one stock-in or stock-out alert waiting out its quiet
// period. DueAt is the moment it should send. ScanIDs are the scans that
// were in the batch then, in scan order.
type scheduledBatch struct {
	Direction scan.ScanDirection
	DueAt     time.Time
	ScanIDs   []string
}

// scheduleStore is the durable copy of an open batch alert. *Subscriptions
// implements it; the two tables share the pantry database.
type scheduleStore interface {
	SaveBatchAlert(ctx context.Context, scope tenant.Scope, batch scheduledBatch) error
	DeleteBatchAlert(ctx context.Context, scope tenant.Scope, direction scan.ScanDirection) error
	ListBatchAlerts(ctx context.Context, scope tenant.Scope) ([]scheduledBatch, error)
}

// SaveBatchAlert inserts or replaces the open alert for a direction.
func (s *Subscriptions) SaveBatchAlert(ctx context.Context, scope tenant.Scope, batch scheduledBatch) error {
	if err := scope.Require(); err != nil {
		return err
	}
	if batch.Direction != scan.StockIn && batch.Direction != scan.StockOut {
		return fmt.Errorf("save batch alert: direction is not stock in or stock out")
	}
	if batch.DueAt.IsZero() || len(batch.ScanIDs) == 0 {
		return fmt.Errorf("save batch alert: missing a due time or a scan")
	}
	encoded, err := json.Marshal(batch.ScanIDs)
	if err != nil {
		return fmt.Errorf("save batch alert: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO scan_alert_schedule (household_id, direction, due_at, scan_ids)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(direction) DO UPDATE SET
			due_at = excluded.due_at,
			scan_ids = excluded.scan_ids
		WHERE scan_alert_schedule.household_id = excluded.household_id`,
		scope.HouseholdID(),
		string(batch.Direction),
		batch.DueAt.UTC().Format(time.RFC3339Nano),
		string(encoded),
	)
	if err != nil {
		return fmt.Errorf("save batch alert: %w", err)
	}
	return nil
}

// DeleteBatchAlert removes the open alert for a direction. A missing row is
// not an error.
func (s *Subscriptions) DeleteBatchAlert(ctx context.Context, scope tenant.Scope, direction scan.ScanDirection) error {
	if err := scope.Require(); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM scan_alert_schedule WHERE household_id = ? AND direction = ?`, scope.HouseholdID(), string(direction))
	if err != nil {
		return fmt.Errorf("delete batch alert: %w", err)
	}
	return nil
}

// ListBatchAlerts returns every open batch alert, stock-in first.
func (s *Subscriptions) ListBatchAlerts(ctx context.Context, scope tenant.Scope) ([]scheduledBatch, error) {
	if err := scope.Require(); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT direction, due_at, scan_ids
		FROM scan_alert_schedule
		WHERE household_id = ?
		ORDER BY direction`, scope.HouseholdID())
	if err != nil {
		return nil, fmt.Errorf("list batch alerts: %w", err)
	}
	defer rows.Close()
	var out []scheduledBatch
	for rows.Next() {
		var direction, dueRaw, idsRaw string
		if err := rows.Scan(&direction, &dueRaw, &idsRaw); err != nil {
			return nil, fmt.Errorf("list batch alerts: %w", err)
		}
		batch, ok := decodeBatchAlert(direction, dueRaw, idsRaw)
		if !ok {
			log.Printf("scan alerts: ignored a batch alert that could not be read")
			continue
		}
		out = append(out, batch)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list batch alerts: %w", err)
	}
	return out, nil
}

func decodeBatchAlert(direction, dueRaw, idsRaw string) (scheduledBatch, bool) {
	if direction != string(scan.StockIn) && direction != string(scan.StockOut) {
		return scheduledBatch{}, false
	}
	due, err := time.Parse(time.RFC3339Nano, dueRaw)
	if err != nil {
		return scheduledBatch{}, false
	}
	var ids []string
	if err := json.Unmarshal([]byte(idsRaw), &ids); err != nil {
		return scheduledBatch{}, false
	}
	if len(ids) == 0 {
		return scheduledBatch{}, false
	}
	return scheduledBatch{
		Direction: scan.ScanDirection(direction),
		DueAt:     due,
		ScanIDs:   ids,
	}, true
}
