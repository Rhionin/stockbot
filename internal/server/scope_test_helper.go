package server

import (
	"context"
	"database/sql"
	"net/http"
	"testing"

	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/Rhionin/stockbot/internal/tenant"
)

// withRequestScope attaches a household to a handler that is not behind the
// server middleware. A database uses household 1. A fake queue gets a valid
// id and does not read it.
func withRequestScope(t *testing.T, db *sql.DB, next http.Handler) http.Handler {
	t.Helper()
	var scope tenant.Scope
	var err error
	if db != nil {
		scope, err = auth.PrimaryScope(context.Background(), db)
	} else {
		scope, err = tenant.New(cedarHouseholdID)
	}
	if err != nil {
		t.Fatal(err)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cloned := r.WithContext(tenant.WithScope(r.Context(), scope))
		*r = *cloned
		next.ServeHTTP(w, r)
	})
}

// withHouseholdScope marks a request as a second household. Handlers that are
// invoked without the server middleware still need a scope.
func withHouseholdScope(t *testing.T, r *http.Request) *http.Request {
	t.Helper()
	scope, err := tenant.New(cedarHouseholdID)
	if err != nil {
		t.Fatal(err)
	}
	return r.WithContext(tenant.WithScope(r.Context(), scope))
}

// cedarHouseholdID is a second household used to prove rows do not leak.
const cedarHouseholdID = "0190bbbb-cccc-7000-8000-0000000000e5"

// cedarHousehold inserts that household and returns its id.
func cedarHousehold(t *testing.T, db *sql.DB) string {
	t.Helper()
	_, err := db.Exec(`
		INSERT INTO households (id, name, restock_mode, created_at, channel_key, is_primary)
		VALUES (?, 'Household Cedar', '', '2026-02-02T00:00:00Z', 'abcdef0123456789abcdef0123456789', 0)`, cedarHouseholdID)
	if err != nil {
		t.Fatal(err)
	}
	return cedarHouseholdID
}

// primaryScope is household 1, the household the HTTP handlers use until
// personal sessions exist.
func primaryScope(t *testing.T, db *sql.DB) tenant.Scope {
	t.Helper()
	scope, err := auth.PrimaryScope(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	return scope
}
