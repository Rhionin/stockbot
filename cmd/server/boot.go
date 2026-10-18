package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"github.com/Rhionin/stockbot/internal/buildinfo"
)

// bootGate answers while the database is still migrating. /health is 503 and
// /api/build reports this binary. Every other route stays unavailable until
// use swaps in the real handler. main listens with this gate before it runs
// migrations, so a deploy can tell "still migrating" from "process is dead".
type bootGate struct {
	mu    sync.RWMutex
	ready http.Handler
}

func newBootGate() *bootGate {
	return &bootGate{}
}

func (g *bootGate) use(handler http.Handler) {
	g.mu.Lock()
	g.ready = handler
	g.mu.Unlock()
}

func (g *bootGate) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.mu.RLock()
	ready := g.ready
	g.mu.RUnlock()
	if ready != nil {
		ready.ServeHTTP(w, r)
		return
	}
	if r.Method == http.MethodGet && r.URL.Path == "/api/build" {
		writeBootBuildInfo(w)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = fmt.Fprintln(w, `{"status":"migrating"}`)
}

func writeBootBuildInfo(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		Commit      string `json:"commit"`
		CommittedAt string `json:"committedAt,omitempty"`
		BuiltAt     string `json:"builtAt,omitempty"`
		Version     string `json:"version"`
		Subject     string `json:"subject,omitempty"`
	}{
		Commit:      buildinfo.Commit,
		CommittedAt: buildinfo.CommittedAt,
		BuiltAt:     buildinfo.BuiltAt,
		Version:     buildinfo.VisibleVersion(),
		Subject:     buildinfo.VisibleSubject(),
	})
}
