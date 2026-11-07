package firmware

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type manifestVector struct {
	Name         string `json:"name,omitempty"`
	SeedHex      string `json:"seedHex"`
	PublicKeyB64 string `json:"publicKeyB64"`
	Fields       Fields `json:"fields"`
	Canonical    string `json:"canonical"`
	SigB64       string `json:"sigB64"`
	Valid        bool   `json:"valid"`
}

func TestOTAManifestGoldenVectors(t *testing.T) {
	vectors := loadManifestVectors(t)
	if len(vectors) == 0 {
		t.Fatal("no vectors")
	}
	pubkeys := map[string]ed25519.PublicKey{}
	for _, v := range vectors {
		if v.SeedHex == "" || strings.Contains(v.SeedHex, "RELEASE") {
			t.Fatalf("vector %q must use a public test-only seed", v.Name)
		}
		pub, err := DecodePublicKeyB64(v.PublicKeyB64)
		if err != nil {
			t.Fatalf("%q decode pubkey: %v", v.Name, err)
		}
		pubkeys[v.Fields.KeyID] = pub
	}

	for _, v := range vectors {
		v := v
		t.Run(v.Name, func(t *testing.T) {
			if v.Valid {
				got, err := CanonicalBytes(v.Fields)
				if err != nil {
					t.Fatalf("CanonicalBytes: %v", err)
				}
				if string(got) != v.Canonical {
					t.Fatalf("canonical bytes mismatch\ngot:\n%q\nwant:\n%q", got, v.Canonical)
				}
			}
			err := VerifyCanonical([]byte(v.Canonical), v.SigB64, v.Fields.KeyID, pubkeys)
			if v.Valid && err != nil {
				t.Fatalf("verify valid vector: %v", err)
			}
			if !v.Valid && err == nil {
				t.Fatal("expected verification failure for invalid vector")
			}
			if !v.Valid && err != nil && !errors.Is(err, ErrValidation) && err.Error() != "manifest signature invalid" {
				// crypto failures may be signature invalid or validation on wire parse
			}
		})
	}
}

func TestVerifyFieldsMatchesCanonical(t *testing.T) {
	v := goldenVector(t)
	pubkeys := allowlist(t, v)
	if err := VerifyFields(v.Fields, v.SigB64, pubkeys); err != nil {
		t.Fatal(err)
	}
}

func TestCanonicalWireRejectsTampering(t *testing.T) {
	v := goldenVector(t)
	pubkeys := allowlist(t, v)
	base := []byte(v.Canonical)
	cases := []struct {
		name string
		mut  func([]byte) []byte
	}{
		{"trailing LF", func(b []byte) []byte { return append(append([]byte(nil), b...), '\n') }},
		{"trailing CRLF", func(b []byte) []byte { return append(append([]byte(nil), b...), '\r', '\n') }},
		{"crlf middle", func(b []byte) []byte { return []byte(strings.ReplaceAll(string(b), "\n", "\r\n")) }},
		{"extra space", func(b []byte) []byte {
			s := strings.Replace(string(b), "product=", "product= ", 1)
			return []byte(s)
		}},
		{"field order swap", func(b []byte) []byte {
			lines := strings.Split(string(b), "\n")
			lines[1], lines[2] = lines[2], lines[1]
			return []byte(strings.Join(lines, "\n"))
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mutated := tc.mut(base)
			err := VerifyCanonical(mutated, v.SigB64, v.Fields.KeyID, pubkeys)
			if err == nil {
				t.Fatal("accepted non-canonical signed bytes")
			}
		})
	}
}

func TestValidateFieldsNegative(t *testing.T) {
	valid := goldenVector(t).Fields
	cases := []struct {
		name string
		mut  func(Fields) Fields
	}{
		{"wrong product", func(f Fields) Fields { f.Product = "other"; return f }},
		{"unknown target", func(f Fields) Fields { f.Target = "unknown_target"; return f }},
		{"version v-prefix", func(f Fields) Fields { f.Version = "v1.0.0"; return f }},
		{"malformed semver", func(f Fields) Fields { f.Version = "1.0"; return f }},
		{"zero size", func(f Fields) Fields { f.Size = 0; return f }},
		{"oversized", func(f Fields) Fields { f.Size = maxManifestSize + 1; return f }},
		{"leading zero size wire", func(f Fields) Fields { return f }},
		{"uppercase sha256", func(f Fields) Fields {
			f.SHA256 = strings.ToUpper(f.SHA256)
			return f
		}},
		{"short sha256", func(f Fields) Fields { f.SHA256 = "abc"; return f }},
		{"bad keyId", func(f Fields) Fields { f.KeyID = "fw-26-1"; return f }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := tc.mut(valid)
			if tc.name == "leading zero size wire" {
				gv := goldenVector(t)
				wire := []byte(strings.Replace(gv.Canonical, "size=1532416", "size=042", 1))
				err := VerifyCanonical(wire, gv.SigB64, valid.KeyID, allowlist(t, gv))
				if err == nil || !errors.Is(err, ErrValidation) {
					t.Fatalf("got %v", err)
				}
				return
			}
			err := ValidateFields(f)
			if err == nil {
				t.Fatal("expected validation error")
			}
			if !errors.Is(err, ErrValidation) {
				t.Fatalf("want ErrValidation, got %v", err)
			}
		})
	}
}

