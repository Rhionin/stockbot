package scan

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Rhionin/stockbot/internal/changes"
	"github.com/Rhionin/stockbot/internal/inventory"
	"github.com/Rhionin/stockbot/internal/product"
	"github.com/Rhionin/stockbot/internal/tenant"
	"github.com/google/uuid"
)

// CreateScanEntryTx inserts or merges a scan entry inside an existing transaction.
// It does not commit or publish live events.
func (r *Queue) CreateScanEntryTx(ctx context.Context, scope tenant.Scope, tx *sql.Tx, entry ScanEntry) (*ScanEntry, error) {
	if err := scope.Require(); err != nil {
		return nil, err
	}
	entry.UserID = scopeUserID(scope, entry.UserID)
	existing, err := r.findMergeableScanEntryTx(ctx, scope, tx, entry.UserID, entry.Barcode, entry.Direction)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		delta := entry.UnitCount
		if delta <= 0 {
			delta = 1
		}
		scannedAt := entry.ScannedAt
		if scannedAt.IsZero() {
			scannedAt = time.Now().UTC()
		}
		if !scannedAt.After(existing.ScannedAt) {
			scannedAt = existing.ScannedAt
		}
		if err := r.recordMergedScanTx(ctx, scope, tx, existing.ID, existing.UnitCount+delta, scannedAt); err != nil {
			return nil, err
		}
		return r.getScanEntryTx(ctx, scope, tx, existing.ID)
	}

	if entry.ID == "" {
		entry.ID = uuid.NewString()
	}
	if entry.Status == "" {
		entry.Status = Pending
	}

	var directionStr *string
	if entry.Direction != nil {
		directionStr = ptr(string(*entry.Direction))
	}

	_, err = tx.ExecContext(ctx,
		`INSERT INTO scan_entries (id, user_id, household_id, device_id, client_scan_id, barcode, scanned_at, direction, unit_count, expires_at, status, product_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		entry.ID,
		entry.UserID,
		scope.HouseholdID(),
		nullableString(ptr(entry.DeviceID)),
		nullableString(ptr(entry.ClientScanID)),
		entry.Barcode,
		entry.ScannedAt,
		nullableString(directionStr),
		entry.UnitCount,
		nullableTime(entry.ExpiresAt),
		string(entry.Status),
		nullableString(entry.ProductID),
	)
	if err != nil {
		return nil, fmt.Errorf("CreateScanEntryTx: %w", err)
	}
	if err := changes.Bump(ctx, tx, scope.HouseholdID(), changes.ScopeScans); err != nil {
		return nil, fmt.Errorf("CreateScanEntryTx: %w", err)
	}
	return r.getScanEntryTx(ctx, scope, tx, entry.ID)
}

func (r *Queue) findMergeableScanEntryTx(ctx context.Context, scope tenant.Scope, tx *sql.Tx, userID, barcode string, direction *ScanDirection) (*ScanEntry, error) {
	var directionStr *string
	if direction != nil {
		directionStr = ptr(string(*direction))
	}

	row := tx.QueryRowContext(ctx, `
		SELECT 
			se.id, se.user_id, se.barcode, se.scanned_at, se.direction, se.unit_count, 
			se.expires_at, se.status, se.product_id, se.committed_at, se.created_at,
			se.removed_count, se.requested_count,
			p.id, `+product.DisplayNameSQL("p")+`, COALESCE(p.category, ''), COALESCE(p.unit_of_measure, ''), COALESCE(p.image_url, ''), COALESCE(p.external_source, ''), p.pack_count
		FROM scan_entries se
		LEFT JOIN products p ON p.id = se.product_id
		WHERE se.household_id = ? AND se.user_id = ? AND se.barcode = ? AND se.direction IS ? AND se.status IN ('pending', 'flagged')
		ORDER BY se.scanned_at ASC, se.id ASC
		LIMIT 1`,
		scope.HouseholdID(), scope.HouseholdID(), userID, barcode, nullableString(directionStr),
	)

	entry, err := scanScanEntry(row, false)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("findMergeableScanEntryTx: %w", err)
	}
	return entry, nil
}

func (r *Queue) recordMergedScanTx(ctx context.Context, scope tenant.Scope, tx *sql.Tx, id string, unitCount int, scannedAt time.Time) error {
	res, err := tx.ExecContext(ctx,
		`UPDATE scan_entries SET unit_count = ?, scanned_at = ? WHERE household_id = ? AND id = ?`,
		unitCount, scannedAt, scope.HouseholdID(), id,
	)
	if err != nil {
		return fmt.Errorf("recordMergedScanTx: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("recordMergedScanTx rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("recordMergedScanTx: scan entry %q not found", id)
	}
	if err := changes.Bump(ctx, tx, scope.HouseholdID(), changes.ScopeScans); err != nil {
		return fmt.Errorf("recordMergedScanTx: %w", err)
	}
	return nil
}

func (r *Queue) getScanEntryTx(ctx context.Context, scope tenant.Scope, tx *sql.Tx, id string) (*ScanEntry, error) {
	row := tx.QueryRowContext(ctx, `
		SELECT 
			se.id, se.user_id, se.barcode, se.scanned_at, se.direction, se.unit_count, 
			se.expires_at, se.status, se.product_id, se.committed_at, se.created_at,
			se.removed_count, se.requested_count,
			p.id, `+product.DisplayNameSQL("p")+`, COALESCE(p.category, ''), COALESCE(p.unit_of_measure, ''), COALESCE(p.image_url, ''), COALESCE(p.external_source, ''), p.pack_count
		FROM scan_entries se
		LEFT JOIN products p ON p.id = se.product_id
		WHERE se.household_id = ? AND se.id = ?`,
		scope.HouseholdID(), scope.HouseholdID(), id,
	)
	entry, err := scanScanEntry(row, false)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("getScanEntryTx: %w", err)
	}
	return entry, nil
}

// PublishAfterIngest emits scan and optional inventory live events after a device ingest transaction commits.
func (r *Queue) PublishAfterIngest(ctx context.Context, householdID, scanEntryID string, stockMoved bool) error {
	if r == nil || r.Broadcaster == nil || scanEntryID == "" {
		return nil
	}
	scope, err := tenant.New(householdID)
	if err != nil {
		return err
	}
	committed, err := r.GetScanEntry(ctx, scope, scanEntryID)
	if err != nil {
		return err
	}
	if committed != nil {
		r.Broadcaster.PublishScanEvent(householdID, *committed)
	}
	if !stockMoved || r.Pantry == nil || committed == nil || committed.ProductID == nil {
		return nil
	}
	var itemID string
	err = r.db.QueryRowContext(ctx, `
		SELECT id FROM items WHERE household_id = ? AND product_id = ?`,
		householdID, *committed.ProductID,
	).Scan(&itemID)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	invItem, err := r.Pantry.GetInventoryItem(ctx, scope, itemID, time.Now(), inventory.DefaultWarningDays)
	if err != nil {
		return err
	}
	if invItem != nil {
		r.Broadcaster.PublishInventoryEvent(householdID, *invItem)
	}
	return nil
}
