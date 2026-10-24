package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/app"
)

func TestOpenListenersTagsEachEntry(t *testing.T) {
	set, err := openListeners([]app.Listener{
		{Addr: "127.0.0.1:0", Entry: app.EntryPublic},
		{Addr: "127.0.0.1:0", Entry: app.EntryPublic},
	}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entry, ok := app.EntryFrom(r.Context())
		if !ok {
			http.Error(w, "missing entry", http.StatusInternalServerError)
			return
		}
		_, _ = io.WriteString(w, string(entry))
	}), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(set.addrs) != 2 {
		t.Fatalf("addrs = %v", set.addrs)
	}
	for i, want := range []string{string(app.EntryPublic), string(app.EntryPublic)} {
		if got := readEntry(t, set.addrs[i]); got != want {
			t.Fatalf("listener %s body = %q, want %q", set.addrs[i], got, want)
		}
	}

	stop := make(chan struct{})
	close(stop)
	done := make(chan error, 1)
	go func() {
		done <- runHTTPServers(set.servers, set.serveErr, stop, func() {})
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("shutdown: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("two listeners did not shut down")
	}
}

func TestOpenListenersRejectsAnEmptySet(t *testing.T) {
	if _, err := openListeners(nil, http.NewServeMux(), nil); err == nil {
		t.Fatal("empty listener set started")
	}
}

func TestOpenListenersClosesTheFirstWhenTheNextBindFails(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	_, err = openListeners([]app.Listener{
		{Addr: addr, Entry: app.EntryPublic},
		{Addr: "not-an-address", Entry: app.EntryPublic},
	}, http.NewServeMux(), nil)
	if err == nil {
		t.Fatal("bad address started")
	}
	rebind, rebindErr := net.Listen("tcp", addr)
	if rebindErr != nil {
		t.Fatalf("first listener stayed open after the second bind failed: %v", rebindErr)
	}
	rebind.Close()
	conn, dialErr := net.DialTimeout("tcp", addr, 200*time.Millisecond)
	if dialErr == nil {
		conn.Close()
		t.Fatal("first listener stayed open after the second bind failed")
	}
}

func TestOpenListenersRegistersTheServer(t *testing.T) {
	var registered int
	set, err := openListeners([]app.Listener{{
		Addr:  "127.0.0.1:0",
		Entry: app.EntryPublic,
	}}, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), func(*http.Server) {
		registered++
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(set.close)
	if registered != 1 {
		t.Fatalf("registered %d servers", registered)
	}
}

func TestShutdownServersTreatsADeadlineAsAClosedListener(t *testing.T) {
	prevTimeout := shutdownTimeout
	shutdownTimeout = 30 * time.Millisecond
	t.Cleanup(func() { shutdownTimeout = prevTimeout })
	orig := shutdownHTTP
	t.Cleanup(func() { shutdownHTTP = orig })
	shutdownHTTP = func(*http.Server, context.Context) error {
		return context.DeadlineExceeded
	}
	srv := &http.Server{Handler: http.NewServeMux()}
	if err := shutdownServers([]*http.Server{nil, srv}); err != nil {
		t.Fatal(err)
	}
}

func TestDrainServeErrorsStopsWaiting(t *testing.T) {
	prev := shutdownTimeout
	shutdownTimeout = 20 * time.Millisecond
	t.Cleanup(func() { shutdownTimeout = prev })
	started := time.Now()
	drainServeErrors(make(chan error), 4)
	if time.Since(started) < 15*time.Millisecond {
		t.Fatal("drain returned before the shutdown budget")
	}
	drainServeErrors(make(chan error), 0)
}

func TestRunHTTPServersClosesTheOthersWhenOneStops(t *testing.T) {
	prev := shutdownTimeout
	shutdownTimeout = 30 * time.Millisecond
	t.Cleanup(func() { shutdownTimeout = prev })

	ln := listenLocal(t)
	srv := &http.Server{Handler: http.NewServeMux()}
	serveErr := make(chan error, 2)
	go func() { serveErr <- srv.Serve(ln) }()
	waitListening(t, ln.Addr().String())

	flushed := false
	done := make(chan error, 1)
	go func() {
		done <- runHTTPServers([]*http.Server{srv, nil}, serveErr, make(chan struct{}), func() {
			flushed = true
		})
	}()
	if err := srv.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the other listeners were not closed")
	}
	if !flushed {
		t.Fatal("error reports were not flushed")
	}
}

func TestRunHTTPServersShutsThePeerDownWhenServeReturnsBeforeStop(t *testing.T) {
	prev := shutdownTimeout
	shutdownTimeout = 200 * time.Millisecond
	t.Cleanup(func() { shutdownTimeout = prev })

	set, err := openListeners([]app.Listener{
		{Addr: "127.0.0.1:0", Entry: app.EntryPublic},
		{Addr: "127.0.0.1:0", Entry: app.EntryPublic},
	}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, addr := range set.addrs {
		waitListening(t, addr)
	}

	stop := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- runHTTPServers(set.servers, set.serveErr, stop, func() {})
	}()
	if err := set.servers[0].Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("shutdown: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("peer listener was not shut down before stop closed")
	}
	select {
	case <-stop:
		t.Fatal("stop was closed")
	default:
	}
	conn, dialErr := net.DialTimeout("tcp", set.addrs[1], 200*time.Millisecond)
	if dialErr == nil {
		conn.Close()
		t.Fatal("peer listener accepted a connection after the other Serve returned")
	}
}

func readEntry(t *testing.T, addr string) string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var last error
	for time.Now().Before(deadline) {
		res, err := http.Get("http://" + addr + "/")
		if err != nil {
			last = err
			time.Sleep(10 * time.Millisecond)
			continue
		}
		defer res.Body.Close()
		body, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		if res.StatusCode != http.StatusOK {
			t.Fatalf("status %d %s", res.StatusCode, body)
		}
		return string(body)
	}
	t.Fatalf("GET %s: %v", addr, last)
	return ""
}
