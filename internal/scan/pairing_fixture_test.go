package scan

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type pairingVector struct {
	Input      string  `json:"input"`
	Normalized *string `json:"normalized"`
	LooksLike  bool    `json:"looksLike"`
	Pairing    bool    `json:"pairing"`
	GTIN       bool    `json:"gtin"`
}

// TestPairingCodeFixture drives testdata/pairing-codes.json through the
// production classifier. Trim is ASCII-only, so U+0085 and U+00A0 stay and
// are not product barcodes.
func TestPairingCodeFixture(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "testdata", "pairing-codes.json"))
	if err != nil {
		t.Fatal(err)
	}
	var vectors []pairingVector
	if err := json.Unmarshal(raw, &vectors); err != nil {
		t.Fatal(err)
	}
	if len(vectors) == 0 {
		t.Fatal("pairing fixture is empty")
	}
	saw0085 := false
	sawNBSP := false
	for _, vector := range vectors {
		got, ok := NormalizeScan(vector.Input)
		switch {
		case vector.Normalized == nil && ok:
			t.Fatalf("%q normalized to %q, want reject", vector.Input, got)
		case vector.Normalized != nil && (!ok || got != *vector.Normalized):
			t.Fatalf("%q normalized to %q ok=%v, want %q", vector.Input, got, ok, *vector.Normalized)
		}
		if LooksLikePairingCode(vector.Input) != vector.LooksLike {
			t.Fatalf("%q looksLike %v, want %v", vector.Input, LooksLikePairingCode(vector.Input), vector.LooksLike)
		}
		if IsPairingCode(vector.Input) != vector.Pairing {
			t.Fatalf("%q pairing %v, want %v", vector.Input, IsPairingCode(vector.Input), vector.Pairing)
		}
		if IsProductBarcode(vector.Input) != vector.GTIN {
			t.Fatalf("%q gtin %v, want %v", vector.Input, IsProductBarcode(vector.Input), vector.GTIN)
		}
		if vector.GTIN && IsQRPayload(vector.Input) {
			t.Fatalf("%q is a product and a QR payload", vector.Input)
		}
		trimmed := TrimASCII(vector.Input)
		if !vector.GTIN && trimmed != "" && !IsQRPayload(vector.Input) {
			t.Fatalf("%q was accepted as a product", vector.Input)
		}
		if vector.GTIN == false && trimmed == "" && IsQRPayload(vector.Input) {
			t.Fatalf("%q empty trim was a QR payload", vector.Input)
		}
		if (IsPairingCode(vector.Input) || LooksLikePairingCode(vector.Input)) && IsProductBarcode(vector.Input) {
			t.Fatalf("%q is both a pairing code and a product barcode", vector.Input)
		}
		if strings.ContainsRune(vector.Input, '\u0085') {
			saw0085 = true
			if IsProductBarcode(vector.Input) || IsQRPayload(vector.Input) == vector.GTIN && vector.GTIN {
				t.Fatalf("U+0085 %q classified as a product", vector.Input)
			}
		}
		if strings.ContainsRune(vector.Input, '\u00a0') {
			sawNBSP = true
			if ok || vector.Normalized != nil || vector.LooksLike || vector.Pairing || vector.GTIN || IsProductBarcode(vector.Input) {
				t.Fatalf("U+00A0 %q was trimmed or accepted", vector.Input)
			}
			if TrimASCII(vector.Input) != vector.Input {
				t.Fatalf("U+00A0 %q was trimmed", vector.Input)
			}
		}
	}
	if !saw0085 {
		t.Fatal("pairing fixture has no U+0085 row")
	}
	if !sawNBSP {
		t.Fatal("pairing fixture has no U+00A0 row")
	}
}

func repoRoot(t *testing.T) string {
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
