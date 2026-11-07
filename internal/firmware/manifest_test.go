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
			if !v.Valid {
				wantValidation := mutationExpectsValidation(v.Name)
				if wantValidation {
					if !errors.Is(err, ErrValidation) {
						t.Fatalf("want ErrValidation, got %v", err)
					}
				} else if err.Error() != "manifest signature invalid" {
					t.Fatalf("want signature crypto failure, got %v", err)
				}
			}
		})
	}
}

func mutationExpectsValidation(name string) bool {
	switch name {
	case "mutate-product-byte", "mutate-target-byte", "mutate-keyId-byte":
		return true
	case "mutate-version-byte", "mutate-sha256-byte", "mutate-size-byte":
		return false
	default:
		return true
	}
}

// TestArchitectWireNOGO locks the four PR #565 wire defects (Architect report
// 02e6905da495c7a56d3bb1674fbd74ea7a440521d00d72391a7f76fa456118ed).
func TestArchitectWireNOGO(t *testing.T) {
	t.Run("semver rejects non-SemVer2 forms", func(t *testing.T) {
		valid := goldenVector(t).Fields
		for _, ver := range []string{"01.0.0", "1.0.0-01", "1.0.0-rc..1"} {
			f := valid
			f.Version = ver
			err := ValidateFields(f)
			if err == nil || !errors.Is(err, ErrValidation) {
				t.Fatalf("version %q: got %v", ver, err)
			}
		}
	})
	t.Run("size wire rejects plus prefix", func(t *testing.T) {
		v := goldenVector(t)
		wire := []byte(strings.Replace(v.Canonical, "size=1532416", "size=+1532416", 1))
		err := VerifyCanonical(wire, v.SigB64, v.Fields.KeyID, allowlist(t, v))
		if err == nil || !errors.Is(err, ErrValidation) {
			t.Fatalf("want validation on non-canonical size text, got %v", err)
		}
	})
	t.Run("strict signature base64 padding", func(t *testing.T) {
		v := goldenVector(t)
		bad := []byte(v.SigB64)
		// Flip unused padding bits on the trailing '=' (RFC 4648 canonical padding).
		if bad[len(bad)-1] != '=' {
			t.Fatalf("expected padded signature ending in =, got %q", v.SigB64[len(v.SigB64)-3:])
		}
		bad[len(bad)-1] = '`' // 0x60 in base64 alphabet; invalid canonical padding symbol
		err := VerifyCanonical([]byte(v.Canonical), string(bad), v.Fields.KeyID, allowlist(t, v))
		if err == nil || !errors.Is(err, ErrValidation) {
			t.Fatalf("want strict base64 rejection, got %v", err)
		}
	})
	t.Run("caller keyId must match signed keyId", func(t *testing.T) {
		v := goldenVector(t)
		wire := []byte(strings.Replace(v.Canonical, "keyId=fw-2026-1", "keyId=fw-2026-2", 1))
		seed := make([]byte, ed25519.SeedSize)
		for i := range seed {
			seed[i] = 0x3b
		}
		priv := ed25519.NewKeyFromSeed(seed)
		sig := ed25519.Sign(priv, wire)
		sigB64 := base64.StdEncoding.EncodeToString(sig)
		allowlist := map[string]ed25519.PublicKey{
			"fw-2026-1": priv.Public().(ed25519.PublicKey),
			"fw-2026-2": priv.Public().(ed25519.PublicKey),
		}
		err := VerifyCanonical(wire, sigB64, "fw-2026-1", allowlist)
		if err == nil || !errors.Is(err, ErrValidation) {
			t.Fatalf("want keyId mismatch rejection, got %v", err)
		}
	})
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
		{"semver leading zero core", func(f Fields) Fields { f.Version = "01.0.0"; return f }},
		{"semver leading zero prerelease", func(f Fields) Fields { f.Version = "1.0.0-01"; return f }},
		{"semver empty prerelease id", func(f Fields) Fields { f.Version = "1.0.0-rc..1"; return f }},
		{"zero size", func(f Fields) Fields { f.Size = 0; return f }},
		{"oversized", func(f Fields) Fields { f.Size = maxManifestSize + 1; return f }},
		{"leading zero size wire", func(f Fields) Fields { return f }},
		{"plus size wire", func(f Fields) Fields { return f }},
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
			if tc.name == "plus size wire" {
				gv := goldenVector(t)
				wire := []byte(strings.Replace(gv.Canonical, "size=1532416", "size=+1532416", 1))
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

func TestSemVer2AcceptsDesignExamples(t *testing.T) {
	valid := goldenVector(t).Fields
	cases := []string{"1.0.2-rc.1", "1.0.0", "10.20.30+build.1"}
	for _, ver := range cases {
		f := valid
		f.Version = ver
		if err := ValidateFields(f); err != nil {
			t.Fatalf("version %q: %v", ver, err)
		}
	}
}

func TestManifestFromWireParseErrors(t *testing.T) {
	gv := goldenVector(t)
	lines := strings.Split(gv.Canonical, "\n")
	mutateLine := func(idx int, line string) []byte {
		out := append([]string(nil), lines...)
		out[idx] = line
		return []byte(strings.Join(out, "\n"))
	}
	cases := []struct {
		name string
		b    []byte
	}{
		{"non-numeric size", mutateLine(4, "size=abc")},
		{"missing product prefix", mutateLine(1, "prod=esp32_scanner")},
		{"version plus only build", mutateLine(3, "version=1.0.0+")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := manifestFromWire(tc.b)
			if err == nil || !errors.Is(err, ErrValidation) {
				t.Fatalf("got %v", err)
			}
		})
	}
}

func TestDecodePublicKeyStrictPadding(t *testing.T) {
	v := goldenVector(t)
	bad := v.PublicKeyB64[:len(v.PublicKeyB64)-1] + "`"
	_, err := DecodePublicKeyB64(bad)
	if err == nil || !errors.Is(err, ErrValidation) {
		t.Fatalf("got %v", err)
	}
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
