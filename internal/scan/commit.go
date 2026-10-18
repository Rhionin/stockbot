package scan

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Rhionin/stockbot/internal/history"
	"github.com/Rhionin/stockbot/internal/inventory"
	"github.com/google/uuid"
)

var (
	// ErrItemNotFound means the household has no item with that id.
	ErrItemNotFound = errors.New("item not found")
	// ErrNoneOnHand means a stock-out found no unit on the shelf.
	ErrNoneOnHand = errors.New("nothing on hand")
	// ErrLinkNotStocked means the chosen product has no household item to scan out.
	ErrLinkNotStocked = errors.New("that product is not in your pantry")
	// ErrScanNotFound means the queue has no scan with that id.
	ErrScanNotFound = errors.New("scan not found")
	// ErrNotPending means the scan was already committed or cancelled.
	ErrNotPending = errors.New("scan is not pending")
	// ErrDirectionUnset means the scan has no stock-in or stock-out direction.
	ErrDirectionUnset = errors.New("scan direction is unset")
	// ErrProductUnresolved means the scan has no product yet.
	ErrProductUnresolved = errors.New("product is unresolved")
)

// CommitStockIn commits a stock-in scan entry by creating one item instance
// per unit and marking the scan entry as committed. A remembered pack count
// greater than one splits the barcode into that many units, so a 6-pack scan
// with a unit count of 2 puts 12 units on the shelf. Each instance receives
// the scan entry's ScannedAt as stock_in_at and the scan entry's ExpiresAt
// (if set).
//
// This function handles the complete stock-in flow:
// 1. Ensures an item exists for the user+product combination
// 2. Creates N item instances with the scan entry's timestamps
// 3. Marks the scan entry as committed
//
// All operations are performed within a transaction to ensure atomicity.
func (r *Queue) CommitStockIn(ctx context.Context, scanEntry *ScanEntry) error {
	if scanEntry.Direction == nil || *scanEntry.Direction != StockIn {
		return fmt.Errorf("scan entry direction must be stock_in, got %v", scanEntry.Direction)
	}

	if scanEntry.ProductID == nil || *scanEntry.ProductID == "" {
		return fmt.Errorf("scan entry must have a product_id")
	}

	if scanEntry.UnitCount < 1 {
		return fmt.Errorf("unit count must be at least 1, got %d", scanEntry.UnitCount)
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	itemID, err := r.findOrCreateItem(ctx, tx, scanEntry.UserID, *scanEntry.ProductID)
	if err != nil {
		return fmt.Errorf("find/create item: %w", err)
	}

	// Opening records the units already on the shelf and stops. The scan
	// still has to leave the queue, so the entry is committed either way.
	units, err := stockInUnits(ctx, tx, *scanEntry.ProductID, scanEntry.UnitCount)
	if err != nil {
		return err
	}
	scanID := scanEntry.ID
	if _, err := r.recordStockInTx(ctx, tx, itemID, *scanEntry.ProductID, scanEntry.ScannedAt, scanEntry.ExpiresAt, units, "scan", &scanID); err != nil {
		return err
	}

	_, err = tx.ExecContext(ctx, `
		UPDATE scan_entries 
		SET status = ?, committed_at = CURRENT_TIMESTAMP 
		WHERE id = ?`,
		string(Committed),
		scanEntry.ID,
	)
	if err != nil {
		return fmt.Errorf("update scan entry: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}

	if r.Broadcaster != nil {
		committed, err := r.GetScanEntry(ctx, scanEntry.ID)
		if err != nil {
			return err
		}
		if committed != nil {
			r.Broadcaster.PublishScanEvent(*committed)
		}

		if r.Pantry != nil {
			invItem, err := r.Pantry.GetInventoryItem(ctx, itemID, time.Now(), inventory.DefaultWarningDays)
			if err != nil {
				return err
			}
			if invItem != nil {
				r.Broadcaster.PublishInventoryEvent(*invItem)
			}
		}
	}

	return nil
}

// trackRestock reports whether a movement is past the opening snapshot.
// During opening the units are already on the shelf, so there is no stock-in
// event and no consumption row. A queue with no supply service reports true:
// hand-built tests keep ledger reset on stock-in and still record usage on
// stock-out. Those tests do not write stock_in_events, because that insert
// also requires a supply service.
func (r *Queue) trackRestock(ctx context.Context, tx *sql.Tx) (bool, error) {
	if r.Supply == nil {
		return true, nil
	}
	opening, err := r.Supply.OpeningTx(ctx, tx)
	if err != nil {
		return false, fmt.Errorf("could not read supply settings: %w", err)
	}
	return !opening, nil
}

// stockInUnits is the number of shelf units one commit adds.
// A missing pack count, or a count of 1, means the scan's unit count is the
// number of units. A remembered multipack multiplies that count.
func stockInUnits(ctx context.Context, tx *sql.Tx, productID string, unitCount int) (int, error) {
	var pack sql.NullInt64
	err := tx.QueryRowContext(ctx, `SELECT pack_count FROM products WHERE id = ?`, productID).Scan(&pack)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("that product was not found")
	}
	if err != nil {
		return 0, fmt.Errorf("could not read how many units are in the pack: %w", err)
	}
	if pack.Valid && pack.Int64 > 1 {
		return unitCount * int(pack.Int64), nil
	}
	return unitCount, nil
}

// recordStockInTx adds units on the shelf. Once opening is finished it also
// resets the provider ledger and writes one stock-in event for the whole call.
// Opening only records units that are already here.
func (r *Queue) recordStockInTx(ctx context.Context, tx *sql.Tx, itemID, productID string, at time.Time, expiresAt *time.Time, units int, source string, scanID *string) ([]string, error) {
	ids := make([]string, 0, units)
	for i := 0; i < units; i++ {
		instanceID := uuid.NewString()
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO item_instances (id, item_id, stock_in_at, expires_at)
			VALUES (?, ?, ?, ?)`,
			instanceID,
			itemID,
			at,
			nullableTime(expiresAt),
		); err != nil {
			return nil, fmt.Errorf("create instance %d: %w", i+1, err)
		}
		ids = append(ids, instanceID)
	}

	trackRestock, err := r.trackRestock(ctx, tx)
	if err != nil {
		return nil, err
	}
	if trackRestock && r.Ledger != nil {
		if err := r.Ledger.ResetForItemTx(ctx, tx, itemID, at); err != nil {
			return nil, fmt.Errorf("reset ledger: %w", err)
		}
	}
	if trackRestock && r.Supply != nil {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO stock_in_events (product_id, at) VALUES (?, ?)`,
			productID, at,
		); err != nil {
			return nil, fmt.Errorf("record stock-in: %w", err)
		}
	}
	if err := history.AttachStockIn(ctx, tx, history.InNote{
		ItemID: itemID, ProductID: productID, At: at, Source: source, ScanEntryID: scanID,
		Tracked: trackRestock && r.Supply != nil, InstanceIDs: ids,
	}); err != nil {
		return nil, fmt.Errorf("record stock-in: %w", err)
	}
	return ids, nil
}

// recordStockOutTx removes one on-hand unit. A nil instanceID selects the
// earliest expiration, with undated units last — the same unit a scan-out
// commits. After opening, the removal is one consumption row. During opening
// the unit was already here, so it is not usage.
func (r *Queue) recordStockOutTx(ctx context.Context, tx *sql.Tx, itemID string, instanceID *string, at time.Time, scanEntryID *string) error {
	var selected string
	if instanceID != nil {
		selected = *instanceID
		var exists int
		err := tx.QueryRowContext(ctx, `
			SELECT 1 FROM item_instances
			WHERE id = ? AND item_id = ? AND removed_at IS NULL`,
			selected, itemID,
		).Scan(&exists)
		if err == sql.ErrNoRows {
			return r.namedInstanceUnavailable(ctx, tx, itemID)
		}
		if err != nil {
			return fmt.Errorf("verify instance: %w", err)
		}
	} else {
		err := tx.QueryRowContext(ctx, `
			SELECT id FROM item_instances
			WHERE item_id = ? AND removed_at IS NULL
			ORDER BY expires_at ASC NULLS LAST
			LIMIT 1`,
			itemID,
		).Scan(&selected)
		if err == sql.ErrNoRows {
			return ErrNoneOnHand
		}
		if err != nil {
			return fmt.Errorf("select instance: %w", err)
		}
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE item_instances
		SET removed_at = CURRENT_TIMESTAMP, removal_reason = 'consumed'
		WHERE id = ?`,
		selected,
	); err != nil {
		return fmt.Errorf("remove instance: %w", err)
	}

	usage, err := r.trackRestock(ctx, tx)
	if err != nil {
		return err
	}
	var productID string
	if err := tx.QueryRowContext(ctx, `SELECT product_id FROM items WHERE id = ?`, itemID).Scan(&productID); err != nil {
		return fmt.Errorf("find item: %w", err)
	}
	source := "manual"
	if scanEntryID != nil && *scanEntryID != "" {
		source = "scan"
	}
	consumptionID := ""
	if usage {
		consumptionID = uuid.NewString()
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO consumption_events (id, item_id, consumed_at, scan_entry_id)
			VALUES (?, ?, ?, ?)`,
			consumptionID,
			itemID,
			at,
			nullableString(scanEntryID),
		); err != nil {
			return fmt.Errorf("create consumption event: %w", err)
		}
	}
	if err := history.AttachStockOut(ctx, tx, history.OutNote{
		ItemID: itemID, ProductID: productID, At: at, Source: source, ScanEntryID: scanEntryID,
		Tracked: usage, InstanceID: selected, ConsumptionID: consumptionID,
	}); err != nil {
		return fmt.Errorf("record stock-out: %w", err)
	}
	return nil
}

