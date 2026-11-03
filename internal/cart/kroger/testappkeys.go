package kroger

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const testTimeout = 10 * time.Second

// TestAppKeys validates Kroger application credentials with a throwaway
// client_credentials request (product.compact only). The access token is never
// stored or returned.
func TestAppKeys(ctx context.Context, baseURL string, transport http.RoundTripper, clientID, secret, redirectURI string) (ok bool, code string, productAccess *int) {
	if transport == nil {
		transport = http.DefaultTransport
	}
	if baseURL == "" {
		baseURL = BaseURL
	}
	ctx, cancel := context.WithTimeout(ctx, testTimeout)
	defer cancel()

	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	form.Set("scope", "product.compact")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(baseURL, "/")+"/v1/connect/oauth2/token", strings.NewReader(form.Encode()))
	if err != nil {
		return false, "kroger_unreachable", nil
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(clientID, secret)

	resp, err := transport.RoundTrip(req)
	if err != nil {
		return false, "kroger_unreachable", nil
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))

	if resp.StatusCode == http.StatusOK {
		one := 1
		return true, "", &one
	}

	var parsed struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(body, &parsed)
	oauthErr := strings.ToLower(strings.TrimSpace(parsed.Error))

	switch {
	case resp.StatusCode == http.StatusUnauthorized || oauthErr == "invalid_client":
		return false, "wrong_keys", nil
	case oauthErr == "invalid_scope" || resp.StatusCode == http.StatusForbidden:
		zero := 0
		return true, "no_product_access", &zero
	case resp.StatusCode >= 500:
		return false, "kroger_unreachable", nil
	default:
		return false, "wrong_keys", nil
	}
}
