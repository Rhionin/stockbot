package server

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSentryTunnel(t *testing.T) {
	const (
		allowed = "https://key@o1.ingest.example/123"
		other   = "https://key@o1.ingest.example/124"
	)

	t.Run("unconfigured", func(t *testing.T) {
		rec := postTunnel(newSentryTunnel(SentryTunnelConfig{}), http.MethodPost, envelope(allowed), sentryEnvelopeMediaType)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d", rec.Code)
		}
	})

	t.Run("wrong method", func(t *testing.T) {
		rec := postTunnel(allowedTunnel(t, nil), http.MethodGet, envelope(allowed), sentryEnvelopeMediaType)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("status = %d", rec.Code)
		}
	})

	t.Run("malformed", func(t *testing.T) {
		var calls int
		handler := allowedTunnel(t, &calls)
		for _, body := range []string{"", "not-json", "{}\n", `{"event_id":"abc"}` + "\n"} {
			rec := postTunnel(handler, http.MethodPost, body, sentryEnvelopeMediaType)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("body %q status = %d", body, rec.Code)
			}
		}
		rec := postTunnel(handler, http.MethodPost, envelope(allowed), "text/plain")
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("content type status = %d", rec.Code)
		}
		if calls != 0 {
			t.Fatal("malformed envelope was forwarded")
		}
	})

	t.Run("wrong host", func(t *testing.T) {
		var calls int
		handler := allowedTunnel(t, &calls)
		rec := postTunnel(handler, http.MethodPost, envelope("https://key@evil.example/123"), sentryEnvelopeMediaType)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d", rec.Code)
		}
		if calls != 0 {
			t.Fatal("wrong host was forwarded")
		}
	})

	t.Run("wrong project", func(t *testing.T) {
		var calls int
		handler := allowedTunnel(t, &calls)
		rec := postTunnel(handler, http.MethodPost, envelope(other), sentryEnvelopeMediaType)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d", rec.Code)
		}
		if calls != 0 {
			t.Fatal("wrong project was forwarded")
		}
	})

	t.Run("oversize", func(t *testing.T) {
		handler := newSentryTunnel(SentryTunnelConfig{
			IngestHost: "o1.ingest.example",
			ProjectIDs: map[string]struct{}{"123": {}},
			MaxBody:    64,
		})
		rec := postTunnel(handler, http.MethodPost, strings.Repeat("a", 65), sentryEnvelopeMediaType)
		if rec.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("status = %d", rec.Code)
		}
		huge := httptest.NewRequest(http.MethodPost, sentryTunnelPath, nil)
		huge.Header.Set("Content-Type", sentryEnvelopeMediaType)
		huge.ContentLength = sentryTunnelMaxBody + 1
		hugeRec := httptest.NewRecorder()
		newSentryTunnel(SentryTunnelConfig{
			IngestHost: "o1.ingest.example",
			ProjectIDs: map[string]struct{}{"123": {}},
		}).ServeHTTP(hugeRec, huge)
		if hugeRec.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("default cap status = %d", hugeRec.Code)
		}
		if sentryTunnelMaxBody != 256*1024 {
			t.Fatalf("cap = %d", sentryTunnelMaxBody)
		}

		// A chunked body has no Content-Length, so only MaxBytesReader can reject it.
		chunked := httptest.NewRequest(http.MethodPost, sentryTunnelPath, strings.NewReader(strings.Repeat("b", 65)))
		chunked.ContentLength = -1
		chunked.Header.Set("Content-Type", sentryEnvelopeMediaType)
		chunked.Header.Del("Content-Length")
		chunkedRec := httptest.NewRecorder()
		handler.ServeHTTP(chunkedRec, chunked)
		if chunkedRec.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("chunked status = %d body %s", chunkedRec.Code, chunkedRec.Body.String())
		}
	})

	t.Run("forwarded", func(t *testing.T) {
		var mu sync.Mutex
		var gotPath, gotType, gotAuth, gotBody string
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			mu.Lock()
			gotPath = r.URL.Path
			gotType = r.Header.Get("Content-Type")
			gotAuth = r.Header.Get("Authorization")
			gotBody = string(body)
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		}))
		t.Cleanup(upstream.Close)
		handler := newSentryTunnel(SentryTunnelConfig{
			IngestHost: "o1.ingest.example",
			ProjectIDs: map[string]struct{}{"123": {}},
			Client:     &http.Client{Transport: rewriteHost(upstream.URL)},
		})
		body := envelope(allowed)
		req := httptest.NewRequest(http.MethodPost, sentryTunnelPath, strings.NewReader(body))
		req.Header.Set("Content-Type", sentryEnvelopeMediaType)
		req.Header.Set("Authorization", "Bearer household-secret")
		req.Header.Set("Cookie", "pantry_session=cookie-secret")
		req.RemoteAddr = "203.0.113.8:1234"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d body %s", rec.Code, rec.Body.String())
		}
		mu.Lock()
		defer mu.Unlock()
		if gotPath != "/api/123/envelope/" {
			t.Fatalf("path = %s", gotPath)
		}
		if gotType != sentryEnvelopeMediaType {
			t.Fatalf("content-type = %s", gotType)
		}
		if gotAuth != "" {
			t.Fatalf("authorization forwarded: %s", gotAuth)
		}
		if gotBody != body {
			t.Fatalf("body = %q", gotBody)
		}
	})

	t.Run("rate limit", func(t *testing.T) {
		handler := newSentryTunnel(SentryTunnelConfig{
			IngestHost: "o1.ingest.example",
			ProjectIDs: map[string]struct{}{"123": {}},
			Client:     okTransport(),
			PerIP:      2,
			Global:     10,
		})
		for i := 0; i < 2; i++ {
			rec := postTunnel(handler, http.MethodPost, envelope(allowed), sentryEnvelopeMediaType)
			if rec.Code != http.StatusNoContent {
				t.Fatalf("request %d status = %d", i, rec.Code)
			}
		}
		limited := postTunnel(handler, http.MethodPost, envelope(allowed), sentryEnvelopeMediaType)
		if limited.Code != http.StatusTooManyRequests {
			t.Fatalf("limited status = %d", limited.Code)
		}
		if limited.Header().Get("Retry-After") == "" {
			t.Fatal("missing Retry-After")
		}
	})
}

