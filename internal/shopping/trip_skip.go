package shopping

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/Rhionin/stockbot/internal/tenant"
)

// ActiveRemoveSkip reports whether itemID has an active remove skip this trip.
// When found, groupID is the skip's group_id (maybe empty).
func (s *Store) ActiveRemoveSkip(ctx context.Context, scope tenant.Scope, itemID string) (groupID string, found bool, err error) {
	if err := scope.Require(); err != nil {
		return "", false, err
	}
	if itemID == "" {
		return "", false, nil
	}
	household := scope.HouseholdID()
	var group sql.NullString
	err = s.db.QueryRowContext(ctx, `
		SELECT COALESCE(s.group_id, '')
		FROM staged_cart_skips s
		JOIN households h ON h.id = s.household_id
		WHERE s.household_id = ? AND s.user_id = ? AND s.item_id = ?
		  AND s.expires_trip > h.trip_seq`,
		household, household, itemID).Scan(&group)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("could not read remove skip: %w", err)
	}
	return group.String, true, nil
}
