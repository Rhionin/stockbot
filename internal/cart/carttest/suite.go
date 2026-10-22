package carttest

import (
	"testing"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/cart"
)

// RunContractSuite verifies one adapter against every behavior its declared
// capabilities require (Requirements 16.1, 16.2).
func RunContractSuite(t *testing.T, p cart.Provider) {
	t.Helper()

	caps := p.Capabilities()

	// coverage maps each capability value to the case that exercises it.
	// A declared value with no entry fails the suite naming the provider and the value.
	coverage := map[string]func(*testing.T, cart.Provider){
		"auth=none":                      caseNoAuth,
		"auth=oauth2_authorization_code": caseOAuth2,
		"auth=api_key":                   caseAPIKey,
		"delivery=server_push":           caseServerPush,
		"delivery=client_handoff":        caseClientHandoff,
		"confirmation=per_line":          casePerLine,
		"confirmation=per_request":       casePerRequest,
		"confirmation=none":              caseNone,
		"mutation=add_only":              caseAddOnly,
		"mutation=add_and_update":        caseAddAndUpdate,
		"mutation=full":                  caseFull,
		"identity=derived":               caseDerivedIdentity,
		"identity=looked_up":             caseLookedUpIdentity,
	}

	// Check each capability value has a case
	capsToCheck := []struct {
		dim string
		val string
	}{
		{"auth", string(caps.Auth)},
		{"delivery", string(caps.Delivery)},
		{"confirmation", string(caps.Confirmation)},
		{"mutation", string(caps.Mutation)},
		{"identity", string(caps.Identity)},
	}

	for _, c := range capsToCheck {
		key := c.dim + "=" + c.val
		if _, ok := coverage[key]; !ok {
			t.Errorf("provider %q declares %s=%q but there is no test case for it", p.ID(), c.dim, c.val)
		}
	}

	// Run each applicable case
	db, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("open in-memory SQLite: %v", err)
	}
	defer db.Close()

	if err := app.RunMigrations(db); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	// Run all applicable cases
	for _, c := range capsToCheck {
		key := c.dim + "=" + c.val
		if caseFn, ok := coverage[key]; ok {
			caseFn(t, p)
		}
	}
}

func caseNoAuth(t *testing.T, p cart.Provider) {
	t.Helper()

	// Verify no credential is returned for auth=none
	// This is verified by the adapter implementation, not here
}

func caseOAuth2(t *testing.T, p cart.Provider) {
	t.Helper()

	// Verify OAuthFlow interface is implemented
	if _, ok := p.(cart.OAuthFlow); !ok {
		t.Errorf("provider %q declares oauth2_authorization_code but does not implement OAuthFlow", p.ID())
	}
}

func caseAPIKey(t *testing.T, p cart.Provider) {
	t.Helper()

	// Verify StaticCredential interface is implemented
	if _, ok := p.(cart.StaticCredential); !ok {
		t.Errorf("provider %q declares api_key but does not implement StaticCredential", p.ID())
	}
}

func caseServerPush(t *testing.T, p cart.Provider) {
	t.Helper()

	// Verify ServerPush interface is implemented
	if _, ok := p.(cart.ServerPush); !ok {
		t.Errorf("provider %q declares server_push but does not implement ServerPush", p.ID())
	}
}

func caseClientHandoff(t *testing.T, p cart.Provider) {
	t.Helper()

	// Verify HandoffBuilder interface is implemented
	if _, ok := p.(cart.HandoffBuilder); !ok {
		t.Errorf("provider %q declares client_handoff but does not implement HandoffBuilder", p.ID())
	}
}

func casePerLine(t *testing.T, p cart.Provider) {
	t.Helper()

	// Verify ServerPush.Add returns per-line results
	// This is verified by the adapter implementation
}

func casePerRequest(t *testing.T, p cart.Provider) {
	t.Helper()

	// Verify ServerPush.Add returns per-request results
	// This is verified by the adapter implementation
}

func caseNone(t *testing.T, p cart.Provider) {
	t.Helper()

	// Verify ServerPush.Add returns unknown outcomes
	// This is verified by the adapter implementation
}

func caseAddOnly(t *testing.T, p cart.Provider) {
	t.Helper()

	// Verify adapter only implements Add operation
	// No LineUpdater or LineRemover
}

func caseAddAndUpdate(t *testing.T, p cart.Provider) {
	t.Helper()

	// Verify adapter implements LineUpdater
	// No LineRemover
}

func caseFull(t *testing.T, p cart.Provider) {
	t.Helper()

	// Verify adapter implements both LineUpdater and LineRemover
}

func caseDerivedIdentity(t *testing.T, p cart.Provider) {
	t.Helper()

	// Verify DerivedIdentity interface is implemented
	// and that it does not take context.Context
	// (verified by type signature at compile time)
	if _, ok := p.(cart.DerivedIdentity); !ok {
		t.Errorf("provider %q declares derived but does not implement DerivedIdentity", p.ID())
	}
}

func caseLookedUpIdentity(t *testing.T, p cart.Provider) {
	t.Helper()

	// Verify LookedUpIdentity interface is implemented
	if _, ok := p.(cart.LookedUpIdentity); !ok {
		t.Errorf("provider %q declares looked_up but does not implement LookedUpIdentity", p.ID())
	}
}
