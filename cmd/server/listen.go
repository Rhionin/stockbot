package main

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"time"

	"github.com/Rhionin/stockbot/internal/app"
)

// listenerSet is the process's bound HTTP servers. serveErr receives one
// result from each Serve call.
type listenerSet struct {
	servers  []*http.Server
	lns      []net.Listener
	addrs    []string
	serveErr chan error
}

// openListeners binds every address in specs and starts serving handler.
// Each request's context carries the listener's entry. A LAN address that
// is not loopback logs the existing unauthenticated-port warning.
func openListeners(specs []app.Listener, handler http.Handler, register func(*http.Server)) (listenerSet, error) {
	if len(specs) == 0 {
		return listenerSet{}, errors.New("no listener is configured")
	}
	set := listenerSet{serveErr: make(chan error, len(specs))}
	for _, spec := range specs {
		ln, err := net.Listen("tcp", spec.Addr)
		if err != nil {
			set.close()
			return listenerSet{}, err
		}
		entry := spec.Entry
		srv := &http.Server{
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				handler.ServeHTTP(w, r.WithContext(app.WithEntry(r.Context(), entry)))
			}),
			ReadHeaderTimeout: 10 * time.Second,
			IdleTimeout:       120 * time.Second,
			MaxHeaderBytes:    1 << 16,
		}
		if register != nil {
			register(srv)
		}
		set.servers = append(set.servers, srv)
		set.lns = append(set.lns, ln)
		set.addrs = append(set.addrs, ln.Addr().String())
		go func(srv *http.Server, ln net.Listener) {
			set.serveErr <- srv.Serve(ln)
		}(srv, ln)
		log.Printf("listening on %s (%s)", ln.Addr().String(), entry)
		if entry == app.EntryLAN {
			if warning := unauthenticatedListenWarning(spec.Addr); warning != "" {
				log.Print(warning)
			}
		}
	}
	return set, nil
}

func (s listenerSet) close() {
	// Close the socket itself. Server.Close only drops a listener that Serve
	// has already registered, and the next bind can fail before that happens.
	for _, ln := range s.lns {
		_ = ln.Close()
	}
	for _, srv := range s.servers {
		_ = srv.Close()
	}
}

// runHTTPServers waits until stop is closed or a listener's Serve returns,
// then shuts the others down and flushes error reports. Flush runs only
// after shutdown has returned.
func runHTTPServers(servers []*http.Server, serveErr <-chan error, stop <-chan struct{}, flush func()) error {
	n := len(servers)
	var err error
	select {
	case err = <-serveErr:
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		if n > 1 {
			_ = shutdownServers(servers)
		}
		drainServeErrors(serveErr, n-1)
	case <-stop:
		err = shutdownServers(servers)
		drainServeErrors(serveErr, n)
	}
	if flush != nil {
		flush()
	}
	return err
}

func shutdownServers(servers []*http.Server) error {
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	errCh := make(chan error, len(servers))
	for _, srv := range servers {
		go func(srv *http.Server) {
			if srv == nil {
				errCh <- nil
				return
			}
			errCh <- shutdownHTTP(srv, ctx)
		}(srv)
	}
	var first error
	for range servers {
		if err := <-errCh; err != nil && first == nil {
			first = err
		}
	}
	if errors.Is(first, context.DeadlineExceeded) {
		log.Print("warning: open connections did not finish during shutdown; closing them")
		for _, srv := range servers {
			if srv != nil {
				_ = srv.Close()
			}
		}
		return nil
	}
	return first
}

func drainServeErrors(serveErr <-chan error, n int) {
	if n <= 0 {
		return
	}
	timer := time.NewTimer(shutdownTimeout)
	defer timer.Stop()
	for i := 0; i < n; i++ {
		select {
		case <-serveErr:
		case <-timer.C:
			return
		}
	}
}
