// Package inventory provides inventory management functionality for items and item instances.
package inventory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Rhionin/stockbot/internal/changes"
	"github.com/Rhionin/stockbot/internal/product"
	"github.com/Rhionin/stockbot/internal/tenant"
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
		PublishInventoryEvent(householdID string, item InventoryItem)
	}
}

// NewPantry creates a new Pantry with the given database connection.
func NewPantry(db *sql.DB) *Pantry {
	return &Pantry{db: db}
}

// GetOrCreateItem returns the item for this household and product.
// If no such item exists, it creates one and returns it.
// user_id and household_id are both the household id so a new household
// does not collide with another household's product row. An existing row
// whose household_id is already this scope is returned instead of inserted.
func (r *Pantry) GetOrCreateItem(ctx context.Context, scope tenant.Scope, productID string) (*Item, error) {
	if err := scope.Require(); err != nil {
		return nil, err
	}
	item, err := r.getItemByProduct(ctx, scope, productID)
	if err != nil {
		return nil, fmt.Errorf("GetOrCreateItem get: %w", err)
	}
	if item != nil {
		return item, nil
	}

	itemID := uuid.NewString()
	householdID := scope.HouseholdID()
	_, err = r.db.ExecContext(ctx,
		`INSERT INTO items (id, user_id, product_id, household_id) VALUES (?, ?, ?, ?)`,
		itemID, householdID, productID, householdID,
	)
	if err != nil {
		return nil, fmt.Errorf("GetOrCreateItem insert: %w", err)
	}

	return r.getItemByID(ctx, scope, itemID)
}

// getItemByProduct returns the item for this household and product, or nil.
func (r *Pantry) getItemByProduct(ctx context.Context, scope tenant.Scope, productID string) (*Item, error) {
	if err := scope.Require(); err != nil {
		return nil, err
	}
	row := r.db.QueryRowContext(ctx, `
		SELECT 
			i.id, i.user_id, i.product_id, i.target_quantity, i.created_at, i.replenishment_mode,
			p.id, `+product.DisplayNameSQL("p")+`, COALESCE(p.category, ''), COALESCE(p.unit_of_measure, ''), COALESCE(p.image_url, ''), COALESCE(p.external_source, '')
		FROM items i
		JOIN products p ON p.id = i.product_id
		WHERE i.household_id = ? AND i.product_id = ?`,
		scope.HouseholdID(), scope.HouseholdID(), productID,
	)

	item, err := scanItem(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("could not read the item: %w", err)
	}
	return item, nil
}

