package supply

import (
	"sort"
	"time"

	"github.com/Rhionin/stockbot/internal/group"
	"github.com/Rhionin/stockbot/internal/product"
	"github.com/Rhionin/stockbot/internal/shopping"
)

// planProductGroup buys one member of a product group.
// The group's target replaces any per-product supply override.
func planProductGroup(now time.Time, phase Phase, account Months, members []Fact) (Line, bool) {
	if len(members) == 0 || members[0].Group == "" {
		return Line{}, false
	}
	head := members[0]
	for _, member := range members {
		if member.Resolved.Level == "group" && member.Resolved.Target.Value == 0 {
			return Line{}, false
		}
	}
	picked, gm := pickMembers(head, members)
	if picked.ProductID == "" {
		return Line{}, false
	}
	target := group.BuyTarget{WindowMonths: int(account)}
	if head.GroupWindow > 0 {
		target.WindowMonths = head.GroupWindow
	}
	if head.GroupHasQty {
		target = group.BuyTarget{HasQuantity: true, Base: head.GroupBase, Dimension: head.GroupDimension}
	}
	buy, explain := group.BuyCount(target, gm, picked.ProductID, groupRate(now, phase, members))
	if buy < 1 {
		return Line{}, false
	}
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
	}, true
}

func pickMembers(head Fact, members []Fact) (group.Result, []group.Member) {
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
		return group.Result{}, gm
	}
	return picked, gm
}

// Usage is how much of a group the household goes through in a 30-day month.
// Unit is "oz" or "fl oz". Nil means the rate is not known yet, including
// when the group is counted by the item instead of by amount.
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
