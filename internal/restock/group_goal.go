package restock

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/Rhionin/stockbot/internal/group"
	"github.com/Rhionin/stockbot/internal/product"
	"github.com/Rhionin/stockbot/internal/tenant"
)

// GroupGoal is a grouped item's group goal as its settings row shows it.
// Unit is nil for don't restock.
type GroupGoal struct {
	Kind   string  `json:"kind"`
	Value  float64 `json:"value"`
	Unit   *string `json:"unit"`
	Status string  `json:"status"`
}

// GroupDefault is what a group with no goal follows, by the same rule as the
// planner: the household months, 3 months under a household amount, or don't
// restock for months 0 or amount 0.
func GroupDefault(home Target) (string, int) {
	if home.Value == 0 {
		return "dont_restock", 0
	}
	if home.Kind == KindAmount {
		return "amount", 3
	}
	return "months", int(home.Value)
}

func unitPtr(s string) *string { return &s }

func groupGoalOf(item *loadedItem, amountStatus string) *GroupGoal {
	switch {
	case item.goalItems.Valid:
		return &GroupGoal{Kind: "items", Value: float64(item.goalItems.Int64), Unit: unitPtr("items"), Status: group.StatusOK}
	case item.window.Valid && item.window.Int64 > 0:
		return &GroupGoal{Kind: "months", Value: float64(item.window.Int64), Unit: unitPtr("months"), Status: group.StatusOK}
	case item.window.Valid, item.quantity.Valid && item.quantity.Float64 <= 0:
		return &GroupGoal{Kind: "dont_restock", Value: 0, Status: group.StatusOK}
	case item.quantity.Valid:
		amount, unit, ok := product.DisplayNetSize(item.quantity.Float64, item.dimension)
		if !ok {
			return nil
		}
		if amountStatus == "" {
			amountStatus = group.StatusOK
		}
		return &GroupGoal{Kind: "amount", Value: amount, Unit: unitPtr(unit), Status: amountStatus}
	}
	return nil
}

// groupGoalStatuses checks every group amount goal against its members'
// sizes with one query for the whole snapshot.
func groupGoalStatuses(ctx context.Context, scope tenant.Scope, q Queryer, items map[string]*loadedItem) (map[string]string, error) {
	if err := scope.Require(); err != nil {
		return nil, err
	}
	dims := map[string]string{}
	for _, item := range items {
		if item.view.GroupID != "" && item.quantity.Valid && item.quantity.Float64 > 0 {
			dims[item.view.GroupID] = item.dimension
		}
	}
	out := map[string]string{}
	if len(dims) == 0 {
		return out, nil
	}
	household := scope.HouseholdID()
	rows, err := q.QueryContext(ctx, `
		SELECT m.group_id, m.product_id, p.net_base_value, COALESCE(p.net_dimension, ''),
		       (SELECT COUNT(*) FROM item_instances ii
		         JOIN items i ON i.id = ii.item_id AND i.household_id = ii.household_id
		         WHERE i.product_id = m.product_id AND i.household_id = ? AND ii.household_id = ? AND ii.removed_at IS NULL),
		       COALESCE((
		         SELECT 1 FROM item_targets t
		         JOIN items it ON it.id = t.item_id AND it.household_id = t.household_id
		         WHERE it.product_id = m.product_id AND it.household_id = ? AND t.household_id = ? AND t.value = 0
		         LIMIT 1
		       ), 0)
		FROM product_group_members m
		JOIN products p ON p.id = m.product_id
		WHERE m.household_id = ?`, household, household, household, household, household)
	if err != nil {
		return nil, fmt.Errorf("could not read group sizes: %w", err)
	}
	defer rows.Close()
	members := map[string][]group.Member{}
	for rows.Next() {
		var groupID string
		var m group.Member
		var net sql.NullFloat64
		var noRestock int
		if err := rows.Scan(&groupID, &m.ProductID, &net, &m.Dimension, &m.OnHand, &noRestock); err != nil {
			return nil, fmt.Errorf("could not read group sizes: %w", err)
		}
		if net.Valid {
			v := net.Float64
			m.NetBase = &v
		}
		m.NoRestock = noRestock != 0
		members[groupID] = append(members[groupID], m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("could not read group sizes: %w", err)
	}
	for id, dim := range dims {
		status, _ := group.AmountStatus(dim, members[id])
		out[id] = status
	}
	return out, nil
}
