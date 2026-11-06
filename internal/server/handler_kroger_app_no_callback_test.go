package server

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/Rhionin/stockbot/internal/cart/appcred"
	"github.com/Rhionin/stockbot/internal/cart/kroger"
)

func TestKrogerAppPutRejectsMissingPublicCallbackURL(t *testing.T) {
	db := openMigratedDB(t, ":memory:")
	vault := appcred.NewVault(db)
	h := &KrogerAppHandler{
		DB: db, Vault: vault, Env: ProviderEnv{}, Site: app.RuntimeConfig{},
		Kroger: kroger.NewUnconfigured(),
	}
	ctx := context.Background()
	scope, err := auth.PrimaryScope(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	house := scope.HouseholdID()
	ctx = auth.WithPrincipal(ctx, auth.Principal{Kind: "account", UserID: "owner-1", Role: "owner"})
	_, err = h.Put(Request[krogerAppPutBody, krogerHouseholdParams]{
		Context:    ctx,
		PathParams: krogerHouseholdParams{ID: house},
		Body: krogerAppPutBody{
			ClientID: "cid", ClientSecret: "sec", Modality: "PICKUP",
		},
	})
	var httpErr *HTTPError
	if err == nil || !errorsAsHTTP(err, &httpErr) || httpErr.Code != http.StatusBadRequest ||
		!strings.Contains(httpErr.Message, "callback URL") {
		t.Fatalf("err = %v", err)
	}
}
