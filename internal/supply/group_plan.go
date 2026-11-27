package supply

import (
	"math"
	"sort"
	"time"

	"github.com/Rhionin/stockbot/internal/group"
	"github.com/Rhionin/stockbot/internal/product"
	"github.com/Rhionin/stockbot/internal/shopping"
)

// Reasons a saved amount goal can't be counted, recorded on the group line key.
const (
	GoalBlockedSizeMissing   = "group_goal_size_missing"
	GoalBlockedUnitsMismatch = "group_goal_units_mismatch"
)

// planProductGroup buys one member of a product group.
// The group's goal replaces any per-product setting.
func planProductGroup(now time.Time, phase Phase, account Months, members []Fact) (Line, *PlanExcluded, bool) {
	if len(members) == 0 || members[0].Group == "" {
		return Line{}, nil, false
	}
	head := members[0]
	key := groupLineKey(string(head.Group))
	for _, member := range members {
		if member.Resolved.Level == "group" && member.Resolved.Target.Value == 0 {
			return Line{}, nil, false
		}
	}
	hasGoal := head.GroupItems > 0 || head.GroupHasQty || head.GroupWindow > 0
	if !hasGoal && account == 0 {
		return Line{}, nil, false
	}
	picked, gm, pickErr := pickMembers(head, members)
	if head.GroupHasQty {
		if status, blockers := group.AmountStatus(head.GroupDimension, gm); status != group.StatusOK {
			reason := GoalBlockedUnitsMismatch
			if status == group.StatusSizeMissing {
				reason = GoalBlockedSizeMissing
			}
			ex := &PlanExcluded{LineKey: key, GoalBlocked: reason, Blockers: blockers}
			if amount, unit, ok := product.DisplayNetSize(head.GroupBase, head.GroupDimension); ok {
				ex.Goal = &GroupGoal{Kind: "amount", Value: amount, Unit: unit}
			}
			return Line{}, ex, false
		}
	}
	if pickErr != nil {
		return Line{}, &PlanExcluded{LineKey: key, Reason: "no_product_picked"}, false
	}
	if picked.ProductID == "" {
		if allMembersNoRestock(gm) {
			return Line{}, nil, false
		}
		return Line{}, &PlanExcluded{LineKey: key, Reason: "no_product_picked"}, false
	}
	target := group.BuyTarget{WindowMonths: int(account)}
	switch {
	case head.GroupItems > 0:
		target = group.BuyTarget{Items: head.GroupItems}
	case head.GroupHasQty:
		target = group.BuyTarget{HasQuantity: true, Base: head.GroupBase, Dimension: head.GroupDimension}
	case head.GroupWindow > 0:
		target.WindowMonths = head.GroupWindow
	}
	rate := groupRate(now, phase, members)
	buy, explain := group.BuyCount(target, gm, picked.ProductID, rate)
	if buy >= 1 {
		note := picked.Because
		if explain != "" {
			if note != "" {
				note += " "
			}
			note += explain
		}
		return Line{
			Product: ProductID(picked.ProductID),
			Buy:     Units(buy),
			Note:    note,
			Source:  SourceFixed,
			GroupID: string(head.Group),
		}, nil, true
	}
	if target.Items > 0 || target.HasQuantity || rate.OK {
		return Line{}, nil, false
	}
	started, ok := phase.StartedAt()
	if !ok {
		return Line{}, nil, false
	}
	qty := groupUnreplaced(started, members)
	if qty < 1 {
		return Line{}, nil, false
	}
	return Line{
		Product: ProductID(picked.ProductID),
		Buy:     qty,
		Note:    noteReplace(qty),
		Source:  SourceReplace,
		GroupID: string(head.Group),
	}, nil, true
}

// groupUnreplaced counts what the group used since its shelf was last filled:
// uses strictly after the later of the start and the latest stock-in of any
// member. A don't-restock member's stock-in moves that boundary, because its
// stock is on the shelf, but its own uses are not bought back.
func groupUnreplaced(started time.Time, members []Fact) Units {
	boundary := started
	for _, member := range members {
		for _, in := range member.StockIns {
			if in.After(boundary) {
				boundary = in
			}
		}
	}
	var sum Units
	for _, member := range members {
		if member.NoRestock {
			continue
		}
		for _, out := range member.StockOuts {
			if out.At.After(boundary) && out.Qty > 0 {
				sum += out.Qty
			}
		}
	}
	return sum
}

func allMembersNoRestock(gm []group.Member) bool {
	if len(gm) == 0 {
		return false
	}
	for _, m := range gm {
		if !m.NoRestock {
			return false
		}
	}
	return true
}