func TestSentryTunnelUsesPantryClientIP(t *testing.T) {
	handler := newSentryTunnel(SentryTunnelConfig{
		IngestHost: "o1.ingest.example",
		ProjectIDs: map[string]struct{}{"123": {}},
		Client:     okTransport(),
		PerIP:      1,
		Global:     10,
	})
	body := envelope("https://public@o1.ingest.example/123")
	for i, ip := range []string{"198.51.100.10", "198.51.100.11"} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, tunnelReq(body, ip))
		if rec.Code != http.StatusNoContent {
			t.Fatalf("client %d status = %d", i, rec.Code)
		}
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, tunnelReq(body, "198.51.100.10"))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("repeat status = %d", rec.Code)
	}

	// X-Forwarded-For is not the client address. The same remote stays one bucket.
	shared := newSentryTunnel(SentryTunnelConfig{
		IngestHost: "o1.ingest.example",
		ProjectIDs: map[string]struct{}{"123": {}},
		Client:     okTransport(),
		PerIP:      1,
		Global:     5,
	})
	a := httptest.NewRequest(http.MethodPost, sentryTunnelPath, strings.NewReader(body))
	a.Header.Set("Content-Type", sentryEnvelopeMediaType)
	a.Header.Set("X-Forwarded-For", "203.0.113.5")
	a.RemoteAddr = "10.0.0.8:4000"
	aRec := httptest.NewRecorder()
	shared.ServeHTTP(aRec, a)
	if aRec.Code != http.StatusNoContent {
		t.Fatalf("first proxy status = %d", aRec.Code)
	}
	b := httptest.NewRequest(http.MethodPost, sentryTunnelPath, strings.NewReader(body))
	b.Header.Set("Content-Type", sentryEnvelopeMediaType)
	b.Header.Set("X-Forwarded-For", "203.0.113.6")
	b.RemoteAddr = "10.0.0.8:4001"
	bRec := httptest.NewRecorder()
	shared.ServeHTTP(bRec, b)
	if bRec.Code != http.StatusTooManyRequests {
		t.Fatalf("forwarded-for status = %d, want one bucket for the proxy address", bRec.Code)
	}
}

