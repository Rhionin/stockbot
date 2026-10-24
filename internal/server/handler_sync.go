package server

import (
	"database/sql"
	"net/http"
	"time"

	"github.com/Rhionin/stockbot/internal/syncsnap"
)

// syncSnapshotHandler serves GET /api/sync/snapshot.
// The bearer check is unreachable behind syncGate, which already returns
// 401 (sync_auth.go). It stays so an unwired handler still refuses the token.
type syncSnapshotHandler struct {
	DB    *sql.DB
	Token string
}

func (h *syncSnapshotHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.Token == "" {
		writeError(w, http.StatusNotFound, codeNotFound, "Not found.")
		return
	}
	if !tokenMatches(bearerToken(r), h.Token) {
		writeError(w, http.StatusUnauthorized, codeUnauthorized, "The sync token is not valid.")
		return
	}
	scope, err := requestScope(r.Context())
	if err != nil {
		writeStatusError(r, w, httpStatusFromError(err), err)
		return
	}
	snap, err := syncsnap.Build(r.Context(), scope, h.DB, time.Now())
	if err != nil {
		writeStatusError(r, w, httpStatusFromError(err), err)
		return
	}
	writeJSON(w, http.StatusOK, snap)
}
