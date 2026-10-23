package webui

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func TestNewHandler_ServesPlaceholderAssets(t *testing.T) {
	// Test that NewHandler() over the real embedded tree serves the tracked placeholder
	handler := NewHandler()

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read response body: %v", err)
	}

	bodyStr := string(body)

	// Verify it's the placeholder HTML
	if !strings.Contains(bodyStr, "Placeholder Assets") {
		t.Error("response should contain placeholder marker")
	}

	if !strings.Contains(bodyStr, "npm run build") {
		t.Error("response should mention npm run build")
	}

	// Verify content type
	contentType := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(contentType, "text/html") {
		t.Errorf("expected Content-Type to start with 'text/html', got %q", contentType)
	}
}

// Test method gate behavior - task 1.2
func TestMethodGate(t *testing.T) {
	testFS := fstest.MapFS{
		"index.html": &fstest.MapFile{
			Data: []byte("<html><body>Test</body></html>"),
		},
		"assets/app.js": &fstest.MapFile{
			Data: []byte("console.log('app');"),
		},
	}

	handler := NewHandlerFS(testFS)

	testCases := []struct {
		method         string
		path           string
		expectedStatus int
		expectAllow    bool
	}{
		{"GET", "/", http.StatusOK, false},
		{"HEAD", "/", http.StatusOK, false},
		{"POST", "/", http.StatusMethodNotAllowed, true},
		{"PUT", "/", http.StatusMethodNotAllowed, true},
		{"DELETE", "/", http.StatusMethodNotAllowed, true},
		{"PATCH", "/", http.StatusMethodNotAllowed, true},
		// Method gate applies even to existing assets
		{"POST", "/assets/app.js", http.StatusMethodNotAllowed, true},
		{"GET", "/assets/app.js", http.StatusOK, false},
	}

	for _, tc := range testCases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)

			if w.Code != tc.expectedStatus {
				t.Errorf("expected status %d, got %d", tc.expectedStatus, w.Code)
			}

			if tc.expectAllow {
				allow := w.Header().Get("Allow")
				if allow != "GET, HEAD" {
					t.Errorf("expected Allow header 'GET, HEAD', got %q", allow)
				}
			}
		})
	}
}

// Test asset serving with proper Content-Type - task 1.2
func TestAssetServing(t *testing.T) {
	testFS := fstest.MapFS{
		"index.html": &fstest.MapFile{
			Data: []byte("<html><body>Index</body></html>"),
		},
		"assets/app.js": &fstest.MapFile{
			Data: []byte("console.log('app');"),
		},
		"assets/style.css": &fstest.MapFile{
			Data: []byte("body { margin: 0; }"),
		},
		"assets/font.woff2": &fstest.MapFile{
			Data: []byte("WOFF2_DATA"),
		},
	}

	handler := NewHandlerFS(testFS)

	testCases := []struct {
		path         string
		expectedType string
		expectedBody string
	}{
		{"/assets/app.js", "text/javascript", "console.log('app');"},
		{"/assets/style.css", "text/css", "body { margin: 0; }"},
		{"/assets/font.woff2", "font/woff2", "WOFF2_DATA"},
	}

	for _, tc := range testCases {
		t.Run(tc.path, func(t *testing.T) {
			req := httptest.NewRequest("GET", tc.path, nil)
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Errorf("expected status 200, got %d", w.Code)
			}

			contentType := w.Header().Get("Content-Type")
			if !strings.HasPrefix(contentType, tc.expectedType) {
				t.Errorf("expected Content-Type to start with %q, got %q", tc.expectedType, contentType)
			}

			body := w.Body.String()
			if body != tc.expectedBody {
				t.Errorf("expected body %q, got %q", tc.expectedBody, body)
			}

			// Verify cache headers for assets under /assets/
			cacheControl := w.Header().Get("Cache-Control")
			if strings.HasPrefix(tc.path, "/assets/") {
				expected := "public, max-age=31536000, immutable"
				if cacheControl != expected {
					t.Errorf("expected Cache-Control %q for asset, got %q", expected, cacheControl)
				}
			}
		})
	}
}

