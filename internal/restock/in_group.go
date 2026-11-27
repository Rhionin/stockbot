package restock

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/Rhionin/stockbot/internal/tenant"
)

// InGroupError refuses an item target above 0 on a grouped product: the
// group's goal is the only amount or months a member follows.
type InGroupError struct {
	GroupID   string
	GroupName string
}

func (e *InGroupError) Error() string {
	return "This product is in a group. Change what the group keeps on hand."
}

func groupOfItem(ctx context.Context, scope tenant.Scope, q Queryer, itemID string) (*InGroupError, error) {
	if err := scope.Require(); err != nil {
		return nil, err
	}
	var id, name string
	err := q.QueryRowContext(ctx, `
		SELECT g.id, g.name
		FROM items i
		JOIN product_group_members m ON m.product_id = i.product_id AND m.household_id = i.household_id
		JOIN product_groups g ON g.id = m.group_id AND g.household_id = m.household_id
		WHERE i.id = ? AND i.household_id = ?`,
		itemID, scope.HouseholdID()).Scan(&id, &name)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("could not read the item's group: %w", err)
	}
	return &InGroupError{GroupID: id, GroupName: name}, nil
}
