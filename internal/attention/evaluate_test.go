package attention

import "testing"

func TestEvaluateOpeningAndSkipped(t *testing.T) {
	in := EvaluateInput{
		TripSeq:     1,
		Opening:     true,
		PlannedKeys: map[string]struct{}{"item:new": {}},
		Skipped:     map[string]struct{}{"item:skip": {}},
	}
	perLine, groups := Evaluate(in)
	if perLine["item:new"].Reason != "opening" || perLine["item:skip"].Reason != "removed" {
		t.Fatalf("%#v", perLine)
	}
	if len(groups) != 2 {
		t.Fatalf("groups %d", len(groups))
	}
}

func TestEvaluateGroupsStuckRows(t *testing.T) {
	in := EvaluateInput{
		TripSeq: 2,
		Marks: map[string]MarkRow{
			"item:a": {LineKey: "item:a", FlaggedTrip: 1},
			"item:b": {LineKey: "item:b", FlaggedTrip: 1},
		},
		StagedKeys: map[string]struct{}{"item:a": {}, "item:b": {}},
	}
	perLine, groups := Evaluate(in)
	if perLine["item:a"] == nil || !perLine["item:a"].Stuck {
		t.Fatalf("item:a %#v", perLine["item:a"])
	}
	if len(groups) != 1 || groups[0].Stuck != 2 {
		t.Fatalf("groups %#v", groups)
	}
}