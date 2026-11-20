package attention

import (
	"fmt"
	"strings"
	"testing"
)

// EmittableReasonStrings returns ticket-A emittable reasons as strings (tests).
func EmittableReasonStrings() []string {
	return []string{
		string(ReasonNoBarcode),
		string(ReasonNoStoreMatch),
		string(ReasonSendUnsure),
		string(ReasonSendFailed),
		string(ReasonNotPlanned),
	}
}

// AssertEmittable fails if reason is not one of the five emittable values.
func AssertEmittable(reason string) bool {
	return isEmittable(Reason(reason))
}

// ReservedReasons are never emitted in ticket A.
func ReservedReasons() []Reason {
	return []Reason{ReasonNotConnected, ReasonReconnectRequired, ReasonProbableMiss}
}

func reservedReason(s string) bool {
	for _, r := range ReservedReasons() {
		if string(r) == s {
			return true
		}
	}
	return s == "attention"
}

// ValidateWireReasons returns an error if any reason string is not emittable.
func ValidateWireReasons(reason string, others []Reason) error {
	if !isEmittable(Reason(reason)) || reservedReason(reason) {
		return fmt.Errorf("non-emittable reason %q", reason)
	}
	for _, o := range others {
		if !isEmittable(o) {
			return fmt.Errorf("non-emittable other %q", o)
		}
	}
	return nil
}

// ContainsReserved reports whether s is a reserved or legacy attention reason.
func ContainsReserved(s string) bool {
	return reservedReason(s) || strings.EqualFold(s, "attention")
}

func TestEvaluateWireHelpers(t *testing.T) {
	if len(EmittableReasonStrings()) != 5 {
		t.Fatal("emittable count")
	}
	if !AssertEmittable(string(ReasonSendFailed)) || AssertEmittable(string(ReasonNotConnected)) {
		t.Fatal("assert emittable")
	}
	if GroupKey(ReasonSendFailed, "api_error") != "send_failed\x00api_error" {
		t.Fatal("group key")
	}
	if got := FormatReasons(nil); len(got) != 0 {
		t.Fatalf("format nil %#v", got)
	}
	if got := FormatReasons([]Reason{ReasonNoBarcode}); got[0] != "no_barcode" {
		t.Fatalf("format %#v", got)
	}
}