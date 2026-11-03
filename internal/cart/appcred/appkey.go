package appcred

import (
	"crypto/sha256"
	"encoding/hex"
)

// AppKey returns the first 16 hex characters of SHA-256(clientID), used to
// bind connections and rate-limit windows to one Kroger developer app.
func AppKey(clientID string) string {
	sum := sha256.Sum256([]byte(clientID))
	return hex.EncodeToString(sum[:])[:16]
}
