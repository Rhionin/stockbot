package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/Rhionin/stockbot/internal/changes"
	"github.com/Rhionin/stockbot/internal/tenant"
	"github.com/google/uuid"
)

// Catalog stores household grocery stores.
type Catalog struct {
	db *sql.DB
}

// NewCatalog opens the store catalog on db.
func NewCatalog(db *sql.DB) *Catalog {
	return &Catalog{db: db}
}

func (c *Catalog) now() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

// List returns every store in order plus household defaults and checking flags.
func (c *Catalog) List(ctx context.Context, scope tenant.Scope, chk CheckingContext) (StoresResponse, error) {
	if err := scope.Require(); err != nil {
		return StoresResponse{}, err
	}
	stores, err := c.loadStores(ctx, scope)
	if err != nil {
		return StoresResponse{}, err
	}
	defaultID, err := c.readDefaultStoreID(ctx, scope)
	if err != nil {
		return StoresResponse{}, err
	}
	lookups, err := c.lookupsEnabled(ctx, scope, chk)
	if err != nil {
		return StoresResponse{}, err
	}
	canConnect, err := c.canConnect(ctx, scope, chk)
	if err != nil {
		return StoresResponse{}, err
	}
	if chk.KrogerDisabled {
		for i := range stores {
			if stores[i].LinkedProvider != nil {
				stores[i].CheckReason = nil
				stores[i].Check = nil
			}
		}
	} else if !lookups {
		for i := range stores {
			if stores[i].LinkedProvider != nil {
				reason := c.checkReason(ctx, scope, chk, stores[i])
				stores[i].CheckReason = reason
			}
		}
	}
	return StoresResponse{
		Stores:         stores,
		DefaultStoreID: defaultID,
		LookupsEnabled: lookups,
		CanConnect:     canConnect,
	}, nil
}

// Create appends a store at the end of household order.
func (c *Catalog) Create(ctx context.Context, scope tenant.Scope, in CreateInput) (Store, error) {
	if err := scope.Require(); err != nil {
		return Store{}, err
	}
	display, key, err := NameKey(in.Name)
	if err != nil {
		return Store{}, err
	}
	kind, err := validateKind(in.Kind)
	if err != nil {
		return Store{}, err
	}
	linked, err := validateLinkedProvider(in.LinkedProvider)
	if err != nil {
		return Store{}, err
	}
	var locID, locChain *string
	if linked != nil {
		if err := validateLocation(linked, in.LocationID, in.LocationChain); err != nil {
			return Store{}, err
		}
		locID = trimPtr(in.LocationID)
		locChain = trimPtr(in.LocationChain)
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return Store{}, fmt.Errorf("could not add the store: %w", err)
	}
	defer tx.Rollback()
	var maxPos int
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(position), -1) FROM stores WHERE household_id = ?`,
		scope.HouseholdID()).Scan(&maxPos); err != nil {
		return Store{}, fmt.Errorf("could not add the store: %w", err)
	}
	id := uuid.NewString()
	when := c.now()
	_, err = tx.ExecContext(ctx, `
		INSERT INTO stores (
			id, household_id, name, name_key, kind, linked_provider,
			location_id, location_chain, position, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, scope.HouseholdID(), display, key, kind, linked, locID, locChain, maxPos+1, when, when)
	if err = sqlUnique(err); err != nil {
		return Store{}, err
	}
	if err := changes.Bump(ctx, tx, scope.HouseholdID(), changes.ScopeList); err != nil {
		return Store{}, fmt.Errorf("could not add the store: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Store{}, fmt.Errorf("could not add the store: %w", err)
	}
	st, err := c.getStore(ctx, scope, id)
	if err != nil {
		return Store{}, err
	}
	return st, nil
}

