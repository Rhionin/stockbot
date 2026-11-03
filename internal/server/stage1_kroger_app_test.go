package server

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/auth"
)

func TestStage1KrogerAppRoutesNilHandlerNotFound(t *testing.T) {
	db := openMigratedDB(t, filepath.Join(t.TempDir(), "stage1-kroger-nil.db"))
	setupDir := t.TempDir()
	svc := auth.NewService(db, setupDir)
	site := stage1Site(t)
	handler, _ := setupOnDB(t, db, nil, WithStage1(svc, site))
	ensureStage1Account(t, svc, handler, site, setupDir)

	pair := stage1Do(handler, http.MethodPost, "/api/auth/login", "", map[string]string{
		"email": "owner@pantry.test", "password": "correct horse",
	}, app.EntryPublic, "")
	cookie := cookieFrom(t, pair, site.SessionCookie().Name)
	house := primaryScope(t, db).HouseholdID()
	path := "/api/households/" + house + "/kroger-app"

	gate := &stage1{svc: svc, site: site, krogerApp: nil}
	cases := []struct {
		method string
		serve  func(http.ResponseWriter, *http.Request)
	}{
		{http.MethodGet, gate.serveGetKrogerApp},
		{http.MethodPut, gate.servePutKrogerApp},
		{http.MethodDelete, gate.serveDeleteKrogerApp},
		{http.MethodPost, gate.serveTestKrogerApp},
	}
	for _, tc := range cases {
		t.Run(tc.method, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := stage1Request(tc.method, path, cookie, nil, app.EntryPublic, "")
			if tc.method == http.MethodPut {
				req = stage1Request(tc.method, path, cookie, map[string]string{
					"clientId": "x", "clientSecret": "y", "redirectUri": "https://cb.example",
				}, app.EntryPublic, "")
			}
			if tc.method == http.MethodPost {
				req = stage1Request(tc.method, path+"/test", cookie, nil, app.EntryPublic, "")
			}
			req = req.WithContext(auth.WithPrincipal(req.Context(), auth.Principal{Kind: "account", UserID: "user-1"}))
			tc.serve(rec, req)
			if rec.Code == http.StatusUnauthorized {
				t.Fatalf("%s unauthorized %s", tc.method, rec.Body.String())
			}
			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("%s %d %s", tc.method, rec.Code, rec.Body.String())
			}
		})
	}
}

func ensureStage1Account(t *testing.T, svc *auth.Service, handler http.Handler, site app.RuntimeConfig, setupDir string) {
	t.Helper()
	if err := svc.Ensure(t.Context()); err != nil {
		t.Fatal(err)
	}
	code, err := auth.ReadSetupCode(setupDir)
	if err != nil {
		t.Fatal(err)
	}
	setup := stage1Do(handler, http.MethodPost, "/setup", "", map[string]string{
		"setupCode": code, "name": "Owner", "email": "owner@pantry.test", "password": "correct horse",
	}, app.EntryPublic, "")
	if setup.Code != http.StatusOK {
		t.Fatalf("setup %d %s", setup.Code, setup.Body.String())
	}
}
