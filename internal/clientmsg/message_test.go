package clientmsg

import "testing"

func TestSentencesAreTheAgreedWords(t *testing.T) {
	if Busy != "Stockbot is busy right now. Try again in a moment." {
		t.Fatalf("busy = %q", Busy)
	}
	if NoSpace != "Stockbot is out of storage space, so this wasn't saved." {
		t.Fatalf("no space = %q", NoSpace)
	}
	if Generic != "Something went wrong. Try again." {
		t.Fatalf("generic = %q", Generic)
	}
	if AuthNotConfigured != "Sign-in isn't set up yet." {
		t.Fatalf("auth not configured = %q", AuthNotConfigured)
	}
}
