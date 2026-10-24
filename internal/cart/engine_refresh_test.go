package cart

import (
	"context"
	"errors"
	"testing"
	"time"
)

type plainProvider struct{}

func (plainProvider) ID() ProviderID             { return "plain" }
func (plainProvider) DisplayName() string        { return "Plain" }
func (plainProvider) Capabilities() Capabilities { return Capabilities{} }

type refreshProvider struct {
	set TokenSet
	err error
}

func (refreshProvider) ID() ProviderID      { return "refresh" }
func (refreshProvider) DisplayName() string { return "Refresh" }
func (refreshProvider) Capabilities() Capabilities {
	return Capabilities{Auth: AuthOAuth2}
}
func (refreshProvider) AuthorizationScope() string { return "cart.basic" }
func (refreshProvider) AuthorizationURL(string) (string, error) {
	return "https://example.com/auth", nil
}
func (refreshProvider) ExchangeCode(context.Context, string) (TokenSet, error) {
	return TokenSet{}, nil
}
func (p refreshProvider) RefreshAccessToken(context.Context, string) (TokenSet, error) {
	return p.set, p.err
}

func TestExchangeRefreshUsesTheProviderFlow(t *testing.T) {
	engine := NewEngine(NewRegistry(), nil, nil)
	refresh := engine.exchangeRefresh(context.Background(), plainProvider{})
	if _, _, _, err := refresh("old"); err == nil {
		t.Fatal("a provider without a refresh flow was accepted")
	}

	want := TokenSet{AccessToken: "access", RefreshToken: "next", ExpiresIn: time.Minute}
	refresh = engine.exchangeRefresh(context.Background(), refreshProvider{set: want})
	access, next, expires, err := refresh("old")
	if err != nil {
		t.Fatal(err)
	}
	if access != want.AccessToken || next != want.RefreshToken || expires != want.ExpiresIn {
		t.Fatalf("tokens = %s %s %s", access, next, expires)
	}

	refresh = engine.exchangeRefresh(context.Background(), refreshProvider{err: errors.New("the store declined the refresh")})
	if _, _, _, err := refresh("old"); err == nil || err.Error() != "the store declined the refresh" {
		t.Fatalf("refresh error = %v", err)
	}
}
