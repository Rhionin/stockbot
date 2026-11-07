// Package firmware holds pure OTA manifest validation and Ed25519 verification
// for #452 T2a. It does not import releases, sign, or touch production keys.
package firmware

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

const (
	manifestHeader   = "stockbot-fw-manifest-v1"
	productESP32     = "esp32_scanner"
	maxManifestSize  = 3145728
	sha256HexLen     = 64
	ed25519SigLen    = 64
	ed25519PubLen    = 32
	ed25519SigB64Len = 88
)

// Targets allowed in manifest v1 (design §2).
var allowedTargets = map[string]struct{}{
	"sticks3_u173":            {},
	"sticks3_u173_uart":       {},
	"atoms3lite_u173_bench":   {},
	"atoms3lite_symcode_hid": {},
}

var (
	keyIDPattern    = regexp.MustCompile(`^fw-[0-9]{4}-[0-9]+$`)
	sizeWirePattern = regexp.MustCompile(`^[1-9][0-9]*$`)
	strictB64       = base64.StdEncoding.Strict()
)

// Fields are the validated manifest field values. Callers that already hold raw
// canonical bytes should use VerifyCanonical instead of rebuilding from Fields.
type Fields struct {
	Product string `json:"product"`
	Target  string `json:"target"`
	Version string `json:"version"`
	Size    int64  `json:"size"`
	SHA256  string `json:"sha256"`
	KeyID   string `json:"keyId"`
}

// ErrValidation marks a field or wire-format problem before cryptography runs.
var ErrValidation = errors.New("manifest validation failed")

// ValidateFields checks manifest field rules from firmware-ota.md §4.
func ValidateFields(f Fields) error {
	if f.Product != productESP32 {
		return fmt.Errorf("%w: product", ErrValidation)
	}
	if _, ok := allowedTargets[f.Target]; !ok {
		return fmt.Errorf("%w: target", ErrValidation)
	}
	if !validateSemVer2(f.Version) {
		return fmt.Errorf("%w: version", ErrValidation)
	}
	if f.Size <= 0 || f.Size > maxManifestSize {
		return fmt.Errorf("%w: size", ErrValidation)
	}
	if len(f.SHA256) != sha256HexLen || !isLowerHex(f.SHA256) {
		return fmt.Errorf("%w: sha256", ErrValidation)
	}
	if !keyIDPattern.MatchString(f.KeyID) {
		return fmt.Errorf("%w: keyId", ErrValidation)
	}
	return nil
}

// CanonicalBytes builds the signed manifest bytes from validated fields.
// It rejects invalid fields and never normalizes a bad representation into a good one.
func CanonicalBytes(f Fields) ([]byte, error) {
	if err := ValidateFields(f); err != nil {
		return nil, err
	}
	sizeText := strconv.FormatInt(f.Size, 10)
	lines := []string{
		manifestHeader,
		"product=" + f.Product,
		"target=" + f.Target,
		"version=" + f.Version,
		"size=" + sizeText,
		"sha256=" + f.SHA256,
		"keyId=" + f.KeyID,
	}
	return []byte(strings.Join(lines, "\n")), nil
}

// VerifyFields validates fields, rebuilds canonical bytes, and verifies the
// detached signature with crypto/ed25519.Verify (RFC 8032, pure, no prehash).
func VerifyFields(f Fields, sigB64 string, pubkeys map[string]ed25519.PublicKey) error {
	canonical, err := CanonicalBytes(f)
	if err != nil {
		return err
	}
	return VerifyCanonical(canonical, sigB64, f.KeyID, pubkeys)
}

// VerifyCanonical checks raw manifest bytes and a detached signature. The bytes
// must already be the exact signed form; this function does not parse or reorder.
func VerifyCanonical(canonical []byte, sigB64, keyID string, pubkeys map[string]ed25519.PublicKey) error {
	f, err := manifestFromWire(canonical)
	if err != nil {
		return err
	}
	if f.KeyID != keyID {
		return fmt.Errorf("%w: keyId mismatch", ErrValidation)
	}
	pub, ok := pubkeys[f.KeyID]
	if !ok {
		return fmt.Errorf("%w: unknown keyId", ErrValidation)
	}
	sig, err := decodeSigB64(sigB64)
	if err != nil {
		return err
	}
	if len(pub) != ed25519PubLen {
		return fmt.Errorf("%w: public key length", ErrValidation)
	}
	if !ed25519.Verify(pub, canonical, sig) {
		return errors.New("manifest signature invalid")
	}
	return nil
}

// DecodePublicKeyB64 decodes a standard padded base64 Ed25519 public key (32 bytes).
func DecodePublicKeyB64(b64 string) (ed25519.PublicKey, error) {
	raw, err := strictB64.DecodeString(b64)
	if err != nil {
		return nil, fmt.Errorf("%w: public key base64", ErrValidation)
	}
	if len(raw) != ed25519PubLen {
		return nil, fmt.Errorf("%w: public key length", ErrValidation)
	}
	return ed25519.PublicKey(raw), nil
}

func decodeSigB64(b64 string) ([]byte, error) {
	if len(b64) != ed25519SigB64Len {
		return nil, fmt.Errorf("%w: signature base64 length", ErrValidation)
	}
	raw, err := strictB64.DecodeString(b64)
	if err != nil {
		return nil, fmt.Errorf("%w: signature base64", ErrValidation)
	}
	if len(raw) != ed25519SigLen {
		return nil, fmt.Errorf("%w: signature length", ErrValidation)
	}
	return raw, nil
}

