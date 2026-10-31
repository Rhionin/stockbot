package server

import (
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/Rhionin/stockbot/internal/cart"
	"github.com/Rhionin/stockbot/internal/cart/kroger"
)

// TestNewHandlerWiresAuthIntoProviderCredentials verifies stage-1 auth is
// connected to the provider credential routes (same wiring main uses via
// NewHandler + WithStage1).
func TestNewHandlerWiresAuthIntoProviderCredentials(t *testing.T) {
	db := openMigratedDB(t, filepath.Join(t.TempDir(), "provider-auth.db"))
	svc := auth.NewService(db, t.TempDir())
	registry := cart.NewRegistry()
	if err := registry.Register(kroger.NewUnconfigured()); err != nil {
		t.Fatal(err)
	}
	handler, _ := setupOnDB(t, db, nil,
		WithStage1(svc, stage1Site(t)),
		WithCartRegistry(registry, cart.NewLedger(db)),
	)
	c := &stage1Caller{t: t, h: handler}
	rec := c.anon(http.MethodPut, "/api/providers/kroger/credentials", map[string]string{
		"clientId": "x", "clientSecret": "y", "redirectUri": "https://x.example/cb",
	}, app.EntryPublic)
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), auth.CodeUnauthorized) {
		t.Fatalf("anon PUT %d %s", rec.Code, rec.Body.String())
	}
}
