package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Rhionin/stockbot/internal/cart"
	"github.com/Rhionin/stockbot/internal/cart/carttest"
	"github.com/Rhionin/stockbot/internal/cart/kroger"
	"github.com/Rhionin/stockbot/internal/inventory"
	"github.com/Rhionin/stockbot/internal/shopping"
	"github.com/Rhionin/stockbot/internal/tenant"
)

type stubProvisioner struct {
	err error
}

func (s stubProvisioner) Provision(context.Context, tenant.Scope, cart.ProviderID) (cart.ProvisionReport, error) {
	return cart.ProvisionReport{}, s.err
}

func TestExportMapsAProvisionRefusal(t *testing.T) {
	_, env := setupTestWithDB(t)
	registry := cart.NewRegistry()
	provider := carttest.NewFake(cart.Capabilities{
		Auth:         cart.AuthNone,
		Delivery:     cart.DeliveryServerPush,
		Confirmation: cart.ConfirmNone,
		Mutation:     cart.MutateAddOnly,
		Identity:     cart.IdentityDerived,
	}, carttest.NewScript())
	if err := registry.Register(provider, cart.WithCredentialsConfigured(true)); err != nil {
		t.Fatal(err)
	}
	handler := &ShoppingListExportHandler{
		ShoppingList: shopping.NewStore(env.DB),
		Pantry:       inventory.NewPantry(env.DB),
		Registry:     registry,
		Provisioner:  stubProvisioner{err: &cart.ProvisionConflict{Reason: "already sending this cart"}},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/shopping-list/export", HandleJSON(handler.Handle))
	scoped := withRequestScope(t, env.DB, mux)

	conflict := httptest.NewRecorder()
	conflictReq := httptest.NewRequest(http.MethodPost, "/api/shopping-list/export", nil)
	scoped.ServeHTTP(conflict, conflictReq)
	if conflict.Code != http.StatusConflict || !strings.Contains(conflict.Body.String(), "already sending this cart") {
		t.Fatalf("conflict = %d %s", conflict.Code, conflict.Body.String())
	}

	handler.Provisioner = stubProvisioner{err: errors.New("the retailer did not answer")}
	internal := httptest.NewRecorder()
	scoped.ServeHTTP(internal, httptest.NewRequest(http.MethodPost, "/api/shopping-list/export", nil))
	if internal.Code != http.StatusInternalServerError {
		t.Fatalf("internal = %d %s", internal.Code, internal.Body.String())
	}

	oauth := cart.NewRegistry()
	adapter, err := kroger.New("client", "secret", "https://pantry.example/cb", "PICKUP")
	if err != nil {
		t.Fatal(err)
	}
	if err := oauth.Register(adapter, cart.WithCredentialsConfigured(true)); err != nil {
		t.Fatal(err)
	}
	handler.Registry = oauth
	handler.Connections = nil
	missing := httptest.NewRecorder()
	missingReq := httptest.NewRequest(http.MethodPost, "/api/shopping-list/export", strings.NewReader(`{"provider":"kroger"}`))
	missingReq.Header.Set("Content-Type", "application/json")
	scoped.ServeHTTP(missing, missingReq)
	if missing.Code != http.StatusConflict || !strings.Contains(missing.Body.String(), "not connected") {
		t.Fatalf("missing connection = %d %s", missing.Code, missing.Body.String())
	}
}
