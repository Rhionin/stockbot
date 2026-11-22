package scan

import (
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Rhionin/stockbot/internal/barcode"
)

// qrPreviewRunes is how much of a URL host the queue and the push show. The cut
// matches frontend/src/components/queue/qrPreview.ts.
const qrPreviewRunes = 48

// maxQRStoredBytes is how many UTF-8 bytes of a QR payload are stored on scan_entries.
const maxQRStoredBytes = 128

// IsQRPayload reports whether raw is a QR payload rather than any lookup path.
func IsQRPayload(raw string) bool {
	return barcode.Classify(raw) == barcode.LookupQR
}

// QRPreview is the short text shown for a QR payload: a URL host, a scheme
// label, or a generic QR label. An empty payload previews as empty.
func QRPreview(raw string) string {
	value := TrimASCII(raw)
	if value == "" {
		return ""
	}
	if host, ok := qrHost(value); ok {
		return shortenQR(host, qrPreviewRunes)
	}
	if label := qrSchemeLabel(value); label != "" {
		return label
	}
	return "QR code"
}

// NewQREntry builds a queue row for a QR payload. Product lookup is not
// involved, so ProductID stays unset and the status is QR.
func NewQREntry(userID, barcode string, direction *ScanDirection, scannedAt time.Time) ScanEntry {
	return ScanEntry{
		UserID:    userID,
		Barcode:   sanitizeQRStored(TrimASCII(barcode)),
		ScannedAt: scannedAt,
		Direction: direction,
		UnitCount: 1,
		Status:    QR,
	}
}

func sanitizeQRStored(value string) string {
	value = stripControlBytes(value)
	return shortenQRBytes(value, maxQRStoredBytes)
}

func stripControlBytes(value string) string {
	var b strings.Builder
	b.Grow(len(value))
	for i := 0; i < len(value); i++ {
		c := value[i]
		if c == 0 || c < 0x20 || c == 0x7f {
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

func shortenQRBytes(value string, maxBytes int) string {
	if len(value) <= maxBytes {
		return value
	}
	const ellipsis = "…"
	budget := maxBytes - len(ellipsis)
	if budget < 1 {
		return ellipsis
	}
	n := 0
	for _, r := range value {
		rb := utf8.RuneLen(r)
		if n+rb > budget {
			break
		}
		n += rb
	}
	return value[:n] + ellipsis
}

func qrSchemeLabel(value string) string {
	lower := strings.ToLower(value)
	switch {
	case strings.HasPrefix(lower, "wifi:"):
		return "Wi-Fi code"
	case strings.HasPrefix(lower, "begin:vcard"):
		return "Contact card"
	case strings.HasPrefix(lower, "otpauth:"):
		return "Authenticator code"
	}
	colon := strings.Index(value, ":")
	if colon <= 0 || colon >= 32 {
		return ""
	}
	prefix := lower[:colon]
	if strings.HasPrefix(lower, "http:") || strings.HasPrefix(lower, "https:") {
		return ""
	}
	for i := 0; i < len(prefix); i++ {
		c := prefix[i]
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '+' || c == '-' || c == '.' {
			continue
		}
		return ""
	}
	return "QR code"
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
