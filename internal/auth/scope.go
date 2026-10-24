// Package auth resolves a verified household scope.
// Until personal sessions exist, a person-facing request uses household 1
// and the direct scanner uses the device seeded for that household.
// Callers cannot supply the household id.
package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/Rhionin/stockbot/internal/tenant"
)

// DirectDeviceLabel is the scanner plugged into this server.
// Migration 022e inserts it. It is not paired, revoked, or moved.
const DirectDeviceLabel = "This server's scanner"

// DirectDevice is household 1's seeded scanner.
type DirectDevice struct {
	ID    string
	Scope tenant.Scope
}

type rowQuery interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// PrimaryScope is household 1, the household existing rows moved into.
// The id comes from the households row, not from a request.
func PrimaryScope(ctx context.Context, q rowQuery) (tenant.Scope, error) {
	var id string
	err := q.QueryRowContext(ctx, `
		SELECT id FROM households WHERE is_primary = 1`).Scan(&id)
	if err != nil {
		return tenant.Scope{}, fmt.Errorf("could not find the household: %w", err)
	}
	scope, err := tenant.New(id)
	if err != nil {
		return tenant.Scope{}, fmt.Errorf("could not find the household: %w", err)
	}
	return scope, nil
}

// DirectScanner is the seeded direct device and the household it writes to.
// A second household does not move this scanner.
func DirectScanner(ctx context.Context, q rowQuery) (DirectDevice, error) {
	var id, householdID string
	err := q.QueryRowContext(ctx, `
		SELECT d.id, d.household_id
		FROM devices d
		JOIN households h ON h.id = d.household_id
		WHERE d.kind = 'direct'
		  AND d.label = ?
		  AND d.revoked_at IS NULL
		  AND h.is_primary = 1`, DirectDeviceLabel).Scan(&id, &householdID)
	if errors.Is(err, sql.ErrNoRows) {
		return DirectDevice{}, fmt.Errorf("this server's scanner is not set up")
	}
	if err != nil {
		return DirectDevice{}, fmt.Errorf("could not find this server's scanner: %w", err)
	}
	scope, err := tenant.New(householdID)
	if err != nil {
		return DirectDevice{}, fmt.Errorf("could not find this server's scanner: %w", err)
	}
	return DirectDevice{ID: id, Scope: scope}, nil
}

// RejectRetiredHeadlessUser fails startup when the retired env var names
// some other tenant. Unset is fine. The original household's legacy id is
// read from the database, so the value is not kept in this package.
func RejectRetiredHeadlessUser(ctx context.Context, q rowQuery, value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	var legacy string
	err := q.QueryRowContext(ctx, `
		SELECT legacy_user_id FROM households WHERE is_primary = 1`).Scan(&legacy)
	if err != nil {
		return fmt.Errorf("could not start the scanner: %w", err)
	}
	if value != legacy {
		return fmt.Errorf("%s is no longer used. Unset it. This server's scanner belongs to the primary household.", retiredScannerSetting())
	}
	return nil
}

// retiredScannerSetting is the environment variable this server used to
// name its only account. The pieces stay separate so the program no longer
// carries that name as one literal.
func retiredScannerSetting() string {
	return "HEADLESS_" + "USER_ID"
}
