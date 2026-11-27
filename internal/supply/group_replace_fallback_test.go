package supply

import (
	"strings"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/product"
	"github.com/Rhionin/stockbot/internal/restock"
)

func householdMonths() restock.Resolution {
	return restock.Resolution{
		Level:  restock.LevelHousehold,
		Target: restock.Target{Kind: restock.KindMonths, Value: 3},
	}
}

func groupZeroTarget() restock.Resolution {
	return restock.Resolution{
		Level:  restock.LevelGroup,
		Target: restock.Target{Kind: restock.KindMonths, Value: 0},
	}
}

func itemMonths(value float64) restock.Resolution {
	return restock.Resolution{
		Level:  restock.LevelItem,
		Target: restock.Target{Kind: restock.KindMonths, Value: value},
	}
}

func itemDontRestock() restock.Resolution {
	return restock.Resolution{
		Level:  restock.LevelItem,
		Target: restock.Target{Kind: restock.KindMonths, Value: 0},
	}
}

// shared fixture times: onboarding started 60d ago, plan runs now.
var (
	grpNow     = time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	grpStarted = grpNow.Add(-60 * 24 * time.Hour)
	grpPhase   = Phase{started: grpStarted}
)

func useAt(daysAfterStart int) time.Time {
	return grpStarted.Add(time.Duration(daysAfterStart) * 24 * time.Hour)
}

func stockInAt(daysAfterStart int) time.Time {
	return grpStarted.Add(time.Duration(daysAfterStart) * 24 * time.Hour)
}

func baseMember(productID, name, group string, resolved restock.Resolution, outs ...Withdrawal) Fact {
	return Fact{
		Product:  ProductID(productID),
		Group:    GroupID(group),
		Name:     name,
		Rule:     "favor_variety",
		ItemID:   "item-" + productID,
		Resolved: resolved,
		StockOuts: outs,
	}
}

func planGroupFacts(now time.Time, phase Phase, facts []Fact) []Line {
	lines, _ := planResolved(now, phase, 3, facts)
	return lines
}

