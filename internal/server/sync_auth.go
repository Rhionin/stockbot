package server

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strings"
)

// syncAuthKey marks a request the sync token already authorized.
// Household login must not ask for a session on that request.
type syncAuthKey struct{}

func syncAuthorized(r *http.Request) bool {
	ok, _ := r.Context().Value(syncAuthKey{}).(bool)
	return ok
}

const syncSnapshotPath = "/api/sync/snapshot"

// syncGate lets the grocy-sync token read the snapshot and the event stream.
// Any other route presented with that token is refused, so the token is not
// a household password. When the token is unset the snapshot is a 404.
func syncGate(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The browser tunnel is not a sync call. A sync token must not
		// block it, and this exemption does not apply to any other path.
		if r.URL.Path == sentryTunnelPath {
			next.ServeHTTP(w, r)
			return
		}
		if token == "" {
			if isSyncSnapshot(r) {
				writeError(w, http.StatusNotFound, codeNotFound, "Not found.")
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		if got := bearerToken(r); got != "" && tokenMatches(got, token) {
			if isSyncSnapshot(r) || isSyncNudge(r) {
				r = r.WithContext(context.WithValue(r.Context(), syncAuthKey{}, true))
				next.ServeHTTP(w, r)
				return
			}
			writeError(w, http.StatusForbidden, codeSyncReadOnly, "This token only reads the sync snapshot.")
			return
		}
		if isSyncSnapshot(r) {
			writeError(w, http.StatusUnauthorized, codeUnauthorized, "The sync token is not valid.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isSyncSnapshot(r *http.Request) bool {
	return r.Method == http.MethodGet && r.URL.Path == syncSnapshotPath
}

func isSyncNudge(r *http.Request) bool {
	return r.Method == http.MethodGet && r.URL.Path == "/api/events"
}

func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if len(h) < len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) {
		return ""
	}
	return strings.TrimSpace(h[len(prefix):])
}

func tokenMatches(got, want string) bool {
	if got == "" || want == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}
