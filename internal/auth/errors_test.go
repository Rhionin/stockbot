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

// B14 / #571 NB1: forbidden_role and forbidden_owner share one hosted sentence.
func TestForbiddenSentenceHouseholdAdmin(t *testing.T) {
	const want = "Only a household admin can do that."
	if SentenceForbidden != want {
		t.Fatalf("SentenceForbidden = %q want %q", SentenceForbidden, want)
	}
	for _, code := range []string{CodeForbiddenRole, CodeForbiddenOwner} {
		if code == "" {
			t.Fatalf("empty forbidden code constant")
		}
		ae := fail(code, SentenceForbidden)
		if ae.Code != code {
			t.Fatalf("fail(%q, …).Code = %q", code, ae.Code)
		}
		if ae.Message != want {
			t.Fatalf("fail(%q, SentenceForbidden).Message = %q want %q", code, ae.Message, want)
		}
	}
}
