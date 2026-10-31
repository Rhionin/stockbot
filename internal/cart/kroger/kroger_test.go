package kroger

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/cart"
)

type fakeRoundTripper struct {
	resp    *http.Response
	err     error
	gotReq  *http.Request
	gotBody string
}

func (f *fakeRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	f.gotReq = req
	if req.Body != nil {
		b, _ := io.ReadAll(req.Body)
		f.gotBody = string(b)
	}
	if f.err != nil {
		return nil, f.err
	}
	return f.resp, nil
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(bytes.NewBufferString(body)),
		Header:     make(http.Header),
	}
}

func TestNew(t *testing.T) {
	tests := []struct {
		name        string
		clientID    string
		redirectURI string
		modality    string
		wantErr     bool
	}{
		{name: "pickup accepted", clientID: "id", redirectURI: "https://app/cb", modality: "PICKUP"},
		{name: "delivery accepted", clientID: "id", redirectURI: "https://app/cb", modality: "DELIVERY"},
		{name: "unknown modality rejected", clientID: "id", redirectURI: "https://app/cb", modality: "SHIP", wantErr: true},
		{name: "empty modality rejected", clientID: "id", redirectURI: "https://app/cb", modality: "", wantErr: true},
		{name: "empty client id rejected", clientID: "", redirectURI: "https://app/cb", modality: "PICKUP", wantErr: true},
		{name: "empty redirect uri rejected", clientID: "id", redirectURI: "", modality: "PICKUP", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adapter, err := New(tt.clientID, "secret", tt.redirectURI, tt.modality)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("New(%q, secret, %q, %q) error = nil, want error", tt.clientID, tt.redirectURI, tt.modality)
				}
				return
			}
			if err != nil {
				t.Fatalf("New(%q, secret, %q, %q) unexpected error: %v", tt.clientID, tt.redirectURI, tt.modality, err)
			}
			if adapter == nil {
				t.Fatal("New returned nil adapter without error")
			}
		})
	}
}

func TestAdapterIdentity(t *testing.T) {
	adapter, err := New("id", "secret", "https://app/cb", "PICKUP")
	if err != nil {
		t.Fatal(err)
	}
	if got := adapter.ID(); got != cart.ProviderID("kroger") {
		t.Errorf("ID() = %q, want %q", got, "kroger")
	}
	if got := adapter.DisplayName(); got != "Kroger" {
		t.Errorf("DisplayName() = %q, want %q", got, "Kroger")
	}
}

func TestCapabilities(t *testing.T) {
	adapter, err := New("id", "secret", "https://app/cb", "PICKUP")
	if err != nil {
		t.Fatal(err)
	}
	want := cart.Capabilities{
		Auth:         cart.AuthOAuth2,
		Delivery:     cart.DeliveryServerPush,
		Confirmation: cart.ConfirmPerRequest,
		Mutation:     cart.MutateAddOnly,
		Identity:     cart.IdentityDerived,
	}
	if got := adapter.Capabilities(); got != want {
		t.Errorf("Capabilities() = %+v, want %+v", got, want)
	}
}

func TestAuthorizationScope(t *testing.T) {
	adapter, err := New("id", "secret", "https://app/cb", "PICKUP")
	if err != nil {
		t.Fatal(err)
	}
	if got := adapter.OAuthFlow().AuthorizationScope(); got != "cart.basic:write" {
		t.Errorf("AuthorizationScope() = %q, want %q", got, "cart.basic:write")
	}
}

func TestAuthorizationURL(t *testing.T) {
	adapter, err := New("my-client", "secret", "https://app/cb", "PICKUP")
	if err != nil {
		t.Fatal(err)
	}
	got, err := adapter.OAuthFlow().AuthorizationURL("xyz state")
	if err != nil {
		t.Fatalf("AuthorizationURL error: %v", err)
	}
	want := "https://api.kroger.com/v1/connect/oauth2/authorize?" +
		"client_id=my-client&redirect_uri=https://app/cb&scope=cart.basic:write&response_type=code&state=xyz%20state"
	if got != want {
		t.Errorf("AuthorizationURL() = %q, want %q", got, want)
	}
}

