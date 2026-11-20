package attention

import "testing"

func TestWireFromStoredCatchAll(t *testing.T) {
	cases := []struct {
		status, reason string
		wantReason     Reason
		wantDetail     string
		wantOK         bool
	}{
		{"failed", "", ReasonSendFailed, "", true},
		{"failed", "zz_future", ReasonSendFailed, "", true},
		{"failed", "no_upc", ReasonSendFailed, "", true},
		{"unknown", "zz_future", ReasonSendUnsure, "", true},
		{"skipped", "api_error", "", "", false},
		{"sent", "zz_future", "", "", false},
	}
	for _, tc := range cases {
		r, d, ok := WireFromStored(tc.status, tc.reason)
		if ok != tc.wantOK || r != tc.wantReason || d != tc.wantDetail {
			t.Fatalf("%s/%s -> (%s,%q,%v) want (%s,%q,%v)", tc.status, tc.reason, r, d, ok, tc.wantReason, tc.wantDetail, tc.wantOK)
		}
		if ok && !isEmittable(r) {
			t.Fatalf("non-emittable %s", r)
		}
	}
}