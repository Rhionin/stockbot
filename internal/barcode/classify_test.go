package barcode

import (
	"strings"
	"testing"
)

func TestClassifyVectors(t *testing.T) {
	t.Parallel()
	token20 := strings.Repeat("A", 18) + "12"
	token21 := token20 + "Z"
	const bom = "\ufeff"
	cases := []struct {
		name string
		raw  string
		want LookupKind
	}{
		{name: "12 digits product", raw: "123456789012", want: LookupProduct},
		{name: "14 digits leading zero", raw: "01234567890123", want: LookupProduct},
		{name: "15 digits qr", raw: "123456789012345", want: LookupQR},
		{name: "16 digit qr", raw: "1234567890123456", want: LookupQR},
		{name: "20 digit qr", raw: "12345678901234567890", want: LookupQR},
		{name: "FNSKU local", raw: "X002MB5N7L", want: LookupLocalOnly},
		{name: "1E10 local", raw: "1E10", want: LookupLocalOnly},
		{name: "1E5 qr", raw: "1E5", want: LookupQR},
		{name: "20 char local", raw: token20, want: LookupLocalOnly},
		{name: "21 char qr", raw: token21, want: LookupQR},
		{name: "letters only", raw: "ABCDEFGH", want: LookupQR},
		{name: "hello qr", raw: "hello", want: LookupQR},
		{name: "NUL inside", raw: "1234\x005", want: LookupQR},
		{name: "U+0085 inside", raw: "1234567\u0085", want: LookupQR},
		{name: "U+00A0 inside", raw: "1234567\u00a0", want: LookupQR},
		{name: "BOM prefix", raw: bom + "1234567", want: LookupQR},
		{name: "AB12 local", raw: "AB12", want: LookupLocalOnly},
		{name: "AB1 qr", raw: "AB1", want: LookupQR},
		{name: "url qr", raw: "https://example.com", want: LookupQR},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Classify(tc.raw); got != tc.want {
				t.Fatalf("Classify(%q) = %v, want %v", tc.raw, got, tc.want)
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
}