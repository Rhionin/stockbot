package supply

import (
	"encoding/json"
	"testing"
)

func TestPlanExcludedUnknownRuleKind(t *testing.T) {
	facts := []Fact{
		baseMember("prod-a", "A", "grp-g", householdMonths()),
		baseMember("prod-b", "B", "grp-g", householdMonths()),
	}
	facts[0].Rule = "not_a_real_rule"
	facts[1].Rule = "not_a_real_rule"
	lines, excluded := planResolved(grpNow, grpPhase, 3, facts)
	if len(lines) != 0 {
		t.Fatalf("lines %#v", lines)
	}
	if len(excluded) != 1 || excluded[0].LineKey != "group:grp-g" || excluded[0].Reason != "no_product_picked" {
		t.Fatalf("excluded %#v", excluded)
	}
}

func TestPlanExcludedAllNoRestock(t *testing.T) {
	facts := []Fact{
		baseMember("prod-a", "A", "grp-g", itemDontRestock()),
		baseMember("prod-b", "B", "grp-g", itemDontRestock()),
	}
	lines, excluded := planResolved(grpNow, grpPhase, 3, facts)
	if len(lines) != 0 {
		t.Fatalf("lines %#v", lines)
	}
	if len(excluded) != 0 {
		t.Fatalf("all don't restock: excluded %#v", excluded)
	}
}

func TestPlanExcludedGroupTargetZero(t *testing.T) {
	facts := []Fact{
		baseMember("prod-a", "A", "grp-g", groupZeroTarget()),
		baseMember("prod-b", "B", "grp-g", groupZeroTarget()),
	}
	lines, excluded := planResolved(grpNow, grpPhase, 3, facts)
	if len(lines) != 0 {
		t.Fatalf("lines %#v", lines)
	}
	if len(excluded) != 0 {
		t.Fatalf("group target zero: excluded %#v", excluded)
	}
}

func TestPlanCallersReturnLinesOnly(t *testing.T) {
	facts := []Fact{
		baseMember("prod-a", "A", "grp-g", householdMonths()),
		baseMember("prod-b", "B", "grp-g", householdMonths()),
	}
	facts[0].Rule = "not_a_real_rule"
	facts[1].Rule = "not_a_real_rule"
	lines, excluded := planResolved(grpNow, grpPhase, 3, facts)
	if len(excluded) == 0 {
		t.Fatal("fixture must produce excluded rows")
	}
	// Plan() and other callers discard excluded; golden lock on []Line only.
	got, _ := json.Marshal(lines)
	want := []byte("[]")
	if string(got) != string(want) {
		t.Fatalf("lines JSON %s want %s", got, want)
	}
}