// Test SPA fallback behavior - task 1.2/1.3
func TestSPAFallback(t *testing.T) {
	testFS := fstest.MapFS{
		"index.html": &fstest.MapFile{
			Data: []byte("<html><body>SPA Root</body></html>"),
		},
		"assets/app.js": &fstest.MapFile{
			Data: []byte("console.log('app');"),
		},
	}

	handler := NewHandlerFS(testFS)

	// Test paths that should fall back to index.html
	fallbackPaths := []string{
		"/",              // root
		"/inventory",     // client-side route
		"/products/123",  // nested client-side route
		"/nonexistent",   // missing file
		"/index.html",    // literal index.html (special case)
		"/../etc/passwd", // traversal attempt (cleaned by path.Clean)
	}

	for _, path := range fallbackPaths {
		t.Run("fallback_"+path, func(t *testing.T) {
			req := httptest.NewRequest("GET", path, nil)
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Errorf("expected status 200 for fallback, got %d", w.Code)
			}

			body := w.Body.String()
			if !strings.Contains(body, "SPA Root") {
				t.Error("fallback should return index.html content")
			}

			contentType := w.Header().Get("Content-Type")
			if !strings.HasPrefix(contentType, "text/html") {
				t.Errorf("expected Content-Type to start with 'text/html', got %q", contentType)
			}

			// Verify no-cache headers for fallback
			cacheControl := w.Header().Get("Cache-Control")
			if cacheControl != "no-cache" {
				t.Errorf("expected Cache-Control 'no-cache' for fallback, got %q", cacheControl)
			}
		})
	}
}

// Test HEAD method support - task 1.2
func TestHEADSupport(t *testing.T) {
	testFS := fstest.MapFS{
		"index.html": &fstest.MapFile{
			Data: []byte("<html><body>Test</body></html>"),
		},
		"assets/app.js": &fstest.MapFile{
			Data: []byte("console.log('app');"),
		},
	}

	handler := NewHandlerFS(testFS)

	testCases := []struct {
		path         string
		expectedType string
	}{
		{"/", "text/html"},
		{"/assets/app.js", "text/javascript"},
		{"/nonexistent", "text/html"}, // should fallback
	}

	for _, tc := range testCases {
		t.Run("HEAD_"+tc.path, func(t *testing.T) {
			req := httptest.NewRequest("HEAD", tc.path, nil)
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Errorf("expected status 200, got %d", w.Code)
			}

			// HEAD should have no body
			if w.Body.Len() > 0 {
				t.Errorf("HEAD response should have empty body, got %d bytes", w.Body.Len())
			}

			// But should have the correct headers
			contentType := w.Header().Get("Content-Type")
			if !strings.HasPrefix(contentType, tc.expectedType) {
				t.Errorf("expected Content-Type to start with %q, got %q", tc.expectedType, contentType)
			}
		})
	}
}

// Test error handling for bad filesystem - task 1.2
func TestBadFilesystem(t *testing.T) {
	// Test filesystem with no readable index.html
	testFS := fstest.MapFS{
		"assets/app.js": &fstest.MapFile{
			Data: []byte("console.log('app');"),
		},
		// No index.html file
	}

	logs := captureLog(t)
	handler := NewHandlerFS(testFS)

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500 when index.html is missing, got %d", w.Code)
	}
	if w.Body.String() != "Something went wrong. Try again.\n" {
		t.Errorf("body = %q", w.Body.String())
	}
	if strings.Contains(w.Body.String(), "index.html") {
		t.Errorf("body leaked the filesystem error: %s", w.Body.String())
	}
	if !strings.Contains(logs.String(), "web ui: open index.html: file does not exist") {
		t.Errorf("log = %q", logs.String())
	}
}

