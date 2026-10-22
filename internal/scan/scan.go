// Package scan provides scan queue management functionality.
package scan

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Rhionin/stockbot/internal/inventory"
	"github.com/Rhionin/stockbot/internal/product"
	"github.com/Rhionin/stockbot/internal/supply"
	"github.com/google/uuid"
)

// ScanDirection represents the direction of a scan (stock-in or stock-out).
type ScanDirection string

const (
	StockIn  ScanDirection = "stock_in"
	StockOut ScanDirection = "stock_out"
)

// ScanStatus represents the status of a scan entry.
type ScanStatus string

const (
	Pending   ScanStatus = "pending"
	Flagged   ScanStatus = "flagged"
	Committed ScanStatus = "committed"
	Cancelled ScanStatus = "cancelled"
	// QR is a scanned QR payload rather than a product barcode. It is not looked up.
	QR ScanStatus = "qr"
)

// ScanEntry represents a single item in the scan queue.
type ScanEntry struct {
	ID        string                  `json:"id"`
	UserID    string                  `json:"userId"`
	Barcode   string                  `json:"barcode"`
	ScannedAt time.Time               `json:"scannedAt"`
	Direction *ScanDirection          `json:"direction"` // nil means not yet set
	UnitCount int                     `json:"unitCount"`
	ExpiresAt *time.Time              `json:"expiresAt"`
	Status    ScanStatus              `json:"status"`
	ProductID *string                 `json:"productId"`
	Product   *product.ProductSummary `json:"product,omitempty"` // populated by join queries
	// GroupHint is set by the scan list handler when this product looks like a group.
	// The scan package does not compute it.
	GroupHint   *GroupHint `json:"groupHint,omitempty"`
	CommittedAt *time.Time `json:"committedAt"`
	CreatedAt   time.Time  `json:"createdAt"`
}

// GroupHint is the one line on a scan row that offers to add the product to a group.
type GroupHint struct {
	GroupID string `json:"groupId,omitempty"`
	Name    string `json:"name"`
}

// NewEntryFromLookup builds a ScanEntry for a freshly scanned barcode from a
// product lookup result. ProductID and Status are derived from the lookup: a
// found product yields ProductID set and Status Pending; no match yields
// ProductID nil and Status Flagged. UnitCount is always 1. This is the single
// rule both the HTTP scan-create path and ScanListener use, so entries
// created through either path are identical apart from UserID.
func NewEntryFromLookup(userID, barcode string, lookup product.LookupResult, direction *ScanDirection, scannedAt time.Time) ScanEntry {
	status := Pending
	var productID *string
	if lookup.IsFound() {
		productID = &lookup.Product.ID
	} else {
		status = Flagged
	}
	return ScanEntry{
		UserID:    userID,
		Barcode:   barcode,
		ScannedAt: scannedAt,
		Direction: direction,
		UnitCount: 1,
		Status:    status,
		ProductID: productID,
	}
}

// Queue provides database operations for scan entries.
type Queue struct {
	db *sql.DB

	// Ledger resets the fulfillment ledger for items when stock-in occurs.
	// Only required when providers are configured and the ledger needs
	// to be reset on new stock.
	Ledger interface {
		ResetForItemTx(ctx context.Context, tx *sql.Tx, itemID string, at time.Time) error
	}

	// Broadcaster publishes a Scan_Event (and, for commit paths, an
	// Inventory_Event) after each successful mutation. Nil in every existing
	// test and in any caller that doesn't need live updates; publish calls
	// are skipped entirely when nil.
	Broadcaster interface {
		PublishScanEvent(entry ScanEntry)
		PublishInventoryEvent(item inventory.InventoryItem)
	}

	// Pantry supplies the aggregated InventoryItem for a scan commit's
	// affected item, so CommitStockIn/CommitStockOut can publish an
	// Inventory_Event without duplicating inventory's aggregation logic.
	// Only required when Broadcaster is set.
	Pantry interface {
		GetInventoryItem(ctx context.Context, itemID string, now time.Time, warningDays int) (*inventory.InventoryItem, error)
	}

	// Supply distinguishes an opening snapshot from a later restock.
	// Nil only in tests that construct a queue directly; the server always sets it.
	Supply *supply.Service

	// Now is the clock CommitStockIn stores in committed_at.
	// Nil means time.Now().UTC, which drops a monotonic reading.
	Now func() time.Time

	// BeforeResolve, when set, runs at the start of ResolveFlaggedEntry
	// before that call opens a transaction. A test lets a cancel request
	// commit on the single pooled connection in that window. Nil in production.
	BeforeResolve func()

	// AfterResolveCommit, when set, runs after the resolve transaction has
	// committed and the written row has already been read. The caller returns
	// that row, so a later change is not what the client is shown. Nil in production.
	AfterResolveCommit func()
}