// namedInstanceUnavailable distinguishes a stale pick from an empty shelf.
// The chosen unit is gone. When nothing else is on hand, that is the same
// refusal as a scan-out with no unit to take.
func (r *Queue) namedInstanceUnavailable(ctx context.Context, tx *sql.Tx, itemID string) error {
	var onHand int
	err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM item_instances
		WHERE item_id = ? AND removed_at IS NULL`,
		itemID,
	).Scan(&onHand)
	if err != nil {
		return fmt.Errorf("count on hand: %w", err)
	}
	if onHand == 0 {
		return ErrNoneOnHand
	}
	return inventory.ErrInstanceNotFound
}

// StockIn adds one on-hand unit of an item the household already keeps.
// The usage ledger matches a committed one-unit stock-in scan of that product.
func (r *Queue) StockIn(ctx context.Context, itemID string, at time.Time, expiresAt *time.Time) (*inventory.ItemInstance, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	var productID string
	err = tx.QueryRowContext(ctx, `SELECT product_id FROM items WHERE id = ?`, itemID).Scan(&productID)
	if err == sql.ErrNoRows {
		return nil, ErrItemNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find item: %w", err)
	}

	ids, err := r.recordStockInTx(ctx, tx, itemID, productID, at, expiresAt, 1, "manual", nil)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit transaction: %w", err)
	}

	r.publishInventory(ctx, itemID)
	return r.readInstance(ctx, ids[0])
}

// StockOut removes one on-hand unit of an item, choosing the same unit a
// scan-out would. The usage ledger matches that committed scan.
func (r *Queue) StockOut(ctx context.Context, itemID string, at time.Time) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	var exists int
	err = tx.QueryRowContext(ctx, `SELECT 1 FROM items WHERE id = ?`, itemID).Scan(&exists)
	if err == sql.ErrNoRows {
		return ErrItemNotFound
	}
	if err != nil {
		return fmt.Errorf("find item: %w", err)
	}
	if err := r.recordStockOutTx(ctx, tx, itemID, nil, at, nil); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	r.publishInventory(ctx, itemID)
	return nil
}

// StockOutInstance removes one specific on-hand unit. It is the same stock-out
// as StockOut, including the usage ledger.
func (r *Queue) StockOutInstance(ctx context.Context, instanceID string, at time.Time) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	var itemID string
	err = tx.QueryRowContext(ctx, `
		SELECT item_id FROM item_instances
		WHERE id = ? AND removed_at IS NULL`,
		instanceID,
	).Scan(&itemID)
	if err == sql.ErrNoRows {
		return inventory.ErrInstanceNotFound
	}
	if err != nil {
		return fmt.Errorf("find instance: %w", err)
	}
	if err := r.recordStockOutTx(ctx, tx, itemID, &instanceID, at, nil); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	r.publishInventory(ctx, itemID)
	return nil
}

func (r *Queue) publishInventory(ctx context.Context, itemID string) {
	if r.Broadcaster == nil || r.Pantry == nil {
		return
	}
	invItem, err := r.Pantry.GetInventoryItem(ctx, itemID, time.Now(), inventory.DefaultWarningDays)
	if err != nil || invItem == nil {
		return
	}
	r.Broadcaster.PublishInventoryEvent(*invItem)
}

func (r *Queue) readInstance(ctx context.Context, id string) (*inventory.ItemInstance, error) {
	var inst inventory.ItemInstance
	var expiresAt, removedAt sql.NullTime
	var removalReason sql.NullString
	err := r.db.QueryRowContext(ctx, `
		SELECT id, item_id, stock_in_at, expires_at, removed_at, removal_reason, created_at
		FROM item_instances WHERE id = ?`,
		id,
	).Scan(&inst.ID, &inst.ItemID, &inst.StockInAt, &expiresAt, &removedAt, &removalReason, &inst.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("read instance: %w", err)
	}
	if expiresAt.Valid {
		inst.ExpiresAt = &expiresAt.Time
	}
	if removedAt.Valid {
		inst.RemovedAt = &removedAt.Time
	}
	if removalReason.Valid {
		inst.RemovalReason = &removalReason.String
	}
	return &inst, nil
}

// findOrCreateItem retrieves the item ID for a user+product combination,
// creating the item if it doesn't already exist. A shelved row is still
// this household's item: stock-in finds it, and the unshelve trigger puts
// it back on the shelf.
func (r *Queue) findOrCreateItem(ctx context.Context, tx *sql.Tx, userID, productID string) (string, error) {
	var itemID string
	err := tx.QueryRowContext(ctx, `
		SELECT id FROM items 
		WHERE user_id = ? AND product_id = ?`,
		userID, productID,
	).Scan(&itemID)

	if err == nil {
		return itemID, nil
	}

	if err != sql.ErrNoRows {
		return "", fmt.Errorf("query item: %w", err)
	}

	itemID = uuid.NewString()
	_, err = tx.ExecContext(ctx, `
		INSERT INTO items (id, user_id, product_id)
		VALUES (?, ?, ?)`,
		itemID, userID, productID,
	)
	if err != nil {
		return "", fmt.Errorf("create item: %w", err)
	}

	return itemID, nil
}

// CommitStockOut commits a stock-out scan entry by removing one item instance
// from the inventory and marking the scan entry as committed.
//
// If instanceID is nil, the function selects the use-oldest-first instance
// (ORDER BY expires_at ASC NULLS LAST) to remove. If instanceID is provided,
// that specific instance is removed.
//
// A missing household item returns *NoItemError and changes nothing.
// An item with nothing left on the shelf returns ErrNoneOnHand and changes
// nothing, including a barcode link started in the same transaction.
// A named instance that is not on that item returns ErrNoneOnHand when the
// shelf is empty, and inventory.ErrInstanceNotFound when another unit remains.
// Either way the transaction rolls back, so a barcode link is not kept.
// CommitStockOutLink is the explicit choice that adds the scanned barcode
// and decrements that product. Nothing is linked on its own.
//
// All operations are performed within a transaction to ensure atomicity.
func (r *Queue) CommitStockOut(ctx context.Context, scanEntry *ScanEntry, instanceID *string) error {
	return r.commitStockOut(ctx, scanEntry, instanceID, "")
}

// CommitStockOutLink adds the scanned barcode to linkProductID, keeps that
// product's existing barcodes, and commits the stock-out against the household
// item for linkProductID.
func (r *Queue) CommitStockOutLink(ctx context.Context, scanEntry *ScanEntry, instanceID *string, linkProductID string) error {
	if linkProductID == "" {
		return fmt.Errorf("link product id is required")
	}
	return r.commitStockOut(ctx, scanEntry, instanceID, linkProductID)
}

func (r *Queue) commitStockOut(ctx context.Context, scanEntry *ScanEntry, instanceID *string, linkProductID string) error {
	if scanEntry.Direction == nil || *scanEntry.Direction != StockOut {
		return fmt.Errorf("scan entry direction must be stock_out, got %v", scanEntry.Direction)
	}

	if scanEntry.ProductID == nil || *scanEntry.ProductID == "" {
		return fmt.Errorf("scan entry must have a product_id")
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	targetProductID := *scanEntry.ProductID
	if linkProductID != "" {
		targetProductID = linkProductID
	}

	var itemID string
	var shelved int
	err = tx.QueryRowContext(ctx, `
		SELECT id, shelved FROM items 
		WHERE user_id = ? AND product_id = ?`,
		scanEntry.UserID, targetProductID,
	).Scan(&itemID, &shelved)

	// A wipe keeps the item row and hides it. Scanning that product out is
	// the same as scanning a product the household does not stock.
	if err == sql.ErrNoRows || (err == nil && shelved != 0) {
		if linkProductID != "" {
			return ErrLinkNotStocked
		}
		// Same connection as the open transaction. A second query on r.db
		// would deadlock the single-connection test database, or read an
		// empty :memory: database from the pool.
		suggestions, sugErr := r.suggestLinks(ctx, tx, scanEntry)
		if sugErr != nil {
			return sugErr
		}
		return &NoItemError{Suggestions: suggestions}
	}
	if err != nil {
		return fmt.Errorf("find item: %w", err)
	}

	if linkProductID != "" {
		_, err = tx.ExecContext(ctx, `
			INSERT INTO barcodes (barcode, product_id, source, user_id)
			VALUES (?, ?, 'user_override', ?)
			ON CONFLICT(barcode, source, user_id) DO UPDATE SET product_id = excluded.product_id`,
			scanEntry.Barcode, linkProductID, scanEntry.UserID,
		)
		if err != nil {
			return fmt.Errorf("link barcode: %w", err)
		}
	}

	scanID := scanEntry.ID
	if err := r.recordStockOutTx(ctx, tx, itemID, instanceID, scanEntry.ScannedAt, &scanID); err != nil {
		return err
	}

	if linkProductID != "" {
		_, err = tx.ExecContext(ctx, `
			UPDATE scan_entries
			SET product_id = ?, status = ?, committed_at = CURRENT_TIMESTAMP
			WHERE id = ?`,
			linkProductID, string(Committed), scanEntry.ID,
		)
	} else {
		_, err = tx.ExecContext(ctx, `
			UPDATE scan_entries
			SET status = ?, committed_at = CURRENT_TIMESTAMP
			WHERE id = ?`,
			string(Committed), scanEntry.ID,
		)
	}
	if err != nil {
		return fmt.Errorf("update scan entry: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}

	if r.Broadcaster != nil {
		committed, err := r.GetScanEntry(ctx, scanEntry.ID)
		if err != nil {
			return err
		}
		if committed != nil {
			r.Broadcaster.PublishScanEvent(*committed)
		}

		if r.Pantry != nil {
			invItem, err := r.Pantry.GetInventoryItem(ctx, itemID, time.Now(), inventory.DefaultWarningDays)
			if err != nil {
				return err
			}
			if invItem != nil {
				r.Broadcaster.PublishInventoryEvent(*invItem)
			}
		}
	}

	return nil
}

// ResolveFlaggedEntry resolves a flagged scan entry by associating it with a product.
// This function creates a user_override barcode mapping and transitions the scan entry
// from flagged to pending status.
//
// This function handles the complete flagged entry resolution flow:
// 1. Validates the scan entry exists and has status 'flagged'
// 2. Creates a product override (barcodes row with source='user_override')
// 3. Sets the product_id on the scan entry
// 4. Transitions the scan entry status from 'flagged' to 'pending'
//
// All operations are performed within a transaction to ensure atomicity.
func (r *Queue) ResolveFlaggedEntry(ctx context.Context, scanEntryID, productID string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	var userID, barcode, status string
	err = tx.QueryRowContext(ctx, `
		SELECT user_id, barcode, status FROM scan_entries WHERE id = ?`,
		scanEntryID,
	).Scan(&userID, &barcode, &status)

	if err == sql.ErrNoRows {
		return fmt.Errorf("scan entry %q not found", scanEntryID)
	}
	if err != nil {
		return fmt.Errorf("query scan entry: %w", err)
	}

	if status != string(Flagged) {
		return fmt.Errorf("scan entry %q has status %q, expected %q", scanEntryID, status, Flagged)
	}

	var productExists bool
	err = tx.QueryRowContext(ctx, `
		SELECT 1 FROM products WHERE id = ?`,
		productID,
	).Scan(&productExists)

	if err == sql.ErrNoRows {
		return fmt.Errorf("product %q not found", productID)
	}
	if err != nil {
		return fmt.Errorf("verify product: %w", err)
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO barcodes (barcode, product_id, source, user_id)
		VALUES (?, ?, 'user_override', ?)
		ON CONFLICT(barcode, source, user_id) DO UPDATE SET product_id = excluded.product_id`,
		barcode,
		productID,
		userID,
	)
	if err != nil {
		return fmt.Errorf("create barcode override: %w", err)
	}

	pendingStatus := Pending
	err = r.updateScanEntryInTx(ctx, tx, scanEntryID, nil, nil, nil, &productID, &pendingStatus)
	if err != nil {
		return fmt.Errorf("update scan entry: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}

	if r.Broadcaster != nil {
		resolved, err := r.GetScanEntry(ctx, scanEntryID)
		if err != nil {
			return err
		}
		if resolved != nil {
			r.Broadcaster.PublishScanEvent(*resolved)
		}
	}

	return nil
}
