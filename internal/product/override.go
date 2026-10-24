package product

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

// DisplayNameSQL is the name one household sees for a product alias.
// The caller binds household id to the single placeholder.
func DisplayNameSQL(alias string) string {
	if alias == "" {
		alias = "p"
	}
	return fmt.Sprintf(
		`COALESCE((SELECT o.display_name FROM household_product_overrides o WHERE o.product_id = %s.id AND o.household_id = ?), %s.name)`,
		alias, alias,
	)
}

// SaveDisplayName stores a name this household typed.
// The shared products row is not changed.
func (r *Catalog) SaveDisplayName(ctx context.Context, scope tenant.Scope, productID, name string) error {
	if err := scope.Require(); err != nil {
		return err
	}
	name = strings.TrimSpace(name)
	productID = strings.TrimSpace(productID)
	if productID == "" || name == "" {
		return fmt.Errorf("a product name is required")
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO household_product_overrides (household_id, product_id, display_name, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT (household_id, product_id) DO UPDATE SET
			display_name = excluded.display_name,
			updated_at = excluded.updated_at`,
		scope.HouseholdID(), productID, name, time.Now().UTC().Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("could not save the product name: %w", err)
	}
	return nil
}

// CreateTypedProduct stores a product this household named.
// The typed name is not written on the shared catalog row.
func (r *Catalog) CreateTypedProduct(ctx context.Context, scope tenant.Scope, product Product) (Product, error) {
	if err := scope.Require(); err != nil {
		return Product{}, err
	}
	typed := strings.TrimSpace(product.Name)
	if typed == "" {
		return Product{}, fmt.Errorf("a product name is required")
	}
	if strings.TrimSpace(product.ID) == "" {
		product.ID = uuid.NewString()
	}
	product.Name = ""
	product.Source = SourceUser
	product.NameOverridden = false
	if err := r.CreateProduct(ctx, product); err != nil {
		return Product{}, err
	}
	if err := r.SaveDisplayName(ctx, scope, product.ID, typed); err != nil {
		return Product{}, err
	}
	product.Name = typed
	product.NameOverridden = true
	return product, nil
}

// ProductFor returns the product as this household sees it.
// A name another household typed is not returned. An unknown id and a
// typed product this household does not own are both a missing product.
func (r *Catalog) ProductFor(ctx context.Context, scope tenant.Scope, id string) (*Product, error) {
	if err := scope.Require(); err != nil {
		return nil, err
	}
	got, err := r.GetProductByID(ctx, id)
	if err != nil || got == nil {
		return got, err
	}
	owned, err := r.ownsProduct(ctx, scope, got)
	if err != nil {
		return nil, err
	}
	if !owned {
		return nil, nil
	}
	name, overridden, err := r.overrideName(ctx, scope, got.ID)
	if err != nil {
		return nil, err
	}
	if overridden {
		got.Name = name
		got.NameOverridden = true
	}
	return got, nil
}

// LookupBarcode resolves a barcode for one household.
// That household's own mapping wins. A global catalog mapping is next.
// Another household's mapping is never used.
func (r *Catalog) LookupBarcode(ctx context.Context, scope tenant.Scope, barcode string) (*ProductSummary, error) {
	if err := scope.Require(); err != nil {
		return nil, err
	}
	row := r.db.QueryRowContext(ctx, `
		SELECT p.id, `+DisplayNameSQL("p")+`, COALESCE(p.category, ''), COALESCE(p.unit_of_measure, ''),
		       COALESCE(p.image_url, ''), COALESCE(p.external_source, ''), COALESCE(p.source, '')
		FROM barcodes b
		JOIN products p ON p.id = b.product_id
		WHERE b.barcode = ?
		  AND (
		        (b.source = 'user_override' AND b.household_id = ?)
		     OR (b.source = 'global' AND b.user_id = '')
		      )
		ORDER BY CASE b.source WHEN 'user_override' THEN 0 ELSE 1 END ASC
		LIMIT 1`,
		scope.HouseholdID(), barcode, scope.HouseholdID(),
	)
	var summary ProductSummary
	var externalSource, source string
	if err := row.Scan(&summary.ID, &summary.Name, &summary.Category, &summary.UnitOfMeasure, &summary.ImageURL, &externalSource, &source); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("could not look up that barcode: %w", err)
	}
	summary.ExternalSource = ExternalSource(externalSource)
	if source == SourceUser {
		owned, err := r.ownsProduct(ctx, scope, &Product{ID: summary.ID, Source: source})
		if err != nil {
			return nil, err
		}
		if !owned {
			return nil, nil
		}
	}
	return &summary, nil
}

// UpsertHouseholdBarcode maps a barcode to a product for this household only.
func (r *Catalog) UpsertHouseholdBarcode(ctx context.Context, scope tenant.Scope, barcode, productID string) error {
	if err := scope.Require(); err != nil {
		return err
	}
	barcode = strings.TrimSpace(barcode)
	productID = strings.TrimSpace(productID)
	if barcode == "" || productID == "" {
		return fmt.Errorf("a barcode and a product are required")
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO barcodes (barcode, product_id, source, user_id, household_id)
		VALUES (?, ?, 'user_override', ?, ?)
		ON CONFLICT (household_id, barcode, source, user_id) DO UPDATE SET
			product_id = excluded.product_id,
			household_id = excluded.household_id`,
		barcode, productID, scope.HouseholdID(), scope.HouseholdID(),
	)
	if err != nil {
		return fmt.Errorf("could not save that barcode: %w", err)
	}
	return nil
}

func (r *Catalog) overrideName(ctx context.Context, scope tenant.Scope, productID string) (string, bool, error) {
	var name string
	err := r.db.QueryRowContext(ctx, `
		SELECT display_name FROM household_product_overrides
		WHERE household_id = ? AND product_id = ?`,
		scope.HouseholdID(), productID).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("could not read the product name: %w", err)
	}
	return name, true, nil
}

