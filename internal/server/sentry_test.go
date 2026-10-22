package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/buildinfo"
	"github.com/getsentry/sentry-go"
	"github.com/go-json-experiment/json"
)

const fakeSentryDSN = "https://key@o1.ingest.example/123"

type recordTransport struct {
	mu     sync.Mutex
	events []*sentry.Event
	flush  time.Duration
}

func (t *recordTransport) Flush(timeout time.Duration) bool {
	t.mu.Lock()
	t.flush = timeout
	t.mu.Unlock()
	return true
}

func (t *recordTransport) FlushWithContext(ctx context.Context) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if deadline, ok := ctx.Deadline(); ok {
		t.flush = time.Until(deadline)
	}
	return true
}

func (t *recordTransport) Configure(sentry.ClientOptions) {}

func (t *recordTransport) SendEvent(event *sentry.Event) {
	t.mu.Lock()
	t.events = append(t.events, event)
	t.mu.Unlock()
}

func (t *recordTransport) Close() {}

func (t *recordTransport) snapshot() []*sentry.Event {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]*sentry.Event, len(t.events))
	copy(out, t.events)
	return out
}

func (t *recordTransport) errors() []*sentry.Event {
	var out []*sentry.Event
	for _, event := range t.snapshot() {
		if event.Type != "transaction" {
			out = append(out, event)
		}
	}
	return out
}

func assertNoHouseholdText(t *testing.T, event *sentry.Event) {
	t.Helper()
	var parts []string
	parts = append(parts, event.Message)
	for _, ex := range event.Exception {
		parts = append(parts, ex.Value, ex.Type)
		if ex.Mechanism != nil {
			parts = append(parts, ex.Mechanism.Description)
		}
	}
	if event.Request != nil {
		parts = append(parts, event.Request.URL, event.Request.QueryString, event.Request.Data, event.Request.Cookies)
		for name, value := range event.Request.Headers {
			parts = append(parts, name, value)
		}
	}
	for key, value := range event.Tags {
		parts = append(parts, key, value)
	}
	parts = append(parts, event.User.Email, event.User.Username, event.User.Name)
	for _, crumb := range event.Breadcrumbs {
		if crumb == nil {
			continue
		}
		parts = append(parts, crumb.Message)
		for key, value := range crumb.Data {
			parts = append(parts, key, fmt.Sprint(value))
		}
	}
	body := strings.Join(parts, "\n")
	for _, secret := range []string{"Oat Milk", "query-secret", "cookie-secret", "auth-secret", "header-token", "header-secret", "ada@example.com"} {
		if strings.Contains(body, secret) {
			t.Fatalf("event still contains %q: %s", secret, body)
		}
	}
}

func useSentryTransport(t *testing.T, tr sentry.Transport) {
	t.Helper()
	prev := sentryTestTransport
	sentryTestTransport = tr
	t.Cleanup(func() {
		sentryTestTransport = prev
		sentryEnabled.Store(false)
		_ = sentry.Init(sentry.ClientOptions{})
	})
}

