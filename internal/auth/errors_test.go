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

// #571 §F: last-owner demote copy (CJ-approved).
func TestLastOwnerSentenceDemoteHouseholdAdmin(t *testing.T) {
	const want = "You're the only household admin. Make another member a household admin first."
	if SentenceLastOwnerDemote != want {
		t.Fatalf("SentenceLastOwnerDemote = %q want %q", SentenceLastOwnerDemote, want)
	}
	ae := fail(CodeLastOwner, SentenceLastOwnerDemote)
	if ae.Message != want {
		t.Fatalf("fail(last_owner, …).Message = %q want %q", ae.Message, want)
	}
}

// #571 §F: last-owner leave copy (CJ-approved).
func TestLastOwnerSentenceLeaveHouseholdAdmin(t *testing.T) {
	const want = "You're the only household admin, so you can't leave yet. Make another member a household admin first, or delete the household."
	if SentenceLastOwnerLeave != want {
		t.Fatalf("SentenceLastOwnerLeave = %q want %q", SentenceLastOwnerLeave, want)
	}
	ae := fail(CodeLastOwner, SentenceLastOwnerLeave)
	if ae.Message != want {
		t.Fatalf("fail(last_owner, …).Message = %q want %q", ae.Message, want)
	}
}

// #571 §F: hosted last_owner registry fallback (CJ-approved).
func TestLastOwnerSentenceHostedFallback(t *testing.T) {
	const want = "You're the only household admin. Make another member a household admin first, or delete the household."
	if SentenceLastOwner != want {
		t.Fatalf("SentenceLastOwner = %q want %q", SentenceLastOwner, want)
	}
}

// #571 §F: account delete while last household admin (CJ-approved).
func TestLastOwnerAccountDeleteSentence(t *testing.T) {
	const want = "You're the only household admin of north-pantry-z9. Make another member a household admin or delete the household first."
	if LastOwnerSentence("north-pantry-z9") != want {
		t.Fatalf("LastOwnerSentence = %q want %q", LastOwnerSentence("north-pantry-z9"), want)
	}
}