func TestSentryTunnelGlobalLimit(t *testing.T) {
	handler := newSentryTunnel(SentryTunnelConfig{
		IngestHost: "o1.ingest.example",
		ProjectIDs: map[string]struct{}{"123": {}},
		Client:     okTransport(),
		PerIP:      5,
		Global:     2,
	})
	body := envelope("https://key@o1.ingest.example/123")
	for _, ip := range []string{"192.0.2.1", "192.0.2.2"} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, tunnelReq(body, ip))
		if rec.Code != http.StatusNoContent {
			t.Fatalf("%s status = %d", ip, rec.Code)
		}
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, tunnelReq(body, "192.0.2.3"))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("global status = %d", rec.Code)
	}
}

func TestSentryTunnelBlankLineEnvelopeIsLinear(t *testing.T) {
	header := `{"event_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","dsn":"https://key@o1.ingest.example/123","sent_at":"2026-01-01T00:00:00.000Z"}` + "\n"
	item := `{"type":"event"}` + "\n" + `{"message":"ok"}` + "\n"
	gap := sentryTunnelMaxBody - len(header) - len(item)
	if gap < 200000 {
		t.Fatalf("blank run = %d", gap)
	}
	body := header + strings.Repeat("\n", gap) + item
	if len(body) != sentryTunnelMaxBody {
		t.Fatalf("body length = %d", len(body))
	}

	// The old walk re-trimmed the remaining body on every blank line and took
	// about 14s. Two seconds still fails that loop and stays quiet on a busy CI machine.
	const bound = 2 * time.Second

	var calls int
	handler := allowedTunnel(t, &calls)
	before := envelopeItemParses.Load()
	start := time.Now()
	rec := postTunnel(handler, http.MethodPost, body, sentryEnvelopeMediaType)
	if elapsed := time.Since(start); elapsed > bound {
		t.Fatalf("allowed host took %s", elapsed)
	}
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", rec.Code)
	}
	if calls != 0 {
		t.Fatal("blank-line envelope was forwarded")
	}
	if envelopeItemParses.Load() != before+1 {
		t.Fatalf("item parses = %d, want %d", envelopeItemParses.Load(), before+1)
	}

	wrong := newSentryTunnel(SentryTunnelConfig{
		IngestHost: "elsewhere.example",
		ProjectIDs: map[string]struct{}{"123": {}},
		Client:     okTransport(),
		PerIP:      100,
		Global:     100,
	})
	before = envelopeItemParses.Load()
	start = time.Now()
	rec = postTunnel(wrong, http.MethodPost, body, sentryEnvelopeMediaType)
	if elapsed := time.Since(start); elapsed > bound {
		t.Fatalf("rejected host took %s", elapsed)
	}
	if rec.Code != http.StatusForbidden {
		t.Fatalf("rejected host status = %d", rec.Code)
	}
	if envelopeItemParses.Load() != before {
		t.Fatal("rejected host parsed items")
	}

	trailing := envelope("https://key@o1.ingest.example/123") + "\n\n  \t"
	rec = postTunnel(handler, http.MethodPost, trailing, sentryEnvelopeMediaType)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("trailing space status = %d body %s", rec.Code, rec.Body.String())
	}
	if calls != 1 {
		t.Fatalf("trailing space calls = %d", calls)
	}
}

func TestSentryTunnelBlankLineBeforeDisallowedItem(t *testing.T) {
	var calls int
	handler := allowedTunnel(t, &calls)
	// The blank line sits between a valid item and a disallowed one. Ending the
	// walk at the first blank line would forward the envelope and hide the
	// replay item; onlySpace must reject it.
	body := `{"dsn":"https://key@o1.ingest.example/123"}` + "\n" +
		`{"type":"event"}` + "\n" + "{}\n\n" +
		`{"type":"replay_recording"}` + "\n" + "{}\n"
	rec := postTunnel(handler, http.MethodPost, body, sentryEnvelopeMediaType)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body %s", rec.Code, rec.Body.String())
	}
	if calls != 0 {
		t.Fatal("disallowed item after a blank line was forwarded")
	}
}

func TestSentryTunnelRejectsTwentyDigitItemLength(t *testing.T) {
	var calls int
	handler := allowedTunnel(t, &calls)
	const length = "10000000000000000000"
	if len(length) != 20 {
		t.Fatalf("digits = %d", len(length))
	}
	body := `{"dsn":"https://key@o1.ingest.example/123"}` + "\n" +
		`{"type":"attachment","length":` + length + `}` + "\n" + "hi"
	rec := postTunnel(handler, http.MethodPost, body, sentryEnvelopeMediaType)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body %s", rec.Code, rec.Body.String())
	}
	if calls != 0 {
		t.Fatal("20-digit length was forwarded")
	}
}