func TestScrubSentryEventStripsSecretsAndItemNames(t *testing.T) {
	event := &sentry.Event{
		Message: "could not stock item Oat Milk for ada@example.com",
		Tags: map[string]string{
			"item":  "Oat Milk",
			"note":  "the good oats",
			"email": "ada@example.com",
			"route": "POST /api/inventory/item-9",
		},
		User: sentry.User{Email: "ada@example.com", Username: "Ada", Name: "Oat Milk"},
		Request: &sentry.Request{
			URL:         "https://stockbot.example/api/inventory/item-9?token=query-secret&name=Oat+Milk",
			Method:      "POST",
			QueryString: "token=query-secret&name=Oat+Milk",
			Data:        `{"name":"Oat Milk","note":"weekly"}`,
			Cookies:     "pantry_session=cookie-secret",
			Headers: map[string]string{
				"Authorization":             "Bearer auth-secret",
				"Cookie":                    "pantry_session=cookie-secret",
				"Set-Cookie":                "pantry_session=cookie-secret",
				"X-Sync-Token":              "header-token",
				"X-Pantry-Secret":           "header-secret",
				"X-Pantry-Client-Ip":        "198.51.100.77",
				"Cf-Connecting-Ip":          "203.0.113.77",
				"X-Pantry-Deploy-Signature": "deploy-signature-value",
				"Cf-Ray":                    "cf-ray-value",
				"Host":                      "stockbot.example",
				"Content-Type":              "application/json",
				"User-Agent":                "Stockbot/1",
			},
			Env: map[string]string{"REMOTE_ADDR": "203.0.113.8", "QUERY_STRING": "token=query-secret"},
		},
		Exception: []sentry.Exception{{
			Type:  "*scan.CommitError",
			Value: `update item "Oat Milk": database is locked`,
		}},
		Breadcrumbs: []*sentry.Breadcrumb{{
			Type:     "http",
			Category: "http",
			Message:  "saved Oat Milk ada@example.com",
			Data: map[string]interface{}{
				"url":         "https://stockbot.example/api/items/item-9?token=query-secret",
				"method":      "POST",
				"status_code": 500,
				"body":        "Oat Milk",
				"note":        "weekly",
			},
		}},
	}

	got := scrubSentryEvent(event)
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	body := string(raw)
	for _, secret := range []string{
		"Oat Milk", "ada@example.com", "query-secret", "cookie-secret",
		"auth-secret", "header-token", "header-secret", "weekly", "the good oats",
		"203.0.113.8", "198.51.100.77", "203.0.113.77",
		"deploy-signature-value", "cf-ray-value", "stockbot.example",
	} {
		if strings.Contains(body, secret) {
			t.Fatalf("event still contains %q: %s", secret, body)
		}
	}
	if got.Request.URL != "/api/inventory/item-9" {
		t.Fatalf("url = %q", got.Request.URL)
	}
	if got.Request.QueryString != "" || got.Request.Data != "" || got.Request.Cookies != "" {
		t.Fatalf("request kept query, body, or cookies: %+v", got.Request)
	}
	if got.Request.Headers["Content-Type"] != "application/json" || got.Request.Headers["User-Agent"] != "Stockbot/1" {
		t.Fatalf("headers = %#v", got.Request.Headers)
	}
	if len(got.Request.Headers) != 2 {
		t.Fatalf("headers = %#v", got.Request.Headers)
	}
	if got.Exception[0].Type != "*scan.CommitError" {
		t.Fatalf("type = %s", got.Exception[0].Type)
	}
	if got.Exception[0].Value != "server error (*scan.CommitError)" {
		t.Fatalf("value = %s", got.Exception[0].Value)
	}
	if got.Message != "server error (*scan.CommitError)" {
		t.Fatalf("message = %s", got.Message)
	}
	if got.Tags["route"] != "POST /api/inventory/item-9" {
		t.Fatalf("tags = %#v", got.Tags)
	}
	if _, ok := got.Tags["item"]; ok {
		t.Fatal("item tag kept")
	}
	if got.Breadcrumbs[0].Message != "" {
		t.Fatalf("breadcrumb message = %q", got.Breadcrumbs[0].Message)
	}
	if got.Breadcrumbs[0].Data["url"] != "/api/items/item-9" {
		t.Fatalf("breadcrumb url = %#v", got.Breadcrumbs[0].Data)
	}
	if _, ok := got.Breadcrumbs[0].Data["body"]; ok {
		t.Fatal("breadcrumb body kept")
	}
	if !got.User.IsEmpty() {
		t.Fatalf("user kept: %+v", got.User)
	}
}