func TestGroupReplaceFallbackCases(t *testing.T) {
	t.Run("1_regression_two_members_one_use_each", func(t *testing.T) {
		facts := []Fact{
			baseMember("prod-a", "Product A", "grp-g", householdMonths(), Withdrawal{At: useAt(10), Qty: 1}),
			baseMember("prod-b", "Product B", "grp-g", householdMonths(), Withdrawal{At: useAt(20), Qty: 1}),
		}
		lines := planGroupFacts(grpNow, grpPhase, facts)
		if len(lines) != 1 {
			t.Fatalf("lines = %#v, want one group line", lines)
		}
		line := lines[0]
		if line.GroupID != "grp-g" || line.Buy != 2 || line.Note != "replacing 2 you used" || line.Source != SourceReplace {
			t.Fatalf("line = %#v", line)
		}
		soloA := baseMember("prod-a", "Product A", "", householdMonths(), Withdrawal{At: useAt(10), Qty: 1})
		soloB := baseMember("prod-b", "Product B", "", householdMonths(), Withdrawal{At: useAt(20), Qty: 1})
		soloA.Group = ""
		soloB.Group = ""
		soloLines := planGroupFacts(grpNow, grpPhase, []Fact{soloA, soloB})
		if len(soloLines) != 2 {
			t.Fatalf("solo lines = %#v", soloLines)
		}
		var soloBuy Units
		for _, sl := range soloLines {
			if sl.Buy != 1 || sl.Note != "replacing 1 you used" || sl.Source != SourceReplace {
				t.Fatalf("solo line = %#v", sl)
			}
			soloBuy += sl.Buy
		}
		if soloBuy != line.Buy {
			t.Fatalf("group buy %d != solo sum %d", line.Buy, soloBuy)
		}
	})

	t.Run("2_rate_ok_par_above_on_hand", func(t *testing.T) {
		firstIn := stockInAt(1)
		facts := []Fact{
			{
				Product: "prod-a", Group: "grp-g", Name: "Product A", Rule: "same_as_ran_out",
				ItemID: "item-a", Resolved: householdMonths(), OnHand: 0,
				StockIns: []time.Time{firstIn},
				StockOuts: []Withdrawal{
					{At: useAt(15), Qty: 1},
					{At: useAt(40), Qty: 1},
				},
				LastConsumed: useAt(40),
			},
			{
				Product: "prod-b", Group: "grp-g", Name: "Product B", Rule: "same_as_ran_out",
				ItemID: "item-b", Resolved: householdMonths(), OnHand: 0,
				StockIns: []time.Time{firstIn},
				StockOuts: []Withdrawal{
					{At: useAt(25), Qty: 1},
					{At: useAt(45), Qty: 1},
				},
				LastConsumed: useAt(45),
			},
		}
		lines := planGroupFacts(grpNow, grpPhase, facts)
		if len(lines) != 1 {
			t.Fatalf("lines = %#v", lines)
		}
		if lines[0].Source == SourceReplace {
			t.Fatalf("expected rate/fixed line, got replace: %#v", lines[0])
		}
		if lines[0].Buy < 1 {
			t.Fatalf("line = %#v", lines[0])
		}
		if !strings.Contains(lines[0].Note, "month") && lines[0].Note != "" {
			// item-count rate uses BuyCount explain text
			if !strings.Contains(lines[0].Note, "counts items") && lines[0].Source != SourceFixed {
				t.Fatalf("note = %q", lines[0].Note)
			}
		}
	})

	t.Run("3_on_hand_covers_par", func(t *testing.T) {
		firstIn := stockInAt(1)
		facts := []Fact{
			{
				Product: "prod-a", Group: "grp-g", Name: "Product A", Rule: "same_as_ran_out",
				ItemID: "item-a", Resolved: householdMonths(), OnHand: 10,
				StockIns: []time.Time{firstIn},
				StockOuts: []Withdrawal{
					{At: useAt(15), Qty: 1},
					{At: useAt(40), Qty: 1},
				},
				LastConsumed: useAt(40),
			},
			{
				Product: "prod-b", Group: "grp-g", Name: "Product B", Rule: "same_as_ran_out",
				ItemID: "item-b", Resolved: householdMonths(), OnHand: 10,
				StockIns: []time.Time{firstIn},
				StockOuts: []Withdrawal{
					{At: useAt(25), Qty: 1},
					{At: useAt(45), Qty: 1},
				},
				LastConsumed: useAt(45),
			},
		}
		lines := planGroupFacts(grpNow, grpPhase, facts)
		if len(lines) != 0 {
			t.Fatalf("lines = %#v, want none", lines)
		}
	})

	t.Run("4_group_target_zero", func(t *testing.T) {
		facts := []Fact{
			baseMember("prod-a", "Product A", "grp-g", groupZeroTarget(), Withdrawal{At: useAt(10), Qty: 1}),
			baseMember("prod-b", "Product B", "grp-g", groupZeroTarget(), Withdrawal{At: useAt(20), Qty: 1}),
		}
		lines := planGroupFacts(grpNow, grpPhase, facts)
		if len(lines) != 0 {
			t.Fatalf("lines = %#v", lines)
		}
	})

	t.Run("5_explicit_quantity_target", func(t *testing.T) {
		base := 16.0
		facts := []Fact{
			{
				Product: "prod-a", Group: "grp-g", Name: "Product A", Rule: "favor_variety",
				ItemID: "item-a", Resolved: householdMonths(),
				GroupHasQty: true, GroupBase: 32, GroupDimension: product.DimensionMass,
				NetBase: &base, NetDimension: product.DimensionMass, OnHand: 0,
				StockOuts: []Withdrawal{{At: useAt(10), Qty: 1}},
			},
			{
				Product: "prod-b", Group: "grp-g", Name: "Product B", Rule: "favor_variety",
				ItemID: "item-b", Resolved: householdMonths(),
				GroupHasQty: true, GroupBase: 32, GroupDimension: product.DimensionMass,
				NetBase: &base, NetDimension: product.DimensionMass, OnHand: 0,
				StockOuts: []Withdrawal{{At: useAt(20), Qty: 1}},
			},
		}
		lines := planGroupFacts(grpNow, grpPhase, facts)
		if len(lines) != 1 {
			t.Fatalf("lines = %#v", lines)
		}
		if lines[0].Source == SourceReplace || strings.Contains(lines[0].Note, "replacing") {
			t.Fatalf("quantity path must not use replace fallback: %#v", lines[0])
		}
		if lines[0].Buy < 1 {
			t.Fatalf("line = %#v", lines[0])
		}
	})

	t.Run("6_item_target_stays_with_the_group", func(t *testing.T) {
		facts := []Fact{
			baseMember("prod-a", "Product A", "grp-g", itemMonths(2), Withdrawal{At: useAt(10), Qty: 1}),
			baseMember("prod-b", "Product B", "grp-g", householdMonths(), Withdrawal{At: useAt(20), Qty: 1}),
		}
		lines := planGroupFacts(grpNow, grpPhase, facts)
		if len(lines) != 1 {
			t.Fatalf("lines = %#v, want only the group line", lines)
		}
		if lines[0].GroupID != "grp-g" || lines[0].Source != SourceReplace || lines[0].Buy != 2 {
			t.Fatalf("group line = %#v, want both uses replaced as the group", lines[0])
		}
		opening := planGroupFacts(grpNow, Phase{}, facts)
		if len(opening) != 0 {
			t.Fatalf("opening must not plan the member alone: %#v", opening)
		}
	})

	t.Run("7_one_member_dont_restock", func(t *testing.T) {
		facts := []Fact{
			baseMember("prod-a", "Product A", "grp-g", itemDontRestock(), Withdrawal{At: useAt(10), Qty: 1}),
			baseMember("prod-b", "Product B", "grp-g", householdMonths(), Withdrawal{At: useAt(20), Qty: 1}),
		}
		lines := planGroupFacts(grpNow, grpPhase, facts)
		if len(lines) != 1 {
			t.Fatalf("lines = %#v", lines)
		}
		if lines[0].Buy != 1 || lines[0].Product == "prod-a" {
			t.Fatalf("line = %#v, want only B's use", lines[0])
		}
	})

	t.Run("8_all_members_dont_restock", func(t *testing.T) {
		facts := []Fact{
			baseMember("prod-a", "Product A", "grp-g", itemDontRestock(), Withdrawal{At: useAt(10), Qty: 1}),
			baseMember("prod-b", "Product B", "grp-g", itemDontRestock(), Withdrawal{At: useAt(20), Qty: 1}),
		}
		lines := planGroupFacts(grpNow, grpPhase, facts)
		if len(lines) != 0 {
			t.Fatalf("lines = %#v", lines)
		}
	})

	t.Run("9_opening_phase", func(t *testing.T) {
		facts := []Fact{
			baseMember("prod-a", "Product A", "grp-g", householdMonths(), Withdrawal{At: useAt(10), Qty: 1}),
			baseMember("prod-b", "Product B", "grp-g", householdMonths(), Withdrawal{At: useAt(20), Qty: 1}),
		}
		lines := planGroupFacts(grpNow, Phase{}, facts)
		if len(lines) != 0 {
			t.Fatalf("opening: lines = %#v", lines)
		}
		solo := baseMember("prod-solo", "Solo", "", householdMonths(), Withdrawal{At: useAt(10), Qty: 1})
		solo.Group = ""
		soloLines := planGroupFacts(grpNow, Phase{}, []Fact{solo})
		if len(soloLines) != 0 {
			t.Fatalf("opening solo: lines = %#v", soloLines)
		}
	})

	t.Run("10_group_boundary_stock_in_of_any_member_clears_buy_back", func(t *testing.T) {
		facts := []Fact{
			{
				Product: "prod-a", Group: "grp-g", Name: "Product A", Rule: "favor_variety",
				ItemID: "item-a", Resolved: householdMonths(),
				StockOuts: []Withdrawal{{At: useAt(5), Qty: 1}},
			},
			{
				Product: "prod-b", Group: "grp-g", Name: "Product B", Rule: "favor_variety",
				ItemID: "item-b", Resolved: householdMonths(),
				StockIns: []time.Time{stockInAt(15)},
			},
		}
		if lines := planGroupFacts(grpNow, grpPhase, facts); len(lines) != 0 {
			t.Fatalf("stocking B covers A's earlier use: %#v", lines)
		}
		facts[0].StockOuts = append(facts[0].StockOuts, Withdrawal{At: useAt(25), Qty: 1})
		lines := planGroupFacts(grpNow, grpPhase, facts)
		if len(lines) != 1 || lines[0].Buy != 1 || lines[0].Note != "replacing 1 you used" {
			t.Fatalf("A's use after B's stock-in: %#v", lines)
		}

		soloA := facts[0]
		soloA.Group = ""
		soloB := facts[1]
		soloB.Group = ""
		soloLines := planGroupFacts(grpNow, grpPhase, []Fact{soloA, soloB})
		if len(soloLines) != 1 || soloLines[0].Product != "prod-a" || soloLines[0].Buy != 2 {
			t.Fatalf("ungrouped items keep their own boundary: %#v", soloLines)
		}
	})

	t.Run("10b_dont_restock_member_stock_in_moves_the_boundary", func(t *testing.T) {
		facts := []Fact{
			baseMember("prod-a", "Product A", "grp-g", householdMonths(), Withdrawal{At: useAt(5), Qty: 1}),
			baseMember("prod-b", "Product B", "grp-g", itemDontRestock()),
		}
		facts[1].StockIns = []time.Time{stockInAt(15)}
		if lines := planGroupFacts(grpNow, grpPhase, facts); len(lines) != 0 {
			t.Fatalf("a don't-restock member's stock-in still fills the shelf: %#v", lines)
		}
	})

	t.Run("11_A_stock_in_after_use_only_B_counts", func(t *testing.T) {
		facts := []Fact{
			{
				Product: "prod-a", Group: "grp-g", Name: "Product A", Rule: "favor_variety",
				ItemID: "item-a", Resolved: householdMonths(),
				StockIns:  []time.Time{stockInAt(20)},
				StockOuts: []Withdrawal{{At: useAt(10), Qty: 1}},
			},
			{
				Product: "prod-b", Group: "grp-g", Name: "Product B", Rule: "favor_variety",
				ItemID: "item-b", Resolved: householdMonths(),
				StockIns:  []time.Time{stockInAt(12)},
				StockOuts: []Withdrawal{{At: useAt(25), Qty: 1}},
			},
		}
		lines := planGroupFacts(grpNow, grpPhase, facts)
		if len(lines) != 1 || lines[0].Buy != 1 {
			t.Fatalf("only B's use after its stock-in: %#v", lines)
		}
	})

	t.Run("12_shelved_member_omitted", func(t *testing.T) {
		facts := []Fact{
			baseMember("prod-a", "Product A", "grp-g", householdMonths(), Withdrawal{At: useAt(10), Qty: 1}),
			baseMember("prod-b", "Product B", "grp-g", householdMonths(), Withdrawal{At: useAt(20), Qty: 1}),
		}
		lines := planGroupFacts(grpNow, grpPhase, facts)
		if len(lines) != 1 || lines[0].Buy != 2 {
			t.Fatalf("with two members: %#v", lines)
		}
		// Shelved products are not loaded; planner only sees tracked members.
		linesOne := planGroupFacts(grpNow, grpPhase, facts[:1])
		if len(linesOne) != 1 || linesOne[0].Buy != 1 {
			t.Fatalf("one visible member: %#v", linesOne)
		}
	})
}
