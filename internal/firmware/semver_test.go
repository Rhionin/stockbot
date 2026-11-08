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