func TestSentryTunnelForwardsAttachmentEndingAtEOF(t *testing.T) {
	var calls int
	var got string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		body, _ := io.ReadAll(r.Body)
		got = string(body)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(upstream.Close)
	handler := newSentryTunnel(SentryTunnelConfig{
		IngestHost: "o1.ingest.example",
		ProjectIDs: map[string]struct{}{"123": {}},
		Client:     &http.Client{Transport: rewriteHost(upstream.URL)},
		PerIP:      100,
		Global:     100,
	})
	payload := "hello"
	body := `{"dsn":"https://key@o1.ingest.example/123"}` + "\n" +
		`{"type":"attachment","length":` + strconv.Itoa(len(payload)) + `}` + "\n" +
		payload
	if strings.HasSuffix(body, "\n") {
		t.Fatal("payload has a trailing newline")
	}
	rec := postTunnel(handler, http.MethodPost, body, sentryEnvelopeMediaType)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body %s", rec.Code, rec.Body.String())
	}
	if calls != 1 || !strings.HasSuffix(got, payload) || strings.HasSuffix(got, "\n") {
		t.Fatalf("calls = %d forwarded = %q", calls, got)
	}
}

func TestSentryTunnelRejectsCaseVariantKeys(t *testing.T) {
	var calls int
	handler := allowedTunnel(t, &calls)
	header := `{"event_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","dsn":"https://key@o1.ingest.example/123","sent_at":"2026-01-01T00:00:00.000Z"}` + "\n"
	bodies := []string{
		header + `{"type":"replay_recording","Type":"event"}` + "\n" + `{"message":"ok"}` + "\n",
		header + `{"type":"event","Length":1}` + "\n" + "x\n",
		header + `{"type":"event","type":"replay_recording"}` + "\n" + `{"message":"ok"}` + "\n",
		header + `{"Type":"event"}` + "\n" + `{"message":"ok"}` + "\n",
		`{"DSN":"https://key@o1.ingest.example/123"}` + "\n" + `{"type":"event"}` + "\n" + `{"message":"ok"}` + "\n",
		`{"dsn":"https://key@o1.ingest.example/123","dsn":"https://key@o1.ingest.example/123"}` + "\n" + `{"type":"event"}` + "\n" + `{"message":"ok"}` + "\n",
		`{"dsn":"https://key@o1.ingest.example/123","DSN":"https://other@o1.ingest.example/123"}` + "\n" + `{"type":"event"}` + "\n" + `{"message":"ok"}` + "\n",
		header + `{"type":"event"}{"type":"event"}` + "\n" + `{"message":"ok"}` + "\n",
	}
	for _, body := range bodies {
		rec := postTunnel(handler, http.MethodPost, body, sentryEnvelopeMediaType)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("body %q status = %d", body, rec.Code)
		}
	}
	if calls != 0 {
		t.Fatal("case-variant envelope was forwarded")
	}
}

func TestSentryTunnelItemAllowlist(t *testing.T) {
	for _, typ := range []string{"event", "transaction", "session", "sessions", "client_report"} {
		t.Run(typ, func(t *testing.T) {
			var calls int
			handler := allowedTunnel(t, &calls)
			body := `{"dsn":"https://key@o1.ingest.example/123","sdk":{"name":"browser","version":"8"}}` + "\n" +
				`{"type":"` + typ + `"}` + "\n" + `{"ok":true}` + "\n"
			rec := postTunnel(handler, http.MethodPost, body, sentryEnvelopeMediaType)
			if rec.Code != http.StatusNoContent {
				t.Fatalf("status = %d body %s", rec.Code, rec.Body.String())
			}
			if calls != 1 {
				t.Fatalf("calls = %d", calls)
			}
		})
	}

	t.Run("attachment", func(t *testing.T) {
		var calls int
		handler := allowedTunnel(t, &calls)
		payload := "hello"
		body := `{"dsn":"https://key@o1.ingest.example/123"}` + "\n" +
			`{"type":"attachment","length":` + strconv.Itoa(len(payload)) + `,"content_type":"text/plain","filename":"note.txt"}` + "\n" +
			payload + "\n"
		rec := postTunnel(handler, http.MethodPost, body, sentryEnvelopeMediaType)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d body %s", rec.Code, rec.Body.String())
		}
		if calls != 1 {
			t.Fatalf("calls = %d", calls)
		}
	})

	t.Run("replay_recording", func(t *testing.T) {
		var calls int
		handler := allowedTunnel(t, &calls)
		body := envelope("https://key@o1.ingest.example/123")
		body = strings.Replace(body, `{"type":"event"}`, `{"type":"replay_recording"}`, 1)
		rec := postTunnel(handler, http.MethodPost, body, sentryEnvelopeMediaType)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d", rec.Code)
		}
		if calls != 0 {
			t.Fatal("replay_recording was forwarded")
		}
	})
}

