package clientmsg

import (
	"errors"
	"testing"
)

func TestRejectKeepsTheFixedSentence(t *testing.T) {
	text, code := Reject(ProductUnresolvedText, ProductUnresolvedCode, errors.New(`household user-1: product "no-such"`))
	if text != ProductUnresolvedText || code != ProductUnresolvedCode {
		t.Fatalf("unresolved: %s %s", text, code)
	}

	text, code = Reject(InvalidResolveText, InvalidResolveCode, nil)
	if text != InvalidResolveText || code != InvalidResolveCode {
		t.Fatalf("resolve: %s %s", text, code)
	}
}
