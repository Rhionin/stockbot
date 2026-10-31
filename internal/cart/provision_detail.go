package cart

import "strings"

// Known safe provider/OAuth error codes that may appear in user-visible detail.
var allowedProviderErrorCodes = map[string]struct{}{
	"invalid_grant":         {},
	"invalid_token":         {},
	"access_denied":         {},
	"unauthorized_client":   {},
	"invalid_request":       {},
	"unsupported_grant_type": {},
	"invalid_scope":         {},
	"invalid_client":        {},
}

// batchRejectedDetail returns a short, safe message for a rejected batch.
func batchRejectedDetail(code string) string {
	code = SanitizeProviderErrorCode(code)
	if code == "" {
		return "The store rejected this batch."
	}
	return "The store reported: " + code
}

// SanitizeProviderErrorCode returns code when it is on the allowlist.
func SanitizeProviderErrorCode(code string) string {
	code = strings.TrimSpace(code)
	if code == "" {
		return ""
	}
	if _, ok := allowedProviderErrorCodes[code]; ok {
		return code
	}
	return ""
}