func TestSentryTunnelItemLengthBound(t *testing.T) {
	var calls int
	handler := allowedTunnel(t, &calls)
	header := `{"dsn":"https://key@o1.ingest.example/123"}` + "\n"
	bodies := []string{
		header + `{"type":"attachment","length":300000}` + "\n" + "hi\n",
		header + `{"type":"attachment","length":50}` + "\n" + "hi\n",
		header + `{"type":"attachment","length":999999999999999999999}` + "\n" + "hi\n",
		header + `{"type":"attachment","length":-1}` + "\n" + "hi\n",
		header + `{"type":"attachment","length":"5"}` + "\n" + "hello\n",
	}
	for _, body := range bodies {
		rec := postTunnel(handler, http.MethodPost, body, sentryEnvelopeMediaType)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("body %q status = %d", body, rec.Code)
		}
	}
	if calls != 0 {
		t.Fatal("oversize item was forwarded")
	}
}

func TestSentryTunnelRejectsOtherItemTypes(t *testing.T) {
	var calls int
	handler := allowedTunnel(t, &calls)
	body := `{"event_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","dsn":"https://key@o1.ingest.example/123","sent_at":"2026-01-01T00:00:00.000Z"}` + "\n" +
		`{"type":"replay_event"}` + "\n" + `{"message":"Oat Milk"}` + "\n"
	rec := postTunnel(handler, http.MethodPost, body, sentryEnvelopeMediaType)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", rec.Code)
	}
	if calls != 0 {
		t.Fatal("disallowed item was forwarded")
	}

	payload := "hello"
	attachment := `{"type":"attachment","length":` + strconv.Itoa(len(payload)) + `}` + "\n" + payload + "\n"
	ok := envelope("https://key@o1.ingest.example/123") + attachment
	rec = postTunnel(handler, http.MethodPost, ok, sentryEnvelopeMediaType)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("attachment status = %d body %s", rec.Code, rec.Body.String())
	}
	if calls != 1 {
		t.Fatalf("forwarded calls = %d", calls)
	}
}

func TestSentryTunnelPublicKey(t *testing.T) {
	var calls int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(upstream.Close)
	handler := newSentryTunnel(SentryTunnelConfig{
		IngestHost: "o1.ingest.example",
		ProjectIDs: map[string]struct{}{"123": {}},
		PublicKeys: map[string]struct{}{"browser-key": {}},
		Client:     &http.Client{Transport: rewriteHost(upstream.URL)},
		PerIP:      10,
		Global:     10,
	})
	wrong := postTunnel(handler, http.MethodPost, envelope("https://key@o1.ingest.example/123"), sentryEnvelopeMediaType)
	if wrong.Code != http.StatusForbidden {
		t.Fatalf("wrong key status = %d", wrong.Code)
	}
	if calls != 0 {
		t.Fatal("wrong key was forwarded")
	}
	right := postTunnel(handler, http.MethodPost, envelope("https://browser-key@o1.ingest.example/123"), sentryEnvelopeMediaType)
	if right.Code != http.StatusNoContent {
		t.Fatalf("right key status = %d body %s", right.Code, right.Body.String())
	}
	if calls != 1 {
		t.Fatalf("calls = %d", calls)
	}
}

