package server

// The SDK's own HTTP transport posts to a local httptest server.
// The DSN is fake and the host is that loopback server.

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/getsentry/sentry-go"
)

type wireItem struct {
	Header  map[string]any
	Payload map[string]any
	Raw     string
}

type wireSink struct {
	mu    sync.Mutex
	raw   []string
	items []wireItem
	paths []string
}

func (s *wireSink) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var body io.Reader = r.Body
	if r.Header.Get("Content-Encoding") == "gzip" {
		zr, err := gzip.NewReader(r.Body)
		if err == nil {
			body = zr
		}
	}
	b, _ := io.ReadAll(body)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.paths = append(s.paths, r.URL.Path)
	s.raw = append(s.raw, string(b))
	lines := bytes.Split(b, []byte("\n"))
	for i := 1; i+1 < len(lines); i += 2 {
		var h, p map[string]any
		_ = json.Unmarshal(lines[i], &h)
		if err := json.Unmarshal(lines[i+1], &p); err != nil {
			continue
		}
		s.items = append(s.items, wireItem{Header: h, Payload: p, Raw: string(lines[i+1])})
	}
	w.WriteHeader(200)
	_, _ = w.Write([]byte(`{"id":"x"}`))
}

var wireSecrets = []string{
	"Oat Milk", "query-secret", "cookie-secret", "auth-secret", "header-token", "header-secret",
	"ada@example.com", "198.51.100.77", "203.0.113.77", "192.0.2.55", "stockbot.example",
	"tag-secret", "ctx-secret", "crumb-secret", "user-secret", "span-secret", "extra-secret",
	"deploy-signature-value", "cf-ray-value", "Ada Lovelace", "join-secret", "panic-secret",
}

