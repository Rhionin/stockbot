package connection_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/cart/connection"
	"github.com/Rhionin/stockbot/internal/tenant"
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

func TestAccessTokenReauthRequiredIsScopedPerHousehold(t *testing.T) {
	dir, db, primaryScope := newTestDirectory(t)
	ctx := context.Background()
	const cedarID = "0190bbbb-cccc-7000-8000-0000000000e5"
	if _, err := db.Exec(`
		INSERT INTO households (id, name, restock_mode, created_at, channel_key, is_primary)
		VALUES (?, 'Household Cedar', '', '2026-02-02T00:00:00Z', 'abcdef0123456789abcdef0123456789', 0)`, cedarID); err != nil {
		t.Fatalf("insert cedar household: %v", err)
	}
	cedarScope, err := tenant.New(cedarID)
	if err != nil {
		t.Fatal(err)
	}
	if err := dir.Write(ctx, primaryScope, &connection.Connection{
		Provider:     "kroger",
		State:        connection.StateReauthRequired,
		RefreshToken: "refresh-primary",
	}); err != nil {
		t.Fatalf("Write primary: %v", err)
	}
	expiresAt := time.Now().UTC().Add(time.Hour)
	if err := dir.Write(ctx, cedarScope, &connection.Connection{
		Provider:     "kroger",
		State:        connection.StateConnected,
		AccessToken:  "cedar-token",
		RefreshToken: "refresh-cedar",
		ExpiresAt:    &expiresAt,
	}); err != nil {
		t.Fatalf("Write cedar: %v", err)
	}

	broker := connection.NewTokenBroker(dir)
	got, err := broker.AccessToken(ctx, cedarScope, "kroger", func(string) (string, string, time.Duration, error) {
		t.Fatal("refresh should not run for cedar while primary is reauth_required")
		return "", "", 0, nil
	})
	if err != nil {
		t.Fatalf("AccessToken cedar: %v", err)
	}
	if got != "cedar-token" {
		t.Fatalf("AccessToken cedar = %q, want cedar-token", got)
	}
	cedarConn, err := dir.Read(ctx, cedarScope, "kroger")
	if err != nil {
		t.Fatal(err)
	}
	if cedarConn.State != connection.StateConnected {
		t.Fatalf("cedar connection state = %q, want connected", cedarConn.State)
	}
}

func TestForceRefreshExchangesEvenWhenAccessTokenFresh(t *testing.T) {
	dir, _, scope := newTestDirectory(t)
	ctx := context.Background()
	expiresAt := time.Now().UTC().Add(time.Hour)
	if err := dir.Write(ctx, scope, &connection.Connection{
		Provider:     "kroger",
		State:        connection.StateConnected,
		AccessToken:  "still-valid",
		RefreshToken: "refresh-1",
		ExpiresAt:    &expiresAt,
	}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	broker := connection.NewTokenBroker(dir)
	called := false
	got, err := broker.ForceRefresh(ctx, scope, "kroger", func(refreshToken string) (string, string, time.Duration, error) {
		called = true
		if refreshToken != "refresh-1" {
			t.Fatalf("refresh token = %q, want refresh-1", refreshToken)
		}
		return "rotated", "refresh-2", time.Hour, nil
	})
	if err != nil {
		t.Fatalf("ForceRefresh: %v", err)
	}
	if !called || got != "rotated" {
		t.Fatalf("ForceRefresh = %q, called=%v", got, called)
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

func TestForceRefreshWithoutRefreshToken(t *testing.T) {
	dir, _, scope := newTestDirectory(t)
	ctx := context.Background()
	if err := dir.Write(ctx, scope, &connection.Connection{
		Provider: "kroger", State: connection.StateConnected, AccessToken: "tok",
	}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	broker := connection.NewTokenBroker(dir)
	_, err := broker.ForceRefresh(ctx, scope, "kroger", func(string) (string, string, time.Duration, error) {
		t.Fatal("refresh should not run")
		return "", "", 0, nil
	})
	if err == nil {
		t.Fatal("ForceRefresh error = nil, want missing refresh token")
	}
}

func TestMarkReauthRequiredWithNilConnection(t *testing.T) {
	dir, _, scope := newTestDirectory(t)
	ctx := context.Background()
	broker := connection.NewTokenBroker(dir)
	if err := broker.MarkReauthRequired(ctx, scope, "kroger", nil); err != nil {
		t.Fatalf("MarkReauthRequired: %v", err)
	}
	conn, err := dir.Read(ctx, scope, "kroger")
	if err != nil {
		t.Fatal(err)
	}
	if conn.State != connection.StateReauthRequired || conn.RefreshToken != "" {
		t.Fatalf("conn = %+v", conn)
	}
}