func TestSentryTunnelPassesUpstreamRateLimitHeaders(t *testing.T) {
	handler := newSentryTunnel(SentryTunnelConfig{
		IngestHost: "o1.ingest.example",
		ProjectIDs: map[string]struct{}{"123": {}},
		Client: &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
			header := make(http.Header)
			header.Set("Retry-After", "12")
			header.Set("X-Sentry-Rate-Limits", "60:transaction")
			header.Set("X-Other", "nope")
			return &http.Response{
				StatusCode: http.StatusTooManyRequests,
				Header:     header,
				Body:       io.NopCloser(strings.NewReader("slow down")),
			}, nil
		})},
	})
	rec := postTunnel(handler, http.MethodPost, envelope("https://key@o1.ingest.example/123"), sentryEnvelopeMediaType)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d", rec.Code)
	}
	if rec.Header().Get("Retry-After") != "12" || rec.Header().Get("X-Sentry-Rate-Limits") != "60:transaction" {
		t.Fatalf("headers = %#v", rec.Header())
	}
	if rec.Header().Get("X-Other") != "" {
		t.Fatal("unrelated upstream header was copied")
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestSentryTunnelDefaultClient(t *testing.T) {
	if sentryTunnelTimeout != 5*time.Second {
		t.Fatalf("timeout constant = %s", sentryTunnelTimeout)
	}
	var landed bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/landed") {
			landed = true
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("Location", "/landed")
		w.WriteHeader(http.StatusFound)
	}))
	t.Cleanup(upstream.Close)

	handler := newSentryTunnel(SentryTunnelConfig{
		IngestHost: "o1.ingest.example",
		ProjectIDs: map[string]struct{}{"123": {}},
	})
	tun := handler.(*sentryTunnel)
	if tun.client.Timeout != 5*time.Second {
		t.Fatalf("client timeout = %s", tun.client.Timeout)
	}
	if tun.client.CheckRedirect == nil {
		t.Fatal("CheckRedirect is nil")
	}
	redirectErr := tun.client.CheckRedirect(httptest.NewRequest(http.MethodPost, "https://o1.ingest.example/api/123/envelope/", nil), nil)
	if !errors.Is(redirectErr, http.ErrUseLastResponse) {
		t.Fatalf("CheckRedirect = %v", redirectErr)
	}
	tun.client.Transport = rewriteHost(upstream.URL)
	rec := postTunnel(handler, http.MethodPost, envelope("https://key@o1.ingest.example/123"), sentryEnvelopeMediaType)
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, redirect followed=%v", rec.Code, landed)
	}
	if landed {
		t.Fatal("default client followed the redirect")
	}
}

func TestSentryTunnelSweepsStaleIPKeys(t *testing.T) {
	lim := newTunnelLimiter(2, 10, time.Minute)
	now := time.Unix(1_700_000_000, 0)
	if _, ok := lim.allow(now, "198.51.100.9"); !ok {
		t.Fatal("first allow")
	}
	if _, ok := lim.hits["198.51.100.9"]; !ok {
		t.Fatal("ip key missing")
	}
	if _, ok := lim.allow(now.Add(2*time.Minute), "192.0.2.9"); !ok {
		t.Fatal("later allow")
	}
	if _, ok := lim.hits["198.51.100.9"]; ok {
		t.Fatal("stale per-IP key kept")
	}
	if _, ok := lim.hits["192.0.2.9"]; !ok {
		t.Fatal("current ip key missing")
	}
}

