package server

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/cart/connection"
)

// seedHouseholdProviderApp inserts a provider_app_credentials row for export and
// other household-scoped provider routes in handler tests.
func seedHouseholdProviderApp(t *testing.T, db *sql.DB, providerID, clientID, secret, redirect string) {
	t.Helper()
	scope := primaryScope(t, db)
	when := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := db.Exec(`
		INSERT INTO provider_app_credentials (
			id, user_id, household_id, provider_id, client_id, client_secret, redirect_uri, updated_at
		) VALUES (?, 'user-1', ?, ?, ?, ?, ?, ?)`,
		scope.HouseholdID()+":"+providerID, scope.HouseholdID(), providerID, clientID, secret, redirect, when); err != nil {
		t.Fatal(err)
	}
}

func seedDefaultExportApp(t *testing.T, db *sql.DB, providerID string) {
	t.Helper()
	scope := primaryScope(t, db)
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM provider_app_credentials WHERE household_id = ? AND provider_id = ?`,
		scope.HouseholdID(), providerID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n > 0 {
		return
	}
	seedHouseholdProviderApp(t, db, providerID, "export-client", "export-secret", "https://export.test/cb")
}

// seedKrogerConnectedForAttention keeps ?status=1 fixtures off connection reasons unless a test overrides state.
func seedKrogerConnectedForAttention(t *testing.T, db *sql.DB) {
	t.Helper()
	const provider = "kroger"
	scope := primaryScope(t, db)
	expires := time.Now().UTC().Add(24 * time.Hour)
	if err := connection.NewDirectory(db).Write(context.Background(), scope, &connection.Connection{
		Provider:     provider,
		State:        connection.StateConnected,
		AccessToken:  "fixture-connected",
		RefreshToken: "fixture-refresh",
		ExpiresAt:    &expires,
	}); err != nil {
		t.Fatal(err)
	}
}
