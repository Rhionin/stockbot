// Package connection provides data access for provider connections.
// A second package rather than a second type in internal/cart,
// precisely because of the one-data-access-type-per-package rule:
// the ledger and the connection records are two persisted concepts.
package connection

import (
	"context"
	"database/sql"
	"time"

	"github.com/Rhionin/stockbot/internal/tenant"
)

// ConnectionState is the connection state of one provider.
type ConnectionState string

const (
	StateNotRequired    ConnectionState = "not_required"
	StateConnected      ConnectionState = "connected"
	StateReauthRequired ConnectionState = "reauth_required"
	StateDisconnected   ConnectionState = "disconnected"
)

// Connection is server-side only. No JSON tags: this type is never marshalled.
type Connection struct {
	Provider     string
	State        ConnectionState
	AccessToken  string
	RefreshToken string
	ExpiresAt    *time.Time
	AuthState    string
	AuthStateAt  *time.Time
}

// Directory is the data-access type for provider connections.
type Directory struct {
	db *sql.DB
}

// NewDirectory creates a new Directory backed by the given database.
func NewDirectory(db *sql.DB) *Directory {
	return &Directory{db: db}
}

// Read returns the connection record for one provider, or (nil, nil) if not found.
func (d *Directory) Read(ctx context.Context, scope tenant.Scope, provider string) (*Connection, error) {
	if err := scope.Require(); err != nil {
		return nil, err
	}
	var (
		state        string
		accessToken  sql.NullString
		refreshToken sql.NullString
		expiresAt    sql.NullTime
		authState    sql.NullString
		authStateAt  sql.NullTime
	)

	err := d.db.QueryRowContext(ctx,
		`SELECT state, access_token, refresh_token, access_token_expires_at, auth_state, auth_state_at
		 FROM provider_connections
		 WHERE household_id = ? AND provider_id = ?`, scope.HouseholdID(), provider).Scan(
		&state, &accessToken, &refreshToken, &expiresAt, &authState, &authStateAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var stateEnum ConnectionState
	switch ConnectionState(state) {
	case StateNotRequired, StateConnected, StateReauthRequired, StateDisconnected:
		stateEnum = ConnectionState(state)
	default:
		// Default to disconnected for unknown states
		stateEnum = StateDisconnected
	}

	var expiresAtPtr *time.Time
	if expiresAt.Valid {
		expiresAtPtr = &expiresAt.Time
	}

	var authStateAtPtr *time.Time
	if authStateAt.Valid {
		authStateAtPtr = &authStateAt.Time
	}

	return &Connection{
		Provider:     provider,
		State:        stateEnum,
		AccessToken:  accessToken.String,
		RefreshToken: refreshToken.String,
		ExpiresAt:    expiresAtPtr,
		AuthState:    authState.String,
		AuthStateAt:  authStateAtPtr,
	}, nil
}

// Write creates or updates the connection record for one provider.
func (d *Directory) Write(ctx context.Context, scope tenant.Scope, conn *Connection) error {
	if err := scope.Require(); err != nil {
		return err
	}
	householdID := scope.HouseholdID()
	_, err := d.db.ExecContext(ctx,
		`INSERT INTO provider_connections (id, household_id, user_id, provider_id, state, access_token, refresh_token, access_token_expires_at, auth_state, auth_state_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(user_id, provider_id) DO UPDATE SET
		     state = excluded.state,
		     access_token = excluded.access_token,
		     refresh_token = excluded.refresh_token,
		     access_token_expires_at = excluded.access_token_expires_at,
		     auth_state = excluded.auth_state,
		     auth_state_at = excluded.auth_state_at
		 WHERE provider_connections.household_id = excluded.household_id`,
		householdID+":"+conn.Provider, householdID, householdID, conn.Provider,
		string(conn.State), conn.AccessToken, conn.RefreshToken,
		conn.ExpiresAt, conn.AuthState, conn.AuthStateAt)
	return err
}

// Disconnect clears the credentials for one provider and leaves the state as disconnected.
// Every ledger entry is left unchanged (requirement 4.3).
func (d *Directory) Disconnect(ctx context.Context, scope tenant.Scope, provider string) error {
	if err := scope.Require(); err != nil {
		return err
	}
	_, err := d.db.ExecContext(ctx,
		`UPDATE provider_connections SET
		     state = ?, access_token = NULL, refresh_token = NULL,
		     access_token_expires_at = NULL, auth_state = NULL, auth_state_at = NULL
		 WHERE household_id = ? AND provider_id = ?`,
		StateDisconnected, scope.HouseholdID(), provider)
	return err
}

// GenerateAuthState creates a new authorization state for the provider.
// Generate at least 32 characters from an injected io.Reader (crypto/rand in production).
func (d *Directory) GenerateAuthState(ctx context.Context, scope tenant.Scope, provider, state string) error {
	if err := scope.Require(); err != nil {
		return err
	}
	now := time.Now().UTC()
	householdID := scope.HouseholdID()
	// Insert a disconnected row when this is the first authorization attempt,
	// and on conflict touch only the single-use state so a refresh token
	// already stored for this provider is left in place.
	_, err := d.db.ExecContext(ctx,
		`INSERT INTO provider_connections (id, household_id, user_id, provider_id, state, auth_state, auth_state_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(user_id, provider_id) DO UPDATE SET
		     auth_state = excluded.auth_state,
		     auth_state_at = excluded.auth_state_at
		 WHERE provider_connections.household_id = excluded.household_id`,
		householdID+":"+provider, householdID, householdID, provider, StateDisconnected, state, now)
	return err
}

// ConsumeAuthState verifies and consumes an authorization state.
// Returns true if valid, false if rejected.
// Reject an absent or mismatched state, reject past 600 seconds, reject a
// callback carrying an error parameter, and discard the state in every case.
func (d *Directory) ConsumeAuthState(ctx context.Context, scope tenant.Scope, provider, state string) (bool, error) {
	if err := scope.Require(); err != nil {
		return false, err
	}
	var (
		storedState string
		storedAt    time.Time
	)

	err := d.db.QueryRowContext(ctx,
		`SELECT auth_state, auth_state_at FROM provider_connections WHERE household_id = ? AND provider_id = ?`,
		scope.HouseholdID(), provider).Scan(&storedState, &storedAt)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	// A single-use state is discarded whether accepted or rejected, so a mismatched
	// or expired guess cannot be retried against the same stored value.
	if _, err := d.db.ExecContext(ctx,
		`UPDATE provider_connections SET auth_state = NULL, auth_state_at = NULL WHERE household_id = ? AND provider_id = ?`,
		scope.HouseholdID(), provider); err != nil {
		return false, err
	}

	if storedState != state {
		return false, nil
	}
	if time.Since(storedAt) > 600*time.Second {
		return false, nil
	}

	return true, nil
}