func TestSentryTunnelIngestFailure(t *testing.T) {
	handler := newSentryTunnel(SentryTunnelConfig{
		IngestHost: "o1.ingest.example",
		ProjectIDs: map[string]struct{}{"123": {}},
		Client: &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
			return nil, io.ErrUnexpectedEOF
		})},
	})
	rec := postTunnel(handler, http.MethodPost, envelope("https://key@o1.ingest.example/123"), sentryEnvelopeMediaType)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "o1.ingest.example") || strings.Contains(rec.Body.String(), "Oat Milk") {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestSentryTunnelLoginAndSyncExemption(t *testing.T) {
	const token = "sync-token-value-ok"
	gate := testHousehold(t, "pantry", "correct-horse-battery")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(upstream.Close)

	handler, _ := setupTestWithContributor(t, nil,
		WithHouseholdAuth(gate),
		WithSyncToken(token),
		WithSentryTunnel(SentryTunnelConfig{
			IngestHost: "o1.ingest.example",
			ProjectIDs: map[string]struct{}{"123": {}},
			Client:     &http.Client{Transport: rewriteHost(upstream.URL)},
		}),
	)

	open := httptest.NewRequest(http.MethodPost, sentryTunnelPath, strings.NewReader(envelope("https://key@o1.ingest.example/123")))
	open.Header.Set("Content-Type", sentryEnvelopeMediaType)
	open.Header.Set(entryHeader, entryPublic)
	open.Header.Set("Authorization", "Bearer "+token)
	openRec := httptest.NewRecorder()
	handler.ServeHTTP(openRec, open)
	if openRec.Code != http.StatusNoContent {
		t.Fatalf("tunnel status = %d body %s", openRec.Code, openRec.Body.String())
	}

	closed := httptest.NewRequest(http.MethodPost, "/api/products", strings.NewReader(`{"name":"Nope"}`))
	closed.Header.Set("Content-Type", "application/json")
	closed.Header.Set(entryHeader, entryPublic)
	closedRec := httptest.NewRecorder()
	handler.ServeHTTP(closedRec, closed)
	if closedRec.Code != http.StatusUnauthorized {
		t.Fatalf("products without a session = %d", closedRec.Code)
	}

	syncOnly := httptest.NewRequest(http.MethodPost, "/api/products", strings.NewReader(`{"name":"Nope"}`))
	syncOnly.Header.Set("Content-Type", "application/json")
	syncOnly.Header.Set("Authorization", "Bearer "+token)
	syncOnly.Header.Set(entryHeader, entryPublic)
	syncRec := httptest.NewRecorder()
	handler.ServeHTTP(syncRec, syncOnly)
	if syncRec.Code != http.StatusForbidden {
		t.Fatalf("sync token on products = %d body %s", syncRec.Code, syncRec.Body.String())
	}

	hidden, _ := setupTestWithContributor(t, nil, WithHouseholdAuth(gate))
	miss := httptest.NewRequest(http.MethodPost, sentryTunnelPath, strings.NewReader(envelope("https://key@o1.ingest.example/123")))
	miss.Header.Set("Content-Type", sentryEnvelopeMediaType)
	miss.Header.Set(entryHeader, entryPublic)
	missRec := httptest.NewRecorder()
	hidden.ServeHTTP(missRec, miss)
	if missRec.Code != http.StatusNotFound {
		t.Fatalf("unconfigured tunnel = %d", missRec.Code)
	}
}

func allowedTunnel(t *testing.T, calls *int) http.Handler {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls != nil {
			*calls++
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(upstream.Close)
	return newSentryTunnel(SentryTunnelConfig{
		IngestHost: "o1.ingest.example",
		ProjectIDs: map[string]struct{}{"123": {}},
		Client:     &http.Client{Transport: rewriteHost(upstream.URL)},
		PerIP:      100,
		Global:     100,
	})
}

func envelope(dsn string) string {
	return `{"event_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","dsn":"` + dsn + `","sent_at":"2026-01-01T00:00:00.000Z"}` + "\n" + `{"type":"event"}` + "\n" + `{"message":"Oat Milk"}` + "\n"
}

func postTunnel(handler http.Handler, method, body, contentType string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, sentryTunnelPath, strings.NewReader(body))
	req.Header.Set("Content-Type", contentType)
	req.RemoteAddr = "203.0.113.8:1234"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func tunnelReq(body, ip string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, sentryTunnelPath, strings.NewReader(body))
	req.Header.Set("Content-Type", sentryEnvelopeMediaType)
	req.Header.Set(entryHeader, entryPublic)
	req.Header.Set(clientIPHeader, ip)
	req.RemoteAddr = "10.0.0.8:1234"
	return req
}

func okTransport() *http.Client {
	return &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusNoContent,
			Body:       io.NopCloser(strings.NewReader("")),
			Header:     make(http.Header),
		}, nil
	})}
}

func rewriteHost(raw string) http.RoundTripper {
	dest, err := url.Parse(raw)
	if err != nil {
		panic(err)
	}
	return hostRewrite{dest: dest}
}

type hostRewrite struct {
	dest *url.URL
}

func (h hostRewrite) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.URL.Scheme = h.dest.Scheme
	clone.URL.Host = h.dest.Host
	clone.Host = h.dest.Host
	return http.DefaultTransport.RoundTrip(clone)
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
