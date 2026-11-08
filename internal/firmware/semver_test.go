package firmware

import "testing"

func TestNumericPrereleaseOrderingNoOverflow(t *testing.T) {
	if compareSemVerPrecedence("1.0.0-rc.1000000000000", "1.0.0-rc.999999999999") <= 0 {
		t.Fatal("longer numeric id should sort higher")
	}
	if compareSemVerPrecedence("1.0.0-rc.2", "1.0.0-rc.10") >= 0 {
		t.Fatal("2 < 10 numerically")
	}
}

// Architect NO-GO efd883eb: int64 core overflow must not fall back to lexical order.
func TestHugeCoreNumericOrdering(t *testing.T) {
	high := "10000000000000000000.0.0"
	low := "9999999999999999999.0.0"
	if compareSemVerPrecedence(high, low) <= 0 {
		t.Fatalf("%q must outrank %q", high, low)
	}
	if compareSemVerPrecedence(low, high) >= 0 {
		t.Fatalf("%q must be below %q", low, high)
	}
	t.Run("minor patch huge", func(t *testing.T) {
		if compareSemVerPrecedence("1.10000000000000000000.0", "1.9999999999999999999.0") <= 0 {
			t.Fatal("huge minor ordering")
		}
		if compareSemVerPrecedence("1.0.10000000000000000000", "1.0.9999999999999999999") <= 0 {
			t.Fatal("huge patch ordering")
		}
	})
}

func TestHugeCoreBuildMetadataEqualPrecedence(t *testing.T) {
	core := "10000000000000000000.0.0"
	a := core + "+build.a"
	b := core + "+build.b"
	if !semverEqualPrecedence(a, b) {
		t.Fatalf("%q and %q must be equal precedence", a, b)
	}
}
