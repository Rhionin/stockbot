package barcode

import "strings"

// asciiSpace is the only whitespace removed before classification. U+0085,
// U+00A0, and U+FEFF stay put.
const asciiSpace = " \t\n\v\f\r"

const (
	// MaxGTINDigits is the longest GS1 GTIN (ITF-14). Longer digit strings are QR.
	MaxGTINDigits = 14
	// MinLookupTokenLen and MaxLookupTokenLen bound letter-digit product codes
	// (FNSKU, ASIN, and the like). The Architect may adjust these bounds.
	MinLookupTokenLen = 4
	MaxLookupTokenLen = 32
)

// TrimASCII drops leading and trailing space, tab, LF, VT, FF, and CR.
func TrimASCII(raw string) string {
	return strings.Trim(raw, asciiSpace)
}

// IsLookupEligible reports whether raw may be sent to product lookup after
// ASCII trim (PM rule). Adjust letter-digit bounds via the token length
// constants above; future Architect rules should change only this function.
func IsLookupEligible(raw string) bool {
	if IsProductBarcode(raw) {
		return true
	}
	return isLetterDigitProductToken(raw)
}

// IsProductBarcode reports ASCII digits of length 1 through MaxGTINDigits after
// the ASCII-only trim. Bytes that were not trimmed (U+0085, U+00A0, U+FEFF)
// keep the value from being a product barcode.
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

func isLetterDigitProductToken(raw string) bool {
	value := TrimASCII(raw)
	if len(value) < MinLookupTokenLen || len(value) > MaxLookupTokenLen {
		return false
	}
	hasLetter := false
	for i := 0; i < len(value); i++ {
		c := value[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
			hasLetter = true
		case c >= '0' && c <= '9':
		default:
			return false
		}
	}
	return hasLetter
}