// Package kroger provides a grocery cart adapter for Kroger's Cart API.
package kroger

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Rhionin/stockbot/internal/cart"
)

const (
	// BaseURL is the production Kroger API base URL.
	BaseURL = "https://api.kroger.com"
	// CertificationURL is the certification/testing API base URL.
	CertificationURL = "https://api-ce.kroger.com"
)

// Adapter implements Kroger's Cart API.
type Adapter struct {
	mu           sync.RWMutex
	baseURL      string
	transport    http.RoundTripper
	clientID     string
	clientSecret string
	redirectURI  string
	modality     string
	timeout      time.Duration
	rand         io.Reader
}

// ApplyAppCredentials replaces the application credentials used for OAuth and
// cart calls. The secret stays on the adapter and is not returned by
// PublicAppCredentials.
func (a *Adapter) ApplyAppCredentials(clientID, clientSecret, redirectURI, modality string) error {
	if clientID == "" {
		return fmt.Errorf("client_id cannot be empty")
	}
	if clientSecret == "" {
		return fmt.Errorf("client_secret cannot be empty")
	}
	if redirectURI == "" {
		return fmt.Errorf("redirect_uri cannot be empty")
	}
	if modality != "PICKUP" && modality != "DELIVERY" {
		return fmt.Errorf("invalid modality %q: must be PICKUP or DELIVERY", modality)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.clientID = clientID
	a.clientSecret = clientSecret
	a.redirectURI = redirectURI
	a.modality = modality
	return nil
}

// ClearAppCredentials drops the application credentials. Modality returns to
// the default pickup value.
func (a *Adapter) ClearAppCredentials() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.clientID = ""
	a.clientSecret = ""
	a.redirectURI = ""
	a.modality = "PICKUP"
}

// PublicAppCredentials returns the non-secret credential fields.
func (a *Adapter) PublicAppCredentials() cart.AppCredentials {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return cart.AppCredentials{
		ClientID:    a.clientID,
		RedirectURI: a.redirectURI,
		Modality:    a.modality,
		SecretSet:   a.clientSecret != "",
	}
}

func (a *Adapter) credentials() (clientID, clientSecret, redirectURI, modality string) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.clientID, a.clientSecret, a.redirectURI, a.modality
}

// New creates a new Kroger adapter.
// It validates the modality at construction.
func New(clientID, clientSecret, redirectURI, modality string) (*Adapter, error) {
	// Validate modality
	if modality != "PICKUP" && modality != "DELIVERY" {
		return nil, fmt.Errorf("invalid modality %q: must be PICKUP or DELIVERY", modality)
	}

	// Validate client_id is not empty
	if clientID == "" {
		return nil, fmt.Errorf("client_id cannot be empty")
	}

	// Validate redirect_uri is not empty
	if redirectURI == "" {
		return nil, fmt.Errorf("redirect_uri cannot be empty")
	}

	return &Adapter{
		baseURL:      BaseURL,
		clientID:     clientID,
		clientSecret: clientSecret,
		redirectURI:  redirectURI,
		modality:     modality,
		timeout:      10 * time.Second,
		rand:         rand.Reader,
	}, nil
}

// WithBaseURL sets the base URL (for testing with certification or mock servers).
func (a *Adapter) WithBaseURL(url string) *Adapter {
	a.baseURL = url
	return a
}

// WithTransport sets the HTTP transport (for testing).
func (a *Adapter) WithTransport(transport http.RoundTripper) *Adapter {
	a.transport = transport
	return a
}

// WithTimeout sets the request timeout (for testing).
func (a *Adapter) WithTimeout(timeout time.Duration) *Adapter {
	a.timeout = timeout
	return a
}

// WithRand sets the random source (for testing).
func (a *Adapter) WithRand(rand io.Reader) *Adapter {
	a.rand = rand
	return a
}