// NewQueue creates a new Queue with the given database connection.
func NewQueue(db *sql.DB) *Queue {
	return &Queue{db: db}
}

// CreateScanEntry inserts a new scan entry, unless a pending or flagged entry
// already exists for the same user, barcode, and direction — in that case
// the existing entry's unit_count is incremented instead of inserting a
// duplicate row, and scanned_at moves to this scan. The queue orders by
// scanned_at, so the open card returns to the top, where a new scan lands,
// for every open client and for a reload. A missing incoming time is treated
// as now. A time older than the card is ignored so a late timestamp cannot
// bury it. If entry.ID is empty, a new UUID is generated for the inserted
// row. Status defaults to "pending" if not set.
func (r *Queue) CreateScanEntry(ctx context.Context, entry ScanEntry) (*ScanEntry, error) {
	existing, err := r.findMergeableScanEntry(ctx, entry.UserID, entry.Barcode, entry.Direction)
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
		if err := r.recordMergedScan(ctx, existing.ID, existing.UnitCount+delta, scannedAt); err != nil {
			return nil, err
		}
		return r.GetScanEntry(ctx, existing.ID)
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

	_, err = r.db.ExecContext(ctx,
		`INSERT INTO scan_entries (id, user_id, barcode, scanned_at, direction, unit_count, expires_at, status, product_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		entry.ID,
		entry.UserID,
		entry.Barcode,
		entry.ScannedAt,
		nullableString(directionStr),
		entry.UnitCount,
		nullableTime(entry.ExpiresAt),
		string(entry.Status),
		nullableString(entry.ProductID),
	)
	if err != nil {
		return nil, fmt.Errorf("CreateScanEntry: %w", err)
	}

	created, err := r.GetScanEntry(ctx, entry.ID)
	if err != nil {
		return nil, err
	}

	if created != nil && r.Broadcaster != nil {
		r.Broadcaster.PublishScanEvent(*created)
	}

	return created, nil
}

// recordMergedScan applies one more scan to an open entry and publishes that
// entry once. scannedAt is the later of the stored time and this scan: an
// older clock must not pull the card backward. ListScanEntries orders by
// scanned_at, and clients sort the same column newest-first, so one write
// keeps live updates and reloads in the same order.
func (r *Queue) recordMergedScan(ctx context.Context, id string, unitCount int, scannedAt time.Time) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE scan_entries SET unit_count = ?, scanned_at = ? WHERE id = ?`,
		unitCount, scannedAt, id,
	)
	if err != nil {
		return fmt.Errorf("recordMergedScan: %w", err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("recordMergedScan rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("recordMergedScan: scan entry %q not found", id)
	}

	updated, err := r.GetScanEntry(ctx, id)
	if err != nil {
		return err
	}
	if updated != nil && r.Broadcaster != nil {
		r.Broadcaster.PublishScanEvent(*updated)
	}
	return nil
}

const scanEntryByID = `
	SELECT 
		se.id, se.user_id, se.barcode, se.scanned_at, se.direction, se.unit_count, 
		se.expires_at, se.status, se.product_id, se.committed_at, se.created_at,
		p.id, p.name, COALESCE(p.category, ''), COALESCE(p.unit_of_measure, ''), COALESCE(p.image_url, ''), COALESCE(p.external_source, ''), p.pack_count
	FROM scan_entries se
	LEFT JOIN products p ON p.id = se.product_id
	WHERE se.id = ?`

type queryRower interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// GetScanEntry returns the scan entry with the given ID, or nil if no such
// entry exists. Includes joined product information if available.
func (r *Queue) GetScanEntry(ctx context.Context, id string) (*ScanEntry, error) {
	entry, err := r.readScanEntry(ctx, r.db, id)
	if err != nil {
		return nil, fmt.Errorf("GetScanEntry: %w", err)
	}
	return entry, nil
}

func (r *Queue) readScanEntry(ctx context.Context, q queryRower, id string) (*ScanEntry, error) {
	entry, err := scanScanEntry(q.QueryRowContext(ctx, scanEntryByID, id))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return entry, nil
}

// ensureHouseholdProduct reports ErrProductNotResolved when productID is
// blank or not a row in products. The lookup is SELECT id FROM products
// WHERE id = ?. householdID is the scan's user, named in the error so the
// log says which scan was refused. The check runs on the same transaction
// as the write.
func ensureHouseholdProduct(ctx context.Context, q queryRower, householdID, productID string) error {
	if productID == "" {
		return fmt.Errorf("household %s: empty product id: %w", householdID, ErrProductNotResolved)
	}
	var id string
	err := q.QueryRowContext(ctx, `SELECT id FROM products WHERE id = ?`, productID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("household %s: product %q: %w", householdID, productID, ErrProductNotResolved)
	}
	if err != nil {
		return fmt.Errorf("verify product: %w", err)
	}
	return nil
}

// findMergeableScanEntry returns the oldest pending or flagged scan entry for
// the given user, barcode, and direction, or nil if no such entry exists.
// direction is compared with IS rather than = so a nil direction only matches
// an existing entry whose direction is also nil (SQLite's IS is null-safe
// equality), keeping direction an exact-match key including the not-yet-set
// case. Ties are broken by id for a deterministic result.
func (r *Queue) findMergeableScanEntry(ctx context.Context, userID, barcode string, direction *ScanDirection) (*ScanEntry, error) {
	var directionStr *string
	if direction != nil {
		directionStr = ptr(string(*direction))
	}

	row := r.db.QueryRowContext(ctx, `
		SELECT 
			se.id, se.user_id, se.barcode, se.scanned_at, se.direction, se.unit_count, 
			se.expires_at, se.status, se.product_id, se.committed_at, se.created_at,
			p.id, p.name, COALESCE(p.category, ''), COALESCE(p.unit_of_measure, ''), COALESCE(p.image_url, ''), COALESCE(p.external_source, ''), p.pack_count
		FROM scan_entries se
		LEFT JOIN products p ON p.id = se.product_id
		WHERE se.user_id = ? AND se.barcode = ? AND se.direction IS ? AND se.status IN ('pending', 'flagged')
		ORDER BY se.scanned_at ASC, se.id ASC
		LIMIT 1`,
		userID, barcode, nullableString(directionStr),
	)

	entry, err := scanScanEntry(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("findMergeableScanEntry: %w", err)
	}
	return entry, nil
}

// ListScanEntries returns scan entries filtered by status and ordered by
// scanned_at ascending (chronological order). If status is empty, all entries
// are returned.
func (r *Queue) ListScanEntries(ctx context.Context, userID string, status ScanStatus) ([]ScanEntry, error) {
	query := `
		SELECT 
			se.id, se.user_id, se.barcode, se.scanned_at, se.direction, se.unit_count, 
			se.expires_at, se.status, se.product_id, se.committed_at, se.created_at,
			p.id, p.name, COALESCE(p.category, ''), COALESCE(p.unit_of_measure, ''), COALESCE(p.image_url, ''), COALESCE(p.external_source, ''), p.pack_count
		FROM scan_entries se
		LEFT JOIN products p ON p.id = se.product_id
		WHERE se.user_id = ?`

	args := []interface{}{userID}

	if status != "" {
		query += ` AND se.status = ?`
		args = append(args, string(status))
	}

	query += ` ORDER BY se.scanned_at ASC`

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("ListScanEntries: %w", err)
	}
	defer rows.Close()

	var entries []ScanEntry
	for rows.Next() {
		entry, err := scanScanEntry(rows)
		if err != nil {
			return nil, fmt.Errorf("ListScanEntries scan: %w", err)
		}
		entries = append(entries, *entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("ListScanEntries rows: %w", err)
	}
	return entries, nil
}

// ErrNotOpen means a patch wrote nothing because the scan is no longer
// pending, flagged, or a QR misread. The text is the sentence the household
// sees. Undoing a committed scan is a move undo, not a patch.
var ErrNotOpen = errors.New("This scan was already handled.")

// ErrNotFound means the scan id is not in the queue.
var ErrNotFound = errors.New("scan entry not found")

// ErrNotFlagged means a resolve was asked of a scan that is still open but
// not flagged. The caller updates the row as a normal patch.
var ErrNotFlagged = errors.New("only a flagged scan can be resolved")

// ErrProductNotResolved means the product id names nothing in the household
// catalog. The HTTP body uses a fixed sentence; this text stays in the log.
var ErrProductNotResolved = errors.New("product not resolved")

// ErrInvalidResolve means a flagged resolve included a field other than the
// product, or a status other than pending. Nothing is written.
var ErrInvalidResolve = errors.New("invalid resolve")

// ResolveFields are the other patch fields sent alongside a product id.
// On a flagged scan, only a pending status is allowed with the product.
type ResolveFields struct {
	Direction *ScanDirection
	UnitCount *int
	ExpiresAt *time.Time
	Status    *ScanStatus
}

// Rejects reports whether these fields are not a resolve. Status pending,
// or no status, is the resolve. Any other field or status is not.
func (f ResolveFields) Rejects() bool {
	if f.Direction != nil || f.UnitCount != nil || f.ExpiresAt != nil {
		return true
	}
	return f.Status != nil && *f.Status != Pending
}

// UpdateScanEntry updates direction, unit_count, expires_at, product_id, and
// status for the given scan entry ID. Field edits go through here. A repeat
// scan does not: CreateScanEntry writes unit_count and scanned_at together so
// the live event carries the new activity time.
//
// Pass nil for fields that should not be updated. To clear a nullable field,
// pass a pointer to a zero value (e.g., &time.Time{} for expires_at).
//
// Every change matches only an open scan (pending, flagged, or qr) in the
// same statement as the write. A commit that lands first leaves the row and
// its stock move alone, and the call returns ErrNotOpen.
func (r *Queue) UpdateScanEntry(ctx context.Context, id string, direction *ScanDirection, unitCount *int, expiresAt *time.Time, productID *string, status *ScanStatus) error {
	query := "UPDATE scan_entries SET"
	args := []interface{}{}
	updates := []string{}

	if direction != nil {
		updates = append(updates, " direction = ?")
		args = append(args, nullableString(ptr(string(*direction))))
	}
	if unitCount != nil {
		updates = append(updates, " unit_count = ?")
		args = append(args, *unitCount)
	}
	if expiresAt != nil {
		updates = append(updates, " expires_at = ?")
		args = append(args, nullableTime(expiresAt))
	}
	if productID != nil {
		updates = append(updates, " product_id = ?")
		args = append(args, nullableString(productID))
	}
	if status != nil {
		updates = append(updates, " status = ?")
		args = append(args, string(*status))
	}

	if len(updates) == 0 {
		return nil
	}

	for i, u := range updates {
		query += u
		if i < len(updates)-1 {
			query += ","
		}
	}

	// The status predicate is part of the write, including when the body does
	// not mention status. A read of the status before this statement would
	// let a commit land in between and leave the move out of step with the row.
	query += " WHERE id = ? AND status IN (?, ?, ?)"
	args = append(args, id, string(Pending), string(Flagged), string(QR))

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("UpdateScanEntry: %w", err)
	}
	defer tx.Rollback()

	// The product check shares this transaction with the write. A missing
	// product returns before the update, for every status, and writes nothing.
	if productID != nil {
		var householdID string
		err := tx.QueryRowContext(ctx, `SELECT user_id FROM scan_entries WHERE id = ?`, id).Scan(&householdID)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("UpdateScanEntry: %w", ErrNotFound)
		}
		if err != nil {
			return fmt.Errorf("UpdateScanEntry: %w", err)
		}
		if err := ensureHouseholdProduct(ctx, tx, householdID, *productID); err != nil {
			return fmt.Errorf("UpdateScanEntry: %w", err)
		}
	}

	res, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("UpdateScanEntry: %w", err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("UpdateScanEntry rows affected: %w", err)
	}
	if n == 0 {
		existing, getErr := r.readScanEntry(ctx, tx, id)
		if getErr != nil {
			return getErr
		}
		if existing != nil {
			return fmt.Errorf("UpdateScanEntry: %w", ErrNotOpen)
		}
		return fmt.Errorf("UpdateScanEntry: %w", ErrNotFound)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("UpdateScanEntry: %w", err)
	}

	updated, err := r.GetScanEntry(ctx, id)
	if err != nil {
		return err
	}

	if updated != nil && r.Broadcaster != nil {
		r.Broadcaster.PublishScanEvent(*updated)
	}

	return nil
}

