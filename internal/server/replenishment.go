package server

import (
	"context"
	"time"

	"github.com/Rhionin/stockbot/internal/inventory"
	"github.com/Rhionin/stockbot/internal/shopping"
	"github.com/Rhionin/stockbot/internal/supply"
	"github.com/Rhionin/stockbot/internal/tenant"
)

// shoppingProvision is the list after the supply snapshot has been written.
type shoppingProvision struct {
	Items  []inventory.Item
	Counts map[string]int
	Rows   []shopping.ShoppingListItem
	Needs  []shopping.ReplenishmentItem
	Deals  []shopping.Deal
}

type shoppingListReader interface {
	ListManualItems(ctx context.Context, scope tenant.Scope) ([]shopping.ShoppingListItem, error)
	ListUnpurchased(ctx context.Context, scope tenant.Scope) ([]shopping.ShoppingListItem, error)
	ListDeals(ctx context.Context, scope tenant.Scope) ([]shopping.Deal, error)
}

type shoppingPlanWriter interface {
	shoppingListReader
	SavePlannedLines(ctx context.Context, scope tenant.Scope, lines []shopping.PlannedLine, members []shopping.ShelfMember) error
}

type pantryLister interface {
	ListItems(ctx context.Context, scope tenant.Scope) ([]inventory.Item, error)
	ListItemInstances(ctx context.Context, scope tenant.Scope, itemID string) ([]inventory.ItemInstance, error)
}

// loadShoppingSnapshot reads the staged cart. It does not recompute the supply plan.
func loadShoppingSnapshot(ctx context.Context, scope tenant.Scope, pantry pantryLister, list shoppingListReader) (shoppingProvision, error) {
	if err := scope.Require(); err != nil {
		return shoppingProvision{}, err
	}
	items, err := pantry.ListItems(ctx, scope)
	if err != nil {
		return shoppingProvision{}, err
	}
	deals, err := list.ListDeals(ctx, scope)
	if err != nil {
		return shoppingProvision{}, err
	}
	counts, err := loadInstanceCounts(ctx, scope, pantry.ListItemInstances, items)
	if err != nil {
		return shoppingProvision{}, err
	}
	rows, err := list.ListUnpurchased(ctx, scope)
	if err != nil {
		return shoppingProvision{}, err
	}
	return shoppingProvision{
		Items:  items,
		Counts: counts,
		Rows:   rows,
		Needs:  replenishmentNeeds(items, counts),
		Deals:  deals,
	}, nil
}

// stageShoppingPlan snapshots the current supply plan into the staged cart.
// A later read or send uses those rows, including quantities the owner changed.
func stageShoppingPlan(ctx context.Context, scope tenant.Scope, pantry pantryLister, list shoppingPlanWriter, supplySvc *supply.Service, now time.Time) (shoppingProvision, error) {
	if err := scope.Require(); err != nil {
		return shoppingProvision{}, err
	}
	snap, err := loadShoppingSnapshot(ctx, scope, pantry, list)
	if err != nil {
		return shoppingProvision{}, err
	}
	if supplySvc == nil {
		return snap, nil
	}
	planned, err := supplySvc.Plan(ctx, scope, now)
	if err != nil {
		return shoppingProvision{}, err
	}
	groupOf := map[string]string{}
	if lookup, ok := list.(interface {
		ItemGroups(ctx context.Context, scope tenant.Scope) (map[string]string, error)
	}); ok {
		groupOf, err = lookup.ItemGroups(ctx, scope)
		if err != nil {
			return shoppingProvision{}, err
		}
	}
	if err := list.SavePlannedLines(ctx, scope, plannedLines(planned, snap.Items), shelfMembers(snap.Items, groupOf)); err != nil {
		return shoppingProvision{}, err
	}
	rows, err := list.ListUnpurchased(ctx, scope)
	if err != nil {
		return shoppingProvision{}, err
	}
	snap.Rows = rows
	return snap, nil
}

func plannedLines(lines []supply.Line, items []inventory.Item) []shopping.PlannedLine {
	byProduct := map[string]inventory.Item{}
	for _, item := range items {
		byProduct[item.ProductID] = item
	}
	out := make([]shopping.PlannedLine, 0, len(lines))
	for _, line := range lines {
		item, ok := byProduct[string(line.Product)]
		if !ok {
			continue
		}
		out = append(out, shopping.PlannedLine{
			ProductID: string(line.Product),
			ItemID:    item.ID,
			GroupKey:  line.GroupID,
			Quantity:  int(line.Buy),
			Note:      line.Note,
			Manual:    line.Source == supply.SourceManual,
		})
	}
	return out
}

func shelfMembers(items []inventory.Item, groupOf map[string]string) []shopping.ShelfMember {
	members := make([]shopping.ShelfMember, 0, len(items))
	for _, item := range items {
		members = append(members, shopping.ShelfMember{ItemID: item.ID, GroupKey: groupOf[item.ID]})
	}
	return members
}

func productNameUnit(item inventory.Item) (string, string) {
	if item.Product == nil {
		return "", ""
	}
	return item.Product.Name, item.Product.UnitOfMeasure
}

func replenishmentNeeds(items []inventory.Item, counts map[string]int) []shopping.ReplenishmentItem {
	needs := make([]shopping.ReplenishmentItem, 0, len(items))
	for _, item := range items {
		name, unit := productNameUnit(item)
		needs = append(needs, shopping.ReplenishmentItem{
			ItemID:        item.ID,
			Name:          name,
			UnitOfMeasure: unit,
			CurrentCount:  counts[item.ID],
		})
	}
	return needs
}

func itemName(needs []shopping.ReplenishmentItem, itemID string) string {
	for _, item := range needs {
		if item.ItemID == itemID {
			return item.Name
		}
	}
	return ""
}

func loadInstanceCounts(ctx context.Context, scope tenant.Scope, list func(context.Context, tenant.Scope, string) ([]inventory.ItemInstance, error), items []inventory.Item) (map[string]int, error) {
	counts := make(map[string]int, len(items))
	for _, item := range items {
		instances, err := list(ctx, scope, item.ID)
		if err != nil {
			return nil, err
		}
		counts[item.ID] = len(instances)
	}
	return counts, nil
}