func TestScrubSentryContextsAllowlist(t *testing.T) {
	event := &sentry.Event{
		Contexts: map[string]sentry.Context{
			"trace":     {"trace_id": "abc"},
			"os":        {"name": "linux"},
			"device":    {"family": "pi"},
			"runtime":   {"name": "go"},
			"browser":   {"name": "chrome"},
			"app":       {"app_start_time": "1"},
			"extra":     {"name": "Oat Milk", "note": "a note"},
			"response":  {"body": "a note"},
			"household": {"item": "Oat Milk"},
		},
	}
	got := scrubSentryEvent(event)
	for _, key := range []string{"trace", "os", "device", "runtime", "browser", "app"} {
		if _, ok := got.Contexts[key]; !ok {
			t.Fatalf("context %s dropped", key)
		}
	}
	for _, key := range []string{"extra", "response", "household"} {
		if _, ok := got.Contexts[key]; ok {
			t.Fatalf("context %s kept", key)
		}
	}
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	body := string(raw)
	for _, secret := range []string{"Oat Milk", "a note"} {
		if strings.Contains(body, secret) {
			t.Fatalf("event still contains %q: %s", secret, body)
		}
	}
}

func TestScrubSentryRejectsForwardedForSensitiveTagsAndSpanQueries(t *testing.T) {
	event := &sentry.Event{
		Request: &sentry.Request{
			URL: "/api/items/item-9",
			Headers: map[string]string{
				"Content-Type":    "application/json",
				"User-Agent":      "Stockbot/1",
				"X-Forwarded-For": "198.51.100.77",
			},
		},
		Tags: map[string]string{
			"route":       "POST /api/items?token=query-secret",
			"environment": "ada@example.com",
			"method":      "POST",
		},
		Spans: []*sentry.Span{{
			Name:        "db.query /api/x?token=query-secret",
			Description: "select item where id=? -- /x?token=query-secret",
			Data: map[string]interface{}{
				"name": "Oat Milk",
				"url":  "/x?q=query-secret",
			},
		}},
	}
	got := scrubSentryEvent(event)
	if _, ok := got.Request.Headers["X-Forwarded-For"]; ok {
		t.Fatalf("X-Forwarded-For kept: %#v", got.Request.Headers)
	}
	if got.Request.Headers["Content-Type"] != "application/json" || got.Request.Headers["User-Agent"] != "Stockbot/1" {
		t.Fatalf("headers = %#v", got.Request.Headers)
	}
	if _, ok := got.Tags["route"]; ok {
		t.Fatalf("route tag with a query kept: %#v", got.Tags)
	}
	if _, ok := got.Tags["environment"]; ok {
		t.Fatalf("environment tag with an email kept: %#v", got.Tags)
	}
	if got.Tags["method"] != "POST" {
		t.Fatalf("tags = %#v", got.Tags)
	}
	span := got.Spans[0]
	if strings.Contains(span.Name, "?") || strings.Contains(span.Name, "query-secret") {
		t.Fatalf("span name = %q", span.Name)
	}
	if span.Name != "db.query /api/x" {
		t.Fatalf("span name = %q", span.Name)
	}
	if strings.Contains(span.Description, "query-secret") || strings.Contains(span.Description, "/x?") {
		t.Fatalf("span description = %q", span.Description)
	}
	if _, ok := span.Data["name"]; ok {
		t.Fatalf("span name data kept: %#v", span.Data)
	}
	if span.Data["url"] != "/x" {
		t.Fatalf("span url = %#v", span.Data)
	}
}

func TestScrubSentryBreadcrumbDropsNil(t *testing.T) {
	if scrubSentryEvent(nil) != nil {
		t.Fatal("nil event")
	}
	if scrubSentryBreadcrumb(nil) != nil {
		t.Fatal("nil breadcrumb")
	}
}

