package cart_test

import (
	"context"
	"testing"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/Rhionin/stockbot/internal/cart"
	"github.com/Rhionin/stockbot/internal/tenant"
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
	ownerScope, err := auth.PrimaryScope(ctx, conn)
	if err != nil {
		t.Fatal(err)
	}
	hid := ownerScope.HouseholdID()
	if _, err := conn.Exec(`INSERT INTO users (id, name, email_norm, pw_hash, idp, operator, created_at) VALUES ('user-member', 'Member', 'member@test', 'hash', 'local', 0, datetime('now'))`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO memberships (household_id, user_id, role, created_at) VALUES (?, 'user-member', 'member', datetime('now'))`, hid); err != nil {
		t.Fatal(err)
	}
	memberScope, err := tenant.New(hid)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO provider_app_credentials (id, household_id, user_id, provider_id, client_id, client_secret, redirect_uri, modality, updated_at)
		VALUES (?, ?, ?, 'kroger', 'cid', 'sec', 'https://example.test/cb', 'PICKUP', datetime('now'))`, hid+":kroger", hid, hid); err != nil {
		t.Fatal(err)
	}
	in := cart.OAuthConnectInput{}
	for _, tc := range []struct {
		name  string
		scope tenant.Scope
		role  string
	}{
		{name: "owner", scope: ownerScope, role: "owner"},
		{name: "member", scope: memberScope, role: "member"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			allowed, err := cart.OAuthAuthorizeAllowed(ctx, conn, tc.scope, in)
			if err != nil {
				t.Fatal(err)
			}
			if !allowed {
				t.Fatalf("role %q should allow connect when credentials are configured", tc.role)
			}
		})
	}
	if _, err := cart.OAuthAuthorizeAllowed(ctx, conn, tenant.Scope{}, in); err == nil {
		t.Fatal("zero scope should fail Require")
	}
	disabled := cart.OAuthConnectInput{KrogerDisabled: true}
	allowed, err := cart.OAuthAuthorizeAllowed(ctx, conn, ownerScope, disabled)
	if err != nil || allowed {
		t.Fatalf("disabled: allowed=%v err=%v", allowed, err)
	}
}
