package main

import (
	"fmt"
	"html/template"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/Rhionin/stockbot/internal/clientmsg"
)

// Colors are the locked mark and stock tokens, copied as written:
// #0d9488 and #f59e0b from frontend/src/theme.ts (teal and amber shade 6)
// and frontend/src/index.css (--accent, --stock-full, --stock-low).
// Ink #08060d and paper #fff are --text-h and --bg on that sheet.
// Amber is the attention color, so the page does not look like a failure.
var migratingPage = template.Must(template.New("migrating").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta http-equiv="refresh" content="10">
<title>{{.}}</title>
<style>
  html { color-scheme: light; }
  body {
    margin: 0;
    min-height: 100vh;
    background: #fff;
    color: #08060d;
    font: 1.125rem/1.4 system-ui, "Segoe UI", Roboto, sans-serif;
  }
  body::before {
    content: "";
    display: block;
    height: 0.875rem;
    background: #0d9488;
  }
  main {
    min-height: calc(100vh - 0.875rem);
    display: flex;
    align-items: center;
    box-sizing: border-box;
    padding: 2.5rem 1.5rem 3.5rem;
  }
  h1 {
    max-width: 11.5em;
    margin: 0;
    font-size: 1.75rem;
    font-weight: 600;
    letter-spacing: -0.025em;
    line-height: 1.2;
  }
  h1::after {
    content: "";
    display: block;
    width: 3.25rem;
    height: 0.35rem;
    margin-top: 1.15rem;
    background: #f59e0b;
  }
</style>
</head>
<body>
<main>
<h1>{{.}}</h1>
</main>
</body>
</html>
`))

// writeMigrating answers a request that arrived before migrations finished.
// A browser page load gets the HTML notice. Every API call keeps the JSON
// status the deploy check and the apps already read.
func writeMigrating(w http.ResponseWriter, r *http.Request) {
	if !isAPIPath(r.URL.Path) && acceptsHTML(r.Header.Get("Accept")) {
		writeMigratingPage(w)
		return
	}
	writeMigratingJSON(w)
}

// isAPIPath keeps /api on JSON even when a browser asks for HTML.
// The apps read {"status":"migrating"} from that body.
func isAPIPath(path string) bool {
	return path == "/api" || strings.HasPrefix(path, "/api/")
}

// acceptsHTML reports whether Accept explicitly lists text/html at a
// non-zero quality. A wildcard such as */* does not count: curl and the
// deploy health check send that, and they need the JSON status. A
// substring search would also treat a type like nottext/html as a page.
func acceptsHTML(header string) bool {
	for _, part := range strings.Split(header, ",") {
		mediaType, params, err := mime.ParseMediaType(strings.TrimSpace(part))
		if err != nil || mediaType != "text/html" {
			continue
		}
		quality := params["q"]
		if quality == "" {
			return true
		}
		parsed, err := strconv.ParseFloat(quality, 64)
		if err != nil || parsed <= 0 {
			continue
		}
		return true
	}
	return false
}

func writeMigratingPage(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Retry-After", "10")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusServiceUnavailable)
	_ = migratingPage.Execute(w, clientmsg.MigrationUpdating)
}

func writeMigratingJSON(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Retry-After", "10")
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = fmt.Fprintln(w, `{"status":"migrating"}`)
}