// Patch updates one store.
func (c *Catalog) Patch(ctx context.Context, scope tenant.Scope, id string, in PatchInput) (Store, error) {
	if err := scope.Require(); err != nil {
		return Store{}, err
	}
	cur, err := c.getStoreRow(ctx, scope, id)
	if err != nil {
		return Store{}, err
	}
	name := cur.name
	key := cur.nameKey
	if in.Name != nil {
		name, key, err = NameKey(*in.Name)
		if err != nil {
			return Store{}, err
		}
	}
	kind := cur.kind
	if in.Kind != nil {
		kind, err = validateKind(*in.Kind)
		if err != nil {
			return Store{}, err
		}
	}
	linked := cur.linked
	locID := cur.locationID
	locChain := cur.locationChain
	if in.SetLinked {
		if in.LinkedProvider == nil {
			linked = nil
			locID = nil
			locChain = nil
		} else {
			lp := strings.TrimSpace(*in.LinkedProvider)
			if lp == "" {
				linked = nil
				locID = nil
				locChain = nil
			} else {
				if lp != "kroger" {
					return Store{}, invalid("Check what you entered and try again.", "linkedProvider")
				}
				linked = &lp
			}
		}
	}
	if in.SetLocation {
		if linked == nil {
			return Store{}, invalid("Choose a store location.", "locationId")
		}
		locID = trimPtr(in.LocationID)
		locChain = trimPtr(in.LocationChain)
	}
	if linked != nil {
		lid, lch := locID, locChain
		if err := validateLocation(linked, lid, lch); err != nil {
			return Store{}, err
		}
	}
	locationChanged := !ptrEqual(locID, cur.locationID) || !ptrEqual(locChain, cur.locationChain)
	unlinked := cur.linked != nil && linked == nil

	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return Store{}, fmt.Errorf("could not update the store: %w", err)
	}
	defer tx.Rollback()
	when := c.now()
	res, err := tx.ExecContext(ctx, `
		UPDATE stores SET name = ?, name_key = ?, kind = ?, linked_provider = ?,
			location_id = ?, location_chain = ?, updated_at = ?
		WHERE id = ? AND household_id = ?`,
		name, key, kind, linked, locID, locChain, when, id, scope.HouseholdID())
	if err = sqlUnique(err); err != nil {
		return Store{}, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return Store{}, notFound()
	}
	if locationChanged || unlinked {
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM store_availability WHERE household_id = ? AND store_id = ?`,
			scope.HouseholdID(), id); err != nil {
			return Store{}, fmt.Errorf("could not update the store: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM store_checks WHERE household_id = ? AND store_id = ?`,
			scope.HouseholdID(), id); err != nil {
			return Store{}, fmt.Errorf("could not update the store: %w", err)
		}
	}
	if err := changes.Bump(ctx, tx, scope.HouseholdID(), changes.ScopeList); err != nil {
		return Store{}, fmt.Errorf("could not update the store: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Store{}, fmt.Errorf("could not update the store: %w", err)
	}
	return c.getStore(ctx, scope, id)
}

// SetOrder replaces the full store order.
func (c *Catalog) SetOrder(ctx context.Context, scope tenant.Scope, ids []string, chk CheckingContext) (StoresResponse, error) {
	if err := scope.Require(); err != nil {
		return StoresResponse{}, err
	}
	normalized := make([]string, 0, len(ids))
	seen := map[string]struct{}{}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			return StoresResponse{}, invalid("Check what you entered and try again.", "storeIds")
		}
		if _, ok := seen[id]; ok {
			return StoresResponse{}, invalid("Check what you entered and try again.", "storeIds")
		}
		seen[id] = struct{}{}
		normalized = append(normalized, id)
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return StoresResponse{}, fmt.Errorf("could not reorder stores: %w", err)
	}
	defer tx.Rollback()
	existing, err := c.loadStoreIDsTx(ctx, tx, scope)
	if err != nil {
		return StoresResponse{}, err
	}
	if len(normalized) == 0 {
		if len(existing) != 0 {
			return StoresResponse{}, invalid("Choose at least one store.", "storeIds")
		}
		if err := tx.Commit(); err != nil {
			return StoresResponse{}, fmt.Errorf("could not reorder stores: %w", err)
		}
		return c.List(ctx, scope, chk)
	}
	if len(existing) != len(normalized) {
		return StoresResponse{}, notFound()
	}
	for _, id := range existing {
		if _, ok := seen[id]; !ok {
			return StoresResponse{}, notFound()
		}
	}
	for pos, id := range normalized {
		res, err := tx.ExecContext(ctx, `
			UPDATE stores SET position = ?, updated_at = ? WHERE id = ? AND household_id = ?`,
			pos, c.now(), id, scope.HouseholdID())
		if err != nil {
			return StoresResponse{}, fmt.Errorf("could not reorder stores: %w", err)
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return StoresResponse{}, notFound()
		}
	}
	if err := changes.Bump(ctx, tx, scope.HouseholdID(), changes.ScopeList); err != nil {
		return StoresResponse{}, fmt.Errorf("could not reorder stores: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return StoresResponse{}, fmt.Errorf("could not reorder stores: %w", err)
	}
	return c.List(ctx, scope, chk)
}

