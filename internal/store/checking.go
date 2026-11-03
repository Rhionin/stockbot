package store

import (
	"context"
	"database/sql"
	"log"
	"strings"

	"github.com/Rhionin/stockbot/internal/cart"
	"github.com/Rhionin/stockbot/internal/tenant"
)

func grantedScopeIncludesProductCompact(grantedScope string) bool {
	for _, tok := range strings.Fields(grantedScope) {
		if tok == "product.compact" {
			return true
		}
	}
	return false
}

func (c *Catalog) anyConnectedGrantIncludesProductCompact(ctx context.Context, householdID string) (bool, error) {
	rows, err := c.db.QueryContext(ctx, `
		SELECT granted_scope FROM provider_connections
		WHERE household_id = ? AND provider_id = 'kroger' AND state = 'connected'`, householdID)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var scopeText sql.NullString
		if err := rows.Scan(&scopeText); err != nil {
			return false, err
		}
		if scopeText.Valid && grantedScopeIncludesProductCompact(scopeText.String) {
			return true, nil
		}
	}
	return false, rows.Err()
}

func (c *Catalog) lookupsEnabled(ctx context.Context, scope tenant.Scope, chk CheckingContext) (bool, error) {
	if chk.KrogerDisabled {
		return false, nil
	}
	var productAccess sql.NullInt64
	err := c.db.QueryRowContext(ctx, `
		SELECT product_access FROM provider_app_credentials
		WHERE household_id = ? AND provider_id = 'kroger'`, scope.HouseholdID()).Scan(&productAccess)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !productAccess.Valid || productAccess.Int64 != 1 {
		return false, nil
	}
	hasCompact, err := c.anyConnectedGrantIncludesProductCompact(ctx, scope.HouseholdID())
	if err != nil {
		return false, err
	}
	if !hasCompact {
		return false, nil
	}
	var loc sql.NullString
	err = c.db.QueryRowContext(ctx, `
		SELECT location_id FROM stores
		WHERE household_id = ? AND linked_provider = 'kroger' AND location_id IS NOT NULL AND location_id != ''
		LIMIT 1`, scope.HouseholdID()).Scan(&loc)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !loc.Valid {
		return false, nil
	}
	return true, nil
}

func (c *Catalog) canConnect(ctx context.Context, scope tenant.Scope, chk CheckingContext) (bool, error) {
	return cart.OAuthAuthorizeAllowed(ctx, c.db, scope, cart.OAuthConnectInput{
		KrogerDisabled: chk.KrogerDisabled,
	})
}

func (c *Catalog) checkReason(ctx context.Context, scope tenant.Scope, chk CheckingContext, st Store) *string {
	if st.LinkedProvider == nil || chk.KrogerDisabled {
		return nil
	}
	var productAccess sql.NullInt64
	err := c.db.QueryRowContext(ctx, `
		SELECT product_access FROM provider_app_credentials
		WHERE household_id = ? AND provider_id = 'kroger'`, scope.HouseholdID()).Scan(&productAccess)
	if err == sql.ErrNoRows {
		r := "not_configured"
		return &r
	}
	if err != nil {
		log.Printf("store checkReason: read product_access: %v", err)
		return nil
	}
	var connected int
	err = c.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM provider_connections
		WHERE household_id = ? AND provider_id = 'kroger' AND state = 'connected'`, scope.HouseholdID()).Scan(&connected)
	if err != nil {
		log.Printf("store checkReason: count connected: %v", err)
		return nil
	}
	if connected == 0 {
		r := "not_connected"
		return &r
	}
	if !productAccess.Valid || productAccess.Int64 != 1 {
		r := "no_product_access"
		return &r
	}
	hasCompact, err := c.anyConnectedGrantIncludesProductCompact(ctx, scope.HouseholdID())
	if err != nil {
		log.Printf("store checkReason: read granted_scope: %v", err)
		return nil
	}
	if !hasCompact {
		r := "no_product_access"
		return &r
	}
	if st.LocationID == nil || strings.TrimSpace(*st.LocationID) == "" {
		r := "no_location"
		return &r
	}
	return nil
}
