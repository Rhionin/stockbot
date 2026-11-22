package barcode

import "testing"

// Pins the rules that were untested at 70062141: trim applies to the local-only
// branch, lowercase letters count, and classification is ASCII-only (no
// Arabic-Indic / fullwidth / Latin-1 letters or digits reach a lookup).
func TestClassifyTrimCaseAndASCIIOnly(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		raw  string
		want LookupKind
	}{
		// trim reaches the local-only branch (kills: isLocalOnlyToken uses raw)
		{"padded FNSKU CRLF", "  X002MB5N7L \r\n", LookupLocalOnly},
		{"tab padded AB12", "\tAB12\n", LookupLocalOnly},
		{"trailing space only", "X002MB5N7L ", LookupLocalOnly},
		{"padded 14 digits", " 12345678901234\n", LookupProduct},
		{"padded 15 digits", " 123456789012345\n", LookupQR},
		{"padding does not count toward 20-char cap", " " + "AAAAAAAAAAAAAAAAAA12" + " ", LookupLocalOnly},
		{"padding does not rescue 21 chars", " " + "AAAAAAAAAAAAAAAAAAA12" + " ", LookupQR},
		{"padded 3-char token still QR", " AB1 ", LookupQR},
		// lowercase counts as a letter (kills: drop a-z)
		{"lowercase FNSKU", "x002mb5n7l", LookupLocalOnly},
		{"lowercase ab12", "ab12", LookupLocalOnly},
		{"lowercase only letters", "abcdefgh", LookupQR},
		{"lowercase letters trailing digit", "abcdefg1", LookupLocalOnly},
		// internal separators are QR (kills: non-alnum skipped)
		{"space inside", "AB 12", LookupQR},
		{"hyphen inside", "AB-12", LookupQR},
		{"underscore inside", "AB_12", LookupQR},
		{"dot inside", "AB.12", LookupQR},
		// ASCII-only: non-ASCII digits/letters never classify (kills: unicode-aware checks)
		{"arabic-indic digits", "\u0661\u0662\u0663\u0664\u0665\u0666\u0667", LookupQR},
		{"arabic-indic digits 4", "\u0661\u0662\u0663\u0664", LookupQR},
		{"fullwidth digits", "\uff11\uff12\uff13\uff14", LookupQR},
		{"fullwidth letters+digits", "\uff21\uff22\uff11\uff12", LookupQR},
		{"ascii letters + arabic-indic digits", "AB\u0661\u0662", LookupQR},
		{"latin-1 letter in token", "AB12\u00e9", LookupQR},
		{"latin-1 letter leading", "\u00c9X123", LookupQR},
		{"ascii digits then fullwidth digit", "123\uff14", LookupQR},
		{"NUL after valid token", "AB12\x00", LookupQR},
		{"empty", "", LookupQR},
		{"whitespace only", " \t\r\n", LookupQR},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Classify(tc.raw); got != tc.want {
				t.Fatalf("Classify(%q) = %v, want %v", tc.raw, got, tc.want)
			}
		})
	}
}