func manifestFromWire(canonical []byte) (Fields, error) {
	if len(canonical) == 0 {
		return Fields{}, fmt.Errorf("%w: empty manifest", ErrValidation)
	}
	if canonical[len(canonical)-1] == '\n' || canonical[len(canonical)-1] == '\r' {
		return Fields{}, fmt.Errorf("%w: trailing newline", ErrValidation)
	}
	if strings.Contains(string(canonical), "\r") {
		return Fields{}, fmt.Errorf("%w: crlf", ErrValidation)
	}
	text := string(canonical)
	if !strings.HasPrefix(text, manifestHeader+"\n") {
		return Fields{}, fmt.Errorf("%w: header", ErrValidation)
	}
	lines := strings.Split(text, "\n")
	if len(lines) != 7 {
		return Fields{}, fmt.Errorf("%w: line count", ErrValidation)
	}
	if lines[0] != manifestHeader {
		return Fields{}, fmt.Errorf("%w: header line", ErrValidation)
	}
	expectedKeys := []string{"product=", "target=", "version=", "size=", "sha256=", "keyId="}
	for i, prefix := range expectedKeys {
		if !strings.HasPrefix(lines[i+1], prefix) {
			return Fields{}, fmt.Errorf("%w: field order", ErrValidation)
		}
		if strings.Contains(lines[i+1], " ") {
			return Fields{}, fmt.Errorf("%w: spaces", ErrValidation)
		}
	}
	f, err := fieldsFromCanonicalLines(lines)
	if err != nil {
		return Fields{}, err
	}
	if err := ValidateFields(f); err != nil {
		return Fields{}, err
	}
	return f, nil
}

func fieldsFromCanonicalLines(lines []string) (Fields, error) {
	parse := func(line, key string) (string, error) {
		prefix := key + "="
		if !strings.HasPrefix(line, prefix) {
			return "", fmt.Errorf("%w: parse", ErrValidation)
		}
		return line[len(prefix):], nil
	}
	product, err := parse(lines[1], "product")
	if err != nil {
		return Fields{}, err
	}
	target, err := parse(lines[2], "target")
	if err != nil {
		return Fields{}, err
	}
	version, err := parse(lines[3], "version")
	if err != nil {
		return Fields{}, err
	}
	sizeText, err := parse(lines[4], "size")
	if err != nil {
		return Fields{}, err
	}
	if !sizeWirePattern.MatchString(sizeText) {
		return Fields{}, fmt.Errorf("%w: size", ErrValidation)
	}
	size, err := strconv.ParseInt(sizeText, 10, 64)
	if err != nil {
		return Fields{}, fmt.Errorf("%w: size", ErrValidation)
	}
	sha, err := parse(lines[5], "sha256")
	if err != nil {
		return Fields{}, err
	}
	keyID, err := parse(lines[6], "keyId")
	if err != nil {
		return Fields{}, err
	}
	return Fields{
		Product: product,
		Target:  target,
		Version: version,
		Size:    size,
		SHA256:  sha,
		KeyID:   keyID,
	}, nil
}

// validateSemVer2 checks SemVer 2.0.0 without a leading v (design §4).
func validateSemVer2(v string) bool {
	if v == "" || strings.HasPrefix(v, "v") {
		return false
	}
	build := ""
	if i := strings.IndexByte(v, '+'); i >= 0 {
		build = v[i+1:]
		v = v[:i]
		if build == "" {
			return false
		}
	}
	pre := ""
	if i := strings.IndexByte(v, '-'); i >= 0 {
		pre = v[i+1:]
		v = v[:i]
		if pre == "" {
			return false
		}
	}
	core := strings.Split(v, ".")
	if len(core) != 3 {
		return false
	}
	for _, part := range core {
		if !semverNumericID(part) {
			return false
		}
	}
	if pre != "" && !semverPrereleaseIdentifierList(pre) {
		return false
	}
	if build != "" && !semverBuildIdentifierList(build) {
		return false
	}
	return true
}

func semverNumericID(s string) bool {
	if s == "" {
		return false
	}
	if len(s) > 1 && s[0] == '0' {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func semverPrereleaseIdentifierList(s string) bool {
	parts := strings.Split(s, ".")
	for _, part := range parts {
		if part == "" {
			return false
		}
		if isAllDigits(part) {
			if !semverNumericID(part) {
				return false
			}
		} else if !semverAlphanumericID(part) {
			return false
		}
	}
	return true
}

// Build metadata allows leading zeros on numeric ids (SemVer 2.0.0 §10).
func semverBuildIdentifierList(s string) bool {
	parts := strings.Split(s, ".")
	for _, part := range parts {
		if part == "" || !semverAlphanumericID(part) {
			return false
		}
	}
	return true
}

func semverAlphanumericID(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'A' || c > 'Z') && (c < 'a' || c > 'z') && c != '-' {
			return false
		}
	}
	return true
}

func isAllDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func isLowerHex(s string) bool {
	if len(s) != sha256HexLen {
		return false
	}
	_, err := hex.DecodeString(s)
	if err != nil {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
