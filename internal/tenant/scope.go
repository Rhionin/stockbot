// Package tenant is the household a store call is allowed to see.
// A scope is built only after the household has been verified. Handlers take
// one from the auth resolver. They do not pass a raw household id into a store.
package tenant

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

// ErrMissingHousehold means a store was called without a verified household.
var ErrMissingHousehold = errors.New("missing household")

// Scope is one verified household. The zero value is refused by every store.
type Scope struct {
	householdID string
}

type ctxKey struct{}

// New returns a scope for a household id that has already been verified.
// Production callers outside the auth package must not call New. The scope
// lint rejects those call sites. Tests call New for a household they inserted.
func New(householdID string) (Scope, error) {
	id, err := uuid.Parse(householdID)
	if err != nil || id.String() != householdID {
		return Scope{}, fmt.Errorf("that household is not available")
	}
	return Scope{householdID: id.String()}, nil
}

// HouseholdID is the verified household. It is empty when the scope is zero.
func (s Scope) HouseholdID() string {
	return s.householdID
}

// Valid reports whether the scope names a household.
func (s Scope) Valid() bool {
	return s.householdID != ""
}

// Require rejects the zero scope before a query runs.
func (s Scope) Require() error {
	if !s.Valid() {
		return ErrMissingHousehold
	}
	return nil
}

// WithScope stores a verified scope on ctx.
func WithScope(ctx context.Context, scope Scope) context.Context {
	return context.WithValue(ctx, ctxKey{}, scope)
}

// FromContext returns the scope a resolver stored on ctx.
func FromContext(ctx context.Context) (Scope, bool) {
	if ctx == nil {
		return Scope{}, false
	}
	scope, ok := ctx.Value(ctxKey{}).(Scope)
	if !ok || !scope.Valid() {
		return Scope{}, false
	}
	return scope, true
}
