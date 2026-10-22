package main

import (
	"encoding/json"
	"net/http"
	"sync"

	"github.com/Rhionin/stockbot/internal/buildinfo"
)

// bootGate answers while the database is still migrating. A browser page
// load gets a short HTML notice. API clients, including /health, get
// 503 {"status":"migrating"}. GET /api/build reports this binary. use swaps
// in the real handler when migrations finish. main listens with this gate
// before it runs migrations, so a deploy can tell "still migrating" from
// "process is dead".
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
	writeMigrating(w, r)
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
