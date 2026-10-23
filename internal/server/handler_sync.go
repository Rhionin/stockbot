package server

import (
	"database/sql"
	"net/http"
	"time"

	"github.com/Rhionin/stockbot/internal/syncsnap"
)

// syncSnapshotHandler serves GET /api/sync/snapshot.
// The gate in front of the mux already checks the token. The handler checks
// again so the route stays closed if that gate is ever wired differently.
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
	snap, err := syncsnap.Build(r.Context(), h.DB, time.Now())
	if err != nil {
		writeStatusError(r, w, httpStatusFromError(err), err)
		return
	}
	writeJSON(w, http.StatusOK, snap)
}