// NewUnconfigured returns a Kroger provider that declares the same capabilities
// but has no client credentials. The HTTP layer refuses authorization and
// provisioning while credentials are not configured.
func NewUnconfigured() *Adapter {
	return &Adapter{
		baseURL:  BaseURL,
		modality: "PICKUP",
		timeout:  10 * time.Second,
		rand:     rand.Reader,
	}
}

// ID returns the provider ID for Kroger.
func (a *Adapter) ID() cart.ProviderID {
	return cart.ProviderID("kroger")
}

// DisplayName returns a user-friendly name for Kroger.
func (a *Adapter) DisplayName() string {
	return "Kroger"
}

// Capabilities returns Kroger's declared capabilities:
// - auth: oauth2_authorization_code
// - delivery: server_push
// - confirmation: per_request (conservative reading of API docs)
// - mutation: add_only (Cart API only has one operation)
// - identity: derived (we normalize barcodes locally)
func (a *Adapter) Capabilities() cart.Capabilities {
	return cart.Capabilities{
		Auth:         cart.AuthOAuth2,
		Delivery:     cart.DeliveryServerPush,
		Confirmation: cart.ConfirmPerRequest,
		Mutation:     cart.MutateAddOnly,
		Identity:     cart.IdentityDerived,
	}
}

// OAuthFlow implementation for Kroger.
type oauthFlow struct {
	adapter *Adapter
}

func (a *Adapter) OAuthFlow() cart.OAuthFlow {
	return &oauthFlow{adapter: a}
}

// AuthorizationScope implements cart.OAuthFlow on the adapter itself so the
// engine can dispatch through the interface without a side lookup.
func (a *Adapter) AuthorizationScope() string { return a.OAuthFlow().AuthorizationScope() }

// AuthorizationURL implements cart.OAuthFlow.
func (a *Adapter) AuthorizationURL(state string) (string, error) {
	return a.OAuthFlow().AuthorizationURL(state)
}

// ExchangeCode implements cart.OAuthFlow.
func (a *Adapter) ExchangeCode(ctx context.Context, code string) (cart.TokenSet, error) {
	return a.OAuthFlow().ExchangeCode(ctx, code)
}

// RefreshAccessToken implements cart.OAuthFlow.
func (a *Adapter) RefreshAccessToken(ctx context.Context, refreshToken string) (cart.TokenSet, error) {
	return a.OAuthFlow().RefreshAccessToken(ctx, refreshToken)
}

// Add implements cart.ServerPush.
func (a *Adapter) Add(ctx context.Context, cred cart.Credential, req cart.ProvisionRequest) (cart.ProvisionResult, error) {
	return a.ServerPush().Add(ctx, cred, req)
}

// DeriveIdentity implements cart.DerivedIdentity. The converted identifier is
// for this call only and is never written back to the barcode table.
func (a *Adapter) DeriveIdentity(barcode string) (cart.ProductIdentity, bool) {
	return Normalize(barcode)
}

// AuthorizationScope returns the scope string for Kroger Cart API.
func (o *oauthFlow) AuthorizationScope() string {
	return "cart.basic:write"
}

// AuthorizationURL generates the authorization URL for OAuth2 flow.
func (o *oauthFlow) AuthorizationURL(state string) (string, error) {
	clientID, _, redirectURI, _ := o.adapter.credentials()
	// Build the authorization URL
	url := fmt.Sprintf("%s/v1/connect/oauth2/authorize", o.adapter.baseURL)
	params := fmt.Sprintf(
		"client_id=%s&redirect_uri=%s&scope=%s&response_type=code&state=%s",
		clientID,
		urlQueryEscape(redirectURI),
		urlQueryEscape(o.AuthorizationScope()),
		urlQueryEscape(state),
	)
	return url + "?" + params, nil
}

