package shopping

import "testing"

func TestBuildScopedChangeEffects(t *testing.T) {
	cause := CauseTargetChanged
	added := buildScopedChange("item:x", 0, 2, cause, nil, 0)
	if added == nil || added.Effect != EffectAdded {
		t.Fatal("added")
	}
	if added.FromQty != nil || added.ToQty != nil {
		t.Fatalf("added must omit qty, from %v to %v", added.FromQty, added.ToQty)
	}
	if ch := buildScopedChange("item:x", 2, 0, cause, nil, 0); ch == nil || ch.Effect != EffectTakenOff {
		t.Fatal("taken_off")
	}
	if ch := buildScopedChange("item:x", 1, 3, cause, nil, 0); ch == nil || ch.Effect != EffectAmountUp {
		t.Fatal("amount_up")
	}
	if ch := buildScopedChange("item:x", 3, 1, cause, nil, 0); ch == nil || ch.Effect != EffectAmountDown {
		t.Fatal("amount_down")
	}
	if ch := buildScopedChange("item:x", 2, 2, cause, nil, 0); ch != nil {
		t.Fatal("unchanged should be nil")
	}
}