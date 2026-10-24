package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"encoding/hex"
	"strings"
)

const (
	crockfordAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
	checkAlphabet     = "0123456789ABCDEFGHJKMNPQRSTVWXYZ*~$=U"
)

// HashOpaque is the SHA-256 hex of a token or short code.
// Session and device tokens are stored only as this digest.
func HashOpaque(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// NewSessionToken is sbs_ plus 32 random bytes, base64 raw url.
func NewSessionToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "sbs_" + base64.RawURLEncoding.EncodeToString(buf), nil
}

// NewDeviceToken is sbd_ plus 32 random bytes, base32, no padding.
func NewDeviceToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	enc := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(buf)
	return "sbd_" + enc, nil
}

// DevicePrefix is the first eight characters, for logs that must not
// carry the rest of the token.
func DevicePrefix(token string) string {
	if len(token) <= 8 {
		return token
	}
	return token[:8]
}

// NewSetupCode is 40 random bits rendered as XXXX-XXXX.
func NewSetupCode() (string, error) {
	raw, err := crockfordBits(40)
	if err != nil {
		return "", err
	}
	return raw[:4] + "-" + raw[4:], nil
}

// NewInviteCode is 48 random bits, Crockford, with a check character.
func NewInviteCode() (string, error) {
	payload, err := crockfordBits(48)
	if err != nil {
		return "", err
	}
	return payload + string(checkCharacter(payload)), nil
}

// NewPairingCode is SBP-XXXX-XXXX-C. The last character checks the payload.
func NewPairingCode() (string, error) {
	payload, err := crockfordBits(40)
	if err != nil {
		return "", err
	}
	// Same check character the scan classifier accepts.
	check := checkCharacter(payload)
	return "SBP-" + payload[:4] + "-" + payload[4:] + "-" + string(check), nil
}

func crockfordBits(nBits int) (string, error) {
	if nBits <= 0 || nBits > 60 {
		return "", validation()
	}
	var acc uint64
	nbytes := (nBits + 7) / 8
	buf := make([]byte, nbytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	extra := nbytes*8 - nBits
	if extra > 0 {
		buf[0] &= 0xff >> extra
	}
	for _, b := range buf {
		acc = (acc << 8) | uint64(b)
	}
	nChars := (nBits + 4) / 5
	chars := make([]byte, nChars)
	for i := nChars - 1; i >= 0; i-- {
		chars[i] = crockfordAlphabet[acc&31]
		acc >>= 5
	}
	return string(chars), nil
}

func checkCharacter(payload string) byte {
	n := 0
	for _, c := range payload {
		n = (n*32 + crockfordIndex(c)) % 37
	}
	return checkAlphabet[n]
}

func crockfordIndex(c rune) int {
	switch c {
	case 'O', 'o':
		c = '0'
	case 'I', 'i', 'L', 'l':
		c = '1'
	}
	if c >= 'a' && c <= 'z' {
		c = c - 'a' + 'A'
	}
	return strings.IndexRune(crockfordAlphabet, c)
}

func checkIndex(c rune) int {
	switch c {
	case 'O', 'o':
		c = '0'
	case 'I', 'i', 'L', 'l':
		c = '1'
	}
	if c >= 'a' && c <= 'z' {
		c = c - 'a' + 'A'
	}
	return strings.IndexRune(checkAlphabet, c)
}

// NormalizeSetupCode accepts XXXX-XXXX or eight Crockford characters.
// The result includes the hyphen, which is the form that was hashed.
func NormalizeSetupCode(raw string) (string, bool) {
	body := foldCrockford(raw)
	if len(body) != 8 || !payloadChars(body) {
		return "", false
	}
	return body[:4] + "-" + body[4:], true
}

// NormalizeInviteCode strips separators and checks the final character.
func NormalizeInviteCode(raw string) (string, bool) {
	folded := foldCrockford(raw)
	if len(folded) < 2 {
		return "", false
	}
	payload, check := folded[:len(folded)-1], folded[len(folded)-1]
	if !payloadChars(payload) {
		return "", false
	}
	if checkIndex(rune(check)) < 0 {
		return "", false
	}
	if checkCharacter(payload) != check {
		return "", false
	}
	return payload + string(check), true
}

// NormalizePairingCode trims ASCII whitespace, folds lookalikes, and
// checks SBP-XXXX-XXXX-C. A bad check character is distinguished by the
// second result: the code matched the shape but not the check.
func NormalizePairingCode(raw string) (code string, wellFormed, checkOK bool) {
	trimmed := trimASCII(raw)
	var b strings.Builder
	for _, r := range trimmed {
		if r > 127 {
			return "", false, false
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
	folded := b.String()
	if !pairingPattern(folded) {
		return "", false, false
	}
	parts := strings.Split(folded, "-")
	payload := parts[1] + parts[2]
	if checkCharacter(payload) != parts[3][0] {
		return folded, true, false
	}
	return folded, true, true
}

func pairingPattern(raw string) bool {
	parts := strings.Split(raw, "-")
	if len(parts) != 4 || parts[0] != "SBP" || len(parts[1]) != 4 || len(parts[2]) != 4 || len(parts[3]) != 1 {
		return false
	}
	if !payloadChars(parts[1]) || !payloadChars(parts[2]) {
		return false
	}
	return checkIndex(rune(parts[3][0])) >= 0
}

func payloadChars(raw string) bool {
	if raw == "" {
		return false
	}
	for _, r := range raw {
		if crockfordIndex(r) < 0 {
			return false
		}
	}
	return true
}

func foldCrockford(raw string) string {
	var b strings.Builder
	for _, r := range raw {
		if r == '-' || r == ' ' || r == '\t' {
			continue
		}
		switch r {
		case 'O', 'o':
			b.WriteByte('0')
		case 'I', 'i', 'L', 'l':
			b.WriteByte('1')
		default:
			if r >= 'a' && r <= 'z' {
				b.WriteRune(r - 'a' + 'A')
			} else {
				b.WriteRune(r)
			}
		}
	}
	return b.String()
}

func trimASCII(raw string) string {
	return strings.Trim(raw, " \t\r\n\v\f")
}
