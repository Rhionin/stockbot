package scan

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestProductBarcodeKeepsUnicodeWhitespace(t *testing.T) {
	t.Parallel()
	if IsProductBarcode("1234567\u0085") || IsProductBarcode("1234567\u00a0") {
		t.Fatal("unicode whitespace was trimmed into a product barcode")
	}
	if !IsProductBarcode("1234567 ") || !IsProductBarcode("\t1234567\r\n") {
		t.Fatal("ASCII whitespace was not trimmed")
	}
	if TrimASCII("1234567\u00a0") != "1234567\u00a0" {
		t.Fatal("U+00A0 was trimmed")
	}
}

func TestIsQRPayload(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		raw  string
		isQR bool
	}{
		{name: "UPC-A", raw: "036000291452", isQR: false},
		{name: "EAN-8", raw: "96385074", isQR: false},
		{name: "EAN-13", raw: "4006381333931", isQR: false},
		{name: "ITF-14", raw: "10012345678902", isQR: false},
		{name: "short digits", raw: "123456", isQR: false},
		{name: "empty", raw: "", isQR: false},
		{name: "spaces only", raw: "   ", isQR: false},
		{name: "https URL", raw: "https://example.com/product/3017620422003", isQR: true},
		{name: "http URL", raw: "http://example.com", isQR: true},
		{name: "www URL", raw: "www.example.com/item", isQR: true},
		{name: "short text", raw: "hello", isQR: true},
		{name: "text with a space", raw: "buy milk", isQR: true},
		{name: "digits and a symbol", raw: "1234-5678", isQR: true},
		{name: "longer than a GTIN", raw: "123456789012345", isQR: true},
		{name: "digits with ASCII space", raw: "1234567 ", isQR: false},
		{name: "digits with next-line", raw: "1234567\u0085", isQR: true},
		{name: "digits with no-break space", raw: "1234567\u00a0", isQR: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsQRPayload(tc.raw); got != tc.isQR {
				t.Fatalf("IsQRPayload(%q) = %v, want %v", tc.raw, got, tc.isQR)
			}
		})
	}
}

func TestQRPreview(t *testing.T) {
	t.Parallel()
	cases := []struct {
		raw  string
		want string
	}{
		{raw: "https://world.openfoodfacts.org/product/3017620422003", want: "world.openfoodfacts.org"},
		{raw: "http://example.com/a", want: "example.com"},
		{raw: "www.kroger.com/p/milk", want: "www.kroger.com"},
		{raw: "HTTPS://Shop.Example.com/x", want: "shop.example.com"},
		{raw: "hello", want: "hello"},
		{raw: "  buy milk  ", want: "buy milk"},
		{raw: "123456789012345", want: "123456789012345"},
		{raw: "", want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			if got := QRPreview(tc.raw); got != tc.want {
				t.Fatalf("QRPreview(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

func TestQRPreviewCutsOnRunes(t *testing.T) {
	t.Parallel()
	got := QRPreview(strings.Repeat("你", 60))
	if !utf8.ValidString(got) {
		t.Fatalf("preview is not valid UTF-8: %q", got)
	}
	if utf8.RuneCountInString(got) != qrPreviewRunes {
		t.Fatalf("rune count = %d, want %d (%q)", utf8.RuneCountInString(got), qrPreviewRunes, got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("preview = %q", got)
	}
}

func TestNewQREntrySkipsTheProduct(t *testing.T) {
	t.Parallel()
	when := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	direction := StockIn
	entry := NewQREntry("actor", "  https://example.com/milk  ", &direction, when)
	if entry.Status != QR || entry.ProductID != nil {
		t.Fatalf("entry = %+v", entry)
	}
	if entry.Barcode != "https://example.com/milk" || entry.UnitCount != 1 {
		t.Fatalf("entry = %+v", entry)
	}
	if entry.Direction == nil || *entry.Direction != StockIn || !entry.ScannedAt.Equal(when) {
		t.Fatalf("entry = %+v", entry)
	}
}
