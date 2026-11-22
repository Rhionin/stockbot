package barcode

import "strings"

const (
	// MaxGTINDigits is the longest GS1 GTIN (ITF-14). Longer digit-only strings are QR.
	MaxGTINDigits = 14
	// MinLocalTokenLen and MaxLocalTokenLen bound local-only letter+digit tokens.
	MinLocalTokenLen = 4
	MaxLocalTokenLen = 20
)

// LookupKind is how a trimmed scan may be looked up.
type LookupKind int

const (
	// LookupQR means no lookup: store as QR, no barcode_misses.
	LookupQR LookupKind = iota
	// LookupProduct is a 1–14 digit product barcode (full tiered lookup).
	LookupProduct
	// LookupLocalOnly is a 4–20 ASCII letter+digit token with at least one of
	// each (FNSKU, ASIN, and similar): household catalog only.
	LookupLocalOnly
)

// TrimASCII drops leading and trailing space, tab, LF, VT, FF, and CR.
func TrimASCII(raw string) string {
	return strings.Trim(raw, ASCIISpaceChars)
}

// Classify applies the PM/Architect rules after ASCII trim (ASCII byte checks only).
func Classify(raw string) LookupKind {
	if IsProductBarcode(raw) {
		return LookupProduct
	}
	if isLocalOnlyToken(raw) {
		return LookupLocalOnly
	}
	return LookupQR
}

// IsLookupEligible reports whether raw may enter any lookup path (product or local-only).
func IsLookupEligible(raw string) bool {
	return Classify(raw) != LookupQR
}

// IsProductBarcode reports ASCII digits of length 1 through MaxGTINDigits after
// the ASCII-only trim. Non-ASCII bytes keep the value from being a product barcode.
func IsProductBarcode(raw string) bool {
	value := TrimASCII(raw)
	if value == "" || len(value) > MaxGTINDigits {
		return false
	}
	for i := 0; i < len(value); i++ {
		if value[i] < '0' || value[i] > '9' {
			return false
		}
	}
	return true
}

// isLocalOnlyToken reports a single token of ASCII letters and digits only,
// length MinLocalTokenLen through MaxLocalTokenLen, with at least one letter
// and at least one digit.
func isLocalOnlyToken(raw string) bool {
	value := TrimASCII(raw)
	if len(value) < MinLocalTokenLen || len(value) > MaxLocalTokenLen {
		return false
	}
	hasLetter, hasDigit := false, false
	for i := 0; i < len(value); i++ {
		c := value[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
			hasLetter = true
		case c >= '0' && c <= '9':
			hasDigit = true
		default:
			return false
		}
	}
	return hasLetter && hasDigit
}
