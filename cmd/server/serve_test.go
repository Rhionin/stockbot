package main

import (
	"errors"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/server"
)

func TestRunHTTPServerFlushesWhenStopped(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}),
	}
	serveErr := make(chan error, 1)
	go func() {
		serveErr <- srv.Serve(ln)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for {
		conn, err := net.DialTimeout("tcp", ln.Addr().String(), 50*time.Millisecond)
		if err == nil {
			conn.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("server did not listen: %v", err)
		}
	}

	flushed := make(chan struct{})
	stop := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- runHTTPServer(srv, serveErr, stop, func() {
			server.FlushSentry()
			close(flushed)
		})
	}()
	close(stop)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown timed out")
	}
	select {
	case <-flushed:
	default:
		t.Fatal("FlushSentry was not called")
	}
}

func TestRunHTTPServerFlushesOnListenError(t *testing.T) {
	flushed := false
	serveErr := make(chan error, 1)
	serveErr <- errors.New("listen failed")
	err := runHTTPServer(&http.Server{}, serveErr, make(chan struct{}), func() {
		server.FlushSentry()
		flushed = true
	})
	if err == nil {
		t.Fatal("expected a listen error")
	}
	if !flushed {
		t.Fatal("FlushSentry was not called")
	}
}