func TestAuthorizationURLUsesConfiguredBaseURL(t *testing.T) {
	adapter, err := New("id", "secret", "https://app/cb", "PICKUP")
	if err != nil {
		t.Fatal(err)
	}
	adapter.WithBaseURL(CertificationURL)

	got, err := adapter.OAuthFlow().AuthorizationURL("s")
	if err != nil {
		t.Fatalf("AuthorizationURL error: %v", err)
	}
	if !strings.HasPrefix(got, CertificationURL+"/v1/connect/oauth2/authorize") {
		t.Errorf("AuthorizationURL() = %q, want prefix %q", got, CertificationURL)
	}
}

func TestExchangeCodeMalformedBody(t *testing.T) {
	rt := &fakeRoundTripper{resp: jsonResponse(http.StatusOK, `not json`)}
	adapter, err := New("id", "secret", "https://app/cb", "PICKUP")
	if err != nil {
		t.Fatal(err)
	}
	adapter.WithTransport(rt)

	if _, err := adapter.OAuthFlow().ExchangeCode(context.Background(), "code"); err == nil {
		t.Fatal("ExchangeCode error = nil, want error on unparseable body")
	}
}

func TestExchangeCode(t *testing.T) {
	rt := &fakeRoundTripper{resp: jsonResponse(http.StatusOK,
		`{"access_token":"at-123","refresh_token":"rt-456","expires_in":1800}`)}
	adapter, err := New("id", "secret", "https://app/cb", "PICKUP")
	if err != nil {
		t.Fatal(err)
	}
	adapter.WithTransport(rt)

	before := time.Now().UTC()
	token, err := adapter.OAuthFlow().ExchangeCode(context.Background(), "auth-code")
	after := time.Now().UTC()
	if err != nil {
		t.Fatalf("ExchangeCode error: %v", err)
	}

	if token.AccessToken != "at-123" {
		t.Errorf("AccessToken = %q, want %q", token.AccessToken, "at-123")
	}
	if token.RefreshToken != "rt-456" {
		t.Errorf("RefreshToken = %q, want %q", token.RefreshToken, "rt-456")
	}
	if token.ExpiresIn != 1800*time.Second {
		t.Errorf("ExpiresIn = %v, want %v", token.ExpiresIn, 1800*time.Second)
	}
	if token.ReceiptAt.Before(before) || token.ReceiptAt.After(after) {
		t.Errorf("ReceiptAt = %v, want within [%v, %v]", token.ReceiptAt, before, after)
	}
	expiry := token.ReceiptAt.Add(token.ExpiresIn)
	if !expiry.Equal(token.ReceiptAt.Add(1800 * time.Second)) {
		t.Errorf("computed expiry = %v, want ReceiptAt + 1800s", expiry)
	}
}

func TestExchangeCodeNon200(t *testing.T) {
	rt := &fakeRoundTripper{resp: jsonResponse(http.StatusBadRequest, `{"error":"invalid_grant"}`)}
	adapter, err := New("id", "secret", "https://app/cb", "PICKUP")
	if err != nil {
		t.Fatal(err)
	}
	adapter.WithTransport(rt)

	if _, err := adapter.OAuthFlow().ExchangeCode(context.Background(), "bad-code"); err == nil {
		t.Fatal("ExchangeCode error = nil, want error on non-200 response")
	}
}

func TestRefreshAccessToken(t *testing.T) {
	rt := &fakeRoundTripper{resp: jsonResponse(http.StatusOK,
		`{"access_token":"at-new","refresh_token":"rt-new","expires_in":900}`)}
	adapter, err := New("id", "secret", "https://app/cb", "PICKUP")
	if err != nil {
		t.Fatal(err)
	}
	adapter.WithTransport(rt)

	before := time.Now().UTC()
	token, err := adapter.OAuthFlow().RefreshAccessToken(context.Background(), "rt-old")
	after := time.Now().UTC()
	if err != nil {
		t.Fatalf("RefreshAccessToken error: %v", err)
	}

	if token.AccessToken != "at-new" {
		t.Errorf("AccessToken = %q, want %q", token.AccessToken, "at-new")
	}
	if token.RefreshToken != "rt-new" {
		t.Errorf("RefreshToken = %q, want %q", token.RefreshToken, "rt-new")
	}
	if token.ExpiresIn != 900*time.Second {
		t.Errorf("ExpiresIn = %v, want %v", token.ExpiresIn, 900*time.Second)
	}
	if token.ReceiptAt.Before(before) || token.ReceiptAt.After(after) {
		t.Errorf("ReceiptAt = %v, want within [%v, %v]", token.ReceiptAt, before, after)
	}
}

