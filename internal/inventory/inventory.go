// Package inventory provides inventory management functionality for items and item instances.
package inventory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Rhionin/stockbot/internal/product"
	"github.com/google/uuid"
)

var (
	// ErrInstanceNotFound is returned when an item instance does not exist.
	ErrInstanceNotFound = errors.New("item instance not found")
)

// ReplenishmentMode identifies the shopping list calculation mode.
type ReplenishmentMode string

const (
	ReplenishMode ReplenishmentMode = "replenish"
	TargetMode    ReplenishmentMode = "target"
)

// Valid returns true if the mode is a valid value.
func (m ReplenishmentMode) Valid() bool {
	return m == ReplenishMode || m == TargetMode
}

// Item represents a user's specific instance of interest in a product,
// with an optional target quantity and replenishment mode.
type Item struct {
	ID                string                  `json:"id"`
	UserID            string                  `json:"userId"`
	ProductID         string                  `json:"productId"`
	Product           *product.ProductSummary `json:"product"` // populated by join queries
	TargetQuantity    *int                    `json:"targetQuantity"`
	CreatedAt         time.Time               `json:"createdAt"`
	ReplenishmentMode ReplenishmentMode       `json:"replenishmentMode"`
}

// ItemInstance represents a single physical unit tracked in the pantry.
type ItemInstance struct {
	ID            string     `json:"id"`
	ItemID        string     `json:"itemId"`
	StockInAt     time.Time  `json:"stockInAt"`
	ExpiresAt     *time.Time `json:"expiresAt"`
	RemovedAt     *time.Time `json:"removedAt"`
	RemovalReason *string    `json:"removalReason"`
	CreatedAt     time.Time  `json:"createdAt"`
}

// Pantry provides database operations for items and item instances.
type Pantry struct {
	db *sql.DB

	// Broadcaster publishes an Inventory_Event after each successful
	// mutation. Nil in every existing test; publish calls are skipped when nil.
	Broadcaster interface {
		PublishInventoryEvent(item InventoryItem)
	}
}

// NewPantry creates a new Pantry with the given database connection.
func NewPantry(db *sql.DB) *Pantry {
	return &Pantry{db: db}
}

// GetOrCreateItem returns the item for the given userID and productID.
// If no such item exists, it creates one and returns it.
func (r *Pantry) GetOrCreateItem(ctx context.Context, userID, productID string) (*Item, error) {
	// First try to get existing item
	item, err := r.getItemByUserAndProduct(ctx, userID, productID)
	if err != nil {
		return nil, fmt.Errorf("GetOrCreateItem get: %w", err)
	}
	if item != nil {
		return item, nil
	}

	// Create new item
	itemID := uuid.NewString()
	_, err = r.db.ExecContext(ctx,
		`INSERT INTO items (id, user_id, product_id) VALUES (?, ?, ?)`,
		itemID, userID, productID,
	)
	if err != nil {
		return nil, fmt.Errorf("GetOrCreateItem insert: %w", err)
	}

	// Return the newly created item
	return r.getItemByID(ctx, itemID)
}

// getItemByUserAndProduct is a helper that returns the item for the given userID and productID.
func (r *Pantry) getItemByUserAndProduct(ctx context.Context, userID, productID string) (*Item, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT 
			i.id, i.user_id, i.product_id, i.target_quantity, i.created_at, i.replenishment_mode,
			p.id, p.name, COALESCE(p.category, ''), COALESCE(p.unit_of_measure, ''), COALESCE(p.image_url, ''), COALESCE(p.external_source, '')
		FROM items i
		JOIN products p ON p.id = i.product_id
		WHERE i.user_id = ? AND i.product_id = ?`,
		userID, productID,
	)

	item, err := scanItem(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("getItemByUserAndProduct: %w", err)
	}
	return item, nil
}

// getItemByID is a helper that returns the item with the given ID.
func (r *Pantry) getItemByID(ctx context.Context, itemID string) (*Item, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT 
			i.id, i.user_id, i.product_id, i.target_quantity, i.created_at, i.replenishment_mode,
			p.id, p.name, COALESCE(p.category, ''), COALESCE(p.unit_of_measure, ''), COALESCE(p.image_url, ''), COALESCE(p.external_source, '')
		FROM items i
		JOIN products p ON p.id = i.product_id
		WHERE i.id = ?`,
		itemID,
	)

	item, err := scanItem(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("getItemByID: %w", err)
	}
	return item, nil
}

