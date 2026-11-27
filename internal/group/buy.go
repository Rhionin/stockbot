package group

import (
	"math"
	"strconv"

	"github.com/Rhionin/stockbot/internal/product"
)

const (
	explainUnsizedQuantity = "Some sizes aren't known, so an ounce target can't be counted yet."
	explainUnsizedWindow   = "Some sizes aren't known, so this counts items."
)

// BuyTarget is how full the group should be.
// A quantity is already grams or milliliters. A window is a number of months.
// Items is a count of units on hand across every member.
type BuyTarget struct {
	Items        int
	WindowMonths int
	Base         float64
	Dimension    string
	HasQuantity  bool
}

// UsageRate is a consumption rate from supply.DeriveAmount.
// ItemCount is set when the rate counts items because sizes are missing or mixed.
type UsageRate struct {
	PerDay    float64
	OK        bool
	ItemCount bool
}

// BuyCount is how many of chosenID to buy.
// A quantity target never falls back to counting items. A month window does,
// and only when rate.ItemCount is set.
func BuyCount(target BuyTarget, members []Member, chosenID string, rate UsageRate) (buy int, explain string) {
	if target.Items > 0 {
		return buyItems(target.Items, members), ""
	}
	if target.HasQuantity {
		return buyQuantity(target, members, chosenID)
	}
	if target.WindowMonths <= 0 || !rate.OK {
		return 0, ""
	}
	if rate.ItemCount {
		return buyItemWindow(target, members, rate), explainUnsizedWindow
	}
	return buySizeWindow(target, members, chosenID, rate)
}

func buyItems(goal int, members []Member) int {
	on := 0
	for _, m := range members {
		on += m.OnHand
	}
	if on >= goal {
		return 0
	}
	return goal - on
}

// buyQuantity adds up the counted members only: a don't-restock member with
// nothing on the shelf adds 0 and its size does not matter.
func buyQuantity(target BuyTarget, members []Member, chosenID string) (int, string) {
	if target.Base <= 0 || (target.Dimension != product.DimensionMass && target.Dimension != product.DimensionVolume) {
		return 0, explainUnsizedQuantity
	}
	if status, _ := AmountStatus(target.Dimension, members); status != StatusOK {
		return 0, explainUnsizedQuantity
	}
	var on float64
	for _, m := range counted(members) {
		on += float64(m.OnHand) * *m.NetBase
	}
	chosen, ok := memberByID(members, chosenID)
	if !ok || chosen.NetBase == nil || *chosen.NetBase <= 0 || chosen.Dimension != target.Dimension {
		return 0, explainUnsizedQuantity
	}
	buy := ceilDivide(target.Base-on, *chosen.NetBase)
	return buy, explainProgress(on, target.Base, target.Dimension)
}

func buyItemWindow(target BuyTarget, members []Member, rate UsageRate) int {
	par := roundHalfUp(rate.PerDay * float64(target.WindowMonths*30))
	on := 0
	for _, m := range members {
		on += m.OnHand
	}
	buy := par - on
	if buy < 0 {
		return 0
	}
	return buy
}

func buySizeWindow(target BuyTarget, members []Member, chosenID string, rate UsageRate) (int, string) {
	if len(members) == 0 {
		return 0, explainUnsizedWindow
	}
	dim := ""
	var on float64
	for _, m := range members {
		if m.NetBase == nil || *m.NetBase <= 0 {
			return 0, explainUnsizedWindow
		}
		if m.Dimension != product.DimensionMass && m.Dimension != product.DimensionVolume {
			return 0, explainUnsizedWindow
		}
		if dim == "" {
			dim = m.Dimension
		} else if dim != m.Dimension {
			return 0, explainUnsizedWindow
		}
		on += float64(m.OnHand) * *m.NetBase
	}
	chosen, ok := memberByID(members, chosenID)
	if !ok || chosen.NetBase == nil || *chosen.NetBase <= 0 || chosen.Dimension != dim {
		return 0, explainUnsizedWindow
	}
	par := rate.PerDay * float64(target.WindowMonths*30)
	return ceilDivide(par-on, *chosen.NetBase), explainProgress(on, par, dim)
}

func explainProgress(on, goal float64, dimension string) string {
	left := formatBase(on, dimension)
	right := formatBase(goal, dimension)
	if left == "" || right == "" {
		return ""
	}
	return left + " toward " + right + "."
}

func formatBase(base float64, dimension string) string {
	if base <= 0 {
		switch dimension {
		case product.DimensionVolume:
			return "0 fl oz"
		case product.DimensionMass:
			return "0 oz"
		default:
			return ""
		}
	}
	amount, unit, ok := product.DisplayNetSize(base, dimension)
	if !ok {
		return ""
	}
	return formatAmount(amount) + " " + unit
}

func formatAmount(v float64) string {
	if v == math.Trunc(v) {
		return strconv.FormatFloat(v, 'f', 0, 64)
	}
	return strconv.FormatFloat(v, 'f', 1, 64)
}

func ceilDivide(n, d float64) int {
	if n <= 0 || d <= 0 {
		return 0
	}
	q := n / d
	nearest := math.Round(q)
	if math.Abs(q-nearest) < 1e-6 {
		q = nearest
	}
	if q < 0 {
		return 0
	}
	return int(math.Ceil(q - 1e-9))
}

func roundHalfUp(x float64) int {
	if x <= 0 {
		return 0
	}
	return int(math.Floor(x + 0.5))
}
