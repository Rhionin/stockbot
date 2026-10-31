package cart

import "fmt"

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
