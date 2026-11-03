package store

import "testing"

func TestGrantedScopeIncludesProductCompact(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		scope string
		want  bool
	}{
		{"cart.basic:write product.compact", true},
		{"product.compact cart.basic:write", true},
		{"cart.basic:write", false},
		{"product.compactX", false},
		{"xproduct.compact", false},
		{"", false},
	} {
		if got := grantedScopeIncludesProductCompact(tc.scope); got != tc.want {
			t.Errorf("grantedScopeIncludesProductCompact(%q) = %v, want %v", tc.scope, got, tc.want)
		}
	}
}