// ExchangeCode exchanges an authorization code for tokens.
func (o *oauthFlow) ExchangeCode(ctx context.Context, code string) (cart.TokenSet, error) {
	ctx, cancel := context.WithTimeout(ctx, o.adapter.timeout)
	defer cancel()

	clientID, clientSecret, redirectURI, _ := o.adapter.credentials()
	req, err := http.NewRequestWithContext(ctx, "POST",
		fmt.Sprintf("%s/v1/connect/oauth2/token", o.adapter.baseURL),
		strings.NewReader(fmt.Sprintf("code=%s&grant_type=authorization_code&client_id=%s&redirect_uri=%s",
			urlQueryEscape(code), clientID, urlQueryEscape(redirectURI))))
	if err != nil {
		return cart.TokenSet{}, fmt.Errorf("failed to create exchange request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(clientID, clientSecret)

	transport := o.adapter.transport
	if transport == nil {
		transport = http.DefaultTransport
	}

	resp, err := transport.RoundTrip(req)
	if err != nil {
		return cart.TokenSet{}, fmt.Errorf("token exchange failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, resp.Body)
		// The body is where tokens and provider error details live, so the
		// error names only the status.
		return cart.TokenSet{}, fmt.Errorf("token exchange returned status %d", resp.StatusCode)
	}

	var tokenResp struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return cart.TokenSet{}, fmt.Errorf("failed to decode token response: %w", err)
	}

	return cart.TokenSet{
		AccessToken:  tokenResp.AccessToken,
		RefreshToken: tokenResp.RefreshToken,
		ExpiresIn:    time.Duration(tokenResp.ExpiresIn) * time.Second,
		ReceiptAt:    time.Now().UTC(),
	}, nil
}

// RefreshAccessToken refreshes the access token using the refresh token.
func (o *oauthFlow) RefreshAccessToken(ctx context.Context, refreshToken string) (cart.TokenSet, error) {
	ctx, cancel := context.WithTimeout(ctx, o.adapter.timeout)
	defer cancel()

	clientID, clientSecret, _, _ := o.adapter.credentials()
	req, err := http.NewRequestWithContext(ctx, "POST",
		fmt.Sprintf("%s/v1/connect/oauth2/token", o.adapter.baseURL),
		strings.NewReader(fmt.Sprintf("grant_type=refresh_token&refresh_token=%s&client_id=%s",
			urlQueryEscape(refreshToken), clientID)))
	if err != nil {
		return cart.TokenSet{}, fmt.Errorf("failed to create refresh request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(clientID, clientSecret)

	transport := o.adapter.transport
	if transport == nil {
		transport = http.DefaultTransport
	}

	resp, err := transport.RoundTrip(req)
	if err != nil {
		return cart.TokenSet{}, &cart.OAuthTokenError{Err: fmt.Errorf("token refresh failed: %w", err)}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		oauthErr := parseOAuthErrorBody(body)
		return cart.TokenSet{}, &cart.OAuthTokenError{
			StatusCode: resp.StatusCode,
			OAuthError: oauthErr,
			Err:        fmt.Errorf("token refresh returned status %d", resp.StatusCode),
		}
	}

	var tokenResp struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return cart.TokenSet{}, fmt.Errorf("failed to decode token response: %w", err)
	}

	return cart.TokenSet{
		AccessToken:  tokenResp.AccessToken,
		RefreshToken: tokenResp.RefreshToken,
		ExpiresIn:    time.Duration(tokenResp.ExpiresIn) * time.Second,
		ReceiptAt:    time.Now().UTC(),
	}, nil
}

// ServerPush implementation for Kroger.
type serverPush struct {
	adapter *Adapter
}

func (a *Adapter) ServerPush() cart.ServerPush {
	return &serverPush{adapter: a}
}

// Add submits a provisioning request to Kroger's Cart API.
func (s *serverPush) Add(ctx context.Context, cred cart.Credential, req cart.ProvisionRequest) (cart.ProvisionResult, error) {
	ctx, cancel := context.WithTimeout(ctx, s.adapter.timeout)
	defer cancel()

	// Build the request payload
	_, _, _, modality := s.adapter.credentials()
	payload := struct {
		Items []struct {
			UPC      string `json:"upc"`
			Quantity int    `json:"quantity"`
			Modality string `json:"modality"`
		} `json:"items"`
		Modality string `json:"modality,omitempty"`
	}{
		Modality: modality,
	}

	for _, line := range req.Lines {
		// Validate line
		if len(line.Identity) == 0 {
			return cart.ProvisionResult{
				Disposition: cart.DispositionRejected,
				Status:      400,
			}, fmt.Errorf("empty identity in line")
		}
		if line.Quantity < 1 || line.Quantity > 999 {
			return cart.ProvisionResult{
				Disposition: cart.DispositionRejected,
				Status:      400,
			}, fmt.Errorf("invalid quantity %d (must be 1-999)", line.Quantity)
		}

		payload.Items = append(payload.Items, struct {
			UPC      string `json:"upc"`
			Quantity int    `json:"quantity"`
			Modality string `json:"modality"`
		}{
			UPC:      string(line.Identity),
			Quantity: line.Quantity,
			Modality: modality,
		})
	}

	// Serialize to JSON
	body, err := json.Marshal(payload)
	if err != nil {
		return cart.ProvisionResult{
			Disposition: cart.DispositionRejected,
			Status:      500,
		}, fmt.Errorf("failed to serialize request: %w", err)
	}

	// Create HTTP request
	httpReq, err := http.NewRequestWithContext(ctx, "PUT",
		fmt.Sprintf("%s/v1/cart/add", s.adapter.baseURL),
		strings.NewReader(string(body)))
	if err != nil {
		return cart.ProvisionResult{
			Disposition: cart.DispositionRejected,
			Status:      500,
		}, fmt.Errorf("failed to create HTTP request: %w", err)
	}

	// Apply credentials
	cred.Apply(httpReq)
	httpReq.Header.Set("Content-Type", "application/json")

	// Use the transport directly - retryhttp.New requires a Transport type, not interface
	var transport http.RoundTripper = s.adapter.transport
	if transport == nil {
		transport = http.DefaultTransport
	}

	resp, err := transport.RoundTrip(httpReq)
	if err != nil {
		return cart.ProvisionResult{
			Disposition: cart.DispositionIndeterminate,
			Status:      0,
		}, fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	// Read response body (for error messages)
	respBody, _ := io.ReadAll(resp.Body)

	// Determine disposition based on status code
	var disposition cart.ResultDisposition
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		disposition = cart.DispositionAccepted
	case resp.StatusCode >= 400 && resp.StatusCode < 500:
		disposition = cart.DispositionRejected
	default:
		disposition = cart.DispositionIndeterminate
	}

	providerCode := ""
	if disposition == cart.DispositionRejected && len(respBody) > 0 {
		var parsed struct {
			Code string `json:"code"`
		}
		if json.Unmarshal(respBody, &parsed) == nil {
			providerCode = cart.SanitizeProviderErrorCode(parsed.Code)
		}
	}

	// For per_request confirmation, all items are confirmed together
	perLine := make(map[cart.ProductIdentity]bool)
	if disposition == cart.DispositionAccepted {
		for _, line := range req.Lines {
			perLine[line.Identity] = true
		}
	}

	return cart.ProvisionResult{
		Disposition:       disposition,
		PerLine:           perLine,
		Status:            resp.StatusCode,
		ProviderErrorCode: providerCode,
	}, nil
}

// Helper functions

func parseOAuthErrorBody(body []byte) string {
	var parsed struct {
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	if json.Unmarshal(body, &parsed) != nil {
		return ""
	}
	if code := cart.SanitizeProviderErrorCode(parsed.Error); code != "" {
		return code
	}
	return cart.SanitizeProviderErrorCode(parsed.ErrorDescription)
}

func urlQueryEscape(s string) string {
	return strings.ReplaceAll(s, " ", "%20")
}

var (
	_ cart.Provider          = (*Adapter)(nil)
	_ cart.OAuthFlow         = (*Adapter)(nil)
	_ cart.ServerPush        = (*Adapter)(nil)
	_ cart.DerivedIdentity   = (*Adapter)(nil)
	_ cart.AppCredentialSink = (*Adapter)(nil)
)
