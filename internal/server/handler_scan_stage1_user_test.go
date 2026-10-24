package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/Rhionin/stockbot/internal/scan"
)

// TestScansOmitUserID is the Stage-1 contract: once the web client drops
// userId, create, list, and history still succeed. A supplied id is kept.
func TestScansOmitUserID(t *testing.T) {
	runHandlerTests(t, []handlerTestCase{
		{
			name: "create, list, and history succeed without userId",
			setup: func(env testEnv) {
				scope := primaryScope(env.T, env.DB)
				direction := scan.StockIn
				when := time.Now().UTC().Add(-time.Hour)
				if _, err := scan.NewQueue(env.DB).CreateScanEntry(context.Background(), scope, scan.ScanEntry{
					ID:          "scan-committed-omit",
					Barcode:     "444555666777",
					ScannedAt:   when,
					Direction:   &direction,
					UnitCount:   1,
					Status:      scan.Committed,
					CommittedAt: &when,
				}); err != nil {
					env.T.Fatal(err)
				}
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans",
				body:           `{"barcode":"333444555666","direction":"stock_out"}`,
				expectedStatus: http.StatusCreated,
				assertions: []assertion{
					{path: "$.barcode", value: "333444555666"},
					{path: "$.status", value: "flagged"},
					{path: "$.direction", value: "stock_out"},
				},
			},
			afterRequest: func(env testEnv) {
				household := primaryScope(env.T, env.DB).HouseholdID()
				handler := handlerFromEnv(env)
				executeExchange(env.T, handler, httpExchange{
					method:         "GET",
					path:           "/api/scans",
					expectedStatus: http.StatusOK,
					bodyContains:   []string{"333444555666", `"userId":"` + household + `"`},
				})
				executeExchange(env.T, handler, httpExchange{
					method:         "GET",
					path:           "/api/scans/history",
					expectedStatus: http.StatusOK,
					bodyContains:   []string{"scan-committed-omit", "444555666777"},
				})
			},
		},
		{
			name: "a supplied userId is still stored",
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans",
				body:           `{"barcode":"555666777888","userId":"user-legacy","direction":"stock_in"}`,
				expectedStatus: http.StatusCreated,
				assertions: []assertion{
					{path: "$.userId", value: "user-legacy"},
				},
			},
		},
		{
			name: "blank userId uses the household when no principal is present",
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/scans",
				body:           `{"barcode":"666777888999","userId":"   ","direction":"stock_in"}`,
				expectedStatus: http.StatusCreated,
			},
			afterRequest: func(env testEnv) {
				household := primaryScope(env.T, env.DB).HouseholdID()
				executeExchange(env.T, handlerFromEnv(env), httpExchange{
					method:         "GET",
					path:           "/api/scans",
					expectedStatus: http.StatusOK,
					bodyContains:   []string{"666777888999", `"userId":"` + household + `"`},
				})
			},
		},
	})
}

func TestScanCreateDerivesUserIDFromPrincipal(t *testing.T) {
	handler, env := setupTestWithContributor(t, nil)
	household := primaryScope(t, env.DB).HouseholdID()

	post := func(principal *auth.Principal, body string) scan.ScanEntry {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/scans", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if principal != nil {
			req = req.WithContext(auth.WithPrincipal(req.Context(), *principal))
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("POST /api/scans = %d %s", rec.Code, rec.Body.String())
		}
		var created scan.ScanEntry
		if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
			t.Fatal(err)
		}
		return created
	}

	lan := post(&auth.Principal{Kind: "lan"}, `{"barcode":"101010101010","direction":"stock_in"}`)
	if lan.UserID != "lan" {
		t.Fatalf("lan userId = %q", lan.UserID)
	}
	shared := post(&auth.Principal{Kind: "household_password"}, `{"barcode":"202020202020","direction":"stock_in"}`)
	if shared.UserID != "household_password" {
		t.Fatalf("household password userId = %q", shared.UserID)
	}
	account := post(&auth.Principal{Kind: "account", UserID: "user-ada"}, `{"barcode":"303030303030","direction":"stock_in"}`)
	if account.UserID != "user-ada" {
		t.Fatalf("account userId = %q", account.UserID)
	}
	legacy := post(&auth.Principal{Kind: "lan"}, `{"barcode":"404040404040","userId":"user-legacy","direction":"stock_in"}`)
	if legacy.UserID != "user-legacy" {
		t.Fatalf("supplied userId = %q", legacy.UserID)
	}
	blank := post(&auth.Principal{Kind: "lan"}, `{"barcode":"505050505050","userId":"  ","direction":"stock_in"}`)
	if blank.UserID != "lan" {
		t.Fatalf("blank userId = %q", blank.UserID)
	}
	householdOnly := post(nil, `{"barcode":"606060606060","direction":"stock_in"}`)
	if householdOnly.UserID != household {
		t.Fatalf("household userId = %q, want %q", householdOnly.UserID, household)
	}

	list := httptest.NewRecorder()
	handler.ServeHTTP(list, httptest.NewRequest(http.MethodGet, "/api/scans", nil))
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), "101010101010") {
		t.Fatalf("GET /api/scans = %d %s", list.Code, list.Body.String())
	}
	history := httptest.NewRecorder()
	handler.ServeHTTP(history, httptest.NewRequest(http.MethodGet, "/api/scans/history", nil))
	if history.Code != http.StatusOK {
		t.Fatalf("GET /api/scans/history = %d %s", history.Code, history.Body.String())
	}
}

func TestStage1LANScanOmitsUserID(t *testing.T) {
	db := openMigratedDB(t, filepath.Join(t.TempDir(), "pantry.db"))
	svc := auth.NewService(db, t.TempDir())
	site, err := app.LoadRuntimeConfig(func(key string) string {
		if key == "APP_BASE_URL" {
			return "https://pantry.test"
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	handler, _ := setupOnDB(t, db, nil, WithStage1(svc, site))

	created := stage1Do(handler, http.MethodPost, "/api/scans", "", map[string]string{
		"barcode": "121212121212", "direction": "stock_out",
	}, app.EntryLAN, "")
	if created.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", created.Code, created.Body.String())
	}
	var entry scan.ScanEntry
	if err := json.Unmarshal(created.Body.Bytes(), &entry); err != nil {
		t.Fatal(err)
	}
	if entry.UserID != "lan" {
		t.Fatalf("lan userId = %q", entry.UserID)
	}

	listed := stage1Do(handler, http.MethodGet, "/api/scans", "", nil, app.EntryLAN, "")
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), "121212121212") {
		t.Fatalf("list = %d %s", listed.Code, listed.Body.String())
	}
	history := stage1Do(handler, http.MethodGet, "/api/scans/history", "", nil, app.EntryLAN, "")
	if history.Code != http.StatusOK {
		t.Fatalf("history = %d %s", history.Code, history.Body.String())
	}
}
