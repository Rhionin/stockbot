package auth

import "context"

// Account is a local user the directory authenticated.
type Account struct {
	ID       string
	Email    string
	Name     string
	Operator bool
}

// IdentityProvider is the directory a sign-in checks.
// Stage 1 uses the local argon2id directory. A later stage can
// authenticate with an external provider and still mint Stockbot sessions.
type IdentityProvider interface {
	Kind() string
	Authenticate(ctx context.Context, email, password string) (Account, error)
	SetPassword(ctx context.Context, userID, password string) error
}

// Kind is the local directory.
func (s *Service) Kind() string { return "local" }