// SetDefault sets or clears the household default store.
func (c *Catalog) SetDefault(ctx context.Context, scope tenant.Scope, storeID *string, chk CheckingContext) (StoresResponse, error) {
	if err := scope.Require(); err != nil {
		return StoresResponse{}, err
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return StoresResponse{}, fmt.Errorf("could not set the default store: %w", err)
	}
	defer tx.Rollback()
	if storeID == nil || strings.TrimSpace(*storeID) == "" {
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM household_settings WHERE household_id = ? AND key = ?`,
			scope.HouseholdID(), settingDefaultStoreID); err != nil {
			return StoresResponse{}, fmt.Errorf("could not set the default store: %w", err)
		}
	} else {
		id := strings.TrimSpace(*storeID)
		var one int
		if err := tx.QueryRowContext(ctx, `
			SELECT 1 FROM stores WHERE id = ? AND household_id = ?`, id, scope.HouseholdID()).Scan(&one); err != nil {
			if err == sql.ErrNoRows {
				return StoresResponse{}, notFound()
			}
			return StoresResponse{}, fmt.Errorf("could not set the default store: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO household_settings (household_id, key, value) VALUES (?, ?, ?)
			ON CONFLICT(household_id, key) DO UPDATE SET value = excluded.value`,
			scope.HouseholdID(), settingDefaultStoreID, id); err != nil {
			return StoresResponse{}, fmt.Errorf("could not set the default store: %w", err)
		}
	}
	if err := changes.Bump(ctx, tx, scope.HouseholdID(), changes.ScopeList); err != nil {
		return StoresResponse{}, fmt.Errorf("could not set the default store: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return StoresResponse{}, fmt.Errorf("could not set the default store: %w", err)
	}
	return c.List(ctx, scope, chk)
}

// RemovalPreview describes delete impact for one store.
func (c *Catalog) RemovalPreview(ctx context.Context, scope tenant.Scope, id string) (RemovalPreview, error) {
	if err := scope.Require(); err != nil {
		return RemovalPreview{}, err
	}
	row, err := c.getStoreRow(ctx, scope, id)
	if err != nil {
		return RemovalPreview{}, err
	}
	return c.buildRemovalPreview(ctx, scope, row)
}

// Delete removes a store and related rows; trips are kept.
func (c *Catalog) Delete(ctx context.Context, scope tenant.Scope, id string) error {
	if err := scope.Require(); err != nil {
		return err
	}
	row, err := c.getStoreRow(ctx, scope, id)
	if err != nil {
		return err
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("could not remove the store: %w", err)
	}
	defer tx.Rollback()
	if err := c.deleteStoreTx(ctx, tx, scope, id, row.position); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("could not remove the store: %w", err)
	}
	return nil
}