func TestUnreadableAssetUsesSharedServerError(t *testing.T) {
	logs := captureLog(t)
	handler := NewHandlerFS(assetTree{
		"index.html":       &sliceFile{name: "index.html", data: []byte("<html>shell</html>")},
		"assets/broken.js": &readFailFile{name: "broken.js", err: errors.New("disk read failed")},
	})

	req := httptest.NewRequest(http.MethodGet, "/assets/broken.js", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if w.Body.String() != "Something went wrong. Try again.\n" {
		t.Fatalf("body = %q", w.Body.String())
	}
	if strings.Contains(w.Body.String(), "disk read failed") {
		t.Fatalf("body leaked the read error: %s", w.Body.String())
	}
	if !strings.Contains(logs.String(), "web ui: disk read failed") {
		t.Fatalf("log = %q", logs.String())
	}
}

func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var logs bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(previous) })
	return &logs
}

type assetTree map[string]fs.File

func (t assetTree) Open(name string) (fs.File, error) {
	file, ok := t[name]
	if !ok {
		return nil, fs.ErrNotExist
	}
	return file, nil
}

type sliceFile struct {
	name string
	data []byte
	off  int
}

func (f *sliceFile) Stat() (fs.FileInfo, error) {
	return fileInfo{name: f.name, size: int64(len(f.data))}, nil
}

func (f *sliceFile) Read(p []byte) (int, error) {
	if f.off >= len(f.data) {
		return 0, io.EOF
	}
	n := copy(p, f.data[f.off:])
	f.off += n
	return n, nil
}

func (f *sliceFile) Close() error { return nil }

type readFailFile struct {
	name string
	err  error
}

func (f *readFailFile) Stat() (fs.FileInfo, error) {
	return fileInfo{name: f.name, size: 1}, nil
}

func (f *readFailFile) Read([]byte) (int, error) { return 0, f.err }

func (f *readFailFile) Close() error { return nil }

type fileInfo struct {
	name string
	size int64
}

func (i fileInfo) Name() string       { return i.name }
func (i fileInfo) Size() int64        { return i.size }
func (i fileInfo) Mode() fs.FileMode  { return 0o644 }
func (i fileInfo) ModTime() time.Time { return time.Time{} }
func (i fileInfo) IsDir() bool        { return false }
func (i fileInfo) Sys() any           { return nil }

func TestHashedAssetRevalidatesWithoutAFullDownload(t *testing.T) {
	body := []byte("console.log('hashed');")
	handler := NewHandlerFS(fstest.MapFS{
		"index.html":           &fstest.MapFile{Data: []byte("<html>shell</html>")},
		"assets/index-Ab12.js": &fstest.MapFile{Data: body},
	})

	req := httptest.NewRequest(http.MethodGet, "/assets/index-Ab12.js", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if got := w.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Fatalf("Cache-Control = %q", got)
	}
	etag := w.Header().Get("ETag")
	if !strings.HasPrefix(etag, `W/"`) {
		t.Fatalf("ETag = %q", etag)
	}
	if w.Body.String() != string(body) {
		t.Fatalf("body = %q", w.Body.String())
	}

	again := httptest.NewRequest(http.MethodGet, "/assets/index-Ab12.js/", nil)
	again.Header.Set("If-None-Match", etag)
	cached := httptest.NewRecorder()
	handler.ServeHTTP(cached, again)
	if cached.Code != http.StatusNotModified {
		t.Fatalf("revalidated status = %d, want 304", cached.Code)
	}
	if cached.Body.Len() != 0 {
		t.Fatalf("304 body = %q", cached.Body.String())
	}
	if got := cached.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Fatalf("304 Cache-Control = %q", got)
	}
	if cached.Header().Get("ETag") != etag {
		t.Fatalf("304 ETag = %q, want %q", cached.Header().Get("ETag"), etag)
	}
}

