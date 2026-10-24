package auth

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNormalizePairingCodeFixture runs testdata/pairing-codes.json through
// the production pairing normalizer, including U+0085 and U+00A0.
func TestNormalizePairingCodeFixture(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(authRepoRoot(t), "testdata", "pairing-codes.json"))
	if err != nil {
		t.Fatal(err)
	}
	var vectors []struct {
		Input      string  `json:"input"`
		Normalized *string `json:"normalized"`
		LooksLike  bool    `json:"looksLike"`
		Pairing    bool    `json:"pairing"`
	}
	if err := json.Unmarshal(raw, &vectors); err != nil {
		t.Fatal(err)
	}
	saw0085, sawNBSP := false, false
	for _, vector := range vectors {
		code, well, checkOK := NormalizePairingCode(vector.Input)
		if well != vector.LooksLike || checkOK != vector.Pairing {
			t.Fatalf("%q well=%v check=%v, want looks %v pairing %v", vector.Input, well, checkOK, vector.LooksLike, vector.Pairing)
		}
		if vector.LooksLike && vector.Normalized != nil && code != *vector.Normalized {
			t.Fatalf("%q normalized %q, want %q", vector.Input, code, *vector.Normalized)
		}
		if !vector.LooksLike && (well || checkOK) {
			t.Fatalf("%q was accepted", vector.Input)
		}
		if strings.ContainsRune(vector.Input, '\u0085') {
			saw0085 = true
			if well || checkOK || code != "" {
				t.Fatalf("U+0085 %q was trimmed into a pairing code %q", vector.Input, code)
			}
		}
		if strings.ContainsRune(vector.Input, '\u00a0') {
			sawNBSP = true
			if well || checkOK || code != "" || trimASCII(vector.Input) != vector.Input {
				t.Fatalf("U+00A0 %q was trimmed", vector.Input)
			}
		}
	}
	if !saw0085 || !sawNBSP {
		t.Fatal("fixture is missing U+0085 or U+00A0")
	}
}

func authRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}
