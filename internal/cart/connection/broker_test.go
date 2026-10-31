package connection_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/cart/connection"
)

func TestAccessTokenReturnsPersistedTokenWhenFresh(t *testing.T) {
	dir, _, scope := newTestDirectory(t)
	ctx := context.Background()

	expiresAt := time.Now().UTC().Add(30 * time.Minute)
	if err := dir.Write(ctx, scope, &connection.Connection{
		Provider:     "kroger",
		State:        connection.StateConnected,
		AccessToken:  "fresh-token",
		RefreshToken: "refresh-1",
		ExpiresAt:    &expiresAt,
	}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	broker := connection.NewTokenBroker(dir)
	refreshed := false
	got, err := broker.AccessToken(ctx, scope, "kroger", func(string) (string, string, time.Duration, error) {
		refreshed = true
		return "should-not-run", "", 0, nil
	})
	if err != nil {
		t.Fatalf("AccessToken: %v", err)
	}
	if got != "fresh-token" {
		t.Errorf("AccessToken: want %q, got %q", "fresh-token", got)
	}
	if refreshed {
		t.Error("refresh ran for a token with 30 minutes of lifetime left")
	}
}

func TestAccessTokenRefreshesWhenNearExpiry(t *testing.T) {
	dir, _, scope := newTestDirectory(t)
	ctx := context.Background()

	expiresAt := time.Now().UTC().Add(45 * time.Second)
	if err := dir.Write(ctx, scope, &connection.Connection{
		Provider:     "kroger",
		State:        connection.StateConnected,
		AccessToken:  "stale-token",
		RefreshToken: "refresh-1",
		ExpiresAt:    &expiresAt,
	}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	broker := connection.NewTokenBroker(dir)
	var gotRefreshArg string
	got, err := broker.AccessToken(ctx, scope, "kroger", func(refreshToken string) (string, string, time.Duration, error) {
		gotRefreshArg = refreshToken
		return "renewed-token", "refresh-2", time.Hour, nil
	})
	if err != nil {
		t.Fatalf("AccessToken: %v", err)
	}
	if got != "renewed-token" {
		t.Errorf("AccessToken: want %q, got %q", "renewed-token", got)
	}
	if gotRefreshArg != "refresh-1" {
		t.Errorf("refresh callback received %q, want the persisted refresh token %q", gotRefreshArg, "refresh-1")
	}

	persisted, err := dir.Read(ctx, scope, "kroger")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if persisted.AccessToken != "renewed-token" {
		t.Errorf("persisted AccessToken: want %q, got %q", "renewed-token", persisted.AccessToken)
	}
	if persisted.RefreshToken != "refresh-2" {
		t.Errorf("persisted RefreshToken: want %q, got %q", "refresh-2", persisted.RefreshToken)
	}
	if persisted.State != connection.StateConnected {
		t.Errorf("persisted State: want %q, got %q", connection.StateConnected, persisted.State)
	}
	if persisted.ExpiresAt == nil || persisted.ExpiresAt.Before(time.Now().UTC().Add(30*time.Minute)) {
		t.Errorf("persisted ExpiresAt: want ~1h out, got %v", persisted.ExpiresAt)
	}
}

func TestAccessTokenRefreshesWhenNoTokenPersisted(t *testing.T) {
	dir, _, scope := newTestDirectory(t)
	ctx := context.Background()

	if err := dir.Write(ctx, scope, &connection.Connection{
		Provider:     "kroger",
		State:        connection.StateConnected,
		RefreshToken: "refresh-1",
	}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	broker := connection.NewTokenBroker(dir)
	got, err := broker.AccessToken(ctx, scope, "kroger", func(string) (string, string, time.Duration, error) {
		return "first-token", "refresh-2", time.Hour, nil
	})
	if err != nil {
		t.Fatalf("AccessToken: %v", err)
	}
	if got != "first-token" {
		t.Errorf("AccessToken: want %q, got %q", "first-token", got)
	}

	persisted, err := dir.Read(ctx, scope, "kroger")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if persisted.AccessToken != "first-token" {
		t.Errorf("persisted AccessToken: want %q, got %q", "first-token", persisted.AccessToken)
	}
}

func TestAccessTokenFailsFastWhenReauthRequired(t *testing.T) {
	dir, _, scope := newTestDirectory(t)
	ctx := context.Background()

	if err := dir.Write(ctx, scope, &connection.Connection{
		Provider:     "kroger",
		State:        connection.StateReauthRequired,
		RefreshToken: "refresh-1",
	}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	broker := connection.NewTokenBroker(dir)
	called := false
	_, err := broker.AccessToken(ctx, scope, "kroger", func(string) (string, string, time.Duration, error) {
		called = true
		return "first-token", "refresh-2", time.Hour, nil
	})
	if !errors.Is(err, connection.ErrReauthRequired) {
		t.Fatalf("AccessToken error = %v, want ErrReauthRequired", err)
	}
	if called {
		t.Fatal("refresh ran while connection is reauth_required")
	}
}

func TestAccessTokenSharesOneRefreshAcrossConcurrentCallers(t *testing.T) {
	dir, _, scope := newTestDirectory(t)
	ctx := context.Background()

	expired := time.Now().UTC().Add(-time.Hour)
	if err := dir.Write(ctx, scope, &connection.Connection{
		Provider: "kroger", State: connection.StateConnected, RefreshToken: "refresh-1",
		AccessToken: "stale", ExpiresAt: &expired,
	}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	broker := connection.NewTokenBroker(dir)
	release := make(chan struct{})
	entered := make(chan struct{})
	secondQueued := make(chan struct{})
	var calls int32
	refreshFn := func(string) (string, string, time.Duration, error) {
		atomic.AddInt32(&calls, 1)
		close(entered)
		<-secondQueued
		<-release
		return "shared-token", "refresh-2", time.Hour, nil
	}

	var wg sync.WaitGroup
	results := make(chan string, 2)
	wg.Add(1)
	go func() {
		defer wg.Done()
		tok, err := broker.AccessToken(ctx, scope, "kroger", refreshFn)
		if err != nil {
			t.Errorf("first AccessToken: %v", err)
		}
		results <- tok
	}()

	<-entered
	wg.Add(1)
	go func() {
		defer wg.Done()
		tok, err := broker.AccessToken(ctx, scope, "kroger", func(string) (string, string, time.Duration, error) {
			t.Error("second caller ran its own refresh instead of sharing the in-flight one")
			return "", "", 0, nil
		})
		if err != nil {
			t.Errorf("second AccessToken: %v", err)
		}
		results <- tok
	}()

	time.Sleep(20 * time.Millisecond)
	close(secondQueued)
	close(release)
	wg.Wait()
	close(results)
	for got := range results {
		if got != "shared-token" {
			t.Errorf("caller: want %q, got %q", "shared-token", got)
		}
	}
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Errorf("refresh ran %d times, want exactly 1 shared exchange", n)
	}
}

func TestAccessTokenPropagatesRefreshError(t *testing.T) {
	dir, _, scope := newTestDirectory(t)
	ctx := context.Background()

	expired := time.Now().UTC().Add(-time.Hour)
	if err := dir.Write(ctx, scope, &connection.Connection{
		Provider: "kroger", State: connection.StateConnected, RefreshToken: "refresh-1",
		AccessToken: "stale", ExpiresAt: &expired,
	}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	broker := connection.NewTokenBroker(dir)
	wantErr := errors.New("provider rejected refresh")
	got, err := broker.AccessToken(ctx, scope, "kroger", func(string) (string, string, time.Duration, error) {
		return "", "", 0, wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Errorf("AccessToken error: want %v, got %v", wantErr, err)
	}
	if got != "" {
		t.Errorf("AccessToken on error: want empty token, got %q", got)
	}
}
