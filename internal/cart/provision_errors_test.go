package cart

import (
	"errors"
	"testing"
)

func TestOAuthFailureReason(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		err  error
		want OutcomeReason
	}{
		{
			name: "invalid grant reconnects",
			err:  &OAuthTokenError{StatusCode: 400, OAuthError: "invalid_grant"},
			want: ReasonReconnectRequired,
		},
		{
			name: "transport is try again",
			err:  &OAuthTokenError{Err: errors.New("connection reset")},
			want: ReasonProviderUnavailable,
		},
		{
			name: "wrapped invalid grant",
			err:  fmtWrap(errors.New("oauth: invalid_grant")),
			want: ReasonReconnectRequired,
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

func fmtWrap(err error) error {
	return &OAuthTokenError{Err: err}
}
