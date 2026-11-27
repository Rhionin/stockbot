package supply

import (
	"context"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/group"
	"github.com/Rhionin/stockbot/internal/product"
	"github.com/Rhionin/stockbot/internal/restock"
)

func groupItemsGoal(n float64) restock.Resolution {
	return restock.Resolution{Level: restock.LevelGroup, Target: restock.Target{Kind: restock.KindItems, Value: n}}
}

func groupAmountGoal() restock.Resolution {
	return restock.Resolution{Level: restock.LevelGroup, Target: restock.Target{Kind: restock.KindAmount, Value: 24}}
}

func sizedMember(id, dim string, base float64, onHand Units, resolved restock.Resolution) Fact {
	f := baseMember(id, id, "grp-g", resolved)
	f.OnHand = onHand
	if base > 0 {
		b := base
		f.NetBase = &b
	}
	f.NetDimension = dim
	return f
}

func withItemsGoal(n int, facts ...Fact) []Fact {
	for i := range facts {
		facts[i].GroupItems = n
	}
	return facts
}

func withAmountGoal(base float64, dim string, facts ...Fact) []Fact {
	for i := range facts {
		facts[i].GroupHasQty = true
		facts[i].GroupBase = base
		facts[i].GroupDimension = dim
	}
	return facts
}

func TestGroupItemsGoal(t *testing.T) {
	cases := []struct {
		name    string
		onHand  [2]Units
		noB     bool
		wantBuy Units
	}{
		{name: "goal 2 with 1 and 0 on hand buys 1", onHand: [2]Units{1, 0}, wantBuy: 1},
		{name: "goal 2 with 3 on hand across members buys nothing", onHand: [2]Units{1, 2}, wantBuy: 0},
		{name: "a don't-restock member's stock counts", onHand: [2]Units{0, 2}, noB: true, wantBuy: 0},
		{name: "nothing on hand buys the whole goal", onHand: [2]Units{0, 0}, wantBuy: 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := groupItemsGoal(2)
			if tc.noB {
				b = itemDontRestock()
			}
			facts := withItemsGoal(2,
				sizedMember("prod-a", "", 0, tc.onHand[0], groupItemsGoal(2)),
				sizedMember("prod-b", "", 0, tc.onHand[1], b),
			)
			lines := planGroupFacts(grpNow, grpPhase, facts)
			var got Units
			for _, l := range lines {
				if l.GroupID != "grp-g" {
					t.Fatalf("non-group line: %#v", l)
				}
				got += l.Buy
			}
			if got != tc.wantBuy {
				t.Fatalf("buy = %d, want %d (%#v)", got, tc.wantBuy, lines)
			}
			if tc.wantBuy > 0 && (lines[0].Source != SourceFixed || lines[0].Product != "prod-a") {
				t.Fatalf("line = %#v", lines[0])
			}
		})
	}
}

func TestGroupItemsGoalNeverReplaces(t *testing.T) {
	facts := withItemsGoal(1,
		baseMember("prod-a", "A", "grp-g", groupItemsGoal(1), Withdrawal{At: useAt(10), Qty: 3}),
	)
	facts[0].OnHand = 1
	if lines := planGroupFacts(grpNow, grpPhase, facts); len(lines) != 0 {
		t.Fatalf("an items goal is fixed: %#v", lines)
	}
}

func TestGroupItemsGoalPlansDuringOpening(t *testing.T) {
	facts := withItemsGoal(2, sizedMember("prod-a", "", 0, 0, groupItemsGoal(2)))
	lines := planGroupFacts(grpNow, Phase{}, facts)
	if len(lines) != 1 || lines[0].Buy != 2 || lines[0].GroupID != "grp-g" {
		t.Fatalf("opening items goal: %#v", lines)
	}
	months := []Fact{baseMember("prod-a", "A", "grp-g", householdMonths())}
	months[0].GroupWindow = 2
	if lines := planGroupFacts(grpNow, Phase{}, months); len(lines) != 0 {
		t.Fatalf("a months goal waits for opening to end: %#v", lines)
	}
}