func pickMembers(head Fact, members []Fact) (group.Result, []group.Member, error) {
	gm := make([]group.Member, len(members))
	var deals []shopping.Deal
	for i, member := range members {
		gm[i] = group.Member{
			ProductID:      string(member.Product),
			Name:           member.Name,
			OnHand:         int(member.OnHand),
			ItemID:         member.ItemID,
			NetBase:        member.NetBase,
			Dimension:      member.NetDimension,
			LastConsumedAt: member.LastConsumed,
			LastStockedAt:  member.LastStocked,
			NoRestock:      member.NoRestock,
		}
		if member.Deal != nil {
			deals = append(deals, *member.Deal)
		}
	}
	picked, err := group.Pick(group.Kind(head.Rule), group.Input{
		Members:         gm,
		PinnedProductID: head.Pinned,
		Deals:           deals,
	})
	if err != nil {
		return group.Result{}, gm, err
	}
	return picked, gm, nil
}

// Usage is how much of a group the household goes through in a 30-day month.
// Unit is "oz", "fl oz", or "items" when sizes are missing or mixed.
type Usage struct {
	PerMonth float64 `json:"perMonth"`
	Unit     string  `json:"unit"`
}

// usageFromMembers converts a qualified amount rate into the ounces the
// screen shows. Item-count rates are not an amount, so they stay nil.
func usageFromMembers(now time.Time, phase Phase, members []Fact) *Usage {
	if len(members) == 0 {
		return nil
	}
	rate := groupRate(now, phase, members)
	if !rate.OK || rate.ItemCount || rate.PerDay <= 0 {
		return nil
	}
	amount, unit, ok := product.DisplayNetSize(rate.PerDay*30, members[0].NetDimension)
	if !ok || amount <= 0 {
		return nil
	}
	return &Usage{PerMonth: amount, Unit: unit}
}

// groupUsage is the group's pace for the group page: ounces when every
// member is sized alike, otherwise items. Nil means no pace yet.
func groupUsage(now time.Time, phase Phase, members []Fact) *Usage {
	if usage := usageFromMembers(now, phase, members); usage != nil {
		return usage
	}
	if len(members) == 0 {
		return nil
	}
	rate := groupRate(now, phase, members)
	if !rate.OK || !rate.ItemCount || rate.PerDay <= 0 {
		return nil
	}
	return &Usage{PerMonth: math.Round(rate.PerDay*30*10) / 10, Unit: "items"}
}

func groupRate(now time.Time, phase Phase, members []Fact) group.UsageRate {
	started, ok := phase.StartedAt()
	if !ok {
		return group.UsageRate{}
	}
	if !uniformSize(members) {
		ins, outs := itemEvents(members)
		perDay, ok := derive(now, started, ins, outs)
		return group.UsageRate{PerDay: perDay, OK: ok, ItemCount: true}
	}
	ins, outs := sizedEvents(members)
	perDay, ok := DeriveAmount(now, started, ins, outs)
	return group.UsageRate{PerDay: perDay, OK: ok}
}

func uniformSize(members []Fact) bool {
	if len(members) == 0 {
		return false
	}
	dim := ""
	for _, member := range members {
		if member.NetBase == nil || *member.NetBase <= 0 {
			return false
		}
		if member.NetDimension != product.DimensionMass && member.NetDimension != product.DimensionVolume {
			return false
		}
		if dim == "" {
			dim = member.NetDimension
			continue
		}
		if dim != member.NetDimension {
			return false
		}
	}
	return true
}

func itemEvents(members []Fact) ([]time.Time, []Withdrawal) {
	var ins []time.Time
	var outs []Withdrawal
	for _, member := range members {
		ins = append(ins, member.StockIns...)
		outs = append(outs, member.StockOuts...)
	}
	sort.Slice(ins, func(i, j int) bool { return ins[i].Before(ins[j]) })
	sort.Slice(outs, func(i, j int) bool { return outs[i].At.Before(outs[j].At) })
	return ins, outs
}

func sizedEvents(members []Fact) ([]time.Time, []AmountWithdrawal) {
	var ins []time.Time
	var outs []AmountWithdrawal
	for _, member := range members {
		ins = append(ins, member.StockIns...)
		if member.NetBase == nil {
			continue
		}
		for _, out := range member.StockOuts {
			outs = append(outs, AmountWithdrawal{At: out.At, Qty: float64(out.Qty) * *member.NetBase})
		}
	}
	sort.Slice(ins, func(i, j int) bool { return ins[i].Before(ins[j]) })
	sort.Slice(outs, func(i, j int) bool { return outs[i].At.Before(outs[j].At) })
	return ins, outs
}
