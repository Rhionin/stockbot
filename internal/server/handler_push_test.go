package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/Rhionin/stockbot/internal/push"
	webpush "github.com/SherClockHolmes/webpush-go"
)

func testPushConfig(t *testing.T, post func(context.Context, push.Subscription, []byte) (int, error)) push.Config {
	t.Helper()
	privateKey, publicKey, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		t.Fatalf("vapid keys: %v", err)
	}
	if !(push.Config{PublicKey: publicKey, PrivateKey: privateKey}).Configured() {
		t.Fatal("generated VAPID keys were rejected")
	}
	return push.Config{
		PublicKey:  publicKey,
		PrivateKey: privateKey,
		Subject:    "mailto:pantry@localhost",
		Post:       post,
	}
}

func TestPushSubscriptionAPI(t *testing.T) {
	cfg := testPushConfig(t, nil)
	runHandlerTests(t, []handlerTestCase{
		{
			name:    "public key is empty until VAPID is configured",
			options: nil,
			httpExchange: httpExchange{
				method:         "GET",
				path:           "/api/push/vapid-public-key",
				expectedStatus: http.StatusOK,
				assertions:     []assertion{{path: "$.publicKey", value: ""}},
			},
		},
		{
			name:    "subscribe explains that background alerts are not set up",
			options: nil,
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/push/subscriptions",
				body:           `{"endpoint":"https://push.example/device","keys":{"p256dh":"p256","auth":"auth"}}`,
				expectedStatus: http.StatusBadRequest,
				assertions:     []assertion{{path: "$.error", value: "Background scan alerts are not set up on this pantry yet."}},
			},
		},
		{
			name:    "subscribe stores a browser and unsubscribe removes it",
			options: []Option{WithPush(cfg)},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/push/subscriptions",
				body:           `{"endpoint":"https://push.example/device","keys":{"p256dh":"p256dh-key","auth":"auth-key"}}`,
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.endpoint", value: "https://push.example/device"},
				},
				bodyExcludes: []string{cfg.PrivateKey},
			},
			afterRequest: exchanges(
				httpExchange{
					method:         "GET",
					path:           "/api/push/vapid-public-key",
					expectedStatus: http.StatusOK,
					assertions:     []assertion{{path: "$.publicKey", value: cfg.PublicKey}},
					bodyExcludes:   []string{cfg.PrivateKey},
				},
				httpExchange{
					method:         "POST",
					path:           "/api/push/subscriptions",
					body:           `{"endpoint":"https://push.example/device","keys":{"p256dh":"p256dh-key","auth":"auth-rotated"}}`,
					expectedStatus: http.StatusOK,
					assertions:     []assertion{{path: "$.endpoint", value: "https://push.example/device"}},
				},
				httpExchange{
					method:         "POST",
					path:           "/api/push/subscriptions",
					body:           `{"endpoint":"http://push.example/device","keys":{"p256dh":"p256dh-key","auth":"auth-key"}}`,
					expectedStatus: http.StatusBadRequest,
					assertions:     []assertion{{path: "$.error", value: "Enter a push subscription from this browser."}},
				},
				httpExchange{
					method:         "DELETE",
					path:           "/api/push/subscriptions",
					body:           `{"endpoint":"https://push.example/device"}`,
					expectedStatus: http.StatusNoContent,
				},
				httpExchange{
					method:         "DELETE",
					path:           "/api/push/subscriptions",
					body:           `{"endpoint":"https://push.example/device"}`,
					expectedStatus: http.StatusNoContent,
				},
			),
		},
	})
}