func TestSentryStaysOffWithoutDSN(t *testing.T) {
	tr := &recordTransport{}
	useSentryTransport(t, tr)
	settings := LoadSentrySettings(func(key string) string {
		if key == "SENTRY_TUNNEL_PROJECT_IDS" {
			return "123"
		}
		return ""
	})
	if settings.DSN != "" || settings.Rejected {
		t.Fatalf("settings = %+v", settings)
	}
	if err := InitSentry(settings); err != nil {
		t.Fatalf("init: %v", err)
	}
	if sentryEnabled.Load() {
		t.Fatal("reporting enabled with an empty DSN")
	}
	reportServerError(nil, errors.New("stock item Oat Milk"))
	FlushSentry()
	if len(tr.snapshot()) != 0 {
		t.Fatal("empty DSN sent an event")
	}
	if tr.flush != 0 {
		t.Fatal("empty DSN flushed the transport")
	}
}

func TestSentrySettingsUseBuildCommitAndProduction(t *testing.T) {
	tr := &recordTransport{}
	useSentryTransport(t, tr)
	prev := buildinfo.Commit
	buildinfo.Commit = "abc123test"
	t.Cleanup(func() { buildinfo.Commit = prev })

	settings := LoadSentrySettings(func(key string) string {
		switch key {
		case "SENTRY_DSN":
			return fakeSentryDSN
		case "SENTRY_TUNNEL_PROJECT_IDS":
			return "123, 124"
		default:
			return ""
		}
	})
	if settings.Rejected || settings.IngestHost != "o1.ingest.example" || settings.Environment != "production" {
		t.Fatalf("settings = %+v", settings)
	}
	if _, ok := settings.ProjectIDs["123"]; !ok {
		t.Fatal("missing project 123")
	}
	if _, ok := settings.ProjectIDs["124"]; !ok {
		t.Fatal("missing project 124")
	}
	if err := InitSentry(settings); err != nil {
		t.Fatalf("init: %v", err)
	}
	hub := sentry.CurrentHub()
	hub.CaptureException(errors.New("disk full"))
	events := tr.snapshot()
	if len(events) != 1 {
		t.Fatalf("events = %d", len(events))
	}
	if events[0].Release != "abc123test" {
		t.Fatalf("release = %q", events[0].Release)
	}
	if events[0].Environment != "production" {
		t.Fatalf("environment = %q", events[0].Environment)
	}
	FlushSentry()
	low, high := sentryFlushTimeout-100*time.Millisecond, sentryFlushTimeout+100*time.Millisecond
	if tr.flush < low || tr.flush > high {
		t.Fatalf("flush = %s, want between %s and %s", tr.flush, low, high)
	}
}

func TestSentryRejectsUnusableDSN(t *testing.T) {
	settings := LoadSentrySettings(func(key string) string {
		if key == "SENTRY_DSN" {
			return "not-a-dsn"
		}
		return ""
	})
	if !settings.Rejected || settings.DSN != "" {
		t.Fatalf("settings = %+v", settings)
	}
	named := LoadSentrySettings(func(key string) string {
		if key == "SENTRY_DSN" {
			return fakeSentryDSN
		}
		if key == "SENTRY_ENVIRONMENT" {
			return "staging"
		}
		return ""
	})
	if named.Environment != "staging" || named.IngestHost != "o1.ingest.example" {
		t.Fatalf("named = %+v", named)
	}
}

