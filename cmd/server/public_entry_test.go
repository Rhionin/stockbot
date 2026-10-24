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

// The public listener is the entry. A request with no X-Pantry-Entry header
// is still the public site on :8081 and the open LAN on :8080.
func TestUnauthenticatedInventoryFollowsTheListener(t *testing.T) {
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
	var sawLAN, sawPublic bool
	for i := range specs {
		switch specs[i].Entry {
		case app.EntryLAN:
			sawLAN = true
		case app.EntryPublic:
			sawPublic = true
		}
		specs[i].Addr = "127.0.0.1:0"
	}
	if !sawLAN || !sawPublic {
		t.Fatalf("listeners = %+v, want LAN and public when APP_BASE_URL is set", specs)
	}

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
		server.WithHouseholdAuth(loadHouseholdGate(sqlDB)),
		server.WithReadDB(readDB),
		server.WithStage1(directory, site),
	)
	set, err := openListeners(specs, handler, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(set.close)

	client := &http.Client{Timeout: 5 * time.Second}
	for i, spec := range specs {
		req, err := http.NewRequest(http.MethodGet, "http://"+set.addrs[i]+"/api/inventory", nil)
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
		want := http.StatusOK
		if spec.Entry == app.EntryPublic {
			want = http.StatusUnauthorized
		}
		if res.StatusCode != want {
			t.Fatalf("%s GET /api/inventory = %d %s, want %d", spec.Entry, res.StatusCode, body, want)
		}
	}
}
