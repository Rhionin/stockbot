package barcode

import (
	"strings"
	"testing"
)

func TestASCIISpaceCharsMatchesTrimASCII(t *testing.T) {
	for _, r := range ASCIISpaceChars {
		s := string(r) + "x" + string(r)
		if TrimASCII(s) != "x" {
			t.Fatalf("TrimASCII did not strip %q", r)
		}
	}
	for _, r := range []rune{'\u00a0', '\u0085', '\ufeff'} {
		s := string(r) + "x" + string(r)
		if TrimASCII(s) != s {
			t.Fatalf("TrimASCII stripped non-ASCII space %U", r)
		}
	}
}

func TestSQLTrimExprUsesSameCharsetAsTrimASCII(t *testing.T) {
	expr := SQLTrimExpr("col")
	for _, r := range ASCIISpaceChars {
		if !strings.Contains(expr, string(r)) {
			t.Fatalf("SQLTrimExpr %q missing trim rune %q", expr, r)
		}
	}
	if strings.Count(expr, "'") < 2 {
		t.Fatalf("SQLTrimExpr %q is not a quoted trim charset", expr)
	}
}
