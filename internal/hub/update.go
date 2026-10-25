package hub

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// WatchdogLimit is how long a staged binary has to pass its self-check and
// a heartbeat before the hub switches back to the previous release.
const WatchdogLimit = 120 * time.Second

// ParsePublicKey accepts a 32-byte Ed25519 public key as standard base64,
// raw base64, or hex. The key is built into the image as HUB_RELEASE_PUBKEY.
func ParsePublicKey(raw string) (ed25519.PublicKey, error) {
	text := strings.TrimSpace(raw)
	if text == "" {
		return nil, errors.New("a release key is required")
	}
	candidates := [][]byte{}
	if b, err := base64.StdEncoding.DecodeString(text); err == nil {
		candidates = append(candidates, b)
	}
	if b, err := base64.RawStdEncoding.DecodeString(text); err == nil {
		candidates = append(candidates, b)
	}
	if b, err := hex.DecodeString(text); err == nil {
		candidates = append(candidates, b)
	}
	for _, b := range candidates {
		if len(b) == ed25519.PublicKeySize {
			return ed25519.PublicKey(b), nil
		}
	}
	return nil, errors.New("that release key is not an Ed25519 public key")
}

// VerifyRelease checks the advertised SHA-256 and an Ed25519 signature over
// the canonical manifest (version, newline, lowercase sha256 hex). Signing
// the digest together with the version stops a valid older artifact from
// being replayed under a different version string. A tampered file, a
// mismatched version, or a signature from another key fails.
func VerifyRelease(pub ed25519.PublicKey, version string, artifact []byte, shaHex, sigText string) error {
	if len(pub) != ed25519.PublicKeySize {
		return errors.New("that release key is not an Ed25519 public key")
	}
	if strings.TrimSpace(version) == "" {
		return errors.New("that update has no version")
	}
	if len(artifact) == 0 {
		return errors.New("that update is empty")
	}
	sum := sha256.Sum256(artifact)
	got := hex.EncodeToString(sum[:])
	if !strings.EqualFold(strings.TrimSpace(shaHex), got) {
		return errors.New("that update does not match its digest")
	}
	sig, err := decodeSig(sigText)
	if err != nil {
		return err
	}
	if !ed25519.Verify(pub, releaseManifest(version, got), sig) {
		return errors.New("that update signature is not valid")
	}
	return nil
}

func releaseManifest(version, shaHex string) []byte {
	return []byte(strings.TrimSpace(version) + "\n" + strings.ToLower(strings.TrimSpace(shaHex)))
}

// newerRelease reports whether next is strictly newer than current.
// Dotted numeric versions compare numerically, so 1.10 is newer than 1.9.
// Any other distinct version string is accepted; an equal version is not.
func newerRelease(current, next string) bool {
	next = strings.TrimSpace(next)
	current = strings.TrimSpace(current)
	if next == "" || next == current {
		return false
	}
	left, leftOK := dottedVersion(current)
	right, rightOK := dottedVersion(next)
	if !leftOK || !rightOK {
		return true
	}
	n := len(left)
	if len(right) > n {
		n = len(right)
	}
	for i := 0; i < n; i++ {
		var a, b int
		if i < len(left) {
			a = left[i]
		}
		if i < len(right) {
			b = right[i]
		}
		if b > a {
			return true
		}
		if b < a {
			return false
		}
	}
	return false
}

func dottedVersion(value string) ([]int, bool) {
	if value == "" {
		return nil, false
	}
	parts := strings.Split(value, ".")
	nums := make([]int, len(parts))
	for i, part := range parts {
		if part == "" {
			return nil, false
		}
		n := 0
		for _, c := range part {
			if c < '0' || c > '9' {
				return nil, false
			}
			n = n*10 + int(c-'0')
		}
		nums[i] = n
	}
	return nums, true
}

func decodeSig(text string) ([]byte, error) {
	text = strings.TrimSpace(text)
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if sig, err := enc.DecodeString(text); err == nil && len(sig) == ed25519.SignatureSize {
			return sig, nil
		}
	}
	return nil, errors.New("that update signature is not valid")
}

func signRelease(priv ed25519.PrivateKey, version string, artifact []byte) (shaHex, sig string) {
	sum := sha256.Sum256(artifact)
	hexSum := hex.EncodeToString(sum[:])
	return hexSum, base64.StdEncoding.EncodeToString(ed25519.Sign(priv, releaseManifest(version, hexSum)))
}

// stageRelease writes the artifact under releases/<version> and points the
// current symlink at it. previous keeps the prior target. The caller records
// the watchdog deadline.
func stageRelease(root, version string, artifact []byte) (binary, previous string, err error) {
	if version == "" || strings.Contains(version, "/") || strings.Contains(version, "..") {
		return "", "", errors.New("that update version cannot be stored")
	}
	releaseDir := filepath.Join(root, "releases", version)
	if err := os.MkdirAll(releaseDir, 0o755); err != nil {
		return "", "", err
	}
	binary = filepath.Join(releaseDir, "stockbot-hub")
	tmp := binary + ".tmp"
	if err := os.WriteFile(tmp, artifact, 0o755); err != nil {
		return "", "", err
	}
	file, err := os.Open(tmp)
	if err != nil {
		return "", "", err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return "", "", err
	}
	if err := file.Close(); err != nil {
		return "", "", err
	}
	if err := os.Rename(tmp, binary); err != nil {
		return "", "", err
	}
	current := filepath.Join(root, "current")
	if target, err := os.Readlink(current); err == nil {
		previous = target
		if err := swapLink(filepath.Join(root, "previous"), target); err != nil {
			return "", "", err
		}
	}
	if err := swapLink(current, releaseDir); err != nil {
		return "", "", err
	}
	return binary, previous, nil
}

func swapLink(link, target string) error {
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		return err
	}
	tmp := link + ".tmp"
	_ = os.Remove(tmp)
	if err := os.Symlink(target, tmp); err != nil {
		return err
	}
	return os.Rename(tmp, link)
}

// restoreLink points current back at previous.
func restoreLink(root, previous string) error {
	if previous == "" {
		return errors.New("there is no previous release to restore")
	}
	return swapLink(filepath.Join(root, "current"), previous)
}

func pruneReleases(root string) {
	current, _ := os.Readlink(filepath.Join(root, "current"))
	previous, _ := os.Readlink(filepath.Join(root, "previous"))
	dir := filepath.Join(root, "releases")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		path := filepath.Join(dir, entry.Name())
		if path == current || path == previous {
			continue
		}
		_ = os.RemoveAll(path)
	}
}

// defaultSelfCheck runs the staged binary. A non-zero exit fails the update.
func defaultSelfCheck(ctx context.Context, binary string) error {
	if binary == "" {
		return errors.New("the staged hub has no binary")
	}
	cmd := exec.CommandContext(ctx, binary, "--self-check")
	if err := cmd.Run(); err != nil {
		return errors.New("the staged hub failed its self-check")
	}
	return nil
}
