package inventory

import "testing"

func TestNoneOnHandMessage(t *testing.T) {
	if NoneOnHandMessage != "Nothing on hand to take out." {
		t.Fatalf("NoneOnHandMessage = %q, want %q", NoneOnHandMessage, "Nothing on hand to take out.")
	}
}

func TestInstanceGoneMessage(t *testing.T) {
	if InstanceGoneMessage != "That one was already taken out. Pick another." {
		t.Fatalf("InstanceGoneMessage = %q, want %q", InstanceGoneMessage, "That one was already taken out. Pick another.")
	}
}

func TestStockOutConflictCodes(t *testing.T) {
	if CodeNoneOnHand != "none_on_hand" {
		t.Fatalf("CodeNoneOnHand = %q", CodeNoneOnHand)
	}
	if CodeInstanceGone != "instance_gone" {
		t.Fatalf("CodeInstanceGone = %q", CodeInstanceGone)
	}
}
