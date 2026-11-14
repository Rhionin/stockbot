package attention

import "testing"

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