func TestRefreshAccessTokenNon200(t *testing.T) {
	rt := &fakeRoundTripper{resp: jsonResponse(http.StatusUnauthorized, `{"error":"invalid_token"}`)}
	adapter, err := New("id", "secret", "https://app/cb", "PICKUP")
	if err != nil {
		t.Fatal(err)
	}
	adapter.WithTransport(rt)

	_, err = adapter.OAuthFlow().RefreshAccessToken(context.Background(), "rt-old")
	if err == nil {
		t.Fatal("RefreshAccessToken error = nil, want error on non-200 response")
	}
	var tokenErr *cart.OAuthTokenError
	if !errors.As(err, &tokenErr) {
		t.Fatalf("error type = %T, want *cart.OAuthTokenError", err)
	}
	if tokenErr.OAuthError != "invalid_token" {
		t.Fatalf("OAuthError = %q, want invalid_token", tokenErr.OAuthError)
	}
	if !tokenErr.ReconnectRequired() {
		t.Fatal("ReconnectRequired() = false, want true for invalid_token")
	}
	if cart.OAuthFailureReason(err) != cart.ReasonReconnectRequired {
		t.Fatalf("OAuthFailureReason = %q, want reconnect_required", cart.OAuthFailureReason(err))
	}
}

func TestRefreshAccessTokenInvalidGrant(t *testing.T) {
	rt := &fakeRoundTripper{resp: jsonResponse(http.StatusBadRequest, `{"error":"invalid_grant"}`)}
	adapter, err := New("id", "secret", "https://app/cb", "PICKUP")
	if err != nil {
		t.Fatal(err)
	}
	adapter.WithTransport(rt)

	_, err = adapter.OAuthFlow().RefreshAccessToken(context.Background(), "rt-old")
	if err == nil {
		t.Fatal("RefreshAccessToken error = nil, want error")
	}
	var tokenErr *cart.OAuthTokenError
	if !errors.As(err, &tokenErr) || tokenErr.OAuthError != "invalid_grant" {
		t.Fatalf("RefreshAccessToken error = %v", err)
	}
}

func TestServerPushAddUnauthorizedIsReconnectClass(t *testing.T) {
	rt := &fakeRoundTripper{resp: jsonResponse(http.StatusUnauthorized, `{"error":"invalid_token"}`)}
	adapter, err := New("id", "secret", "https://app/cb", "PICKUP")
	if err != nil {
		t.Fatal(err)
	}
	adapter.WithTransport(rt)

	req := provisionRequest(cart.ProvisionLine{Identity: "0001111072822", Quantity: 1})
	result, err := adapter.ServerPush().Add(context.Background(), cart.NewBearerCredential("tok"), req)
	if err == nil {
		t.Fatal("Add error = nil, want error on 401")
	}
	if result.Status != http.StatusUnauthorized {
		t.Fatalf("Status = %d, want 401", result.Status)
	}
	if !cart.CartResponseNeedsReconnect(result.Status) {
		t.Fatal("expected reconnect-class cart response")
	}
}

func TestServerPushAddRejectedSanitizesProviderCode(t *testing.T) {
	rt := &fakeRoundTripper{resp: jsonResponse(http.StatusBadRequest, `{"code":"Bearer deadbeef"}`)}
	adapter, err := New("id", "secret", "https://app/cb", "PICKUP")
	if err != nil {
		t.Fatal(err)
	}
	adapter.WithTransport(rt)

	req := provisionRequest(cart.ProvisionLine{Identity: "0001111072822", Quantity: 1})
	result, err := adapter.ServerPush().Add(context.Background(), cart.NewBearerCredential("tok"), req)
	if err != nil {
		t.Fatalf("Add error: %v", err)
	}
	if result.ProviderErrorCode != "" {
		t.Fatalf("ProviderErrorCode = %q, want empty for hostile code", result.ProviderErrorCode)
	}
}