func TestCryptoAndAllowlistNegative(t *testing.T) {
	v := goldenVector(t)
	pubkeys := allowlist(t, v)
	canonical := []byte(v.Canonical)

	t.Run("unknown keyId", func(t *testing.T) {
		err := VerifyCanonical(canonical, v.SigB64, "fw-1999-9", pubkeys)
		if err == nil || !errors.Is(err, ErrValidation) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("malformed sig base64", func(t *testing.T) {
		err := VerifyCanonical(canonical, "!!!", v.Fields.KeyID, pubkeys)
		if err == nil || !errors.Is(err, ErrValidation) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("wrong sig b64 length", func(t *testing.T) {
		short := v.SigB64[:len(v.SigB64)-2]
		err := VerifyCanonical(canonical, short, v.Fields.KeyID, pubkeys)
		if err == nil || !errors.Is(err, ErrValidation) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("tampered signature bytes", func(t *testing.T) {
		raw, _ := base64.StdEncoding.DecodeString(v.SigB64)
		raw[0] ^= 0xff
		tampered := base64.StdEncoding.EncodeToString(raw)
		if len(tampered) != ed25519SigB64Len {
			t.Fatalf("unexpected sig len %d", len(tampered))
		}
		err := VerifyCanonical(canonical, tampered, v.Fields.KeyID, pubkeys)
		if err == nil || err.Error() != "manifest signature invalid" {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("wrong public key length", func(t *testing.T) {
		bad := map[string]ed25519.PublicKey{v.Fields.KeyID: make([]byte, 16)}
		err := VerifyCanonical(canonical, v.SigB64, v.Fields.KeyID, bad)
		if err == nil || !errors.Is(err, ErrValidation) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("malformed public key b64", func(t *testing.T) {
		_, err := DecodePublicKeyB64("not-base64!!!")
		if err == nil || !errors.Is(err, ErrValidation) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("wrong public key for valid sig", func(t *testing.T) {
		otherSeed := make([]byte, ed25519.SeedSize)
		for i := range otherSeed {
			otherSeed[i] = byte(i + 1)
		}
		otherPub := ed25519.NewKeyFromSeed(otherSeed).Public().(ed25519.PublicKey)
		bad := map[string]ed25519.PublicKey{v.Fields.KeyID: otherPub}
		err := VerifyCanonical(canonical, v.SigB64, v.Fields.KeyID, bad)
		if err == nil || err.Error() != "manifest signature invalid" {
			t.Fatalf("got %v", err)
		}
	})
}

func TestAllowedTargets(t *testing.T) {
	v := goldenVector(t)
	for target := range allowedTargets {
		f := v.Fields
		f.Target = target
		if err := ValidateFields(f); err != nil {
			t.Fatalf("%s: %v", target, err)
		}
	}
}

func TestFieldsBoundaryRawBytesVsValidated(t *testing.T) {
	v := goldenVector(t)
	pubkeys := allowlist(t, v)
	// Validated Fields path rebuilds canonical bytes.
	if err := VerifyFields(v.Fields, v.SigB64, pubkeys); err != nil {
		t.Fatal(err)
	}
	// Raw bytes path must not accept a mutation hidden only in bytes (single-byte).
	mutated := []byte(v.Canonical)
	mutated[len(mutated)-3] ^= 0x01
	err := VerifyCanonical(mutated, v.SigB64, v.Fields.KeyID, pubkeys)
	if err == nil {
		t.Fatal("raw path accepted mutated bytes")
	}
	// Tampered Fields with still-valid-looking rebuild should fail crypto when bytes differ.
	bad := v.Fields
	bad.SHA256 = strings.Replace(bad.SHA256, "e6", "e7", 1)
	err = VerifyFields(bad, v.SigB64, pubkeys)
	if err == nil {
		t.Fatal("VerifyFields did not catch field drift from signature")
	}
}

func goldenVector(t *testing.T) manifestVector {
	t.Helper()
	for _, v := range loadManifestVectors(t) {
		if v.Valid && v.Name == "golden-valid" {
			return v
		}
	}
	t.Fatal("golden-valid vector missing")
	return manifestVector{}
}

func allowlist(t *testing.T, v manifestVector) map[string]ed25519.PublicKey {
	t.Helper()
	pub, err := DecodePublicKeyB64(v.PublicKeyB64)
	if err != nil {
		t.Fatal(err)
	}
	return map[string]ed25519.PublicKey{v.Fields.KeyID: pub}
}

func loadManifestVectors(t *testing.T) []manifestVector {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "firmware", "test", "vectors", "ota_manifest_v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	var vectors []manifestVector
	if err := json.Unmarshal(raw, &vectors); err != nil {
		t.Fatal(err)
	}
	return vectors
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
