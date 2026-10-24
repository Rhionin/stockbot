package connection

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Rhionin/stockbot/internal/tenant"
)

// TokenBroker implements result-sharing single flight for token refresh.
// Every waiting caller receives the token the one in-progress exchange produced.
type TokenBroker struct {
	mu        sync.Mutex
	inFlight  map[string]*refreshCall
	Directory *Directory
}

// refreshCall holds a pending token exchange.
type refreshCall struct {
	done         chan struct{}
	err          error
	accessToken  string
	refreshToken string
	expiresIn    time.Duration
	receiptAt    time.Time
}

// NewTokenBroker creates a new TokenBroker.
func NewTokenBroker(dir *Directory) *TokenBroker {
	return &TokenBroker{
		inFlight:  make(map[string]*refreshCall),
		Directory: dir,
	}
}

// AccessToken returns the current access token for the provider, refreshing it if needed.
// Refresh if and only if the remaining access-token lifetime is 60 seconds or less,
// or no access token is persisted.
// The key is ProviderID, so a slow provider's flight blocks no request to another provider.
func (b *TokenBroker) AccessToken(ctx context.Context, scope tenant.Scope, provider string, refreshFn func(refreshToken string) (accessToken string, refreshTokenOut string, expiresIn time.Duration, err error)) (string, error) {
	if err := scope.Require(); err != nil {
		return "", err
	}
	b.mu.Lock()
	if call, ok := b.inFlight[provider]; ok {
		// Another request is already refreshing this provider's token
		b.mu.Unlock()
		<-call.done
		if call.err != nil {
			return "", call.err
		}
		return call.accessToken, nil
	}

	// Check if we need to refresh (single-flight setup)
	// Read the connection record
	conn, err := b.Directory.Read(ctx, scope, provider)
	if err != nil {
		b.mu.Unlock()
		return "", err
	}

	// Check if we have a valid token or need refresh
	shouldRefresh := true
	if conn != nil && conn.AccessToken != "" && conn.ExpiresAt != nil {
		remaining := conn.ExpiresAt.Sub(time.Now().UTC())
		if remaining > 60*time.Second {
			shouldRefresh = false
		}
	}

	if !shouldRefresh {
		// Return existing token
		b.mu.Unlock()
		return conn.AccessToken, nil
	}

	// Start a new in-flight call
	call := &refreshCall{done: make(chan struct{})}
	b.inFlight[provider] = call
	b.mu.Unlock()

	// Perform the refresh
	accessToken, refreshToken, expiresIn, err := refreshFn(conn.RefreshToken)

	// Populate the result before closing done: waiting callers read these fields
	// as soon as the channel closes, so the writes must happen-before the close.
	call.accessToken = accessToken
	call.err = err
	call.refreshToken = refreshToken
	call.expiresIn = expiresIn
	call.receiptAt = time.Now().UTC()

	b.mu.Lock()
	delete(b.inFlight, provider)
	b.mu.Unlock()

	close(call.done)

	if err != nil {
		return "", err
	}

	// Update the connection record with new tokens
	expiresAt := call.receiptAt.Add(call.expiresIn)
	newConn := &Connection{
		Provider:     provider,
		State:        StateConnected,
		AccessToken:  call.accessToken,
		RefreshToken: call.refreshToken,
		ExpiresAt:    &expiresAt,
	}
	if err := b.Directory.Write(ctx, scope, newConn); err != nil {
		return "", fmt.Errorf("failed to write connection: %w", err)
	}

	return call.accessToken, nil
}
