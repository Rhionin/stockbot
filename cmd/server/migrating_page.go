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

// Colors are copied from the web theme. Light paper #fff and ink #08060d
// are --bg and --text-h in frontend/src/index.css. The bar is the locked
// mark #0d9488 (theme.ts teal shade 6, --accent, --stock-full). The tick
// is attention amber #f59e0b (amber shade 6, --stock-low). Dark mode uses
// the same sheet: paper #16171d (--bg under prefers-color-scheme: dark)
// and ink #f3f4f6 (--text-h under data-mantine-color-scheme='dark').
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
    min-height: 100dvh;
    display: flex;
    flex-direction: column;
    background: #fff;
    color: #08060d;
    font: 1.125rem/1.45 system-ui, "Segoe UI", Roboto, sans-serif;
  }
  body::before {
    content: "";
    display: block;
    flex: none;
    height: 0.875rem;
    background: #0d9488;
  }
  main {
    flex: 1;
    display: flex;
    align-items: center;
    justify-content: center;
    box-sizing: border-box;
    padding: 2.5rem 1.5rem;
  }
  h1 {
    max-width: 30rem;
    margin: 0;
    text-align: center;
    font-size: 1.625rem;
    font-weight: 600;
    letter-spacing: -0.02em;
    line-height: 1.35;
  }
  h1::after {
    content: "";
    display: block;
    width: 3.25rem;
    height: 0.35rem;
    margin: 1.15rem auto 0;
    background: #f59e0b;
  }
  @media (max-width: 480px) {
    main { padding: 2rem 1.25rem; }
    h1 { font-size: 1.375rem; }
  }
  @media (prefers-color-scheme: dark) {
    html { color-scheme: dark; }
    body {
      background: #16171d;
      color: #f3f4f6;
    }
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