// ListItems returns all items for the given userID, ordered by product name.
func (r *Pantry) ListItems(ctx context.Context, userID string) ([]Item, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT 
			i.id, i.user_id, i.product_id, i.target_quantity, i.created_at, i.replenishment_mode,
			p.id, p.name, COALESCE(p.category, ''), COALESCE(p.unit_of_measure, ''), COALESCE(p.image_url, ''), COALESCE(p.external_source, '')
		FROM items i
		JOIN products p ON p.id = i.product_id
		WHERE i.user_id = ? AND i.shelved = 0
		ORDER BY p.name`,
		userID,
	)
	if err != nil {
		return nil, fmt.Errorf("ListItems: %w", err)
	}
	defer rows.Close()

	var items []Item
	for rows.Next() {
		item, err := scanItem(rows)
		if err != nil {
			return nil, fmt.Errorf("ListItems scan: %w", err)
		}
		items = append(items, *item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("ListItems rows: %w", err)
	}
	return items, nil
}

// ListItemInstances returns all item instances for the given itemID,
// ordered by expiration date ascending (use-oldest-first).
func (r *Pantry) ListItemInstances(ctx context.Context, itemID string) ([]ItemInstance, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, item_id, stock_in_at, expires_at, removed_at, removal_reason, created_at
		FROM item_instances
		WHERE item_id = ? AND removed_at IS NULL
		ORDER BY expires_at ASC NULLS LAST`,
		itemID,
	)
	if err != nil {
		return nil, fmt.Errorf("ListItemInstances: %w", err)
	}
	defer rows.Close()

	var instances []ItemInstance
	for rows.Next() {
		instance, err := scanItemInstance(rows)
		if err != nil {
			return nil, fmt.Errorf("ListItemInstances scan: %w", err)
		}
		instances = append(instances, *instance)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("ListItemInstances rows: %w", err)
	}
	return instances, nil
}

// AddInstance adds a new item instance to the inventory.
// If instance.ID is empty, a new UUID is generated.
func (r *Pantry) AddInstance(ctx context.Context, instance ItemInstance) (*ItemInstance, error) {
	if instance.ID == "" {
		instance.ID = uuid.NewString()
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("AddInstance: %w", err)
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx,
		`INSERT INTO item_instances (id, item_id, stock_in_at, expires_at)
		 VALUES (?, ?, ?, ?)`,
		instance.ID,
		instance.ItemID,
		instance.StockInAt,
		nullableTime(instance.ExpiresAt),
	)
	if err != nil {
		return nil, fmt.Errorf("AddInstance: %w", err)
	}
	if err := UnshelveItem(ctx, tx, instance.ItemID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("AddInstance: %w", err)
	}

	addedInstance, err := r.GetInstance(ctx, instance.ID)
	if err != nil {
		return nil, err
	}

	r.publishInventoryEvent(ctx, instance.ItemID)

	return addedInstance, nil
}

// UnshelveItem puts a wiped item back on the shelf. Stocking a unit used to
// do this in a database trigger. The insert and this update share one
// transaction, so a failure leaves the item hidden.
func UnshelveItem(ctx context.Context, q interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, itemID string) error {
	if _, err := q.ExecContext(ctx, `UPDATE items SET shelved = 0 WHERE id = ?`, itemID); err != nil {
		return fmt.Errorf("could not put the item back on the shelf: %w", err)
	}
	return nil
}

