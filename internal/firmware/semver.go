package firmware

import (
	"strconv"
	"strings"
)

// semverPrec holds a version without build metadata for precedence comparison.
type semverPrec struct {
	major int64
	minor int64
	patch int64
	pre   []semverPrecID
}

type semverPrecID struct {
	numeric bool
	text    string
}

func parseSemVerPrecedence(v string) (semverPrec, bool) {
	if !validateSemVer2(v) {
		return semverPrec{}, false
	}
	if i := strings.IndexByte(v, '+'); i >= 0 {
		v = v[:i]
	}
	pre := ""
	if i := strings.IndexByte(v, '-'); i >= 0 {
		pre = v[i+1:]
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return semverPrec{}, false
	}
	parseNum := func(s string) (int64, bool) {
		n, err := strconv.ParseInt(s, 10, 64)
		return n, err == nil
	}
	maj, ok := parseNum(parts[0])
	if !ok {
		return semverPrec{}, false
	}
	min, ok := parseNum(parts[1])
	if !ok {
		return semverPrec{}, false
	}
	pat, ok := parseNum(parts[2])
	if !ok {
		return semverPrec{}, false
	}
	var ids []semverPrecID
	if pre != "" {
		for _, part := range strings.Split(pre, ".") {
			if isAllDigits(part) {
				ids = append(ids, semverPrecID{numeric: true, text: part})
			} else {
				ids = append(ids, semverPrecID{numeric: false, text: part})
			}
		}
	}
	return semverPrec{major: maj, minor: min, patch: pat, pre: ids}, true
}

// compareSemVerPrecedence compares two SemVer 2 strings ignoring build metadata.
// Returns -1 if a<b, 0 if equal precedence, 1 if a>b.
func compareSemVerPrecedence(a, b string) int {
	ap, okA := parseSemVerPrecedence(a)
	bp, okB := parseSemVerPrecedence(b)
	if !okA || !okB {
		return strings.Compare(a, b)
	}
	if ap.major != bp.major {
		return cmpInt64(ap.major, bp.major)
	}
	if ap.minor != bp.minor {
		return cmpInt64(ap.minor, bp.minor)
	}
	if ap.patch != bp.patch {
		return cmpInt64(ap.patch, bp.patch)
	}
	return comparePrerelease(ap.pre, bp.pre)
}

func semverEqualPrecedence(a, b string) bool {
	return compareSemVerPrecedence(a, b) == 0
}

func cmpInt64(a, b int64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

func comparePrerelease(a, b []semverPrecID) int {
	if len(a) == 0 && len(b) == 0 {
		return 0
	}
	if len(a) == 0 {
		return 1
	}
	if len(b) == 0 {
		return -1
	}
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		c := comparePrecID(a[i], b[i])
		if c != 0 {
			return c
		}
	}
	if len(a) == len(b) {
		return 0
	}
	if len(a) > len(b) {
		return 1
	}
	return -1
}

func comparePrecID(a, b semverPrecID) int {
	if a.numeric && b.numeric {
		return compareNumericIdentifiers(a.text, b.text)
	}
	if a.numeric && !b.numeric {
		return -1
	}
	if !a.numeric && b.numeric {
		return 1
	}
	return strings.Compare(a.text, b.text)
}

// compareNumericIdentifiers orders numeric prerelease ids without int overflow.
func compareNumericIdentifiers(a, b string) int {
	if len(a) != len(b) {
		return cmpInt64(int64(len(a)), int64(len(b)))
	}
	return strings.Compare(a, b)
}
