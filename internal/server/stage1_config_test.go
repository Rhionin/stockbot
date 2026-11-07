package server

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/auth"
)

func TestLiveConfigAppNameDefaultAndOverride(t *testing.T) {
	db := openMigratedDB(t, filepath.Join(t.TempDir(), "pantry.db"))
	setupDir := t.TempDir()
	svc := auth.NewService(db, setupDir)

	defaultSite, err := app.LoadRuntimeConfig(func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	defaultHandler, _ := setupOnDB(t, db, nil, WithStage1(svc, defaultSite))
	defaultRec := stage1Do(defaultHandler, http.MethodGet, "/api/config", "", nil, app.EntryPublic, "")
	if defaultRec.Code != http.StatusOK {
		t.Fatalf("default config status = %d", defaultRec.Code)
	}
	var defaultBody struct {
		AppName string `json:"appName"`
		Live    string `json:"live"`
	}
	if err := json.Unmarshal(defaultRec.Body.Bytes(), &defaultBody); err != nil {
		t.Fatal(err)
	}
	if defaultBody.AppName != app.DefaultAppName || defaultBody.Live != "sse" {
		t.Fatalf("default config = %+v", defaultBody)
	}
	if cookies := defaultRec.Header().Get("Set-Cookie"); cookies != "" {
		t.Fatalf("unsigned config set cookies: %q", cookies)
	}

	overrideSite, err := app.LoadRuntimeConfig(func(key string) string {
		switch key {
		case "APP_NAME":
			return "Northwind Pantry"
		case "APP_BASE_URL":
			return "https://pantry.test"
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	overrideDB := openMigratedDB(t, filepath.Join(t.TempDir(), "pantry-override.db"))
	overrideHandler, _ := setupOnDB(t, overrideDB, nil, WithStage1(svc, overrideSite))
	overrideRec := stage1Do(overrideHandler, http.MethodGet, "/api/config", "", nil, app.EntryPublic, "")
	if overrideRec.Code != http.StatusOK {
		t.Fatalf("override config status = %d", overrideRec.Code)
	}
	var overrideBody struct {
		AppName    string `json:"appName"`
		AppBaseURL string `json:"appBaseUrl"`
	}
	if err := json.Unmarshal(overrideRec.Body.Bytes(), &overrideBody); err != nil {
		t.Fatal(err)
	}
	if overrideBody.AppName != "Northwind Pantry" || overrideBody.AppBaseURL != "https://pantry.test" {
		t.Fatalf("override config = %+v", overrideBody)
	}
}
