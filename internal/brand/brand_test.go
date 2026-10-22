package brand

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRegisterServesStableMarkAndLegalPages(t *testing.T) {
	mux := http.NewServeMux()
	Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	tests := []struct {
		path        string
		contentType string
		snippet     string
	}{
		{path: "/brand/logo.png", contentType: "image/png", snippet: "\x89PNG"},
		{path: "/terms", contentType: "text/html; charset=utf-8", snippet: "personal household pantry"},
		{path: "/privacy", contentType: "text/html; charset=utf-8", snippet: "not sold"},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			res, err := srv.Client().Get(srv.URL + tt.path)
			if err != nil {
				t.Fatalf("get: %v", err)
			}
			body, err := io.ReadAll(res.Body)
			res.Body.Close()
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			if res.StatusCode != http.StatusOK {
				t.Fatalf("status %d", res.StatusCode)
			}
			if got := res.Header.Get("Content-Type"); got != tt.contentType {
				t.Fatalf("content-type %q", got)
			}
			if got := res.Header.Get("Cache-Control"); got != "no-cache" {
				t.Fatalf("cache-control %q", got)
			}
			etag := res.Header.Get("ETag")
			if !strings.HasPrefix(etag, `W/"`) || !strings.HasSuffix(etag, `"`) {
				t.Fatalf("etag %q", etag)
			}
			if !strings.Contains(string(body), tt.snippet) {
				t.Fatalf("body missing %q (%d bytes)", tt.snippet, len(body))
			}

			req, err := http.NewRequest(http.MethodGet, srv.URL+tt.path, nil)
			if err != nil {
				t.Fatalf("request: %v", err)
			}
			req.Header.Set("If-None-Match", etag)
			cached, err := srv.Client().Do(req)
			if err != nil {
				t.Fatalf("revalidate: %v", err)
			}
			cached.Body.Close()
			if cached.StatusCode != http.StatusNotModified {
				t.Fatalf("revalidate status %d", cached.StatusCode)
			}
		})
	}
}
