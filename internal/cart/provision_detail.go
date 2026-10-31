package cart

import (
	"regexp"
	"strings"
)

var safeProviderErrorCode = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// batchRejectedDetail returns a short, safe message for a rejected batch.
func batchRejectedDetail(code string) string {
	code = strings.TrimSpace(code)
	if code == "" || !safeProviderErrorCode.MatchString(code) {
		return "The store rejected this batch."
	}
	return "The store reported: " + code
}

// SanitizeProviderErrorCode returns code when it matches a safe pattern.
func SanitizeProviderErrorCode(code string) string {
	code = strings.TrimSpace(code)
	if safeProviderErrorCode.MatchString(code) {
		return code
	}
	return ""
}
