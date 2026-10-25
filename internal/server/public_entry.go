package server

import "net/http"

// isPublicPath matches paths that stay open on the public entry without a session.
func isPublicPath(r *http.Request) bool {
	switch r.URL.Path {
	case "/api/telemetry", "/api/telemetry/client", "/api/build", "/api/deploy-hook", sentryTunnelPath, "/brand/logo.png", "/terms", "/privacy":
		return true
	default:
		return false
	}
}

