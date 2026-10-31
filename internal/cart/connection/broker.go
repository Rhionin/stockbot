package connection

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Rhionin/stockbot/internal/tenant"
)

// ErrReauthRequired means the stored connection must be renewed before any token call.
var ErrReauthRequired = errors.New("provider reauthorization required")

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
		b.mu.Unlock()
		return waitRefreshCall(call)
	}

	conn, err := b.Directory.Read(ctx, scope, provider)
	if err != nil {
		b.mu.Unlock()
		return "", err
	}
	if conn != nil && conn.State == StateReauthRequired {
		b.mu.Unlock()
		return "", ErrReauthRequired
	}

	if conn != nil && conn.AccessToken != "" && conn.ExpiresAt != nil {
		remaining := conn.ExpiresAt.Sub(time.Now().UTC())
		if remaining > 60*time.Second {
			b.mu.Unlock()
			return conn.AccessToken, nil
		}
	}

	b.mu.Unlock()
	return b.runRefreshExchange(ctx, scope, provider, conn, refreshFn)
}

// ForceRefresh exchanges the refresh token even when the persisted access token is still valid.
// Used after a cart API 401/403 to retry the same batch at most once per send.
func (b *TokenBroker) ForceRefresh(ctx context.Context, scope tenant.Scope, provider string, refreshFn func(refreshToken string) (accessToken string, refreshTokenOut string, expiresIn time.Duration, err error)) (string, error) {
	if err := scope.Require(); err != nil {
		return "", err
	}
	b.mu.Lock()
	if call, ok := b.inFlight[provider]; ok {
		b.mu.Unlock()
		return waitRefreshCall(call)
	}
	conn, err := b.Directory.Read(ctx, scope, provider)
	if err != nil {
		b.mu.Unlock()
		return "", err
	}
	if conn != nil && conn.State == StateReauthRequired {
		b.mu.Unlock()
		return "", ErrReauthRequired
	}
	if conn == nil || conn.RefreshToken == "" {
		b.mu.Unlock()
		return "", fmt.Errorf("no refresh token stored for provider %q", provider)
	}
	b.mu.Unlock()
	return b.runRefreshExchange(ctx, scope, provider, conn, refreshFn)
}

func waitRefreshCall(call *refreshCall) (string, error) {
	<-call.done
	if call.err != nil {
		return "", call.err
	}
	return call.accessToken, nil
}

func (b *TokenBroker) runRefreshExchange(ctx context.Context, scope tenant.Scope, provider string, conn *Connection, refreshFn func(refreshToken string) (accessToken string, refreshTokenOut string, expiresIn time.Duration, err error)) (string, error) {
	b.mu.Lock()
	if call, ok := b.inFlight[provider]; ok {
		b.mu.Unlock()
		return waitRefreshCall(call)
	}
	call := &refreshCall{done: make(chan struct{})}
	b.inFlight[provider] = call
	b.mu.Unlock()

	var refreshToken string
	if conn != nil {
		refreshToken = conn.RefreshToken
	}
	accessToken, refreshTokenOut, expiresIn, err := refreshFn(refreshToken)
	call.accessToken = accessToken
	call.err = err
	call.refreshToken = refreshTokenOut
	call.expiresIn = expiresIn
	call.receiptAt = time.Now().UTC()

	b.mu.Lock()
	delete(b.inFlight, provider)
	b.mu.Unlock()
	close(call.done)

	if err != nil {
		return "", err
	}

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

// MarkReauthRequired persists reauth_required so later sends fail fast without calling the token endpoint.
func (b *TokenBroker) MarkReauthRequired(ctx context.Context, scope tenant.Scope, provider string, conn *Connection) error {
	if err := scope.Require(); err != nil {
		return err
	}
	var refreshToken string
	if conn != nil {
		refreshToken = conn.RefreshToken
	}
	return b.Directory.Write(ctx, scope, &Connection{
		Provider:     provider,
		State:        StateReauthRequired,
		RefreshToken: refreshToken,
	})
}
