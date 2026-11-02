package cart

import (
	"context"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/Rhionin/stockbot/internal/cart/appcred"
	"github.com/Rhionin/stockbot/internal/cart/connection"
)

type householdAppRefreshProvider struct {
	set TokenSet
	err error
}

func (householdAppRefreshProvider) ID() ProviderID      { return "kroger" }
func (householdAppRefreshProvider) DisplayName() string { return "Kroger" }
func (householdAppRefreshProvider) Capabilities() Capabilities {
	return Capabilities{Auth: AuthOAuth2}
}
func (p householdAppRefreshProvider) RefreshAccessTokenForHouseholdApp(_ context.Context, _ HouseholdAppOAuth, _ string) (TokenSet, error) {
	return p.set, p.err
}

func TestExchangeRefreshUsesHouseholdAppWhenVaultSet(t *testing.T) {
	db, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := app.RunMigrations(db); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	scope, err := auth.PrimaryScope(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	vault := appcred.NewVault(db)
	if err := vault.Save(ctx, scope, appcred.Saved{
		ProviderID: "kroger", ClientID: "house-client", ClientSecret: "house-secret",
		RedirectURI: "https://app.example/cb", Modality: "PICKUP", ActorUserID: "user-1",
	}); err != nil {
		t.Fatal(err)
	}
	provider := householdAppRefreshProvider{set: TokenSet{
		AccessToken: "refreshed-access", RefreshToken: "refreshed-refresh", ExpiresIn: time.Minute,
	}}
	dir := connection.NewDirectory(db)
	if err := dir.Write(ctx, scope, &connection.Connection{
		Provider: "kroger", State: connection.StateConnected, RefreshToken: "rt-old",
		AppKey: appcred.AppKey("house-client"),
	}); err != nil {
		t.Fatal(err)
	}
	engine := NewEngine(NewRegistry(), NewLedger(db), connection.NewTokenBroker(dir))
	engine.SetCredentialVault(vault)
	refresh := engine.exchangeRefresh(ctx, scope, provider)
	access, next, expires, err := refresh("rt-old")
	if err != nil {
		t.Fatal(err)
	}
	if access != "refreshed-access" || next != "refreshed-refresh" || expires != time.Minute {
		t.Fatalf("tokens = %q %q %s", access, next, expires)
	}
}
