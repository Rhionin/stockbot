package connection_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/cart/connection"
)

func newTestDirectory(t *testing.T) (*connection.Directory, *sql.DB) {
	t.Helper()
	conn, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("open in-memory SQLite: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := app.RunMigrations(conn); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}
	return connection.NewDirectory(conn), conn
}

func TestReadMissingProviderReturnsNil(t *testing.T) {
	dir, _ := newTestDirectory(t)

	got, err := dir.Read(context.Background(), "kroger")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got != nil {
		t.Fatalf("Read of missing provider: want nil, got %+v", got)
	}
}

func TestReadUnknownStateDefaultsToDisconnected(t *testing.T) {
	dir, db := newTestDirectory(t)
	ctx := context.Background()

	if _, err := db.ExecContext(ctx,
		`INSERT INTO provider_connections (id, user_id, provider_id, state) VALUES (?, ?, ?, ?)`,
		"user-1:kroger", "user-1", "kroger", "some_future_state"); err != nil {
		t.Fatalf("seed row: %v", err)
	}

	got, err := dir.Read(ctx, "kroger")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got.State != connection.StateDisconnected {
		t.Errorf("unrecognized stored state: want %q, got %q", connection.StateDisconnected, got.State)
	}
}

func TestWriteThenReadRoundTrips(t *testing.T) {
	dir, _ := newTestDirectory(t)
	ctx := context.Background()

	expiresAt := time.Date(2031, 6, 1, 12, 0, 0, 0, time.UTC)
	authStateAt := time.Date(2031, 5, 1, 8, 0, 0, 0, time.UTC)
	want := &connection.Connection{
		Provider:     "kroger",
		State:        connection.StateConnected,
		AccessToken:  "access-abc",
		RefreshToken: "refresh-xyz",
		ExpiresAt:    &expiresAt,
		AuthState:    "state-token",
		AuthStateAt:  &authStateAt,
	}
	if err := dir.Write(ctx, want); err != nil {
		t.Fatalf("Write: %v", err)
	}

	got, err := dir.Read(ctx, "kroger")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got == nil {
		t.Fatal("Read: want connection, got nil")
	}
	if got.Provider != "kroger" {
		t.Errorf("Provider: want %q, got %q", "kroger", got.Provider)
	}
	if got.State != connection.StateConnected {
		t.Errorf("State: want %q, got %q", connection.StateConnected, got.State)
	}
	if got.AccessToken != "access-abc" {
		t.Errorf("AccessToken: want %q, got %q", "access-abc", got.AccessToken)
	}
	if got.RefreshToken != "refresh-xyz" {
		t.Errorf("RefreshToken: want %q, got %q", "refresh-xyz", got.RefreshToken)
	}
	if got.ExpiresAt == nil || !got.ExpiresAt.Equal(expiresAt) {
		t.Errorf("ExpiresAt: want %v, got %v", expiresAt, got.ExpiresAt)
	}
	if got.AuthState != "state-token" {
		t.Errorf("AuthState: want %q, got %q", "state-token", got.AuthState)
	}
	if got.AuthStateAt == nil || !got.AuthStateAt.Equal(authStateAt) {
		t.Errorf("AuthStateAt: want %v, got %v", authStateAt, got.AuthStateAt)
	}
}

func TestWriteUpdatesExistingConnection(t *testing.T) {
	dir, _ := newTestDirectory(t)
	ctx := context.Background()

	if err := dir.Write(ctx, &connection.Connection{
		Provider:    "kroger",
		State:       connection.StateReauthRequired,
		AccessToken: "old-token",
	}); err != nil {
		t.Fatalf("first Write: %v", err)
	}
	if err := dir.Write(ctx, &connection.Connection{
		Provider:    "kroger",
		State:       connection.StateConnected,
		AccessToken: "new-token",
	}); err != nil {
		t.Fatalf("second Write: %v", err)
	}

	got, err := dir.Read(ctx, "kroger")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got.State != connection.StateConnected {
		t.Errorf("State after update: want %q, got %q", connection.StateConnected, got.State)
	}
	if got.AccessToken != "new-token" {
		t.Errorf("AccessToken after update: want %q, got %q", "new-token", got.AccessToken)
	}
}

func TestDisconnectClearsCredentials(t *testing.T) {
	dir, _ := newTestDirectory(t)
	ctx := context.Background()

	expiresAt := time.Date(2031, 6, 1, 12, 0, 0, 0, time.UTC)
	if err := dir.Write(ctx, &connection.Connection{
		Provider:     "kroger",
		State:        connection.StateConnected,
		AccessToken:  "access-abc",
		RefreshToken: "refresh-xyz",
		ExpiresAt:    &expiresAt,
		AuthState:    "state-token",
		AuthStateAt:  &expiresAt,
	}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	if err := dir.Disconnect(ctx, "kroger"); err != nil {
		t.Fatalf("Disconnect: %v", err)
	}

	got, err := dir.Read(ctx, "kroger")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got == nil {
		t.Fatal("Read after Disconnect: want row, got nil")
	}
	if got.State != connection.StateDisconnected {
		t.Errorf("State: want %q, got %q", connection.StateDisconnected, got.State)
	}
	if got.AccessToken != "" {
		t.Errorf("AccessToken: want empty, got %q", got.AccessToken)
	}
	if got.RefreshToken != "" {
		t.Errorf("RefreshToken: want empty, got %q", got.RefreshToken)
	}
	if got.ExpiresAt != nil {
		t.Errorf("ExpiresAt: want nil, got %v", got.ExpiresAt)
	}
	if got.AuthState != "" {
		t.Errorf("AuthState: want empty, got %q", got.AuthState)
	}
	if got.AuthStateAt != nil {
		t.Errorf("AuthStateAt: want nil, got %v", got.AuthStateAt)
	}
}

func TestDisconnectLeavesOtherProviderUntouched(t *testing.T) {
	dir, _ := newTestDirectory(t)
	ctx := context.Background()

	if err := dir.Write(ctx, &connection.Connection{
		Provider: "kroger", State: connection.StateConnected, AccessToken: "kroger-token",
	}); err != nil {
		t.Fatalf("Write kroger: %v", err)
	}
	if err := dir.Write(ctx, &connection.Connection{
		Provider: "instacart", State: connection.StateConnected, AccessToken: "instacart-token",
	}); err != nil {
		t.Fatalf("Write instacart: %v", err)
	}

	if err := dir.Disconnect(ctx, "kroger"); err != nil {
		t.Fatalf("Disconnect: %v", err)
	}

	other, err := dir.Read(ctx, "instacart")
	if err != nil {
		t.Fatalf("Read instacart: %v", err)
	}
	if other.State != connection.StateConnected || other.AccessToken != "instacart-token" {
		t.Errorf("instacart connection changed: %+v", other)
	}
}

func TestGenerateAuthStatePersistsOneStatePerProvider(t *testing.T) {
	dir, _ := newTestDirectory(t)
	ctx := context.Background()

	if err := dir.Write(ctx, &connection.Connection{Provider: "kroger", State: connection.StateDisconnected}); err != nil {
		t.Fatalf("Write kroger: %v", err)
	}
	if err := dir.Write(ctx, &connection.Connection{Provider: "instacart", State: connection.StateDisconnected}); err != nil {
		t.Fatalf("Write instacart: %v", err)
	}

	before := time.Now().UTC()
	if err := dir.GenerateAuthState(ctx, "kroger", "abc-state"); err != nil {
		t.Fatalf("GenerateAuthState: %v", err)
	}
	after := time.Now().UTC()

	got, err := dir.Read(ctx, "kroger")
	if err != nil {
		t.Fatalf("Read kroger: %v", err)
	}
	if got.AuthState != "abc-state" {
		t.Errorf("AuthState: want %q, got %q", "abc-state", got.AuthState)
	}
	if got.AuthStateAt == nil {
		t.Fatal("AuthStateAt: want a generation time, got nil")
	}
	if got.AuthStateAt.Before(before.Add(-time.Second)) || got.AuthStateAt.After(after.Add(time.Second)) {
		t.Errorf("AuthStateAt %v not within [%v, %v]", got.AuthStateAt, before, after)
	}

	other, err := dir.Read(ctx, "instacart")
	if err != nil {
		t.Fatalf("Read instacart: %v", err)
	}
	if other.AuthState != "" || other.AuthStateAt != nil {
		t.Errorf("instacart auth state should be untouched, got AuthState=%q AuthStateAt=%v", other.AuthState, other.AuthStateAt)
	}
}

func TestConsumeAuthState(t *testing.T) {
	tests := []struct {
		name       string
		stored     string
		storedAgo  time.Duration
		consumeArg string
		want       bool
	}{
		{name: "matching non-expired state accepted", stored: "match-state", storedAgo: 30 * time.Second, consumeArg: "match-state", want: true},
		{name: "mismatched state rejected", stored: "stored-state", storedAgo: 30 * time.Second, consumeArg: "wrong-state", want: false},
		{name: "state older than 600s rejected", stored: "old-state", storedAgo: 601 * time.Second, consumeArg: "old-state", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir, _ := newTestDirectory(t)
			ctx := context.Background()

			storedAt := time.Now().UTC().Add(-tt.storedAgo)
			if err := dir.Write(ctx, &connection.Connection{
				Provider:    "kroger",
				State:       connection.StateDisconnected,
				AuthState:   tt.stored,
				AuthStateAt: &storedAt,
			}); err != nil {
				t.Fatalf("Write: %v", err)
			}

			got, err := dir.ConsumeAuthState(ctx, "kroger", tt.consumeArg)
			if err != nil {
				t.Fatalf("ConsumeAuthState: %v", err)
			}
			if got != tt.want {
				t.Errorf("ConsumeAuthState: want %v, got %v", tt.want, got)
			}

			after, err := dir.Read(ctx, "kroger")
			if err != nil {
				t.Fatalf("Read after consume: %v", err)
			}
			if after.AuthState != "" || after.AuthStateAt != nil {
				t.Errorf("state not discarded: AuthState=%q AuthStateAt=%v", after.AuthState, after.AuthStateAt)
			}
		})
	}
}

func TestConsumeAuthStateAbsentProviderRejected(t *testing.T) {
	dir, _ := newTestDirectory(t)

	got, err := dir.ConsumeAuthState(context.Background(), "kroger", "any-state")
	if err != nil {
		t.Fatalf("ConsumeAuthState: %v", err)
	}
	if got {
		t.Error("ConsumeAuthState on absent provider: want false, got true")
	}
}

func TestConsumeAuthStateEmptyArgAgainstStoredRejected(t *testing.T) {
	dir, _ := newTestDirectory(t)
	ctx := context.Background()

	storedAt := time.Now().UTC()
	if err := dir.Write(ctx, &connection.Connection{
		Provider: "kroger", State: connection.StateDisconnected,
		AuthState: "real-state", AuthStateAt: &storedAt,
	}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	got, err := dir.ConsumeAuthState(ctx, "kroger", "")
	if err != nil {
		t.Fatalf("ConsumeAuthState: %v", err)
	}
	if got {
		t.Error("ConsumeAuthState with empty state against a stored state: want false, got true")
	}

	after, err := dir.Read(ctx, "kroger")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if after.AuthState != "" || after.AuthStateAt != nil {
		t.Errorf("state not discarded on rejection: AuthState=%q AuthStateAt=%v", after.AuthState, after.AuthStateAt)
	}
}