// RemoveInstance marks an item instance as removed by setting removed_at
// and removal_reason. Returns ErrInstanceNotFound if the instance does not exist
// or is already removed.
func (r *Pantry) RemoveInstance(ctx context.Context, instanceID string, reason string) error {
	// First check if the instance exists and is not already removed
	existing, err := r.GetInstance(ctx, instanceID)
	if err != nil {
		return fmt.Errorf("RemoveInstance: %w", err)
	}
	if existing == nil || existing.RemovedAt != nil {
		return ErrInstanceNotFound
	}

	now := time.Now()
	res, err := r.db.ExecContext(ctx,
		`UPDATE item_instances SET removed_at = ?, removal_reason = ? WHERE id = ? AND removed_at IS NULL`,
		now, reason, instanceID,
	)
	if err != nil {
		return fmt.Errorf("RemoveInstance: %w", err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("RemoveInstance rows affected: %w", err)
	}
	if n == 0 {
		return ErrInstanceNotFound
	}

	r.publishInventoryEvent(ctx, existing.ItemID)

	return nil
}

// UpdateTargetQuantity sets the target_quantity for the given item.
// Returns ErrInstanceNotFound if no item with that ID exists.
func (r *Pantry) UpdateTargetQuantity(ctx context.Context, itemID string, qty int) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE items SET target_quantity = ? WHERE id = ?`,
		qty, itemID,
	)
	if err != nil {
		return fmt.Errorf("UpdateTargetQuantity: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("UpdateTargetQuantity rows affected: %w", err)
	}
	if n == 0 {
		return ErrInstanceNotFound
	}

	r.publishInventoryEvent(ctx, itemID)

	return nil
}

// UpdateReplenishmentMode sets the replenishment_mode for the given item.
// Returns ErrInstanceNotFound if no item with that ID exists.
// Enforces the two-value set in Go.
func (r *Pantry) UpdateReplenishmentMode(ctx context.Context, itemID string, mode ReplenishmentMode) error {
	if !mode.Valid() {
		return fmt.Errorf("invalid replenishment mode: %q", mode)
	}
	res, err := r.db.ExecContext(ctx,
		`UPDATE items SET replenishment_mode = ? WHERE id = ?`,
		string(mode), itemID,
	)
	if err != nil {
		return fmt.Errorf("UpdateReplenishmentMode: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("UpdateReplenishmentMode rows affected: %w", err)
	}
	if n == 0 {
		return ErrInstanceNotFound
	}

	r.publishInventoryEvent(ctx, itemID)

	return nil
}

// GetTargetQuantity returns the target_quantity for the given item, or nil if not found.
func (r *Pantry) GetTargetQuantity(ctx context.Context, itemID string) (*int, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT target_quantity FROM items WHERE id = ?`,
		itemID,
	)

	var qty sql.NullInt64
	if err := row.Scan(&qty); err != nil {
		return nil, fmt.Errorf("GetTargetQuantity: %w", err)
	}

	if qty.Valid {
		q := int(qty.Int64)
		return &q, nil
	}
	return nil, nil
}

// GetReplenishmentMode returns the replenishment_mode for the given item.
// Returns TargetMode if not found (default per requirement 6.2).
func (r *Pantry) GetReplenishmentMode(ctx context.Context, itemID string) (ReplenishmentMode, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT replenishment_mode FROM items WHERE id = ?`,
		itemID,
	)

	var mode string
	if err := row.Scan(&mode); err != nil {
		return "", fmt.Errorf("GetReplenishmentMode: %w", err)
	}

	if mode == "" {
		return TargetMode, nil
	}
	m := ReplenishmentMode(mode)
	if !m.Valid() {
		return TargetMode, nil
	}
	return m, nil
}

// List returns all item instances in the pantry, ordered by creation time.
// Used for counting instances per item in provisioning.
func (r *Pantry) List(ctx context.Context) ([]ItemInstance, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, item_id, stock_in_at, expires_at, removed_at, removal_reason, created_at
		 FROM item_instances WHERE removed_at IS NULL
		 ORDER BY created_at ASC`,
	)
	if err != nil {
		return nil, fmt.Errorf("List: %w", err)
	}
	defer rows.Close()

	var instances []ItemInstance
	for rows.Next() {
		instance, err := scanItemInstance(rows)
		if err != nil {
			return nil, fmt.Errorf("List scan: %w", err)
		}
		instances = append(instances, *instance)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("List rows: %w", err)
	}
	return instances, nil
}

