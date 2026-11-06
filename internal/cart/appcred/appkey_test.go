package appcred

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestAppKeyFirst16HexOfSHA256(t *testing.T) {
	clientID := "my-kroger-client"
	sum := sha256.Sum256([]byte(clientID))
	want := hex.EncodeToString(sum[:])[:16]
	if got := AppKey(clientID); got != want || len(got) != 16 {
		t.Fatalf("AppKey() = %q want %q", got, want)
	}
}
