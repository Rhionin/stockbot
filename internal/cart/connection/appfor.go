package connection

import (
	"context"
	"errors"
	"fmt"

	"github.com/Rhionin/stockbot/internal/cart/appcred"
	"github.com/Rhionin/stockbot/internal/tenant"
)

// ErrAppMismatch means the connection was issued by a different app than the
// household currently has configured.
var ErrAppMismatch = errors.New("provider app mismatch")

// AppFor loads the household's Kroger app and verifies conn.AppKey when set.
func AppFor(ctx context.Context, vault *appcred.Vault, scope tenant.Scope, conn *Connection) (appcred.Saved, string, error) {
	if err := scope.Require(); err != nil {
		return appcred.Saved{}, "", err
	}
	if vault == nil {
		return appcred.Saved{}, "", fmt.Errorf("credential store is not available")
	}
	saved, ok, err := vault.Load(ctx, scope, "kroger")
	if err != nil {
		return appcred.Saved{}, "", err
	}
	if !ok {
		return appcred.Saved{}, "", fmt.Errorf("no store app configured")
	}
	key := appcred.AppKey(saved.ClientID)
	if conn != nil && conn.AppKey != "" && conn.AppKey != key {
		return appcred.Saved{}, "", ErrAppMismatch
	}
	return saved, key, nil
}