// publishInventoryEvent fetches the aggregated InventoryItem for itemID and
// publishes it via Broadcaster. It is a no-op if Broadcaster is nil, the
// item can't be found, or the fetch fails, since the mutation that
// triggered it has already succeeded and must not fail because of it.
func (r *Pantry) publishInventoryEvent(ctx context.Context, itemID string) {
	if r.Broadcaster == nil {
		return
	}
	invItem, err := r.GetInventoryItem(ctx, itemID, time.Now(), DefaultWarningDays)
	if err != nil || invItem == nil {
		return
	}
	r.Broadcaster.PublishInventoryEvent(*invItem)
}

// GetItem returns the item with the given ID, or nil if no such item exists.
// A wiped (shelved) item is hidden from ListItems and still returned here.
func (r *Pantry) GetItem(ctx context.Context, itemID string) (*Item, error) {
	return r.getItemByID(ctx, itemID)
}

// GetInstance returns the item instance with the given ID, or nil if no such
// instance exists.
func (r *Pantry) GetInstance(ctx context.Context, instanceID string) (*ItemInstance, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT id, item_id, stock_in_at, expires_at, removed_at, removal_reason, created_at
		 FROM item_instances WHERE id = ?`,
		instanceID,
	)

	instance, err := scanItemInstance(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("GetInstance: %w", err)
	}
	return instance, nil
}

// scanner interface for scanItem and scanItemInstance helpers
type scanner interface {
	Scan(dest ...interface{}) error
}

// scanItem is a helper that scans a row into an Item struct.
func scanItem(row scanner) (*Item, error) {
	var item Item
	var targetQuantity sql.NullInt64
	var replenishmentMode string
	var productID, productName, productCategory, productUnitOfMeasure, productImageURL, productExternalSource string

	err := row.Scan(
		&item.ID,
		&item.UserID,
		&item.ProductID,
		&targetQuantity,
		&item.CreatedAt,
		&replenishmentMode,
		&productID,
		&productName,
		&productCategory,
		&productUnitOfMeasure,
		&productImageURL,
		&productExternalSource,
	)
	if err != nil {
		return nil, err
	}

	if targetQuantity.Valid {
		tq := int(targetQuantity.Int64)
		item.TargetQuantity = &tq
	}

	item.ReplenishmentMode = ReplenishmentMode(replenishmentMode)
	if !item.ReplenishmentMode.Valid() {
		// Default to target mode if invalid
		item.ReplenishmentMode = TargetMode
	}

	item.Product = &product.ProductSummary{
		ID:             productID,
		Name:           productName,
		Category:       productCategory,
		UnitOfMeasure:  productUnitOfMeasure,
		ImageURL:       productImageURL,
		ExternalSource: product.ExternalSource(productExternalSource),
	}

	return &item, nil
}

// scanItemInstance is a helper that scans a row into an ItemInstance struct.
func scanItemInstance(row scanner) (*ItemInstance, error) {
	var instance ItemInstance
	var expiresAt, removedAt sql.NullTime
	var removalReason sql.NullString

	err := row.Scan(
		&instance.ID,
		&instance.ItemID,
		&instance.StockInAt,
		&expiresAt,
		&removedAt,
		&removalReason,
		&instance.CreatedAt,
	)
	if err != nil {
		return nil, err
	}

	if expiresAt.Valid {
		instance.ExpiresAt = &expiresAt.Time
	}
	if removedAt.Valid {
		instance.RemovedAt = &removedAt.Time
	}
	if removalReason.Valid {
		instance.RemovalReason = &removalReason.String
	}

	return &instance, nil
}

// nullableTime converts a *time.Time to sql.NullTime for nullable DATETIME columns.
func nullableTime(t *time.Time) sql.NullTime {
	if t == nil || t.IsZero() {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: *t, Valid: true}
}
