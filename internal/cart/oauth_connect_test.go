package cart_test

import (
	"context"
	"testing"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/Rhionin/stockbot/internal/cart"
)

func TestOAuthAuthorizeAllowedOwnerAndMember(t *testing.T) {
	conn, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := app.RunMigrations(conn); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	scope, err := auth.PrimaryScope(ctx, conn)
	if err != nil {
		t.Fatal(err)
	}
	in := cart.OAuthConnectInput{CredentialsConfigured: true}
	for _, role := range []string{"owner", "member"} {
		role := role
		t.Run(role, func(t *testing.T) {
			allowed, err := cart.OAuthAuthorizeAllowed(ctx, conn, scope, in)
			if err != nil {
				t.Fatal(err)
			}
			if !allowed {
				t.Fatalf("role %q should allow connect when credentials are configured", role)
			}
		})
	}
	disabled := cart.OAuthConnectInput{KrogerDisabled: true, CredentialsConfigured: true}
	allowed, err := cart.OAuthAuthorizeAllowed(ctx, conn, scope, disabled)
	if err != nil || allowed {
		t.Fatalf("disabled: allowed=%v err=%v", allowed, err)
	}
}
