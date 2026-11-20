package scan

import (
	"regexp"
	"strings"
)

// asciiSpace is the only whitespace removed before a pairing code or a
// product barcode is classified. U+0085, U+00A0, and U+FEFF stay put.
const asciiSpace = " \t\n\v\f\r"

const (
	pairingPayloadAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
	pairingCheckAlphabet   = pairingPayloadAlphabet + "*~$=U"

	// maxBareProductTokenLen is the longest single product-code token that may
	// be sent to lookup (FNSKU, ASIN, long digit strings, and the like).
	maxBareProductTokenLen = 32
)

var pairingGrammar = regexp.MustCompile(`^SBP-[0-9A-HJKMNP-TV-Z]{4}-[0-9A-HJKMNP-TV-Z]{4}-[0-9A-HJKMNP-TV-Z*~$=U]$`)

// TrimASCII drops leading and trailing space, tab, LF, VT, FF, and CR.
func TrimASCII(raw string) string {
	return strings.Trim(raw, asciiSpace)
}

// NormalizeScan trims ASCII whitespace, rejects a remaining non-ASCII
// character, uppercases ASCII a-z, and maps O/I/L lookalikes. ok is false
// when the value is rejected; the returned string is empty in that case.
func NormalizeScan(raw string) (string, bool) {
	trimmed := TrimASCII(raw)
	var b strings.Builder
	for _, r := range trimmed {
		if r > 127 {
			return "", false
		}
		if r >= 'a' && r <= 'z' {
			r = r - 'a' + 'A'
		}
		switch r {
		case 'O':
			r = '0'
		case 'I', 'L':
			r = '1'
		}
		b.WriteRune(r)
	}
	return b.String(), true
}

// PairingScanMessage is the scan-page sentence for an SBP- code. It does not
// repeat the code.
const PairingScanMessage = "That code pairs a hub. It is not a product."

// LooksLikePairingCode reports the SBP-XXXX-XXXX-C shape, including a bad
// check character. Those scans are never stored as products.
func LooksLikePairingCode(raw string) bool {
	code, ok := NormalizeScan(raw)
	return ok && pairingGrammar.MatchString(code)
}

// IsPairingCode reports a well-formed code whose check character matches.
func IsPairingCode(raw string) bool {
	code, ok := NormalizeScan(raw)
	if !ok || !pairingGrammar.MatchString(code) {
		return false
	}
	check, ok := pairingCheck(code[4:8] + code[9:13])
	return ok && code[14] == check
}

// isBareProductToken reports whether raw is one lookup-safe product code after
// ASCII trim: letters, digits, and hyphens only, length 1 through 32.
func isBareProductToken(raw string) bool {
	value := TrimASCII(raw)
	if value == "" || len(value) > maxBareProductTokenLen {
		return false
	}
	lower := strings.ToLower(value)
	if lower == "http" || lower == "https" {
		return false
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-':
		default:
			return false
		}
	}
	return true
}

// IsProductBarcode reports ASCII digits of length 1 through 14 after the
// ASCII-only trim. Lookalike letters are not digits, and bytes that were
// not trimmed (U+0085, U+00A0, U+FEFF) keep the value from being a product.
func IsProductBarcode(raw string) bool {
	value := TrimASCII(raw)
	if value == "" || len(value) > maxGTINDigits {
		return false
	}
	for i := 0; i < len(value); i++ {
		if value[i] < '0' || value[i] > '9' {
			return false
		}
	}
	return true
}

// PairingCheck is the ISO 7064 mod 37,2 character for an 8-character payload.
func PairingCheck(payload string) (byte, bool) {
	return pairingCheck(payload)
}

func pairingCheck(payload string) (byte, bool) {
	var value uint64
	for i := 0; i < len(payload); i++ {
		digit := strings.IndexByte(pairingPayloadAlphabet, payload[i])
		if digit < 0 {
			return 0, false
		}
		value = value*32 + uint64(digit)
	}
	return pairingCheckAlphabet[value%37], true
}