func TestGroupAmountGoalBlocked(t *testing.T) {
	const oz = 28.349523125
	cases := []struct {
		name         string
		facts        []Fact
		wantReason   string
		wantBlockers []string
	}{
		{
			name: "an unsized member is size_missing",
			facts: withAmountGoal(24*oz, product.DimensionMass,
				sizedMember("prod-a", product.DimensionMass, 12*oz, 0, groupAmountGoal()),
				sizedMember("prod-b", "", 0, 0, groupAmountGoal()),
			),
			wantReason:   GoalBlockedSizeMissing,
			wantBlockers: []string{"prod-b"},
		},
		{
			name: "a volume member in a weight goal is units_mismatch",
			facts: withAmountGoal(24*oz, product.DimensionMass,
				sizedMember("prod-a", product.DimensionMass, 12*oz, 0, groupAmountGoal()),
				sizedMember("prod-b", product.DimensionVolume, 355, 0, groupAmountGoal()),
			),
			wantReason:   GoalBlockedUnitsMismatch,
			wantBlockers: []string{"prod-b"},
		},
		{
			name: "both problems report size_missing with both rows",
			facts: withAmountGoal(24*oz, product.DimensionMass,
				sizedMember("prod-a", product.DimensionMass, 12*oz, 0, groupAmountGoal()),
				sizedMember("prod-b", product.DimensionVolume, 355, 0, groupAmountGoal()),
				sizedMember("prod-c", "", 0, 0, groupAmountGoal()),
			),
			wantReason:   GoalBlockedSizeMissing,
			wantBlockers: []string{"prod-c", "prod-b"},
		},
		{
			name: "an unsized don't-restock member with stock blocks the total",
			facts: withAmountGoal(24*oz, product.DimensionMass,
				sizedMember("prod-a", product.DimensionMass, 12*oz, 0, groupAmountGoal()),
				sizedMember("prod-b", "", 0, 1, itemDontRestock()),
			),
			wantReason:   GoalBlockedSizeMissing,
			wantBlockers: []string{"prod-b"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lines, excluded := planResolved(grpNow, grpPhase, 3, tc.facts)
			if len(lines) != 0 {
				t.Fatalf("a blocked goal must not buy: %#v", lines)
			}
			if len(excluded) != 1 {
				t.Fatalf("excluded = %#v, want one record", excluded)
			}
			ex := excluded[0]
			if ex.LineKey != "group:grp-g" || ex.GoalBlocked != tc.wantReason || ex.Reason != "" {
				t.Fatalf("excluded = %#v", ex)
			}
			if len(ex.Blockers) != len(tc.wantBlockers) {
				t.Fatalf("blockers = %#v", ex.Blockers)
			}
			for i, id := range tc.wantBlockers {
				if ex.Blockers[i].ProductID != id {
					t.Fatalf("blocker %d = %#v, want %s", i, ex.Blockers[i], id)
				}
			}
		})
	}
}

func TestGroupAmountGoalIgnoresEmptyUnsizedDontRestockMember(t *testing.T) {
	const oz = 28.349523125
	facts := withAmountGoal(24*oz, product.DimensionMass,
		sizedMember("prod-a", product.DimensionMass, 12*oz, 0, groupAmountGoal()),
		sizedMember("prod-b", "", 0, 0, itemDontRestock()),
	)
	lines, excluded := planResolved(grpNow, grpPhase, 3, facts)
	if len(excluded) != 0 {
		t.Fatalf("excluded = %#v", excluded)
	}
	if len(lines) != 1 || lines[0].Buy != 2 || lines[0].Product != "prod-a" {
		t.Fatalf("lines = %#v", lines)
	}
}

func TestGroupWithNoGoalUnderHouseholdDontRestock(t *testing.T) {
	facts := []Fact{
		baseMember("prod-a", "A", "grp-g", householdMonths(), Withdrawal{At: useAt(10), Qty: 1}),
	}
	if lines, _ := planResolved(grpNow, grpPhase, 0, facts); len(lines) != 0 {
		t.Fatalf("household don't restock covers a goal-less group: %#v", lines)
	}
	facts[0].GroupItems = 1
	if lines, _ := planResolved(grpNow, grpPhase, 0, facts); len(lines) != 1 {
		t.Fatalf("a group's own goal still plans: %#v", lines)
	}
}