// getItemByID returns the item with the given ID in this household, or nil.
func (r *Pantry) getItemByID(ctx context.Context, scope tenant.Scope, itemID string) (*Item, error) {
	if err := scope.Require(); err != nil {
		return nil, err
	}
	row := r.db.QueryRowContext(ctx, `
		SELECT 
			i.id, i.user_id, i.product_id, i.target_quantity, i.created_at, i.replenishment_mode,
			p.id, `+product.DisplayNameSQL("p")+`, COALESCE(p.category, ''), COALESCE(p.unit_of_measure, ''), COALESCE(p.image_url, ''), COALESCE(p.external_source, '')
		FROM items i
		JOIN products p ON p.id = i.product_id
		WHERE i.id = ? AND i.household_id = ?`,
		scope.HouseholdID(), itemID, scope.HouseholdID(),
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

// ListItems returns unshelved items for this household, ordered by product name.
func (r *Pantry) ListItems(ctx context.Context, scope tenant.Scope) ([]Item, error) {
	if err := scope.Require(); err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT 
			i.id, i.user_id, i.product_id, i.target_quantity, i.created_at, i.replenishment_mode,
			p.id, `+product.DisplayNameSQL("p")+`, COALESCE(p.category, ''), COALESCE(p.unit_of_measure, ''), COALESCE(p.image_url, ''), COALESCE(p.external_source, '')
		FROM items i
		JOIN products p ON p.id = i.product_id
		WHERE i.household_id = ? AND i.shelved = 0
		ORDER BY `+product.DisplayNameSQL("p"),
		scope.HouseholdID(), scope.HouseholdID(), scope.HouseholdID(),
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

// ListItemInstances returns active instances for the given item in this household,
// ordered by expiration date ascending (use-oldest-first).
func (r *Pantry) ListItemInstances(ctx context.Context, scope tenant.Scope, itemID string) ([]ItemInstance, error) {
	if err := scope.Require(); err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, item_id, stock_in_at, expires_at, removed_at, removal_reason, created_at
		FROM item_instances
		WHERE item_id = ? AND removed_at IS NULL AND household_id = ?
		ORDER BY expires_at ASC NULLS LAST`,
		itemID, scope.HouseholdID(),
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
// The item must already belong to this household.
func (r *Pantry) AddInstance(ctx context.Context, scope tenant.Scope, instance ItemInstance) (*ItemInstance, error) {
	if err := scope.Require(); err != nil {
		return nil, err
	}
	item, err := r.getItemByID(ctx, scope, instance.ItemID)
	if err != nil {
		return nil, fmt.Errorf("AddInstance: %w", err)
	}
	if item == nil {
		return nil, ErrInstanceNotFound
	}
	if instance.ID == "" {
		instance.ID = uuid.NewString()
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("AddInstance: %w", err)
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx,
		`INSERT INTO item_instances (id, item_id, stock_in_at, expires_at, household_id)
		 VALUES (?, ?, ?, ?, ?)`,
		instance.ID,
		instance.ItemID,
		instance.StockInAt,
		nullableTime(instance.ExpiresAt),
		scope.HouseholdID(),
	)
	if err != nil {
		return nil, fmt.Errorf("AddInstance: %w", err)
	}
	if err := UnshelveItem(ctx, tx, scope, instance.ItemID); err != nil {
		return nil, err
	}
	if err := changes.Bump(ctx, tx, scope.HouseholdID(), changes.ScopeStock); err != nil {
		return nil, fmt.Errorf("AddInstance: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("AddInstance: %w", err)
	}

	addedInstance, err := r.GetInstance(ctx, scope, instance.ID)
	if err != nil {
		return nil, err
	}

	r.publishInventoryEvent(ctx, scope, instance.ItemID)

	return addedInstance, nil
}

// UnshelveItem puts a wiped item back on the shelf. Stocking a unit used to
// do this in a database trigger. The insert and this update share one
// transaction, so a failure leaves the item hidden.
func UnshelveItem(ctx context.Context, q interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, scope tenant.Scope, itemID string) error {
	if err := scope.Require(); err != nil {
		return err
	}
	if _, err := q.ExecContext(ctx, `UPDATE items SET shelved = 0 WHERE id = ? AND household_id = ?`, itemID, scope.HouseholdID()); err != nil {
		return fmt.Errorf("could not put the item back on the shelf: %w", err)
	}
	return nil
}

// RemoveInstance marks an item instance as removed by setting removed_at
// and removal_reason. Returns ErrInstanceNotFound if the instance does not exist
// or is already removed.
func (r *Pantry) RemoveInstance(ctx context.Context, scope tenant.Scope, instanceID string, reason string) error {
	if err := scope.Require(); err != nil {
		return err
	}
	existing, err := r.GetInstance(ctx, scope, instanceID)
	if err != nil {
		return fmt.Errorf("RemoveInstance: %w", err)
	}
	if existing == nil || existing.RemovedAt != nil {
		return ErrInstanceNotFound
	}

	now := time.Now()
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("RemoveInstance: %w", err)
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx,
		`UPDATE item_instances SET removed_at = ?, removal_reason = ? WHERE id = ? AND removed_at IS NULL AND household_id = ?`,
		now, reason, instanceID, scope.HouseholdID(),
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
	if err := changes.Bump(ctx, tx, scope.HouseholdID(), changes.ScopeStock); err != nil {
		return fmt.Errorf("RemoveInstance: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("RemoveInstance: %w", err)
	}

	r.publishInventoryEvent(ctx, scope, existing.ItemID)

	return nil
}

// UpdateTargetQuantity sets the target_quantity for the given item.
// Returns ErrInstanceNotFound if no item with that ID exists in this household.
func (r *Pantry) UpdateTargetQuantity(ctx context.Context, scope tenant.Scope, itemID string, qty int) error {
	if err := scope.Require(); err != nil {
		return err
	}
	res, err := r.db.ExecContext(ctx,
		`UPDATE items SET target_quantity = ? WHERE id = ? AND household_id = ?`,
		qty, itemID, scope.HouseholdID(),
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

	r.publishInventoryEvent(ctx, scope, itemID)

	return nil
}

// UpdateReplenishmentMode sets the replenishment_mode for the given item.
// Returns ErrInstanceNotFound if no item with that ID exists in this household.
// Enforces the two-value set in Go.
func (r *Pantry) UpdateReplenishmentMode(ctx context.Context, scope tenant.Scope, itemID string, mode ReplenishmentMode) error {
	if err := scope.Require(); err != nil {
		return err
	}
	if !mode.Valid() {
		return fmt.Errorf("invalid replenishment mode: %q", mode)
	}
	res, err := r.db.ExecContext(ctx,
		`UPDATE items SET replenishment_mode = ? WHERE id = ? AND household_id = ?`,
		string(mode), itemID, scope.HouseholdID(),
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

	r.publishInventoryEvent(ctx, scope, itemID)

	return nil
}

// GetTargetQuantity returns the target_quantity for the given item, or nil if not found.
func (r *Pantry) GetTargetQuantity(ctx context.Context, scope tenant.Scope, itemID string) (*int, error) {
	if err := scope.Require(); err != nil {
		return nil, err
	}
	row := r.db.QueryRowContext(ctx,
		`SELECT target_quantity FROM items WHERE id = ? AND household_id = ?`,
		itemID, scope.HouseholdID(),
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
func (r *Pantry) GetReplenishmentMode(ctx context.Context, scope tenant.Scope, itemID string) (ReplenishmentMode, error) {
	if err := scope.Require(); err != nil {
		return "", err
	}
	row := r.db.QueryRowContext(ctx,
		`SELECT replenishment_mode FROM items WHERE id = ? AND household_id = ?`,
		itemID, scope.HouseholdID(),
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

// List returns this household's active item instances, ordered by creation time.
// Used for counting instances per item in provisioning.
func (r *Pantry) List(ctx context.Context, scope tenant.Scope) ([]ItemInstance, error) {
	if err := scope.Require(); err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, item_id, stock_in_at, expires_at, removed_at, removal_reason, created_at
		 FROM item_instances WHERE removed_at IS NULL AND household_id = ?
		 ORDER BY created_at ASC`,
		scope.HouseholdID(),
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
func (r *Pantry) publishInventoryEvent(ctx context.Context, scope tenant.Scope, itemID string) {
	if r.Broadcaster == nil {
		return
	}
	invItem, err := r.GetInventoryItem(ctx, scope, itemID, time.Now(), DefaultWarningDays)
	if err != nil || invItem == nil {
		return
	}
	r.Broadcaster.PublishInventoryEvent(scope.HouseholdID(), *invItem)
}

// GetItem returns the item with the given ID in this household, or nil if none exists.
// A wiped (shelved) item is hidden from ListItems and still returned here.
func (r *Pantry) GetItem(ctx context.Context, scope tenant.Scope, itemID string) (*Item, error) {
	if err := scope.Require(); err != nil {
		return nil, err
	}
	return r.getItemByID(ctx, scope, itemID)
}

// GetInstance returns the item instance with the given ID in this household,
// or nil if no such instance exists.
func (r *Pantry) GetInstance(ctx context.Context, scope tenant.Scope, instanceID string) (*ItemInstance, error) {
	if err := scope.Require(); err != nil {
		return nil, err
	}
	row := r.db.QueryRowContext(ctx,
		`SELECT id, item_id, stock_in_at, expires_at, removed_at, removal_reason, created_at
		 FROM item_instances WHERE id = ? AND household_id = ?`,
		instanceID, scope.HouseholdID(),
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