func TestShellRevalidationFollowsTheDocument(t *testing.T) {
	first := NewHandlerFS(fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<html>v1</html>")},
	})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	first.ServeHTTP(w, req)
	etag := w.Header().Get("ETag")
	if w.Code != http.StatusOK || !strings.HasPrefix(etag, `W/"`) {
		t.Fatalf("status %d etag %q", w.Code, etag)
	}
	if w.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("Cache-Control = %q", w.Header().Get("Cache-Control"))
	}

	same := httptest.NewRequest(http.MethodGet, "/inventory", nil)
	same.Header.Set("If-None-Match", etag)
	notModified := httptest.NewRecorder()
	first.ServeHTTP(notModified, same)
	if notModified.Code != http.StatusNotModified {
		t.Fatalf("same shell status = %d", notModified.Code)
	}
	if notModified.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("304 Cache-Control = %q", notModified.Header().Get("Cache-Control"))
	}

	second := NewHandlerFS(fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<html>v2</html>")},
	})
	updated := httptest.NewRequest(http.MethodGet, "/", nil)
	updated.Header.Set("If-None-Match", etag)
	fresh := httptest.NewRecorder()
	second.ServeHTTP(fresh, updated)
	if fresh.Code != http.StatusOK {
		t.Fatalf("new shell status = %d", fresh.Code)
	}
	if !strings.Contains(fresh.Body.String(), "v2") {
		t.Fatalf("new shell body = %q", fresh.Body.String())
	}
	if fresh.Header().Get("ETag") == etag {
		t.Fatal("new shell reused the previous ETag")
	}
}

func TestMissingHashedAssetDoesNotServeTheShell(t *testing.T) {
	shell := []byte("<html>shell</html>")
	handler := NewHandlerFS(fstest.MapFS{
		"index.html": &fstest.MapFile{Data: shell},
	})

	probe := httptest.NewRequest(http.MethodGet, "/", nil)
	shellRec := httptest.NewRecorder()
	handler.ServeHTTP(shellRec, probe)
	shellETag := shellRec.Header().Get("ETag")

	req := httptest.NewRequest(http.MethodGet, "/assets/index-oldhash.js", nil)
	req.Header.Set("If-None-Match", shellETag)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d", w.Code)
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q", w.Header().Get("Cache-Control"))
	}
	if strings.Contains(w.Body.String(), "shell") {
		t.Fatalf("missing asset body = %q", w.Body.String())
	}

	icon := httptest.NewRequest(http.MethodHead, "/favicon.ico", nil)
	iconRec := httptest.NewRecorder()
	handler.ServeHTTP(iconRec, icon)
	if iconRec.Code != http.StatusNotFound {
		t.Fatalf("favicon status = %d", iconRec.Code)
	}
	if iconRec.Body.Len() != 0 {
		t.Fatalf("HEAD favicon body = %q", iconRec.Body.String())
	}
	if iconRec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("favicon Cache-Control = %q", iconRec.Header().Get("Cache-Control"))
	}
}

func TestServiceWorkerIsUncompressedAtTheSiteRoot(t *testing.T) {
	handler := NewHandlerFS(fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<html>shell</html>")},
		"sw.js":      &fstest.MapFile{Data: []byte("self.addEventListener('push', function () {});\n")},
	})
	req := httptest.NewRequest(http.MethodGet, "/sw.js", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if got := w.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/javascript") {
		t.Fatalf("Content-Type = %q", got)
	}
	// Values, not Get: a repeated field is what browsers join into "/, /".
	if got := w.Header().Values("Cache-Control"); len(got) != 1 || got[0] != "no-cache, no-transform" {
		t.Fatalf("Cache-Control = %#v, want exactly one %q", got, "no-cache, no-transform")
	}
	if got := w.Header().Values("Service-Worker-Allowed"); len(got) != 1 || got[0] != "/" {
		t.Fatalf("Service-Worker-Allowed = %#v, want exactly one %q", got, "/")
	}
	if !strings.Contains(w.Body.String(), "addEventListener('push'") {
		t.Fatalf("body = %q", w.Body.String())
	}
}
