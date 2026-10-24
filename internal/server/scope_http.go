package server

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"

	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/Rhionin/stockbot/internal/tenant"
)

// withPrimaryScope attaches household 1 to the request.
// Personal sessions replace this in the next stage. A client household id
// is not read. A database that is not open yet leaves the request unchanged
// so liveness checks still answer.
func withPrimaryScope(db *sql.DB, next http.Handler) http.Handler {
	if db == nil || next == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := tenant.FromContext(r.Context()); !ok {
			if scope, err := auth.PrimaryScope(r.Context(), db); err == nil {
				// Keep this request value. A copied request would hide the
				// matched route from the outer telemetry wrapper.
				cloned := r.WithContext(tenant.WithScope(r.Context(), scope))
				*r = *cloned
			}
		}
		next.ServeHTTP(w, r)
	})
}

// requestScope is the household for this request. It was resolved by auth,
// not taken from the client.
func requestScope(ctx context.Context) (tenant.Scope, error) {
	scope, ok := tenant.FromContext(ctx)
	if !ok {
		return tenant.Scope{}, fmt.Errorf("%w", tenant.ErrMissingHousehold)
	}
	return scope, nil
}
