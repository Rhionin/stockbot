package server

import (
	"database/sql"
	"testing"
	"time"
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
	seedHouseholdProviderApp(t, db, providerID, "export-client", "export-secret", "https://export.test/cb")
}