func (c *Catalog) deleteStoreTx(ctx context.Context, tx *sql.Tx, scope tenant.Scope, id string, position int) error {
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM product_stores WHERE household_id = ? AND store_id = ?`,
		scope.HouseholdID(), id); err != nil {
		return fmt.Errorf("could not remove the store: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM store_availability WHERE household_id = ? AND store_id = ?`,
		scope.HouseholdID(), id); err != nil {
		return fmt.Errorf("could not remove the store: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM store_checks WHERE household_id = ? AND store_id = ?`,
		scope.HouseholdID(), id); err != nil {
		return fmt.Errorf("could not remove the store: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE shopping_list_items SET run_store_id = NULL
		WHERE household_id = ? AND run_store_id = ?`,
		scope.HouseholdID(), id); err != nil {
		return fmt.Errorf("could not remove the store: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM household_settings
		WHERE household_id = ? AND key = ? AND value = ?`,
		scope.HouseholdID(), settingDefaultStoreID, id); err != nil {
		return fmt.Errorf("could not remove the store: %w", err)
	}
	res, err := tx.ExecContext(ctx, `
		DELETE FROM stores WHERE id = ? AND household_id = ?`, id, scope.HouseholdID())
	if err != nil {
		return fmt.Errorf("could not remove the store: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return notFound()
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE stores SET position = position - 1, updated_at = ?
		WHERE household_id = ? AND position > ?`,
		c.now(), scope.HouseholdID(), position); err != nil {
		return fmt.Errorf("could not remove the store: %w", err)
	}
	return changes.Bump(ctx, tx, scope.HouseholdID(), changes.ScopeList)
}

type storeRow struct {
	id           string
	name         string
	nameKey      string
	kind         string
	linked       *string
	locationID   *string
	locationChain *string
	position     int
}

func (c *Catalog) getStoreRow(ctx context.Context, scope tenant.Scope, id string) (storeRow, error) {
	var row storeRow
	var linked, locID, locChain sql.NullString
	err := c.db.QueryRowContext(ctx, `
		SELECT id, name, name_key, kind, linked_provider, location_id, location_chain, position
		FROM stores WHERE id = ? AND household_id = ?`, id, scope.HouseholdID()).Scan(
		&row.id, &row.name, &row.nameKey, &row.kind, &linked, &locID, &locChain, &row.position)
	if err == sql.ErrNoRows {
		return storeRow{}, notFound()
	}
	if err != nil {
		return storeRow{}, fmt.Errorf("could not load the store: %w", err)
	}
	row.linked = nullStr(linked)
	row.locationID = nullStr(locID)
	row.locationChain = nullStr(locChain)
	return row, nil
}

func (c *Catalog) getStore(ctx context.Context, scope tenant.Scope, id string) (Store, error) {
	stores, err := c.loadStores(ctx, scope)
	if err != nil {
		return Store{}, err
	}
	for _, st := range stores {
		if st.ID == id {
			return st, nil
		}
	}
	return Store{}, notFound()
}

func (c *Catalog) loadStores(ctx context.Context, scope tenant.Scope) ([]Store, error) {
	rows, err := c.db.QueryContext(ctx, `
		SELECT id, name, kind, linked_provider, location_id, location_chain, position
		FROM stores WHERE household_id = ? ORDER BY position`, scope.HouseholdID())
	if err != nil {
		return nil, fmt.Errorf("could not list stores: %w", err)
	}
	defer rows.Close()
	var out []Store
	for rows.Next() {
		var st Store
		var linked, locID, locChain sql.NullString
		if err := rows.Scan(&st.ID, &st.Name, &st.Kind, &linked, &locID, &locChain, &st.Position); err != nil {
			return nil, fmt.Errorf("could not list stores: %w", err)
		}
		st.LinkedProvider = nullStr(linked)
		st.LocationID = nullStr(locID)
		st.LocationChain = nullStr(locChain)
		out = append(out, st)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("could not list stores: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("could not list stores: %w", err)
	}
	for i := range out {
		check, err := loadCheck(ctx, c.db, scope, out[i].ID)
		if err != nil {
			return nil, fmt.Errorf("could not list stores: %w", err)
		}
		out[i].Check = check
	}
	return out, nil
}

type queryRower interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func (c *Catalog) loadStoreIDs(ctx context.Context, scope tenant.Scope) ([]string, error) {
	return c.loadStoreIDsTx(ctx, c.db, scope)
}

func (c *Catalog) loadStoreIDsTx(ctx context.Context, q queryRower, scope tenant.Scope) ([]string, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT id FROM stores WHERE household_id = ? ORDER BY position`, scope.HouseholdID())
	if err != nil {
		return nil, fmt.Errorf("could not list stores: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (c *Catalog) readDefaultStoreID(ctx context.Context, scope tenant.Scope) (*string, error) {
	var raw string
	err := c.db.QueryRowContext(ctx, `
		SELECT value FROM household_settings WHERE household_id = ? AND key = ?`,
		scope.HouseholdID(), settingDefaultStoreID).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("could not read the default store: %w", err)
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	return &raw, nil
}

func nullStr(n sql.NullString) *string {
	if !n.Valid || strings.TrimSpace(n.String) == "" {
		return nil
	}
	s := strings.TrimSpace(n.String)
	return &s
}

func trimPtr(p *string) *string {
	if p == nil {
		return nil
	}
	s := strings.TrimSpace(*p)
	if s == "" {
		return nil
	}
	return &s
}

func ptrEqual(a, b *string) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

func loadCheck(ctx context.Context, db *sql.DB, scope tenant.Scope, storeID string) (*StoreCheck, error) {
	var st StoreCheck
	var finished, failure sql.NullString
	err := db.QueryRowContext(ctx, `
		SELECT state, started_at, finished_at, checked_count, found_count, total_count, failure
		FROM store_checks WHERE household_id = ? AND store_id = ?`,
		scope.HouseholdID(), storeID).Scan(
		&st.State, &st.StartedAt, &finished, &st.Checked, &st.Found, &st.Total, &failure)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	st.FinishedAt = nullStr(finished)
	st.Failure = nullStr(failure)
	return &st, nil
}
