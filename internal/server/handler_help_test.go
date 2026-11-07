package server

import (
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/auth"
)

func TestPublicScannerHelpPage(t *testing.T) {
	runHandlerTests(t, []handlerTestCase{
		{
			name: "GET /help/scanner is the household scanner help page",
			httpExchange: httpExchange{
				method:         "GET",
				url:            "/help/scanner",
				expectedStatus: http.StatusOK,
				expectedHeaders: map[string]string{
					"Content-Type":  "text/html; charset=utf-8",
					"Cache-Control": "no-cache",
				},
				bodyContains: []string{
					"Set up your barcode scanner",
					"sign in to read it",
					"READY TO SET UP",
					"Connect a scanner",
					"Contact the person who sent your invite",
					"Stockbot · Scanner help · v1 (2026-11)",
					"/help/scanner",
				},
				bodyExcludes: []string{
					placeholderMarker,
					"NOT FOR PUBLISHING",
					"private-admin",
					"setup runbook",
					"{App name}",
					"{help host}",
				},
			},
			afterRequest: func(env testEnv) {
				if got := env.Res.Header.Get("Set-Cookie"); got != "" {
					env.T.Fatalf("help page set cookie %q", got)
				}
			},
		},
		{
			name: "GET /help/scanner/extra stays the app shell",
			httpExchange: httpExchange{
				method:         "GET",
				url:            "/help/scanner/extra",
				expectedStatus: http.StatusOK,
				bodyContains:   []string{placeholderMarker},
				bodyExcludes:   []string{"sign in to read it"},
			},
		},
		{
			name: "GET /help/scanner/ is not the help page",
			httpExchange: httpExchange{
				method:         "GET",
				url:            "/help/scanner/",
				expectedStatus: http.StatusOK,
				bodyContains:   []string{placeholderMarker},
				bodyExcludes:   []string{"sign in to read it"},
			},
		},
	})
}

func TestScannerHelpResolvesConfiguredNames(t *testing.T) {
	db := openMigratedDB(t, filepath.Join(t.TempDir(), "pantry.db"))
	setupDir := t.TempDir()
	svc := auth.NewService(db, setupDir)
	site, err := app.LoadRuntimeConfig(func(key string) string {
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
	handler, _ := setupOnDB(t, db, nil, WithStage1(svc, site))
	rec := stage1Do(handler, http.MethodGet, "/help/scanner", "", nil, app.EntryPublic, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("help status = %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Northwind Pantry · Scanner help") {
		t.Fatalf("footer missing configured app name: %.120s...", body[len(body)-200:])
	}
	if !strings.Contains(body, "pantry.test/help/scanner") {
		t.Fatalf("footer missing help host: %.120s...", body[len(body)-200:])
	}
	if !strings.Contains(body, `alt="Northwind Pantry"`) {
		t.Fatal("logo alt should use configured app name")
	}
	if rec.Header().Get("Set-Cookie") != "" {
		t.Fatalf("signed-out help set cookie")
	}
}