// CommitScanEntry marks a scan entry as committed and records the committed_at
// timestamp. This is a convenience wrapper around UpdateScanEntry.
func (r *Queue) CommitScanEntry(ctx context.Context, id string) error {
	now := time.Now().UTC()
	committed := Committed

	_, err := r.db.ExecContext(ctx,
		`UPDATE scan_entries SET status = ?, committed_at = ? WHERE id = ?`,
		string(committed), now, id,
	)
	if err != nil {
		return fmt.Errorf("CommitScanEntry: %w", err)
	}

	return nil
}

// BatchUpdateScanEntries applies the same direction, unit_count, and expires_at
// to multiple scan entries identified by their IDs.
func (r *Queue) BatchUpdateScanEntries(ctx context.Context, ids []string, direction *ScanDirection, unitCount *int, expiresAt *time.Time) error {
	if len(ids) == 0 {
		return nil
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("BatchUpdateScanEntries begin tx: %w", err)
	}
	defer tx.Rollback()

	for _, id := range ids {
		err := r.updateScanEntryInTx(ctx, tx, id, direction, unitCount, expiresAt, nil, nil)
		if err != nil {
			return fmt.Errorf("BatchUpdateScanEntries: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("BatchUpdateScanEntries commit: %w", err)
	}

	if r.Broadcaster != nil {
		for _, id := range ids {
			updated, err := r.GetScanEntry(ctx, id)
			if err != nil {
				return err
			}
			if updated != nil {
				r.Broadcaster.PublishScanEvent(*updated)
			}
		}
	}

	return nil
}

// updateScanEntryInTx is a helper that updates a scan entry within a transaction.
func (r *Queue) updateScanEntryInTx(ctx context.Context, tx *sql.Tx, id string, direction *ScanDirection, unitCount *int, expiresAt *time.Time, productID *string, status *ScanStatus) error {
	query := "UPDATE scan_entries SET"
	args := []interface{}{}
	updates := []string{}

	if direction != nil {
		updates = append(updates, " direction = ?")
		args = append(args, nullableString(ptr(string(*direction))))
	}
	if unitCount != nil {
		updates = append(updates, " unit_count = ?")
		args = append(args, *unitCount)
	}
	if expiresAt != nil {
		updates = append(updates, " expires_at = ?")
		args = append(args, nullableTime(expiresAt))
	}
	if productID != nil {
		updates = append(updates, " product_id = ?")
		args = append(args, nullableString(productID))
	}
	if status != nil {
		updates = append(updates, " status = ?")
		args = append(args, string(*status))
	}

	if len(updates) == 0 {
		return nil
	}

	for i, u := range updates {
		query += u
		if i < len(updates)-1 {
			query += ","
		}
	}

	query += " WHERE id = ?"
	args = append(args, id)

	res, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("updateScanEntryInTx: %w", err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("updateScanEntryInTx rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("updateScanEntryInTx: scan entry %q not found", id)
	}

	return nil
}

// scanScanEntry is a helper that scans a row into a ScanEntry struct.
// It handles nullable fields and optional product join data.
type scanner interface {
	Scan(dest ...interface{}) error
}

func scanScanEntry(row scanner) (*ScanEntry, error) {
	var entry ScanEntry
	var direction, productIDCol sql.NullString
	var expiresAt, committedAt sql.NullTime
	var productID, productName, productCategory, productUnitOfMeasure, productImageURL, productExternalSource sql.NullString
	var packCount sql.NullInt64

	err := row.Scan(
		&entry.ID,
		&entry.UserID,
		&entry.Barcode,
		&entry.ScannedAt,
		&direction,
		&entry.UnitCount,
		&expiresAt,
		&entry.Status,
		&productIDCol,
		&committedAt,
		&entry.CreatedAt,
		&productID,
		&productName,
		&productCategory,
		&productUnitOfMeasure,
		&productImageURL,
		&productExternalSource,
		&packCount,
	)
	if err != nil {
		return nil, err
	}

	if direction.Valid {
		d := ScanDirection(direction.String)
		entry.Direction = &d
	}
	if expiresAt.Valid {
		entry.ExpiresAt = &expiresAt.Time
	}
	if productIDCol.Valid {
		entry.ProductID = &productIDCol.String
	}
	if committedAt.Valid {
		entry.CommittedAt = &committedAt.Time
	}

	if productID.Valid {
		entry.Product = &product.ProductSummary{
			ID:             productID.String,
			Name:           productName.String,
			Category:       productCategory.String,
			UnitOfMeasure:  productUnitOfMeasure.String,
			ImageURL:       productImageURL.String,
			ExternalSource: product.ExternalSource(productExternalSource.String),
		}
		if packCount.Valid {
			n := int(packCount.Int64)
			entry.Product.PackCount = &n
		}
	}

	return &entry, nil
}

// Helper functions for nullable types

func ptr[T any](v T) *T {
	return &v
}

func nullableString(s *string) sql.NullString {
	if s == nil || *s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: *s, Valid: true}
}

func nullableTime(t *time.Time) sql.NullTime {
	if t == nil || t.IsZero() {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: *t, Valid: true}
}