func keysOf(m map[string]any) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestSentryWireScrubsHouseholdText(t *testing.T) {
	sink := &wireSink{}
	srv := httptest.NewServer(sink)
	t.Cleanup(srv.Close)
	if !strings.HasPrefix(srv.URL, "http://127.0.0.1:") && !strings.HasPrefix(srv.URL, "http://[::1]:") {
		t.Fatalf("wire server is not loopback: %s", srv.URL)
	}
	prevRate := sentryTracesSampleRate
	sentryTracesSampleRate = 1
	t.Cleanup(func() { sentryTracesSampleRate = prevRate })
	prevTr := sentryTestTransport
	sentryTestTransport = nil // production path: SDK's own HTTP transport
	t.Cleanup(func() {
		sentryTestTransport = prevTr
		sentryEnabled.Store(false)
		_ = sentry.Init(sentry.ClientOptions{})
	})
	dsn := "http://fakepublickey@" + strings.TrimPrefix(srv.URL, "http://") + "/123"
	if err := InitSentry(SentrySettings{DSN: dsn, Environment: "test", IngestHost: "127.0.0.1"}); err != nil {
		t.Fatalf("init: %v", err)
	}
	enrich := func(r *http.Request) {
		hub := sentry.GetHubFromContext(r.Context())
		if hub == nil {
			t.Error("no hub on request")
			return
		}
		hub.Scope().SetUser(sentry.User{ID: "user-secret", Email: "ada@example.com", IPAddress: "192.0.2.55", Name: "Ada Lovelace", Username: "user-secret"})
		hub.Scope().SetTag("note", "tag-secret")
		hub.Scope().SetTag("route", "/api/items?token=query-secret")
		hub.Scope().SetTag("environment", "ada@example.com")
		hub.Scope().SetContext("household", sentry.Context{"name": "ctx-secret"})
		hub.Scope().SetContext("extra", sentry.Context{"note": "extra-secret"}) // where Extra users migrate to
		hub.Scope().SetContext("response", sentry.Context{"body": "ctx-secret"})
		hub.AddBreadcrumb(&sentry.Breadcrumb{Message: "crumb-secret Oat Milk", Category: "http", Data: map[string]any{"url": "/api/x?token=query-secret", "body": "crumb-secret", "method": "POST", "status_code": 500}}, nil)
		if span := sentry.SpanFromContext(r.Context()); span != nil {
			child := span.StartChild("db.query", sentry.WithDescription("select ? -- /x?token=query-secret"))
			child.SetData("name", "span-secret")
			child.SetData("url", "/x?q=query-secret")
			child.Finish()
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/items/{id}", func(w http.ResponseWriter, r *http.Request) {
		enrich(r)
		HandleJSON(func(Request[struct{}, struct{}]) (struct{}, error) {
			return struct{}{}, errors.Join(fmt.Errorf("stock item Oat Milk: %w", errors.New("join-secret disk full")), errors.New("ada@example.com"))
		})(w, r)
	})
	mux.HandleFunc("GET /api/panic", func(w http.ResponseWriter, r *http.Request) {
		enrich(r)
		panic("panic-secret Oat Milk")
	})
	mux.HandleFunc("GET /api/ok", func(w http.ResponseWriter, r *http.Request) {
		enrich(r)
		hub := sentry.GetHubFromContext(r.Context())
		hub.CaptureMessage("message Oat Milk ada@example.com")
		w.WriteHeader(204)
	})
	mux.HandleFunc("GET /api/gone", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(404) })
	mux.HandleFunc("GET /api/teapot", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(418) })
	mux.HandleFunc("GET /api/unavail", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) })
	handler := withSentry(mux)

	do := func(method, target, body string) int {
		req := httptest.NewRequest(method, target, strings.NewReader(body))
		req.Host = "stockbot.example"
		req.RemoteAddr = "203.0.113.77:5555"
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "Stockbot/1")
		req.Header.Set("Cookie", "pantry_session=cookie-secret")
		req.Header.Set("Authorization", "Bearer auth-secret")
		req.Header.Set("X-Sync-Token", "header-token")
		req.Header.Set("X-Pantry-Secret", "header-secret")
		req.Header.Set("X-Forwarded-For", "198.51.100.77")
		req.Header.Set("X-Real-Ip", "198.51.100.77")
		req.Header.Set("X-Pantry-Client-Ip", "198.51.100.77")
		req.Header.Set("X-Pantry-Deploy-Signature", "deploy-signature-value")
		req.Header.Set("Cf-Ray", "cf-ray-value")
		rec := httptest.NewRecorder()
		func() {
			defer func() { _ = recover() }()
			handler.ServeHTTP(rec, req)
		}()
		return rec.Code
	}
	codes := map[string]int{
		"error":        do("POST", "/api/items/item-9?token=query-secret", `{"name":"Oat Milk","note":"ada@example.com"}`),
		"panic":        do("GET", "/api/panic?token=query-secret", ""),
		"message":      do("GET", "/api/ok?token=query-secret", ""),
		"unmatched404": do("GET", "/nope/Oat-Milk?token=query-secret", ""),
		"handler404":   do("GET", "/api/gone", ""),
		"teapot418":    do("GET", "/api/teapot", ""),
		"unavail503":   do("GET", "/api/unavail", ""),
		"unmatched405": do("DELETE", "/api/ok", ""),
	}
	FlushSentry()
	time.Sleep(300 * time.Millisecond)
	FlushSentry()

	sink.mu.Lock()
	defer sink.mu.Unlock()
	report := map[string]any{"codes": codes, "paths": sink.paths, "requests": len(sink.raw)}
	var summary []map[string]any
	var txNames []string
	nErr, nTx := 0, 0
	for _, raw := range sink.raw {
		for _, s := range wireSecrets {
			if strings.Contains(raw, s) {
				t.Errorf("WIRE LEAK %q in envelope: %.600s", s, raw)
			}
		}
	}
	allowHeaders := map[string]bool{"Content-Type": true, "User-Agent": true}
	allowCtx := map[string]bool{"trace": true, "os": true, "device": true, "runtime": true, "browser": true, "app": true}
	allowTags := map[string]bool{"route": true, "transaction": true, "status": true, "method": true, "runtime": true, "runtime.name": true, "os": true, "os.name": true, "server_name": true, "release": true, "environment": true, "device": true, "device.family": true, "browser": true, "browser.name": true, "level": true, "mechanism": true, "handled": true, "http.status_code": true}
	for _, it := range sink.items {
		typ, _ := it.Header["type"].(string)
		p := it.Payload
		row := map[string]any{"type": typ, "keys": keysOf(p)}
		switch typ {
		case "event":
			nErr++
		case "transaction":
			nTx++
			name, _ := p["transaction"].(string)
			txNames = append(txNames, name)
			row["transaction"] = name
		default:
			summary = append(summary, row)
			continue
		}
		if u, ok := p["user"].(map[string]any); ok && len(u) > 0 {
			t.Errorf("%s: user on wire: %v", typ, u)
		}
		for _, k := range []string{"extra", "modules"} {
			if v, ok := p[k]; ok {
				row[k] = v
			}
		}
		if req, ok := p["request"].(map[string]any); ok {
			row["requestKeys"] = keysOf(req)
			for _, k := range []string{"cookies", "query_string", "data", "env"} {
				if v, ok := req[k]; ok && v != "" && v != nil {
					t.Errorf("%s: request.%s on wire: %v", typ, k, v)
				}
			}
			if h, ok := req["headers"].(map[string]any); ok {
				row["headers"] = keysOf(h)
				for k := range h {
					if !allowHeaders[k] {
						t.Errorf("%s: header %s on wire", typ, k)
					}
				}
			}
			if u, _ := req["url"].(string); strings.Contains(u, "?") || strings.Contains(u, "://") {
				t.Errorf("%s: request.url %q", typ, u)
			}
		}
		if c, ok := p["contexts"].(map[string]any); ok {
			row["contexts"] = keysOf(c)
			for k := range c {
				if !allowCtx[k] {
					t.Errorf("%s: context %s on wire", typ, k)
				}
			}
			if tr, ok := c["trace"].(map[string]any); ok {
				row["traceKeys"] = keysOf(tr)
			}
		}
		if tg, ok := p["tags"].(map[string]any); ok {
			row["tags"] = tg
			for k := range tg {
				if !allowTags[strings.ToLower(k)] {
					t.Errorf("%s: tag %s on wire", typ, k)
				}
			}
		}
		if bc, ok := p["breadcrumbs"]; ok {
			row["breadcrumbs"] = bc
		}
		if ex, ok := p["exception"]; ok {
			b, _ := json.Marshal(ex)
			row["exceptionValues"] = strings.Count(string(b), `"value"`)
			var vals []any
			switch e := p["exception"].(type) {
			case []any:
				vals = e
			case map[string]any:
				vals, _ = e["values"].([]any)
			}
			for _, v := range vals {
				val, _ := v.(map[string]any)["value"].(string)
				if !strings.HasPrefix(val, "server error (") {
					t.Errorf("%s: exception value %q", typ, val)
				}
			}
		}
		if m, ok := p["message"]; ok {
			row["message"] = m
		}
		summary = append(summary, row)
	}
	report["items"] = summary
	report["errors"] = nErr
	report["transactions"] = nTx
	report["txNames"] = txNames
	if len(sink.raw) > 0 {
		report["envelopeHeaderSample"] = strings.SplitN(sink.raw[0], "\n", 2)[0]
	}
	out, _ := json.MarshalIndent(report, "", " ")
	if path := os.Getenv("ZZ_WIRE_RAW"); path != "" {
		_ = os.WriteFile(path, []byte(strings.Join(sink.raw, "\n=====\n")), 0o644)
	}
	if path := os.Getenv("ZZ_WIRE_OUT"); path != "" {
		_ = os.WriteFile(path, out, 0o644)
	}
	if nErr < 3 {
		t.Errorf("expected >=3 error events (error, panic, message), got %d", nErr)
	}
	for _, n := range txNames {
		if strings.Contains(n, "Oat") || strings.Contains(n, "?") {
			t.Errorf("transaction name %q", n)
		}
	}
	for _, raw := range sink.raw {
		hdr := strings.SplitN(raw, "\n", 2)[0]
		if strings.Contains(hdr, "nope") || strings.Contains(hdr, "item-9") {
			t.Errorf("envelope header carries raw path: %s", hdr)
		}
	}
}
