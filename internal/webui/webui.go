// Package webui serves the compiled frontend assets embedded in the binary.
//
// The package uses //go:embed to include the built React application, providing
// both individual asset serving and SPA (Single Page Application) fallback
// behavior for client-side routes.
package webui

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io"
	"io/fs"
	"log"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/Rhionin/stockbot/internal/clientmsg"
)

// Register missing MIME types for font files that Go's built-in table lacks
// and distroless containers don't provide via /etc/mime.types
func init() {
	mime.AddExtensionType(".woff2", "font/woff2")
	mime.AddExtensionType(".woff", "font/woff")
	mime.AddExtensionType(".ttf", "font/ttf")
}

// embedded holds the compiled frontend assets.
// The all: prefix ensures .vite/ metadata and underscore-prefixed chunk names
// are included, which the default //go:embed pattern silently skips.
//
//go:embed all:assets
var embedded embed.FS

// NewHandler serves the frontend assets compiled into the binary.
//
// Assets are served from the embedded filesystem with proper Content-Type
// headers and cache controls. Requests that don't match embedded assets
// fall back to serving index.html (SPA fallback behavior), except URLs that
// name a static file: those answer 404 so the shell is not stored in their place.
func NewHandler() http.Handler {
	assets, err := fs.Sub(embedded, "assets")
	if err != nil {
		// This should never happen with a valid embed, but handle gracefully
		panic("failed to create assets subtree: " + err.Error())
	}
	return NewHandlerFS(assets)
}

// NewHandlerFS serves an arbitrary asset tree.
//
// NewHandler is NewHandlerFS over the embedded tree; tests use this to
// inject synthetic asset trees. If the filesystem contains no readable
// index.html, fallback requests will return HTTP 500.
func NewHandlerFS(assets fs.FS) http.Handler {
	indexHTML, indexErr := readIndex(assets)
	var indexETag string
	if indexErr == nil {
		indexETag = weakETag(indexHTML)
	}
	return &handler{
		assets:    assets,
		indexHTML: indexHTML,
		indexErr:  indexErr,
		indexETag: indexETag,
		etags:     fileETags(assets),
	}
}

// handler implements the web UI serving logic.
type handler struct {
	assets    fs.FS
	indexHTML []byte
	indexErr  error
	indexETag string
	etags     map[string]string
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Method gate - only allow GET and HEAD, return 405 for everything else
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	// Name resolution - clean path and remove leading slash.
	// ServeContent below uses this cleaned name. http.FileServer would 301
	// "/index.html" and any trailing-slash URL, and browsers store 301s
	// permanently, which pins a hashed URL to the wrong target after a deploy.
	name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if name == "." {
		name = ""
	}

	// Literal index.html is the shell, not a second URL for the same document.
	if name != "index.html" && name != "" {
		if f, err := h.assets.Open(name); err == nil {
			info, statErr := f.Stat()
			if statErr == nil && info.Mode().IsRegular() {
				defer f.Close()
				h.serveOpened(w, r, name, f)
				return
			}
			f.Close()
		}
	}

	// A missing script, style, or font must not answer with the HTML shell.
	// A 200 document stored under that URL is what the browser runs next time.
	if name != "" && name != "index.html" && isStaticAssetMiss(name) {
		h.serveStaticMiss(w, r)
		return
	}

	h.serveFallback(w, r)
}

// serveOpened writes one regular file. Cache-Control is chosen from the
// path: Vite's content-hashed files live under assets/ and can be kept for
// a year. Everything else is revalidated so a replaced favicon still updates.
func (h *handler) serveOpened(w http.ResponseWriter, r *http.Request, name string, f fs.File) {
	rs, ok := f.(io.ReadSeeker)
	if !ok {
		data, err := io.ReadAll(f)
		if err != nil {
			log.Printf("web ui: %s", err)
			http.Error(w, clientmsg.ServerError, http.StatusInternalServerError)
			return
		}
		rs = bytes.NewReader(data)
	}

	// Set these only once the body is readable. A long-lived cache header on
	// an error response would pin the failure to that hashed URL.
	// sw.js must not be transformed by a proxy. Android Firefox has failed
	// to install a compressed worker, and the file has to stay at the site
	// root so its scope is the whole pantry. Cache-Control and
	// Service-Worker-Allowed are set here only. A proxy that repeats either
	// one joins the copies, and "/, /" is not a scope a browser will register.
	if name == "sw.js" {
		w.Header().Set("Cache-Control", "no-cache, no-transform")
		w.Header().Set("Service-Worker-Allowed", "/")
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	} else if strings.HasPrefix(name, "assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	if etag := h.etags[name]; etag != "" {
		w.Header().Set("ETag", etag)
	}
	http.ServeContent(w, r, path.Base(name), time.Time{}, rs)
}

// serveFallback serves the index.html document for SPA routing.
// no-cache forces a check on every navigation. The weak ETag turns that
// check into 304 while the document is unchanged, and a new build changes
// the bytes (new hashed chunk names), so the shell cannot stay stale.
func (h *handler) serveFallback(w http.ResponseWriter, r *http.Request) {
	if h.indexErr != nil {
		log.Printf("web ui: %s", h.indexErr)
		http.Error(w, clientmsg.ServerError, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Cache-Control", "no-cache")
	if h.indexETag != "" {
		w.Header().Set("ETag", h.indexETag)
	}

	http.ServeContent(w, r, "index.html", time.Time{}, bytes.NewReader(h.indexHTML))
}

// serveStaticMiss answers a URL the browser will treat as a file.
// no-store keeps a deploy gap from sticking: the next request asks again
// instead of replaying a cached HTML document or a cached 404.
func (h *handler) serveStaticMiss(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = io.WriteString(w, "404 page not found\n")
}

// isStaticAssetMiss reports paths that are files to the browser, not client routes.
// /assets/ is Vite's output directory even when the extension is unusual.
func isStaticAssetMiss(name string) bool {
	if name == "assets" || strings.HasPrefix(name, "assets/") {
		return true
	}
	switch strings.ToLower(path.Ext(name)) {
	case ".js", ".mjs", ".css", ".map",
		".woff", ".woff2", ".ttf", ".otf", ".eot",
		".svg", ".png", ".jpg", ".jpeg", ".gif", ".webp", ".ico", ".avif",
		".json", ".wasm", ".webmanifest", ".txt", ".xml":
		return true
	default:
		return false
	}
}

func readIndex(assets fs.FS) ([]byte, error) {
	file, err := assets.Open("index.html")
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, err
	}
	return data, nil
}

func fileETags(assets fs.FS) map[string]string {
	tags := make(map[string]string)
	_ = fs.WalkDir(assets, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry == nil || entry.IsDir() {
			return nil
		}
		data, err := fs.ReadFile(assets, name)
		if err != nil {
			return nil
		}
		tags[name] = weakETag(data)
		return nil
	})
	return tags
}

// weakETag is a semantic validator. Caddy gzip leaves weak tags unchanged,
// so a conditional request still matches after the proxy compresses the body.
func weakETag(data []byte) string {
	sum := sha256.Sum256(data)
	return `W/"` + hex.EncodeToString(sum[:]) + `"`
}
