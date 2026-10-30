package auth

import "testing"

// B14 (#456): forbidden_operator sentence is locked for hosted clients.
func TestForbiddenOperatorSentenceB14(t *testing.T) {
	const want = "Only an admin can do this."
	if SentenceForbiddenOperator != want {
		t.Fatalf("SentenceForbiddenOperator = %q want %q", SentenceForbiddenOperator, want)
	}
	if CodeForbiddenOperator != "forbidden_operator" {
		t.Fatalf("CodeForbiddenOperator = %q", CodeForbiddenOperator)
	}
}
