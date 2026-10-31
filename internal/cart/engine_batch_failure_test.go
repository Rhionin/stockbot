package cart

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBatchFailureNeedsReconnect(t *testing.T) {
	t.Parallel()
	if !batchFailureNeedsReconnect(ProvisionResult{Status: http.StatusUnauthorized}, nil) {
		t.Fatal("401 status should need reconnect")
	}
	if !batchFailureNeedsReconnect(ProvisionResult{}, &OAuthTokenError{StatusCode: 400, OAuthError: "invalid_grant"}) {
		t.Fatal("invalid_grant token error should need reconnect")
	}
	if batchFailureNeedsReconnect(ProvisionResult{Status: 503}, errors.New("down")) {
		t.Fatal("503 alone should not need reconnect")
	}
}

func TestBatchFailureIsIndeterminate(t *testing.T) {
	t.Parallel()
	if !batchFailureIsIndeterminate(ProvisionResult{Disposition: DispositionIndeterminate}, nil) {
		t.Fatal("indeterminate disposition should be indeterminate")
	}
	if !batchFailureIsIndeterminate(ProvisionResult{Status: 503}, errors.New("upstream")) {
		t.Fatal("503 should be indeterminate")
	}
	if !batchFailureIsIndeterminate(ProvisionResult{Status: 0}, errors.New("timeout")) {
		t.Fatal("status 0 should be indeterminate")
	}
	if batchFailureIsIndeterminate(ProvisionResult{Status: http.StatusUnauthorized}, errors.New("401")) {
		t.Fatal("401 should not be indeterminate")
	}
}

func TestWholeRequestProvisionError(t *testing.T) {
	t.Parallel()
	if wholeRequestProvisionError("kroger", 1, ProvisionResult{}, errors.New("x")) != nil {
		t.Fatal("confirmedCount > 0 should not be whole-request error")
	}
	err := wholeRequestProvisionError("kroger", 0, ProvisionResult{Status: http.StatusUnauthorized}, errors.New("401"))
	var reconnect *ErrReconnectRequired
	require.ErrorAs(t, err, &reconnect)
	err = wholeRequestProvisionError("kroger", 0, ProvisionResult{}, &OAuthTokenError{Err: errors.New("reset")})
	var unavailable *ErrProviderUnavailable
	require.ErrorAs(t, err, &unavailable)
	if wholeRequestProvisionError("kroger", 0, ProvisionResult{Status: 503}, errors.New("503")) != nil {
		t.Fatal("pre-send 503 on PUT should return per-line report, not whole-request error")
	}
}

func TestMarkConnectionReauthRequiredNoBroker(t *testing.T) {
	env := newProvisionEnv(t)
	engine := NewEngine(env.registry, env.ledger, nil)
	if err := engine.markConnectionReauthRequired(context.Background(), env.scope, "fake-none"); err != nil {
		t.Fatalf("nil broker: %v", err)
	}
}
