package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Rhionin/stockbot/internal/cart"
	"github.com/Rhionin/stockbot/internal/cart/connection"
	"github.com/Rhionin/stockbot/internal/cart/kroger"
)

func TestLoggedInKrogerCallbackStillSucceeds(t *testing.T) {
	db := setupTestDB(t)
	adapter, err := kroger.New("client", "secret", "https://pantry.example/api/providers/kroger/callback", "PICKUP")
	if err != nil {
		t.Fatalf("kroger: %v", err)
	}
	adapter.WithTransport(roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body := `{"access_token":"access-token","refresh_token":"refresh-token","expires_in":3600}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     make(http.Header),
			Request:    r,
		}, nil
	}))
	registry := cart.NewRegistry()
	if err := registry.Register(adapter, cart.WithCredentialsConfigured(true)); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := connection.NewDirectory(db).GenerateAuthState(context.Background(), primaryScope(t, db), "kroger", "state-from-kroger"); err != nil {
		t.Fatalf("auth state: %v", err)
	}

	gate := testHousehold(t, "pantry", "correct-horse-battery")
	handler, _ := NewHandler(nil, nil, nil, db, WithHouseholdAuth(gate), WithCartRegistry(registry, cart.NewLedger(db)))

	open := httptest.NewRequest(http.MethodGet, "/api/providers/kroger/callback?code=abc&state=state-from-kroger", nil)
	withPublicEntry(open)
	openRec := httptest.NewRecorder()
	handler.ServeHTTP(openRec, open)
	if openRec.Code != http.StatusUnauthorized {
		t.Fatalf("callback without a session = %d, want it behind the login", openRec.Code)
	}

	login := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(`{"username":"pantry","password":"correct-horse-battery"}`))
	withPublicEntry(login)
	login.Header.Set(clientIPHeader, "203.0.113.77")
	loginRec := httptest.NewRecorder()
	handler.ServeHTTP(loginRec, login)
	if loginRec.Code != http.StatusOK {
		t.Fatalf("login = %d %s", loginRec.Code, loginRec.Body.String())
	}
	cookies := loginRec.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].Secure {
		t.Fatalf("login cookie = %v", cookies)
	}

	back := httptest.NewRequest(http.MethodGet, "/api/providers/kroger/callback?code=abc&state=state-from-kroger", nil)
	withPublicEntry(back)
	back.Header.Set("X-Forwarded-Proto", "http")
	back.AddCookie(cookies[0])
	backRec := httptest.NewRecorder()
	handler.ServeHTTP(backRec, back)
	if backRec.Code != http.StatusSeeOther {
		t.Fatalf("callback = %d %s", backRec.Code, backRec.Body.String())
	}
	if loc := backRec.Header().Get("Location"); loc != "/shopping?connected=kroger" {
		t.Fatalf("callback location = %q", loc)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}
