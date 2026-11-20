package barcode

import (
	"strings"
	"testing"
)

func TestIsLookupEligibleVectors(t *testing.T) {
	t.Parallel()
	const bom = "\ufeff"
	cases := []struct {
		name string
		raw  string
		want bool
	}{
		{name: "1E10 token", raw: "1E10", want: true},
		{name: "1E5 qr", raw: "1E5", want: false},
		{name: "NUL inside", raw: "1234\x005", want: false},
		{name: "U+0085 inside", raw: "1234567\u0085", want: false},
		{name: "U+00A0 inside", raw: "1234567\u00a0", want: false},
		{name: "BOM prefix", raw: bom + "1234567", want: false},
		{name: "BOM suffix", raw: "1234567" + bom, want: false},
		{name: "14 digits leading zero", raw: "01234567890123", want: true},
		{name: "15 digits", raw: "123456789012345", want: false},
		{name: "FNSKU", raw: "X002MB5N7L", want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsLookupEligible(tc.raw); got != tc.want {
				t.Fatalf("IsLookupEligible(%q) = %v, want %v", tc.raw, got, tc.want)
			}
		})
	}
}

func TestTrimASCIIDoesNotStripUnicodeSpace(t *testing.T) {
	t.Parallel()
	if TrimASCII("1234567\u00a0") != "1234567\u00a0" {
		t.Fatal("U+00A0 was trimmed")
	}
	if !IsProductBarcode("1234567 ") {
		t.Fatal("ASCII space trim failed")
	}
	if IsProductBarcode(strings.Repeat("1", MaxGTINDigits+1)) {
		t.Fatal("too many digits were eligible")
	}
}