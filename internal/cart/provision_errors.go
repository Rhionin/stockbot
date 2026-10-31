package cart

import (
	"errors"
	"fmt"
	"strings"
)

// AuthExchangeFailed means OAuth token refresh or exchange failed before any
// provider cart batch was accepted.
type AuthExchangeFailed struct {
	Provider ProviderID
	Err      error
}

func (e *AuthExchangeFailed) Error() string {
	return fmt.Sprintf("provider %q auth exchange failed: %v", e.Provider, e.Err)
}

func (e *AuthExchangeFailed) Unwrap() error {
	return e.Err
}

// OAuthTokenError classifies a token endpoint response or transport failure.
type OAuthTokenError struct {
	StatusCode int
	OAuthError string
	Err        error
}

func (e *OAuthTokenError) Error() string {
	if e.Err != nil {
		return e.Err.Error()
	}
	if e.OAuthError != "" {
		return e.OAuthError
	}
	return fmt.Sprintf("oauth token error (status %d)", e.StatusCode)
}

func (e *OAuthTokenError) Unwrap() error {
	return e.Err
}

// ReconnectRequired is true when the stored refresh token is no longer valid.
func (e *OAuthTokenError) ReconnectRequired() bool {
	switch strings.ToLower(strings.TrimSpace(e.OAuthError)) {
	case "invalid_grant", "invalid_token", "access_denied", "unauthorized_client":
		return true
	}
	switch e.StatusCode {
	case 400, 401, 403:
		if e.OAuthError != "" {
			return true
		}
	}
	return false
}

// OAuthFailureReason maps a refresh or exchange error to a per-line outcome reason.
func OAuthFailureReason(err error) OutcomeReason {
	for err != nil {
		var tokenErr *OAuthTokenError
		if errors.As(err, &tokenErr) && tokenErr.ReconnectRequired() {
			return ReasonReconnectRequired
		}
		if strings.Contains(strings.ToLower(err.Error()), "invalid_grant") {
			return ReasonReconnectRequired
		}
		err = errors.Unwrap(err)
	}
	return ReasonProviderUnavailable
}

// OAuthFailureDetail is a safe per-line message when token refresh fails.
func OAuthFailureDetail(reason OutcomeReason) string {
	switch reason {
	case ReasonReconnectRequired:
		return "Reconnect the store to send items."
	default:
		return "Could not reach the store. Try again."
	}
}
