package main

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/events"
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

func TestFlushSentryRunsAfterShutdownReturns(t *testing.T) {
	ln := listenLocal(t)
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})}
	serveErr := startServe(srv, ln)
	waitListening(t, ln.Addr().String())

	orig := shutdownHTTP
	t.Cleanup(func() { shutdownHTTP = orig })
	shutdownDone := make(chan struct{})
	shutdownHTTP = func(srv *http.Server, ctx context.Context) error {
		err := orig(srv, ctx)
		close(shutdownDone)
		return err
	}

	flushed := make(chan struct{})
	stop := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- runHTTPServer(srv, serveErr, stop, func() {
			select {
			case <-shutdownDone:
			default:
				t.Errorf("FlushSentry ran before Shutdown returned")
			}
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
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown timed out")
	}
	select {
	case <-flushed:
	default:
		t.Fatal("FlushSentry was not called")
	}
}

func TestShutdownTimeoutClosesLingeringConnection(t *testing.T) {
	prev := shutdownTimeout
	shutdownTimeout = 200 * time.Millisecond
	t.Cleanup(func() { shutdownTimeout = prev })

	ln := listenLocal(t)
	hold := make(chan struct{})
	t.Cleanup(func() { close(hold) })
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		<-hold
	})}
	serveErr := startServe(srv, ln)

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	if _, err := io.WriteString(conn, "GET /hold HTTP/1.1\r\nHost: localhost\r\n\r\n"); err != nil {
		t.Fatalf("write: %v", err)
	}
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := bufio.NewReader(conn).ReadString('\n'); err != nil {
		t.Fatalf("read status: %v", err)
	}

	var flushed bool
	stop := make(chan struct{})
	close(stop)
	done := make(chan error, 1)
	go func() {
		done <- runHTTPServer(srv, serveErr, stop, func() {
			server.FlushSentry()
			flushed = true
		})
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown did not return; a lingering connection held it open")
	}
	if !flushed {
		t.Fatal("FlushSentry was not called")
	}
	conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	buf := make([]byte, 256)
	for {
		_, err := conn.Read(buf)
		if err == nil {
			continue
		}
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			t.Fatal("lingering connection still open after shutdown")
		}
		break
	}
}

func TestSSEClientDisconnectsOnShutdown(t *testing.T) {
	ln := listenLocal(t)
	streams := server.NewStreamShutdown()
	handler := &server.EventsHandler{
		Broadcaster: events.NewBroadcaster(),
		Shutdown:    streams,
		Keepalive:   time.Hour,
	}
	srv := &http.Server{Handler: http.HandlerFunc(handler.Handle)}
	streams.Register(srv)
	serveErr := startServe(srv, ln)

	req, err := http.NewRequest(http.MethodGet, "http://"+ln.Addr().String()+"/api/events", nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/events: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", res.StatusCode)
	}
	bodyDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(io.Discard, res.Body)
		close(bodyDone)
	}()

	stop := make(chan struct{})
	close(stop)
	done := make(chan error, 1)
	go func() {
		done <- runHTTPServer(srv, serveErr, stop, server.FlushSentry)
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown waited on the SSE client")
	}
	select {
	case <-bodyDone:
	case <-time.After(time.Second):
		t.Fatal("SSE client still connected")
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

func listenLocal(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	return ln
}

func startServe(srv *http.Server, ln net.Listener) chan error {
	serveErr := make(chan error, 1)
	go func() {
		serveErr <- srv.Serve(ln)
	}()
	return serveErr
}

func waitListening(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		conn, err := net.DialTimeout("tcp", addr, 50*time.Millisecond)
		if err == nil {
			conn.Close()
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("server did not listen: %v", err)
		}
	}
}
