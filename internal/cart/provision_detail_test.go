package cart

import "testing"

func TestSanitizeProviderErrorCodeAllowlist(t *testing.T) {
	t.Parallel()
	if got := SanitizeProviderErrorCode("invalid_grant"); got != "invalid_grant" {
		t.Fatalf("allowlisted code: got %q", got)
	}
	hostile := []string{
		"Bearer deadbeef",
		"refresh_token=secret",
		`{"error":"invalid_grant"}`,
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"UPPERCASE",
		"bad code",
	}
	for _, code := range hostile {
		if got := SanitizeProviderErrorCode(code); got != "" {
			t.Fatalf("SanitizeProviderErrorCode(%q) = %q, want empty", code, got)
		}
	}
}

func TestBatchRejectedDetailNeverEchoesRawBody(t *testing.T) {
	t.Parallel()
	raw := `{"error":"invalid_grant","error_description":"secret refresh_token=xy"}`
	if got := batchRejectedDetail(raw); got != "The store rejected this batch." {
		t.Fatalf("batchRejectedDetail(raw JSON) = %q, want generic rejection", got)
	}
	if got := batchRejectedDetail("invalid_grant"); got != "The store reported: invalid_grant" {
		t.Fatalf("batchRejectedDetail(invalid_grant) = %q", got)
	}
}
