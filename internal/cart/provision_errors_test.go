package cart

import (
	"errors"
	"testing"

	"github.com/Rhionin/stockbot/internal/cart/connection"
)

func TestOAuthFailureReason(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		err  error
		want OutcomeReason
	}{
		{
			name: "422 invalid_token reconnects without status 400",
			err:  &OAuthTokenError{StatusCode: 422, OAuthError: "invalid_token"},
			want: ReasonReconnectRequired,
		},
		{
			name: "422 access_denied reconnects",
			err:  &OAuthTokenError{StatusCode: 422, OAuthError: "access_denied"},
			want: ReasonReconnectRequired,
		},
		{
			name: "422 unauthorized_client reconnects",
			err:  &OAuthTokenError{StatusCode: 422, OAuthError: "unauthorized_client"},
			want: ReasonReconnectRequired,
		},
		{
			name: "200 with oauth error code reconnects",
			err:  &OAuthTokenError{StatusCode: 200, OAuthError: "invalid_token"},
			want: ReasonReconnectRequired,
		},
		{
			name: "status zero with oauth error reconnects",
			err:  &OAuthTokenError{StatusCode: 0, OAuthError: "invalid_token"},
			want: ReasonReconnectRequired,
		},
		{
			name: "broker reauth required",
			err:  connection.ErrReauthRequired,
			want: ReasonReconnectRequired,
		},
		{
			name: "invalid token reconnects without substring fallback",
			err:  &OAuthTokenError{StatusCode: 400, OAuthError: "invalid_token"},
			want: ReasonReconnectRequired,
		},
		{
			name: "access denied reconnects",
			err:  &OAuthTokenError{StatusCode: 400, OAuthError: "access_denied"},
			want: ReasonReconnectRequired,
		},
		{
			name: "unauthorized client reconnects",
			err:  &OAuthTokenError{StatusCode: 400, OAuthError: "unauthorized_client"},
			want: ReasonReconnectRequired,
		},
		{
			name: "401 with oauth error code reconnects",
			err:  &OAuthTokenError{StatusCode: 401, OAuthError: "invalid_token"},
			want: ReasonReconnectRequired,
		},
		{
			name: "403 with oauth error code reconnects",
			err:  &OAuthTokenError{StatusCode: 403, OAuthError: "access_denied"},
			want: ReasonReconnectRequired,
		},
		{
			name: "401 empty oauth error is try again",
			err:  &OAuthTokenError{StatusCode: 401, OAuthError: ""},
			want: ReasonProviderUnavailable,
		},
		{
			name: "5xx is try again",
			err:  &OAuthTokenError{StatusCode: 503, OAuthError: "server_error"},
			want: ReasonProviderUnavailable,
		},
		{
			name: "transport is try again",
			err:  &OAuthTokenError{Err: errors.New("connection reset")},
			want: ReasonProviderUnavailable,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := OAuthFailureReason(tt.err); got != tt.want {
				t.Fatalf("OAuthFailureReason() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestOAuthFailureReasonInvalidGrantWithoutTypedError(t *testing.T) {
	err := errors.New("oauth failure")
	if got := OAuthFailureReason(err); got != ReasonProviderUnavailable {
		t.Fatalf("untyped error = %q, want provider_unavailable", got)
	}
}