func TestSentryTraceSamplerSkipsHealthAndEvents(t *testing.T) {
	cases := []struct {
		name string
		want float64
	}{
		{name: "GET /health", want: 0},
		{name: "GET /health/", want: 0},
		{name: "GET /api/events", want: 0},
		{name: "GET GET /api/events", want: 0},
		{name: "POST /api/products/item-9", want: sentryTracesSampleRate},
		{name: "", want: 0},
	}
	for _, tc := range cases {
		var ctx sentry.SamplingContext
		if tc.name != "" {
			ctx.Span = &sentry.Span{Name: tc.name}
		}
		if got := sentryTracesSampler(ctx); got != tc.want {
			t.Errorf("%q sample = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestServerErrorIsReportedWithoutHouseholdText(t *testing.T) {
	tr := &recordTransport{}
	useSentryTransport(t, tr)
	settings := LoadSentrySettings(func(key string) string {
		if key == "SENTRY_DSN" {
			return fakeSentryDSN
		}
		if key == "SENTRY_ENVIRONMENT" {
			return "test"
		}
		return ""
	})
	if err := InitSentry(settings); err != nil {
		t.Fatalf("init: %v", err)
	}

	var handler http.Handler = HandleJSON(func(Request[struct{}, struct{}]) (struct{}, error) {
		return struct{}{}, fmt.Errorf("stock item Oat Milk: %w", errors.New("disk full"))
	})
	handler = withSentry(handler)

	req := httptest.NewRequest(http.MethodPost, "/api/items/item-9?token=query-secret", strings.NewReader(`{"name":"Oat Milk"}`))
	req.Host = "stockbot.example"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cookie", "pantry_session=cookie-secret")
	req.Header.Set("Authorization", "Bearer auth-secret")
	req.Header.Set("X-Sync-Token", "header-token")
	req.Header.Set("X-Pantry-Secret", "header-secret")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d body %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "Oat Milk") {
		t.Fatalf("response leaked the item name: %s", rec.Body.String())
	}

	events := tr.errors()
	if len(events) != 1 {
		t.Fatalf("events = %d", len(events))
	}
	assertNoHouseholdText(t, events[0])
	if events[0].Request == nil || events[0].Request.URL != "/api/items/item-9" {
		t.Fatalf("request = %+v", events[0].Request)
	}
	if len(events[0].Exception) == 0 || !strings.Contains(events[0].Exception[0].Value, "server error (") {
		t.Fatalf("exception = %+v", events[0].Exception)
	}

	bad := httptest.NewRequest(http.MethodPost, "/api/items/item-9", strings.NewReader("{"))
	badRec := httptest.NewRecorder()
	handler.ServeHTTP(badRec, bad)
	if badRec.Code != http.StatusBadRequest {
		t.Fatalf("bad status = %d", badRec.Code)
	}
	if len(tr.errors()) != 1 {
		t.Fatal("a 400 was reported")
	}
}

func TestPanicIsReportedAndRepanics(t *testing.T) {
	tr := &recordTransport{}
	useSentryTransport(t, tr)
	if err := InitSentry(SentrySettings{DSN: fakeSentryDSN, Environment: "test", IngestHost: "o1.ingest.example"}); err != nil {
		t.Fatalf("init: %v", err)
	}
	handler := withSentry(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("Oat Milk exploded")
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/items/item-9", nil)
	rec := httptest.NewRecorder()
	defer func() {
		recovered := recover()
		if recovered == nil {
			t.Fatal("panic was swallowed")
		}
		if recovered != "Oat Milk exploded" {
			t.Fatalf("repanic = %#v", recovered)
		}
		events := tr.errors()
		if len(events) != 1 {
			t.Fatalf("events = %d", len(events))
		}
		assertNoHouseholdText(t, events[0])
		if events[0].Message != "server error (error)" {
			t.Fatalf("message = %q", events[0].Message)
		}
	}()
	handler.ServeHTTP(rec, req)
}

func TestTunnelConfigCarriesPublicKeys(t *testing.T) {
	settings := LoadSentrySettings(func(key string) string {
		switch key {
		case "SENTRY_DSN":
			return fakeSentryDSN
		case "SENTRY_TUNNEL_PROJECT_IDS":
			return "123, 124"
		case "SENTRY_TUNNEL_PUBLIC_KEYS":
			return "browser-key, other-key"
		default:
			return ""
		}
	})
	cfg := settings.TunnelConfig()
	if cfg.IngestHost != "o1.ingest.example" {
		t.Fatalf("host = %q", cfg.IngestHost)
	}
	if _, ok := cfg.ProjectIDs["123"]; !ok {
		t.Fatalf("projects = %#v", cfg.ProjectIDs)
	}
	if _, ok := cfg.ProjectIDs["124"]; !ok {
		t.Fatalf("projects = %#v", cfg.ProjectIDs)
	}
	if _, ok := cfg.PublicKeys["browser-key"]; !ok || len(cfg.PublicKeys) != 2 {
		t.Fatalf("public keys = %#v", cfg.PublicKeys)
	}
	if _, ok := cfg.PublicKeys["other-key"]; !ok {
		t.Fatalf("public keys = %#v", cfg.PublicKeys)
	}
}

func TestUnmatchedAndUnauthorizedTransactionNames(t *testing.T) {
	prevRate := sentryTracesSampleRate
	sentryTracesSampleRate = 1
	t.Cleanup(func() { sentryTracesSampleRate = prevRate })

	tr := &recordTransport{}
	useSentryTransport(t, tr)
	if err := InitSentry(SentrySettings{DSN: fakeSentryDSN, Environment: "test", IngestHost: "o1.ingest.example"}); err != nil {
		t.Fatalf("init: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("GET /api/build", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := withSentry(mux)

	miss := httptest.NewRequest(http.MethodGet, "/api/items/item-9", nil)
	missRec := httptest.NewRecorder()
	handler.ServeHTTP(missRec, miss)
	if missRec.Code != http.StatusNotFound {
		t.Fatalf("unmatched status = %d", missRec.Code)
	}

	health := httptest.NewRequest(http.MethodGet, "/health", nil)
	healthRec := httptest.NewRecorder()
	handler.ServeHTTP(healthRec, health)
	if healthRec.Code != http.StatusOK {
		t.Fatalf("health status = %d", healthRec.Code)
	}

	build := httptest.NewRequest(http.MethodGet, "/api/build", nil)
	buildRec := httptest.NewRecorder()
	handler.ServeHTTP(buildRec, build)
	if buildRec.Code != http.StatusOK {
		t.Fatalf("build status = %d", buildRec.Code)
	}

	gate := testHousehold(t, "pantry", "correct-horse-battery")
	authed := http.NewServeMux()
	authed.HandleFunc("POST /api/items/{id}", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	locked := withSentry(gate.Middleware(authed))
	denied := httptest.NewRequest(http.MethodPost, "/api/items/item-9", nil)
	denied.Header.Set(entryHeader, entryPublic)
	deniedRec := httptest.NewRecorder()
	locked.ServeHTTP(deniedRec, denied)
	if deniedRec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d body %s", deniedRec.Code, deniedRec.Body.String())
	}

	var buildName, deniedName string
	for _, event := range tr.snapshot() {
		if event.Type != "transaction" {
			continue
		}
		if strings.Contains(event.Transaction, "item-9") {
			t.Fatalf("transaction name = %q", event.Transaction)
		}
		switch event.Transaction {
		case "GET <unmatched>":
			t.Fatalf("unmatched 404 was traced: %q", event.Transaction)
		case "GET /api/build":
			buildName = event.Transaction
		case "POST <unmatched>":
			deniedName = event.Transaction
		case "GET /health":
			t.Fatal("health was traced")
		}
	}
	if buildName == "" || deniedName == "" {
		t.Fatalf("names build=%q denied=%q", buildName, deniedName)
	}
}

func TestSentryMiddlewareScrubsErrorAndTransaction(t *testing.T) {
	prevRate := sentryTracesSampleRate
	sentryTracesSampleRate = 1
	t.Cleanup(func() { sentryTracesSampleRate = prevRate })

	tr := &recordTransport{}
	useSentryTransport(t, tr)
	if err := InitSentry(SentrySettings{DSN: fakeSentryDSN, Environment: "test", IngestHost: "o1.ingest.example"}); err != nil {
		t.Fatalf("init: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/items/{id}", HandleJSON(func(Request[struct{}, struct{}]) (struct{}, error) {
		return struct{}{}, fmt.Errorf("stock item Oat Milk: %w", errors.New("disk full"))
	}))
	handler := withSentry(mux)

	req := httptest.NewRequest(http.MethodPost, "/api/items/item-9?token=query-secret", strings.NewReader(`{"name":"Oat Milk"}`))
	req.Host = "stockbot.example"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Stockbot/1")
	req.Header.Set("Cookie", "pantry_session=cookie-secret")
	req.Header.Set("Authorization", "Bearer auth-secret")
	req.Header.Set("X-Pantry-Client-Ip", "198.51.100.77")
	req.Header.Set("Cf-Connecting-Ip", "203.0.113.77")
	req.Header.Set("X-Pantry-Deploy-Signature", "deploy-signature-value")
	req.Header.Set("Cf-Ray", "cf-ray-value")
	req.Header.Set("Cf-Visitor", "cf-visitor-value")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d body %s", rec.Code, rec.Body.String())
	}

	var errorEvent, transaction *sentry.Event
	for _, event := range tr.snapshot() {
		if event.Type == "transaction" {
			if transaction != nil {
				t.Fatal("two transactions")
			}
			transaction = event
			continue
		}
		if errorEvent != nil {
			t.Fatal("two errors")
		}
		errorEvent = event
	}
	if errorEvent == nil || transaction == nil {
		t.Fatalf("error present=%v transaction present=%v", errorEvent != nil, transaction != nil)
	}
	assertScrubbedTransportEvent(t, errorEvent)
	assertScrubbedTransportEvent(t, transaction)
	if transaction.Transaction != "POST /api/items/{id}" || strings.Contains(transaction.Transaction, "item-9") {
		t.Fatalf("transaction name = %q", transaction.Transaction)
	}
}

func assertScrubbedTransportEvent(t *testing.T, event *sentry.Event) {
	t.Helper()
	if event.Request == nil {
		t.Fatal("missing request")
	}
	if event.Request.URL != "/api/items/item-9" {
		t.Fatalf("url = %q", event.Request.URL)
	}
	if event.Request.QueryString != "" || event.Request.Data != "" || event.Request.Cookies != "" {
		t.Fatalf("request kept query, body, or cookies: %+v", event.Request)
	}
	if event.Request.Headers["Content-Type"] != "application/json" || event.Request.Headers["User-Agent"] != "Stockbot/1" || len(event.Request.Headers) != 2 {
		t.Fatalf("headers = %#v", event.Request.Headers)
	}
	for _, name := range []string{
		"Authorization", "Cookie", "Set-Cookie", "Host",
		"X-Pantry-Client-Ip", "Cf-Connecting-Ip", "X-Pantry-Deploy-Signature",
		"Cf-Ray", "Cf-Visitor",
	} {
		if _, ok := event.Request.Headers[name]; ok {
			t.Fatalf("header %s kept", name)
		}
	}
	var blob strings.Builder
	blob.WriteString(event.Message)
	blob.WriteString(event.Transaction)
	blob.WriteString(event.Request.URL)
	blob.WriteString(event.Request.QueryString)
	blob.WriteString(event.Request.Data)
	blob.WriteString(event.Request.Cookies)
	for name, value := range event.Request.Headers {
		blob.WriteString(name)
		blob.WriteString(value)
	}
	for _, ex := range event.Exception {
		blob.WriteString(ex.Value)
		blob.WriteString(ex.Type)
	}
	for key, value := range event.Tags {
		blob.WriteString(key)
		blob.WriteString(value)
	}
	for _, span := range event.Spans {
		if span == nil {
			continue
		}
		blob.WriteString(span.Name)
		blob.WriteString(span.Description)
	}
	for _, crumb := range event.Breadcrumbs {
		if crumb == nil {
			continue
		}
		blob.WriteString(crumb.Message)
		for key, value := range crumb.Data {
			blob.WriteString(key)
			if text, ok := value.(string); ok {
				blob.WriteString(text)
			}
		}
	}
	for _, secret := range []string{
		"query-secret", "cookie-secret", "auth-secret", "198.51.100.77", "203.0.113.77",
		"deploy-signature-value", "cf-ray-value", "cf-visitor-value", "stockbot.example", "Oat Milk",
	} {
		if strings.Contains(blob.String(), secret) {
			t.Fatalf("event kept %q", secret)
		}
	}
}
