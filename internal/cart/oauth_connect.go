package cart

import (
	"context"
	"database/sql"

	"github.com/Rhionin/stockbot/internal/tenant"
)

// OAuthConnectInput carries deployment and registry state for starting Kroger OAuth.
type OAuthConnectInput struct {
	KrogerDisabled        bool
	CredentialsConfigured bool
}

// OAuthAuthorizeAllowed reports whether this household may start GET .../authorize.
// Owners and members share the same rule (multi-store design case 14); role is not a gate.
func OAuthAuthorizeAllowed(ctx context.Context, db *sql.DB, scope tenant.Scope, in OAuthConnectInput) (bool, error) {
	if in.KrogerDisabled {
		return false, nil
	}
	if !in.CredentialsConfigured {
		return false, nil
	}
	_ = ctx
	_ = db
	_ = scope
	return true, nil
}
