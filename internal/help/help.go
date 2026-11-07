// Package help serves the household-facing scanner setup page without sign-in.
package help

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"html"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Rhionin/stockbot/internal/app"
)

//go:embed scanner-help.md
var scannerHelpMarkdown []byte

// Register mounts GET /help/scanner with PM-accepted household copy.
func Register(mux *http.ServeMux, site app.RuntimeConfig) {
	mux.HandleFunc("GET /help/scanner", serveScanner(site))
}

func serveScanner(site app.RuntimeConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body := buildScannerPage(site, r)
		tag := weakETag(body)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("ETag", tag)
		http.ServeContent(w, r, "scanner-help.html", time.Time{}, bytes.NewReader(body))
	}
}

func buildScannerPage(site app.RuntimeConfig, r *http.Request) []byte {
	appName := strings.TrimSpace(site.AppName)
	if appName == "" {
		appName = app.DefaultAppName
	}
	helpHost := helpHost(site, r)
	source := string(scannerHelpMarkdown)
	source = strings.ReplaceAll(source, "{App name}", appName)
	source = strings.ReplaceAll(source, "{help host}", helpHost)
	content := renderHelpMarkdown(source)
	var page strings.Builder
	page.WriteString("<!DOCTYPE html>\n<html lang=\"en\">\n<head>\n")
	page.WriteString("  <meta charset=\"utf-8\">\n")
	page.WriteString("  <meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">\n")
	page.WriteString("  <title>")
	page.WriteString(html.EscapeString("Set up your barcode scanner"))
	page.WriteString("</title>\n")
	page.WriteString(pageStyles)
	page.WriteString("</head>\n<body>\n<main>\n")
	page.WriteString("  <img src=\"/brand/logo.png\" width=\"512\" height=\"512\" alt=\"\">\n")
	page.WriteString(content)
	page.WriteString("\n</main>\n</body>\n</html>\n")
	return []byte(page.String())
}

func helpHost(site app.RuntimeConfig, r *http.Request) string {
	if origin := strings.TrimSpace(site.AppBaseURL); origin != "" {
		u, err := url.Parse(origin)
		if err == nil && u.Host != "" {
			return u.Host
		}
	}
	if r != nil {
		return r.Host
	}
	return ""
}

func weakETag(data []byte) string {
	sum := sha256.Sum256(data)
	return `W/"` + hex.EncodeToString(sum[:]) + `"`
}

const pageStyles = `  <style>
    body {
      margin: 0;
      background: #fff;
      color: #1c1917;
      font: 1.05rem/1.55 Georgia, "Iowan Old Style", "Palatino Linotype", serif;
    }
    main { max-width: 38rem; margin: 0 auto; padding: 2.5rem 1.25rem 4rem; }
    img { width: 6rem; height: 6rem; }
    h1 { font-size: 1.8rem; line-height: 1.2; margin: 0.6rem 0 1rem; }
    h2 { font-size: 1.35rem; margin: 2rem 0 0.75rem; }
    h3 { font-size: 1.1rem; margin: 1.5rem 0 0.5rem; }
    h4 { font-size: 1rem; margin: 1.25rem 0 0.35rem; }
    p { margin: 0 0 1rem; }
    ul, ol { margin: 0 0 1rem 1.25rem; padding: 0; }
    li { margin: 0.35rem 0; }
    code {
      font: 0.95em/1.4 ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
      background: #f5f5f4;
      padding: 0.05rem 0.3rem;
      border-radius: 0.2rem;
    }
    a { color: #0f766e; }
    hr { border: 0; border-top: 1px solid #e7e5e4; margin: 2rem 0; }
    .muted { color: #78716c; font-style: italic; }
  </style>
`
