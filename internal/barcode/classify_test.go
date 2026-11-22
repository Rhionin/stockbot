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

func TestClassifyArchitectBoundaryTable(t *testing.T) {
	t.Parallel()
	cases := []struct {
		raw  string
		want LookupKind
	}{
		{"", LookupQR},
		{"   \t\r\n", LookupQR},
		{"0", LookupProduct},
		{"036000291452", LookupProduct},
		{" \t4006381333931\r\n", LookupProduct},
		{"10012345678902", LookupProduct},
		{"1234567890123456789012345678901234", LookupQR},
		{"X002MB5N7L", LookupLocalOnly},
		{"x002mb5n7l", LookupLocalOnly},
		{" \tX002MB5N7L\r\n", LookupLocalOnly},
		{"080442957X", LookupLocalOnly},
		{"ABC1", LookupLocalOnly},
		{"ABCD", LookupQR},
		{"1234567890123A", LookupLocalOnly},
		{"AB 1234", LookupQR},
		{"AB-1234", LookupQR},
		{"AB_1234", LookupQR},
		{"A.1234", LookupQR},
		{"AB1234\x00", LookupQR},
		{"AB\x001234", LookupQR},
		{"AB1234\u0085", LookupQR},
		{"AB1234\u00a0", LookupQR},
		{"\ufeffAB1234", LookupQR},
		{"\uff21\uff22\uff23\uff24" + "1234", LookupQR},
		{"\uff11\uff12\uff13\uff14\uff15\uff16", LookupQR},
		{"\u0661\u0662\u0663\u0664\u0665\u0666", LookupQR},
		{"ABCD\u00e91234", LookupQR},
		{"\u017f12345", LookupQR},
		{"https://example.com/p/3017620422003", LookupQR},
		{"www.example.com", LookupQR},
		{"WIFI:S:home;P:secret;;", LookupQR},
		{"BEGIN:VCARD\nVERSION:3.0\nEND:VCARD", LookupQR},
		{"mailto:a@b.co", LookupQR},
		{"otpauth://totp/x?secret=ABCD1234", LookupQR},
		{"0123456789abcdef0123456789abcdef", LookupQR},
	}
	for _, tc := range cases {
		if got := Classify(tc.raw); got != tc.want {
			t.Errorf("Classify(%q) = %v, want %v", tc.raw, got, tc.want)
		}
	}
	if IsLookupEligible("WIFI:S:home;P:secret;;") {
		t.Error("wifi payload must not be lookup eligible")
	}
}
