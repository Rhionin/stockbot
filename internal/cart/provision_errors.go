package cart

import (
	"errors"
	"fmt"
	"strings"
)

// OAuth token endpoint response or transport failure.
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
	if e.StatusCode >= 500 {
		return false
	}
	return false
}

// OAuthFailureReason maps a refresh or exchange error to a per-line outcome reason.
func OAuthFailureReason(err error) OutcomeReason {
	for err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "reauthorization required") {
			return ReasonReconnectRequired
		}
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

// ErrReconnectRequired is returned when provisioning cannot proceed until the owner reconnects the store.
type ErrReconnectRequired struct {
	Provider ProviderID
	Err      error
}

func (e *ErrReconnectRequired) Error() string {
	if e.Err != nil {
		return e.Err.Error()
	}
	return fmt.Sprintf("reconnect required for provider %q", e.Provider)
}

func (e *ErrReconnectRequired) Unwrap() error {
	return e.Err
}

// ErrProviderUnavailable is returned when the store or token endpoint is unreachable or returns 5xx.
type ErrProviderUnavailable struct {
	Provider ProviderID
	Err      error
}

func (e *ErrProviderUnavailable) Error() string {
	if e.Err != nil {
		return e.Err.Error()
	}
	return fmt.Sprintf("provider %q unavailable", e.Provider)
}

func (e *ErrProviderUnavailable) Unwrap() error {
	return e.Err
}

// ClassifyOAuthAccessError maps a token broker refresh failure to a provisioning error.
func ClassifyOAuthAccessError(provider ProviderID, err error) error {
	if err == nil {
		return nil
	}
	if OAuthFailureReason(err) == ReasonReconnectRequired {
		return &ErrReconnectRequired{Provider: provider, Err: err}
	}
	return &ErrProviderUnavailable{Provider: provider, Err: err}
}

// CartResponseNeedsReconnect is true when a cart API response indicates the credential was rejected.
func CartResponseNeedsReconnect(status int) bool {
	return status == 401 || status == 403
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
