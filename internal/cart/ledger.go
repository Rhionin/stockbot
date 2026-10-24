package cart

import (
	"context"
	"database/sql"
	"time"

	"github.com/Rhionin/stockbot/internal/tenant"
)

// Ledger is the data-access type for the fulfillment ledger.
// Named for the domain concept per AGENTS.md — not a `Repo`, not a bare `Store`.
type Ledger struct {
	db *sql.DB
}

// NewLedger creates a new Ledger backed by the given database.
func NewLedger(db *sql.DB) *Ledger {
	return &Ledger{db: db}
}

// ListForProvider returns all ledger entries for one provider.
// The dominant access is "every entry for one provider, read at once", so the
// SQL is one query, then O(1) per entry, not a per-item read inside the entry loop.
// A missing key in the returned map is the "no entry exists" case, resolved by
// the caller to Requested: 0 with a boundary preceding every consumption event.
func (l *Ledger) ListForProvider(ctx context.Context, scope tenant.Scope, p ProviderID) (map[string]LedgerEntry, error) {
	if err := scope.Require(); err != nil {
		return nil, err
	}
	rows, err := l.db.QueryContext(ctx,
		`SELECT item_id, requested_quantity, ledger_boundary
		 FROM fulfillment_ledger
		 WHERE household_id = ? AND provider_id = ?
		 ORDER BY item_id`, scope.HouseholdID(), string(p))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[string]LedgerEntry)
	for rows.Next() {
		var entry LedgerEntry
		if err := rows.Scan(&entry.ItemID, &entry.Requested, &entry.Boundary); err != nil {
			return nil, err
		}
		result[entry.ItemID] = entry
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return result, nil
}

// AdvanceTx adds qty to the requested_quantity for one provider/item inside an
// existing transaction, so the advance is atomic with the caller's other
// statements (clearing adjustments) and never contends for a second connection
// against a single-connection SQLite pool.
// The advance is an INSERT ... ON CONFLICT(household_id, provider_id, item_id) DO UPDATE SET
// requested_quantity = requested_quantity + excluded.requested_quantity, performing
// the read-modify-write in SQL so no lost update is possible.
// ledger_boundary is absent from the SET list, expressing "an advance moves no boundary"
// as an absent column rather than a rule to remember.
// The advance is additionally conditional on the boundary the operation computed against
// (WHERE ledger_boundary = ?). Zero rows affected means a stock-in intervened.
func (l *Ledger) AdvanceTx(ctx context.Context, scope tenant.Scope, tx *sql.Tx, provider ProviderID, itemID string, qty int, boundary time.Time) (int64, error) {
	if err := scope.Require(); err != nil {
		return 0, err
	}
	result, err := tx.ExecContext(ctx,
		`INSERT INTO fulfillment_ledger (household_id, provider_id, item_id, requested_quantity, ledger_boundary)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(household_id, provider_id, item_id) DO UPDATE SET
		     requested_quantity = fulfillment_ledger.requested_quantity + excluded.requested_quantity
		 WHERE fulfillment_ledger.ledger_boundary = ? AND fulfillment_ledger.household_id = ?`,
		scope.HouseholdID(), string(provider), itemID, qty, boundary, boundary, scope.HouseholdID())
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// boundaryTx returns the stored ledger boundary for one provider and item.
// ok is false when no row exists, which the caller treats as a boundary
// preceding every consumption event.
func (l *Ledger) boundaryTx(ctx context.Context, scope tenant.Scope, tx *sql.Tx, provider ProviderID, itemID string) (time.Time, bool, error) {
	if err := scope.Require(); err != nil {
		return time.Time{}, false, err
	}
	var boundary time.Time
	err := tx.QueryRowContext(ctx,
		`SELECT ledger_boundary FROM fulfillment_ledger WHERE household_id = ? AND provider_id = ? AND item_id = ?`,
		scope.HouseholdID(), string(provider), itemID).Scan(&boundary)
	if err == sql.ErrNoRows {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, err
	}
	return boundary, true, nil
}

// Advance opens its own transaction and adds qty to the requested quantity.
// An existing row keeps its boundary; a new row records boundary as now.
func (l *Ledger) Advance(ctx context.Context, scope tenant.Scope, provider ProviderID, itemID string, qty int) error {
	if err := scope.Require(); err != nil {
		return err
	}
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	boundary, ok, err := l.boundaryTx(ctx, scope, tx, provider, itemID)
	if err != nil {
		return err
	}
	if !ok {
		boundary = time.Now().UTC()
	}
	if _, err := l.AdvanceTx(ctx, scope, tx, provider, itemID, qty, boundary); err != nil {
		return err
	}
	return tx.Commit()
}

// ResetForItemTx resets the ledger entry for one item inside an existing transaction.
// This is the only transaction-bound method, existing so that every statement
// touching fulfillment_ledger stays in one package.
// The caller must pass the provider_id since the table has provider_id as part of
// the primary key.
func (l *Ledger) ResetForItemTx(ctx context.Context, scope tenant.Scope, tx *sql.Tx, provider ProviderID, itemID string, at time.Time) error {
	if err := scope.Require(); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx,
		`INSERT INTO fulfillment_ledger (household_id, provider_id, item_id, requested_quantity, ledger_boundary)
		 VALUES (?, ?, ?, 0, ?)
		 ON CONFLICT(household_id, provider_id, item_id) DO UPDATE SET
		     requested_quantity = 0, ledger_boundary = ?
		 WHERE fulfillment_ledger.household_id = ?`,
		scope.HouseholdID(), string(provider), itemID, at, at, scope.HouseholdID())
	return err
}

// ResetForProvider resets all ledger entries for one provider.
func (l *Ledger) ResetForProvider(ctx context.Context, scope tenant.Scope, provider ProviderID) error {
	if err := scope.Require(); err != nil {
		return err
	}
	_, err := l.db.ExecContext(ctx,
		`UPDATE fulfillment_ledger SET requested_quantity = 0, ledger_boundary = ? WHERE household_id = ? AND provider_id = ?`,
		time.Now().UTC(), scope.HouseholdID(), string(provider))
	return err
}