func TestPushSubscriptionFollowsThePublicHost(t *testing.T) {
	cfg := testPushConfig(t, nil)
	cfg.PublicHost = "stockbot.rhionin.com"
	cfg.LegacyHost = "pantry.rhionin.com"
	runHandlerTests(t, []handlerTestCase{
		{
			name:    "a subscription from the previous host is rejected and removed",
			options: []Option{WithPush(cfg)},
			setup: func(env testEnv) {
				_, err := env.DB.Exec(`
					INSERT INTO push_subscriptions (endpoint, p256dh, auth, origin)
					VALUES ('https://push.example/old', 'p256dh-key', 'auth-key', 'pantry.rhionin.com')`)
				if err != nil {
					env.T.Fatal(err)
				}
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/push/subscriptions",
				headers:        map[string]string{"Origin": "https://pantry.rhionin.com"},
				body:           `{"endpoint":"https://push.example/old","keys":{"p256dh":"p256dh-key","auth":"auth-key"},"userId":"user-1","deviceId":"phone-a"}`,
				expectedStatus: http.StatusBadRequest,
				assertions:     []assertion{{path: "$.error", value: "This address no longer sends scan alerts. Open the current site and allow notifications again."}},
			},
			afterRequest: func(env testEnv) {
				assertSubscriptionEndpoints(env, nil)
			},
		},
		{
			name:    "re-subscribe stores the current host and replaces that device",
			options: []Option{WithPush(cfg)},
			setup: func(env testEnv) {
				_, err := env.DB.Exec(`
					INSERT INTO push_subscriptions (endpoint, p256dh, auth, origin, user_id, device_id)
					VALUES ('https://push.example/old', 'p256dh-key', 'auth-key', 'stockbot.rhionin.com', 'user-1', 'phone-a')`)
				if err != nil {
					env.T.Fatal(err)
				}
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/push/subscriptions",
				headers:        map[string]string{"Origin": "https://stockbot.rhionin.com"},
				body:           `{"endpoint":"https://push.example/new","keys":{"p256dh":"p256dh-key","auth":"auth-key"},"userId":"user-1","deviceId":"phone-a"}`,
				expectedStatus: http.StatusOK,
				assertions:     []assertion{{path: "$.endpoint", value: "https://push.example/new"}},
			},
			afterRequest: func(env testEnv) {
				assertSubscriptionEndpoints(env, map[string]string{
					"https://push.example/new": "stockbot.rhionin.com",
				})
			},
		},
		{
			name:    "a subscription stored before origins existed stays",
			options: []Option{WithPush(cfg)},
			setup: func(env testEnv) {
				_, err := env.DB.Exec(`
					INSERT INTO push_subscriptions (endpoint, p256dh, auth)
					VALUES ('https://push.example/live', 'p256dh-key', 'auth-key')`)
				if err != nil {
					env.T.Fatal(err)
				}
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/push/subscriptions",
				headers:        map[string]string{"Origin": "https://stockbot.rhionin.com"},
				body:           `{"endpoint":"https://push.example/new","keys":{"p256dh":"p256dh-key","auth":"auth-key"},"userId":"user-1","deviceId":"phone-a"}`,
				expectedStatus: http.StatusOK,
			},
			afterRequest: func(env testEnv) {
				assertSubscriptionEndpoints(env, map[string]string{
					"https://push.example/live": "",
					"https://push.example/new":  "stockbot.rhionin.com",
				})
			},
		},
	})
}

func assertSubscriptionEndpoints(env testEnv, want map[string]string) {
	env.T.Helper()
	rows, err := env.DB.Query(`SELECT endpoint, origin FROM push_subscriptions`)
	if err != nil {
		env.T.Fatal(err)
	}
	defer rows.Close()
	got := map[string]string{}
	for rows.Next() {
		var endpoint, origin string
		if err := rows.Scan(&endpoint, &origin); err != nil {
			env.T.Fatal(err)
		}
		got[endpoint] = origin
	}
	if err := rows.Err(); err != nil {
		env.T.Fatal(err)
	}
	if len(got) != len(want) {
		env.T.Fatalf("subscriptions = %#v, want %#v", got, want)
	}
	for endpoint, origin := range want {
		if got[endpoint] != origin {
			env.T.Fatalf("subscriptions = %#v, want %#v", got, want)
		}
	}
}

