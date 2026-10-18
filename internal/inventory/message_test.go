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

func TestCommitFailureCopy(t *testing.T) {
	checks := []struct {
		name string
		got  string
		want string
	}{
		{"not pending", NotPendingMessage, "This scan was already handled."},
		{"product unresolved", ProductUnresolvedMessage, "Pick a product for this scan first."},
		{"which item", WhichItemMessage, "Which item is this?"},
		{"scan not found", ScanNotFoundMessage, "This scan is no longer in the queue."},
		{"direction unset", DirectionUnsetMessage, "Choose stock in or stock out first."},
		{"internal", InternalMessage, "Something went wrong. Try again."},
	}
	for _, tt := range checks {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Fatalf("%s = %q, want %q", tt.name, tt.got, tt.want)
			}
		})
	}
	if CodeNotPending != "not_pending" || CodeProductUnresolved != "product_unresolved" ||
		CodeScanNotFound != "scan_not_found" || CodeDirectionUnset != "direction_unset" ||
		CodeInternal != "internal" || CodeNoItem != "no_item" {
		t.Fatalf("codes drifted: %s %s %s %s %s %s", CodeNotPending, CodeProductUnresolved, CodeScanNotFound, CodeDirectionUnset, CodeInternal, CodeNoItem)
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
