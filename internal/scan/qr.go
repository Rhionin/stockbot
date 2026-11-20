package scan

import (
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

// maxGTINDigits is the longest GS1 GTIN (ITF-14). A longer digit string is
// not a product barcode this queue can stock.
const maxGTINDigits = 14

// qrPreviewRunes is how much of a non-URL payload the queue and the push
// show. The cut matches frontend/src/components/queue/qrPreview.ts.
const qrPreviewRunes = 48

// IsQRPayload reports whether raw is a URL-like QR payload rather than a
// product barcode. After ASCII trim, http://, https://, or www. URLs are QR
// payloads. Alphanumeric codes (FNSKU, ASIN, and the like) are not.
func IsQRPayload(raw string) bool {
	value := TrimASCII(raw)
	if value == "" {
		return false
	}
	_, ok := qrHost(value)
	return ok
}

// QRPreview is the short text shown for a QR payload: the URL host, or the
// payload cut on a rune boundary. An empty payload previews as empty.
func QRPreview(raw string) string {
	value := TrimASCII(raw)
	if value == "" {
		return ""
	}
	if host, ok := qrHost(value); ok {
		return shortenQR(host, qrPreviewRunes)
	}
	return shortenQR(value, qrPreviewRunes)
}

// NewQREntry builds a queue row for a QR payload. Product lookup is not
// involved, so ProductID stays unset and the status is QR.
func NewQREntry(userID, barcode string, direction *ScanDirection, scannedAt time.Time) ScanEntry {
	return ScanEntry{
		UserID:    userID,
		Barcode:   TrimASCII(barcode),
		ScannedAt: scannedAt,
		Direction: direction,
		UnitCount: 1,
		Status:    QR,
	}
}

func qrHost(value string) (string, bool) {
	candidate := value
	lower := strings.ToLower(value)
	switch {
	case strings.HasPrefix(lower, "https://"), strings.HasPrefix(lower, "http://"):
	case strings.HasPrefix(lower, "www."):
		candidate = "https://" + value
	default:
		return "", false
	}
	parsed, err := url.Parse(candidate)
	if err != nil {
		return "", false
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "" {
		return "", false
	}
	return host, true
}

func shortenQR(value string, maxRunes int) string {
	if utf8.RuneCountInString(value) <= maxRunes {
		return value
	}
	if maxRunes <= 1 {
		return "…"
	}
	runes := []rune(value)
	return string(runes[:maxRunes-1]) + "…"
}
