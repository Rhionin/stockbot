// Package suggestion records consumption events. How many units to buy is decided by the supply plan.
package suggestion

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Rhionin/stockbot/internal/tenant"
	"github.com/google/uuid"
)

// ConsumptionEvent records a single stock-out event for suggestion analytics.
type ConsumptionEvent struct {
	ID          string    `json:"id"`
	ItemID      string    `json:"itemId"`
	ConsumedAt  time.Time `json:"consumedAt"`
	ScanEntryID *string   `json:"scanEntryId"` // optional: links back to the scan entry that caused the consumption
}

// ConsumptionLog provides database operations for consumption events.
type ConsumptionLog struct {
	db *sql.DB
}

// NewConsumptionLog creates a new ConsumptionLog with the given database connection.
func NewConsumptionLog(db *sql.DB) *ConsumptionLog {
	return &ConsumptionLog{db: db}
}

// InsertConsumptionEvent inserts a new consumption event record.
// If event.ID is empty, a new UUID is generated.
func (r *ConsumptionLog) InsertConsumptionEvent(ctx context.Context, scope tenant.Scope, event ConsumptionEvent) (*ConsumptionEvent, error) {
	if err := scope.Require(); err != nil {
		return nil, err
	}
	if event.ID == "" {
		event.ID = uuid.NewString()
	}

	_, err := r.db.ExecContext(ctx,
		`INSERT INTO consumption_events (id, household_id, item_id, consumed_at, scan_entry_id)
		 VALUES (?, ?, ?, ?, ?)`,
		event.ID,
		scope.HouseholdID(),
		event.ItemID,
		event.ConsumedAt,
		nullableString(event.ScanEntryID),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to insert consumption event: %w", err)
	}

	return r.getByID(ctx, scope, event.ID)
}

// ListConsumptionEvents returns all consumption events for the given itemID,
// ordered ascending by consumed_at (oldest first).
func (r *ConsumptionLog) ListConsumptionEvents(ctx context.Context, scope tenant.Scope, itemID string) ([]ConsumptionEvent, error) {
	if err := scope.Require(); err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, item_id, consumed_at, scan_entry_id
		 FROM consumption_events
		 WHERE household_id = ? AND item_id = ?
		 ORDER BY consumed_at ASC`,
		scope.HouseholdID(),
		itemID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list consumption events: %w", err)
	}
	defer rows.Close()

	var events []ConsumptionEvent
	for rows.Next() {
		event, err := scanConsumptionEvent(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to read consumption event: %w", err)
		}
		events = append(events, *event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate consumption events: %w", err)
	}
	return events, nil
}

// getByID fetches a single consumption event by its ID.
func (r *ConsumptionLog) getByID(ctx context.Context, scope tenant.Scope, id string) (*ConsumptionEvent, error) {
	if err := scope.Require(); err != nil {
		return nil, err
	}
	row := r.db.QueryRowContext(ctx,
		`SELECT id, item_id, consumed_at, scan_entry_id
		 FROM consumption_events WHERE household_id = ? AND id = ?`,
		scope.HouseholdID(),
		id,
	)

	event, err := scanConsumptionEvent(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get consumption event: %w", err)
	}
	return event, nil
}

// scanner is a common interface for *sql.Row and *sql.Rows.
type scanner interface {
	Scan(dest ...interface{}) error
}

// scanConsumptionEvent scans a row into a ConsumptionEvent.
func scanConsumptionEvent(row scanner) (*ConsumptionEvent, error) {
	var event ConsumptionEvent
	var scanEntryID sql.NullString

	err := row.Scan(
		&event.ID,
		&event.ItemID,
		&event.ConsumedAt,
		&scanEntryID,
	)
	if err != nil {
		return nil, err
	}

	if scanEntryID.Valid {
		event.ScanEntryID = &scanEntryID.String
	}

	return &event, nil
}

// ListConsumedAtByItems returns consumption timestamps for multiple items.
// One query returns (item_id, consumed_at) pairs, then we count per item's boundary.
// Not a per-item COUNT(*) WHERE consumed_at > ? because each item has its own boundary,
// and one query per item would serialize against SetMaxOpenConns(1).
func (r *ConsumptionLog) ListConsumedAtByItems(ctx context.Context, scope tenant.Scope, itemIDs []string) (map[string][]time.Time, error) {
	if err := scope.Require(); err != nil {
		return nil, err
	}
	if len(itemIDs) == 0 {
		return make(map[string][]time.Time), nil
	}

	// Create placeholders for IN clause
	placeholders := make([]string, len(itemIDs))
	args := make([]interface{}, 0, len(itemIDs)+1)
	args = append(args, scope.HouseholdID())
	for i, id := range itemIDs {
		placeholders[i] = "?"
		args = append(args, id)
	}

	// Return every event. Each item has its own ledger boundary, so the
	// caller counts events after that boundary in Go.
	rows, err := r.db.QueryContext(ctx,
		`SELECT item_id, consumed_at FROM consumption_events
		 WHERE household_id = ? AND item_id IN (`+strings.Join(placeholders, ",")+`)
		 ORDER BY consumed_at`,
		args...,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list consumed at by items: %w", err)
	}
	defer rows.Close()

	result := make(map[string][]time.Time)
	for rows.Next() {
		var itemID string
		var consumedAt time.Time
		if err := rows.Scan(&itemID, &consumedAt); err != nil {
			return nil, fmt.Errorf("failed to read consumed event: %w", err)
		}
		result[itemID] = append(result[itemID], consumedAt)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate consumed events: %w", err)
	}

	return result, nil
}

// nullableString converts a *string to sql.NullString for nullable TEXT columns.
func nullableString(s *string) sql.NullString {
	if s == nil || *s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: *s, Valid: true}
}