func (r *Catalog) ownsProduct(ctx context.Context, scope tenant.Scope, got *Product) (bool, error) {
	if got.Source != SourceUser {
		return true, nil
	}
	var mine, others int
	err := r.db.QueryRowContext(ctx, `
		SELECT (
			SELECT COUNT(*) FROM household_product_overrides
			WHERE product_id = ? AND household_id = ?
		) + (
			SELECT COUNT(*) FROM items
			WHERE product_id = ? AND household_id = ?
		) + (
			SELECT COUNT(*) FROM barcodes
			WHERE product_id = ? AND household_id = ? AND source = 'user_override'
		),
		(
			SELECT COUNT(*) FROM household_product_overrides
			WHERE product_id = ? AND household_id != ?
		) + (
			SELECT COUNT(*) FROM items
			WHERE product_id = ? AND household_id != ?
		) + (
			SELECT COUNT(*) FROM barcodes
			WHERE product_id = ? AND household_id != ? AND source = 'user_override'
		)`,
		got.ID, scope.HouseholdID(),
		got.ID, scope.HouseholdID(),
		got.ID, scope.HouseholdID(),
		got.ID, scope.HouseholdID(),
		got.ID, scope.HouseholdID(),
		got.ID, scope.HouseholdID(),
	).Scan(&mine, &others)
	if err != nil {
		return false, fmt.Errorf("could not read the product: %w", err)
	}
	if mine > 0 {
		return true, nil
	}
	if others > 0 {
		return false, nil
	}
	var primary int
	if err := r.db.QueryRowContext(ctx, `SELECT is_primary FROM households WHERE id = ?`, scope.HouseholdID()).Scan(&primary); err != nil {
		return false, fmt.Errorf("could not read the product: %w", err)
	}
	return primary == 1, nil
}
