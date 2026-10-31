package cart

import (
	"errors"
	"testing"

	"github.com/Rhionin/stockbot/internal/cart/connection"
	"github.com/stretchr/testify/require"
)

func TestOAuthTokenErrorErrorStrings(t *testing.T) {
	t.Parallel()
	if got := (&OAuthTokenError{Err: errors.New("wrapped")}).Error(); got != "wrapped" {
		t.Fatalf("Err branch = %q", got)
	}
	if got := (&OAuthTokenError{OAuthError: "invalid_grant"}).Error(); got != "invalid_grant" {
		t.Fatalf("OAuthError branch = %q", got)
	}
	if got := (&OAuthTokenError{StatusCode: 503}).Error(); got != "oauth token error (status 503)" {
		t.Fatalf("status branch = %q", got)
	}
}

func TestProvisionErrorTypes(t *testing.T) {
	t.Parallel()
	reconnect := &ErrReconnectRequired{Provider: "kroger", Err: errors.New("no")}
	if reconnect.Error() != "no" {
		t.Fatalf("ErrReconnectRequired = %q", reconnect.Error())
	}
	if (&ErrReconnectRequired{Provider: "kroger"}).Error() == "" {
		t.Fatal("expected default reconnect message")
	}
	unavailable := &ErrProviderUnavailable{Provider: "kroger", Err: errors.New("down")}
	if unavailable.Error() != "down" {
		t.Fatalf("ErrProviderUnavailable = %q", unavailable.Error())
	}
}

func TestClassifyOAuthAccessError(t *testing.T) {
	t.Parallel()
	err := ClassifyOAuthAccessError("kroger", &OAuthTokenError{StatusCode: 400, OAuthError: "invalid_grant"})
	var reconnect *ErrReconnectRequired
	require.ErrorAs(t, err, &reconnect)
	err = ClassifyOAuthAccessError("kroger", &OAuthTokenError{StatusCode: 503, OAuthError: "server_error"})
	var unavailable *ErrProviderUnavailable
	require.ErrorAs(t, err, &unavailable)
	if ClassifyOAuthAccessError("kroger", nil) != nil {
		t.Fatal("nil err should stay nil")
	}
}

func TestOAuthFailureDetail(t *testing.T) {
	t.Parallel()
	if got := OAuthFailureDetail(ReasonReconnectRequired); got == "" {
		t.Fatal("reconnect detail should not be empty")
	}
	if got := OAuthFailureDetail(ReasonProviderUnavailable); got == "" {
		t.Fatal("unavailable detail should not be empty")
	}
}

func TestOAuthFailureReasonReauthRequired(t *testing.T) {
	if OAuthFailureReason(connection.ErrReauthRequired) != ReasonReconnectRequired {
		t.Fatalf("broker reauth = %q", OAuthFailureReason(connection.ErrReauthRequired))
	}
}