func TestPushUnrecognizedScan(t *testing.T) {
	var mu sync.Mutex
	var got []map[string]string
	cfg := testPushConfig(t, func(_ context.Context, sub push.Subscription, payload []byte) (int, error) {
		if sub.Endpoint != "https://push.example/phone" {
			t.Errorf("endpoint = %s", sub.Endpoint)
		}
		var body map[string]string
		if err := json.Unmarshal(payload, &body); err != nil {
			t.Errorf("payload: %v", err)
		}
		mu.Lock()
		got = append(got, body)
		mu.Unlock()
		return http.StatusCreated, nil
	})
	handler, _ := setupTestWithContributor(t, nil, WithPush(cfg))

	subscribe := httptest.NewRequest(http.MethodPost, "/api/push/subscriptions", strings.NewReader(
		`{"endpoint":"https://push.example/phone","keys":{"p256dh":"phone-p256","auth":"phone-auth"}}`,
	))
	subscribe.Header.Set("Content-Type", "application/json")
	subRec := httptest.NewRecorder()
	handler.ServeHTTP(subRec, subscribe)
	if subRec.Code != http.StatusOK {
		t.Fatalf("subscribe = %d %s", subRec.Code, subRec.Body.String())
	}

	scanReq := httptest.NewRequest(http.MethodPost, "/api/scans", strings.NewReader(
		`{"barcode":"910000000182","userId":"user-1","direction":"stock_in"}`,
	))
	scanReq.Header.Set("Content-Type", "application/json")
	scanRec := httptest.NewRecorder()
	handler.ServeHTTP(scanRec, scanReq)
	if scanRec.Code != http.StatusCreated {
		t.Fatalf("scan = %d %s", scanRec.Code, scanRec.Body.String())
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 {
		t.Fatalf("pushes = %#v", got)
	}
	if got[0]["title"] != "Unrecognized product" {
		t.Fatalf("title = %q", got[0]["title"])
	}
	if !strings.Contains(got[0]["body"], "910000000182") {
		t.Fatalf("body = %q", got[0]["body"])
	}
	if got[0]["url"] != "/?scanAlert=stock_in" {
		t.Fatalf("url = %q", got[0]["url"])
	}
}

func TestPushStaysBehindHouseholdLogin(t *testing.T) {
	cfg := testPushConfig(t, nil)
	gate := testHousehold(t, "pantry", "correct-horse-battery")
	handler, _ := setupTestWithContributor(t, nil, WithHouseholdAuth(gate), WithPush(cfg))

	open := httptest.NewRequest(http.MethodPost, "/api/push/subscriptions", strings.NewReader(
		`{"endpoint":"https://push.example/phone","keys":{"p256dh":"phone-p256","auth":"phone-auth"}}`,
	))
	open.Header.Set("Content-Type", "application/json")
	open.Header.Set(entryHeader, entryPublic)
	openRec := httptest.NewRecorder()
	handler.ServeHTTP(openRec, open)
	if openRec.Code != http.StatusUnauthorized {
		t.Fatalf("public subscribe without a session = %d", openRec.Code)
	}

	worker := httptest.NewRequest(http.MethodGet, "/sw.js", nil)
	worker.Header.Set(entryHeader, entryPublic)
	workerRec := httptest.NewRecorder()
	handler.ServeHTTP(workerRec, worker)
	if workerRec.Code == http.StatusUnauthorized {
		t.Fatal("the service worker was blocked by household login")
	}
	body, _ := io.ReadAll(workerRec.Result().Body)
	if strings.Contains(string(body), cfg.PrivateKey) {
		t.Fatal("service worker response included the VAPID private key")
	}

	login := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(
		`{"username":"pantry","password":"correct-horse-battery"}`,
	))
	login.Header.Set("Content-Type", "application/json")
	login.Header.Set(entryHeader, entryPublic)
	loginRec := httptest.NewRecorder()
	handler.ServeHTTP(loginRec, login)
	if loginRec.Code != http.StatusOK {
		t.Fatalf("login = %d %s", loginRec.Code, loginRec.Body.String())
	}
	cookie := loginRec.Result().Cookies()
	if len(cookie) == 0 {
		t.Fatal("login did not set a session cookie")
	}

	authed := httptest.NewRequest(http.MethodPost, "/api/push/subscriptions", strings.NewReader(
		`{"endpoint":"https://push.example/phone","keys":{"p256dh":"phone-p256","auth":"phone-auth"}}`,
	))
	authed.Header.Set("Content-Type", "application/json")
	authed.Header.Set(entryHeader, entryPublic)
	authed.AddCookie(cookie[0])
	authedRec := httptest.NewRecorder()
	handler.ServeHTTP(authedRec, authed)
	if authedRec.Code != http.StatusOK {
		t.Fatalf("signed-in subscribe = %d %s", authedRec.Code, authedRec.Body.String())
	}
	if strings.Contains(authedRec.Body.String(), cfg.PrivateKey) {
		t.Fatal("subscribe response included the VAPID private key")
	}
}

func TestPushSaveDefaultsUserIDFromPrincipal(t *testing.T) {
	cfg := testPushConfig(t, nil)
	handler, _ := setupTestWithContributor(t, nil, WithPush(cfg))
	body := `{"endpoint":"https://push.example/device","keys":{"p256dh":"p256dh-key","auth":"auth-key"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/push/subscriptions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(auth.WithPrincipal(req.Context(), auth.Principal{Kind: "account", UserID: "not a token"}))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d %s", rec.Code, rec.Body.String())
	}
}
