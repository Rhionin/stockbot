package server

import (
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/Rhionin/stockbot/internal/cart"
)

type sinkTestProvider struct {
	id      cart.ProviderID
	client  string
	secret  string
	uri     string
	modality string
}

func (s *sinkTestProvider) ID() cart.ProviderID      { return s.id }
func (s *sinkTestProvider) DisplayName() string      { return "Sink Test" }
func (s *sinkTestProvider) Capabilities() cart.Capabilities {
	return cart.Capabilities{
		Auth: cart.AuthNone, Delivery: cart.DeliveryServerPush,
		Confirmation: cart.ConfirmPerRequest, Mutation: cart.MutateAddOnly, Identity: cart.IdentityDerived,
	}
}
func (s *sinkTestProvider) ApplyAppCredentials(clientID, clientSecret, redirectURI, modality string) error {
	s.client, s.secret, s.uri, s.modality = clientID, clientSecret, redirectURI, modality
	return nil
}
func (s *sinkTestProvider) ClearAppCredentials() {
	s.client, s.secret, s.uri, s.modality = "", "", "", "PICKUP"
}
func (s *sinkTestProvider) PublicAppCredentials() cart.AppCredentials {
	return cart.AppCredentials{
		ClientID: s.client, RedirectURI: s.uri, Modality: s.modality, SecretSet: s.secret != "",
	}
}

func TestNonKrogerProviderCredentialsOperatorPutDelete(t *testing.T) {
	db := openMigratedDB(t, filepath.Join(t.TempDir(), "sink-creds.db"))
	setupDir := t.TempDir()
	svc := auth.NewService(db, setupDir)
	site := stage1Site(t)
	registry := cart.NewRegistry()
	sink := &sinkTestProvider{id: "sinktest"}
	if err := registry.Register(sink); err != nil {
		t.Fatal(err)
	}
	handler, _ := setupOnDB(t, db, nil,
		WithStage1(svc, site),
		WithCartRegistry(registry, cart.NewLedger(db)),
	)
	c := &stage1Caller{t: t, h: handler}
	c.call(http.MethodGet, "/setup", nil, app.EntryPublic, nil)
	code, _ := auth.ReadSetupCode(setupDir)
	c.call(http.MethodPost, "/setup", map[string]string{
		"setupCode": code, "name": "Op", "email": "op@test", "password": "correct horse",
	}, app.EntryPublic, nil)
	login := c.call(http.MethodPost, "/api/auth/login", map[string]string{
		"email": "op@test", "password": "correct horse",
	}, app.EntryPublic, nil)
	c.cookie = cookieFrom(t, login, site.SessionCookie().Name)

	put := c.authed(http.MethodPut, "/api/providers/sinktest/credentials", map[string]string{
		"clientId": "sink-client", "clientSecret": "sink-secret",
		"redirectUri": "https://sink.example/cb", "modality": "PICKUP",
	})
	if put.Code != http.StatusOK || !strings.Contains(put.Body.String(), `"source":"saved"`) {
		t.Fatalf("PUT %d %s", put.Code, put.Body.String())
	}
	badMod := c.authed(http.MethodPut, "/api/providers/sinktest/credentials", map[string]string{
		"clientId": "x", "clientSecret": "y", "redirectUri": "https://sink.example/cb", "modality": "CURBSIDE",
	})
	if badMod.Code != http.StatusUnprocessableEntity {
		t.Fatalf("bad modality %d", badMod.Code)
	}
	badURL := c.authed(http.MethodPut, "/api/providers/sinktest/credentials", map[string]string{
		"clientId": "x", "clientSecret": "y", "redirectUri": "not-a-url", "modality": "PICKUP",
	})
	if badURL.Code != http.StatusUnprocessableEntity {
		t.Fatalf("bad url %d", badURL.Code)
	}
	if sink.client != "sink-client" {
		t.Fatalf("apply client %q", sink.client)
	}
	list := c.authed(http.MethodGet, "/api/providers", nil)
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), `"clientId":"sink-client"`) {
		t.Fatalf("list %d %s", list.Code, list.Body.String())
	}
	keepSecret := c.authed(http.MethodPut, "/api/providers/sinktest/credentials", map[string]string{
		"clientId": "sink-client", "clientSecret": "", "redirectUri": "https://sink2.example/cb", "modality": "DELIVERY",
	})
	if keepSecret.Code != http.StatusOK || !strings.Contains(keepSecret.Body.String(), `"redirectUri":"https://sink2.example/cb"`) {
		t.Fatalf("keep secret PUT %d %s", keepSecret.Code, keepSecret.Body.String())
	}
	if sink.secret != "sink-secret" {
		t.Fatalf("secret cleared")
	}
	del := c.authed(http.MethodDelete, "/api/providers/sinktest/credentials", nil)
	if del.Code != http.StatusOK {
		t.Fatalf("DELETE %d %s", del.Code, del.Body.String())
	}
	if sink.client != "" {
		t.Fatal("expected cleared sink")
	}
}
