package server

import (
	"net/http"
	"testing"

	"github.com/Rhionin/stockbot/internal/cart"
	"github.com/Rhionin/stockbot/internal/cart/kroger"
)

func TestKrogerAuthorizeConflictWhenDisabledMatchesCanConnect(t *testing.T) {
	db := setupTestDB(t)
	registry := cart.NewRegistry()
	if err := registry.Register(kroger.NewUnconfigured(), cart.WithCredentialsConfigured(true)); err != nil {
		t.Fatal(err)
	}
	handler, _ := setupOnDB(t, db, nil,
		WithCartRegistry(registry, cart.NewLedger(db)),
		WithProviderEnv(ProviderEnv{
			Disabled:     true,
			ClientID:     "env-client",
			ClientSecret: "env-secret",
			RedirectURI:  "https://example.test/cb",
		}),
	)
	executeExchange(t, handler, httpExchange{
		method:         http.MethodGet,
		url:            "/api/stores",
		expectedStatus: http.StatusOK,
		assertions: []assertion{
			{path: "$.canConnect", value: false},
		},
	})
	executeExchange(t, handler, httpExchange{
		method:         http.MethodGet,
		url:            "/api/providers/kroger/authorize",
		expectedStatus: http.StatusConflict,
		bodyContains:   []string{`"code":"store_disabled"`},
	})
}
