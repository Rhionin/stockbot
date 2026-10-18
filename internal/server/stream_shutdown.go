package server

import (
	"context"
	"net/http"
)

// StreamShutdown ends open /api/events responses when the process is stopping.
// Register Stop with http.Server.RegisterOnShutdown. Shutdown does not cancel
// request contexts, so a live SSE tab would otherwise hold the process until
// the shutdown budget runs out.
type StreamShutdown struct {
	ctx    context.Context
	cancel context.CancelFunc
}

func NewStreamShutdown() *StreamShutdown {
	ctx, cancel := context.WithCancel(context.Background())
	return &StreamShutdown{ctx: ctx, cancel: cancel}
}

// Register arranges for Stop to run when srv.Shutdown starts.
func (s *StreamShutdown) Register(srv *http.Server) {
	if s == nil || srv == nil {
		return
	}
	srv.RegisterOnShutdown(s.Stop)
}

// Stop tells every stream started from Context to finish. It does not wait
// for those handlers to return; Shutdown does that.
func (s *StreamShutdown) Stop() {
	if s == nil || s.cancel == nil {
		return
	}
	s.cancel()
}

// Context is cancelled when parent ends or Stop runs.
func (s *StreamShutdown) Context(parent context.Context) context.Context {
	if s == nil || parent == nil {
		return parent
	}
	ctx, cancel := context.WithCancel(parent)
	stop := context.AfterFunc(s.ctx, cancel)
	context.AfterFunc(parent, func() {
		stop()
		cancel()
	})
	return ctx
}