func provisionRequest(lines ...cart.ProvisionLine) cart.ProvisionRequest {
	return cart.ProvisionRequest{Provider: cart.ProviderID("kroger"), Lines: lines}
}

func TestServerPushAddAccepted(t *testing.T) {
	rt := &fakeRoundTripper{resp: jsonResponse(http.StatusNoContent, "")}
	adapter, err := New("id", "secret", "https://app/cb", "PICKUP")
	if err != nil {
		t.Fatal(err)
	}
	adapter.WithTransport(rt)

	req := provisionRequest(
		cart.ProvisionLine{Identity: "0001111072822", Quantity: 2},
		cart.ProvisionLine{Identity: "0003600029145", Quantity: 1},
	)
	result, err := adapter.ServerPush().Add(context.Background(), cart.NewBearerCredential("tok"), req)
	if err != nil {
		t.Fatalf("Add error: %v", err)
	}
	if result.Disposition != cart.DispositionAccepted {
		t.Errorf("Disposition = %q, want %q", result.Disposition, cart.DispositionAccepted)
	}
	if result.Status != http.StatusNoContent {
		t.Errorf("Status = %d, want %d", result.Status, http.StatusNoContent)
	}
	for _, line := range req.Lines {
		if !result.PerLine[line.Identity] {
			t.Errorf("PerLine[%q] = false, want true", line.Identity)
		}
	}
	if len(result.PerLine) != 2 {
		t.Errorf("len(PerLine) = %d, want 2", len(result.PerLine))
	}
	if got := rt.gotReq.Header.Get("Authorization"); got != "Bearer tok" {
		t.Errorf("Authorization header = %q, want %q", got, "Bearer tok")
	}
	if !strings.Contains(rt.gotBody, "0001111072822") {
		t.Errorf("request body missing identity, got %q", rt.gotBody)
	}
}

func TestServerPushAddRejected(t *testing.T) {
	rt := &fakeRoundTripper{resp: jsonResponse(http.StatusBadRequest, `{"error":"bad"}`)}
	adapter, err := New("id", "secret", "https://app/cb", "PICKUP")
	if err != nil {
		t.Fatal(err)
	}
	adapter.WithTransport(rt)

	req := provisionRequest(cart.ProvisionLine{Identity: "0001111072822", Quantity: 1})
	result, err := adapter.ServerPush().Add(context.Background(), cart.NewBearerCredential("tok"), req)
	if err != nil {
		t.Fatalf("Add error: %v", err)
	}
	if result.Disposition != cart.DispositionRejected {
		t.Errorf("Disposition = %q, want %q", result.Disposition, cart.DispositionRejected)
	}
	if result.Status != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", result.Status, http.StatusBadRequest)
	}
	if len(result.PerLine) != 0 {
		t.Errorf("PerLine populated on rejection: %v", result.PerLine)
	}
}

func TestServerPushAddLineValidation(t *testing.T) {
	tests := []struct {
		name string
		line cart.ProvisionLine
	}{
		{name: "empty identity", line: cart.ProvisionLine{Identity: "", Quantity: 1}},
		{name: "quantity below one", line: cart.ProvisionLine{Identity: "0001111072822", Quantity: 0}},
		{name: "quantity above 999", line: cart.ProvisionLine{Identity: "0001111072822", Quantity: 1000}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rt := &fakeRoundTripper{resp: jsonResponse(http.StatusNoContent, "")}
			adapter, err := New("id", "secret", "https://app/cb", "PICKUP")
			if err != nil {
				t.Fatal(err)
			}
			adapter.WithTransport(rt)

			result, err := adapter.ServerPush().Add(context.Background(), cart.NewBearerCredential("tok"), provisionRequest(tt.line))
			if err == nil {
				t.Fatal("Add error = nil, want validation error")
			}
			if result.Disposition != cart.DispositionRejected {
				t.Errorf("Disposition = %q, want %q", result.Disposition, cart.DispositionRejected)
			}
			if result.Status != 400 {
				t.Errorf("Status = %d, want 400", result.Status)
			}
			if rt.gotReq != nil {
				t.Error("transport was called; validation should reject before sending")
			}
		})
	}
}
