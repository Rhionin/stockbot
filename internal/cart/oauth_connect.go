package cart

import (
	"context"
	"database/sql"

	"github.com/Rhionin/stockbot/internal/cart/appcred"
	"github.com/Rhionin/stockbot/internal/tenant"
)

// OAuthConnectInput carries deployment state for starting Kroger OAuth.
type OAuthConnectInput struct {
	KrogerDisabled bool
}

// OAuthAuthorizeAllowed reports whether this household may start GET .../authorize.
// Owners and members share the same rule (multi-store design case 14); role is not a gate.
func OAuthAuthorizeAllowed(ctx context.Context, db *sql.DB, scope tenant.Scope, in OAuthConnectInput) (bool, error) {
	if err := scope.Require(); err != nil {
		return false, err
	}
	if in.KrogerDisabled {
		return false, nil
	}
	if db == nil {
		return false, nil
	}
	return appcred.NewVault(db).HasApp(ctx, scope, "kroger")
}
