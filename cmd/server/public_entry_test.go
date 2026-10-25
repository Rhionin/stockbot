package main

import (
	"context"
	"io"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/Rhionin/stockbot/internal/product"
	"github.com/Rhionin/stockbot/internal/server"
)

// The only listener is the public entry. A request with no session is
// refused, and a client-supplied X-Pantry-Entry is not what makes it so.
func TestUnauthenticatedInventoryRequiresASession(t *testing.T) {
	t.Setenv("APP_BASE_URL", "https://stockbot.rhionin.com")
	t.Setenv("PANTRY_AUTH_FILE", "")

	site, err := app.LoadRuntimeConfig(func(key string) string {
		if key == "APP_BASE_URL" {
			return "https://stockbot.rhionin.com"
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	specs := site.Listeners()
	if len(specs) != 1 || specs[0].Entry != app.EntryPublic {
		t.Fatalf("listeners = %+v, want one public listener", specs)
	}
	specs[0].Addr = "127.0.0.1:0"

	dbPath := filepath.Join(t.TempDir(), "pantry.db")
	sqlDB, readDB, err := app.OpenPantry(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		readDB.Close()
		sqlDB.Close()
	})
	if err := app.RunMigrations(sqlDB); err != nil {
		t.Fatal(err)
	}
	directory := auth.NewService(sqlDB, t.TempDir())
	if err := directory.Boot(context.Background()); err != nil {
		t.Fatal(err)
	}
	catalog := product.NewCatalog(sqlDB)
	handler, _ := server.NewHandler(catalog, nil, nil, sqlDB,
		server.WithReadDB(readDB),
		server.WithStage1(directory, site),
	)
	set, err := openListeners(specs, handler, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(set.close)

	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequest(http.MethodGet, "http://"+set.addrs[0]+"/api/inventory", nil)
	if err != nil {
		t.Fatal(err)
	}
	if req.Header.Get("X-Pantry-Entry") != "" {
		t.Fatal("probe set X-Pantry-Entry")
	}
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("GET /api/inventory = %d %s, want 401", res.StatusCode, body)
	}
	health, err := client.Get("http://" + set.addrs[0] + "/health")
	if err != nil {
		t.Fatal(err)
	}
	health.Body.Close()
	if health.StatusCode != http.StatusOK {
		t.Fatalf("GET /health = %d", health.StatusCode)
	}
}
