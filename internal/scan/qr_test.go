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
	token32 := strings.Repeat("A", 32)
	token33 := strings.Repeat("B", 33)
	cases := []struct {
		name string
		raw  string
		isQR bool
	}{
		{name: "UPC-A", raw: "036000291452", isQR: false},
		{name: "EAN-8", raw: "96385074", isQR: false},
		{name: "EAN-13", raw: "4006381333931", isQR: false},
		{name: "ITF-14", raw: "10012345678902", isQR: false},
		{name: "12 digits", raw: "123456789012", isQR: false},
		{name: "13 digits", raw: "4006381333931", isQR: false},
		{name: "14 digits", raw: "10012345678902", isQR: false},
		{name: "15 digits", raw: "123456789012345", isQR: true},
		{name: "16 digits", raw: "1234567890123456", isQR: true},
		{name: "20 digits", raw: "12345678901234567890", isQR: true},
		{name: "short digits", raw: "123456", isQR: false},
		{name: "empty", raw: "", isQR: true},
		{name: "spaces only", raw: "   ", isQR: true},
		{name: "https URL", raw: "https://example.com/product/3017620422003", isQR: true},
		{name: "http URL", raw: "http://example.com", isQR: true},
		{name: "uppercase HTTP", raw: "HTTP://EXAMPLE.COM/path", isQR: true},
		{name: "uppercase WWW", raw: "WWW.EXAMPLE.COM/item", isQR: true},
		{name: "padded URL", raw: "  https://example.com/x  ", isQR: true},
		{name: "padded https with CRLF", raw: "  https://example.com/x \r\n", isQR: true},
		{name: "padded www with tab", raw: "\twww.example.com/x\n", isQR: true},
		{name: "HTTPS mixed case host", raw: "HTTPS://Example.com/x", isQR: true},
		{name: "padded UPC-A", raw: "  036000291452  ", isQR: false},
		{name: "www URL", raw: "www.example.com/item", isQR: true},
		{name: "four-letter token", raw: "http", isQR: false},
		{name: "mid-string www", raw: "see www.example.com here", isQR: true},
		{name: "lone www dot", raw: "www.", isQR: true},
		{name: "lone http scheme", raw: "http://", isQR: true},
		{name: "https empty host", raw: "https://", isQR: true},
		{name: "http triple slash path", raw: "http:///path", isQR: true},
		{name: "ftp scheme", raw: "ftp://files.example.com/a", isQR: true},
		{name: "mailto", raw: "mailto:you@example.com", isQR: true},
		{name: "WIFI config", raw: "WIFI:T:WPA;S:MyNet;P:supersecret;;", isQR: true},
		{name: "BEGIN VCARD", raw: "BEGIN:VCARD\nFN:Test", isQR: true},
		{name: "geo", raw: "geo:37.78,-122.4", isQR: true},
		{name: "tel", raw: "tel:+15551212", isQR: true},
		{name: "Amazon FNSKU", raw: "X002MB5N7L", isQR: false},
		{name: "Amazon ASIN", raw: "B08XYZ1234", isQR: false},
		{name: "4-char alphanumeric", raw: "AB12", isQR: false},
		{name: "3-char alphanumeric", raw: "AB1", isQR: true},
		{name: "short text token", raw: "hello", isQR: false},
		{name: "text with a space", raw: "hello world", isQR: true},
		{name: "buy milk", raw: "buy milk", isQR: true},
		{name: "hyphenated digits", raw: "1234-5678", isQR: true},
		{name: "32-char token", raw: token32, isQR: false},
		{name: "33-char token", raw: token33, isQR: true},
		{name: "digits with ASCII space", raw: "1234567 ", isQR: false},
		{name: "digits with next-line", raw: "1234567\u0085", isQR: true},
		{name: "digits with no-break space", raw: "1234567\u00a0", isQR: true},
		{name: "1E10 lookup token", raw: "1E10", isQR: false},
		{name: "1E5 qr token", raw: "1E5", isQR: true},
		{name: "NUL inside", raw: "1234\x005", isQR: true},
		{name: "BOM prefix", raw: "\ufeff1234567", isQR: true},
		{name: "14 digits leading zero", raw: "01234567890123", isQR: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsQRPayload(tc.raw); got != tc.isQR {
				t.Fatalf("IsQRPayload(%q) = %v, want %v", tc.raw, got, tc.isQR)
			}
		})
	}
}

func TestSanitizeQRStoredTruncatesOnBytes(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("x", maxQRStoredBytes+40)
	got := sanitizeQRStored(long)
	if len(got) > maxQRStoredBytes {
		t.Fatalf("stored bytes = %d, want <= %d (%q)", len(got), maxQRStoredBytes, got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("stored value = %q", got)
	}
}

func TestSanitizeQRStoredStripsControlBytes(t *testing.T) {
	t.Parallel()
	raw := "WIFI:S:home\x00;P:secret\x1f;;"
	got := sanitizeQRStored(raw)
	if strings.ContainsRune(got, '\x00') || strings.ContainsRune(got, '\x1f') {
		t.Fatalf("control bytes remained: %q", got)
	}
	if !strings.Contains(got, "WIFI:") {
		t.Fatalf("stored value = %q", got)
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
		{raw: "WIFI:T:WPA;S:Net;P:secret;;", want: "Wi-Fi code"},
		{raw: "BEGIN:VCARD\nFN:Test", want: "Contact card"},
		{raw: "otpauth://totp/Example", want: "Authenticator code"},
		{raw: "mailto:you@example.com", want: "QR code"},
		{raw: "  buy milk  ", want: "QR code"},
		{raw: "123456789012345", want: "QR code"},
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
	host := strings.Repeat("你", 60) + ".example.com"
	got := QRPreview("https://" + host + "/path")
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