func TestReadMonthsHouseholdDefault(t *testing.T) {
	cases := []struct {
		name        string
		kind, value string
		want        Months
	}{
		{name: "no setting", want: 3},
		{name: "months 5", kind: "months", value: "5", want: 5},
		{name: "months 0", kind: "months", value: "0", want: 0},
		{name: "amount 2", kind: "amount", value: "2", want: 3},
		{name: "amount 0", kind: "amount", value: "0", want: 0},
		{name: "amount with no value", kind: "amount", want: 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db, err := app.OpenSQLite(":memory:")
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			db.SetMaxOpenConns(1)
			if err := app.RunMigrations(db); err != nil {
				t.Fatal(err)
			}
			scope := scopeOf(t, db)
			if tc.kind != "" {
				if _, err := db.Exec(`INSERT INTO household_settings (household_id, key, value) VALUES (?, ?, ?)`, scope.HouseholdID(), keyTargetKind, tc.kind); err != nil {
					t.Fatal(err)
				}
			}
			if tc.value != "" {
				if _, err := db.Exec(`INSERT INTO household_settings (household_id, key, value) VALUES (?, ?, ?)`, scope.HouseholdID(), keyTargetValue, tc.value); err != nil {
					t.Fatal(err)
				}
			}
			got, err := readMonths(context.Background(), scope, db)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("readMonths = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestPlanViewGroupReplacingAndBlockers(t *testing.T) {
	db, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if err := app.RunMigrations(db); err != nil {
		t.Fatal(err)
	}
	scope := scopeOf(t, db)
	hid := scope.HouseholdID()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`INSERT INTO household_settings (household_id, key, value) VALUES (?, 'onboarding_started_at', '2026-01-01T00:00:00Z')`, hid)
	exec(`INSERT INTO products (id, name, net_base_value, net_dimension) VALUES ('prod-x', 'X', 340, 'mass'), ('prod-y', 'Y', 340, 'mass')`)
	exec(`INSERT INTO items (id, user_id, product_id, household_id) VALUES ('item-x', ?, 'prod-x', ?), ('item-y', ?, 'prod-y', ?)`, hid, hid, hid, hid)
	exec(`INSERT INTO product_groups (id, household_id, user_id, name, name_key) VALUES ('grp-1', ?, ?, 'Beans', 'beans')`, hid, hid)
	exec(`INSERT INTO product_group_members (household_id, product_id, group_id) VALUES (?, 'prod-x', 'grp-1'), (?, 'prod-y', 'grp-1')`, hid, hid)
	exec(`INSERT INTO stock_in_events (id, household_id, product_id, at) VALUES (1, ?, 'prod-y', '2026-01-10T00:00:00Z')`, hid)
	exec(`INSERT INTO consumption_events (id, household_id, item_id, consumed_at) VALUES ('c1', ?, 'item-x', '2026-01-05T00:00:00Z'), ('c2', ?, 'item-x', '2026-01-12T00:00:00Z')`, hid, hid)

	now := time.Date(2026, 1, 20, 0, 0, 0, 0, time.UTC)
	views, err := Open(db).PlanView(context.Background(), scope, now, nil)
	if err != nil {
		t.Fatal(err)
	}
	v := views["group:grp-1"]
	if v.Buy != 1 || v.Replacing == nil || *v.Replacing != 1 {
		t.Fatalf("group view = %#v, want buy 1 replacing 1", v)
	}

	exec(`UPDATE product_groups SET quantity_base_value = 680, quantity_dimension = 'mass' WHERE id = 'grp-1'`)
	exec(`UPDATE products SET net_base_value = NULL WHERE id = 'prod-y'`)
	views, err = Open(db).PlanView(context.Background(), scope, now, nil)
	if err != nil {
		t.Fatal(err)
	}
	v = views["group:grp-1"]
	if v.Buy != 0 || v.GoalBlocked != GoalBlockedSizeMissing || v.Excluded != "" {
		t.Fatalf("blocked view = %#v", v)
	}
	if len(v.GoalBlockers) != 1 || v.GoalBlockers[0] != (group.Blocker{ProductID: "prod-y", Name: "Y", Reason: group.StatusSizeMissing}) {
		t.Fatalf("blockers = %#v", v.GoalBlockers)
	}
}
