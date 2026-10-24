package main

import (
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/app"
)

func TestOpenListenersTagsEachEntry(t *testing.T) {
	set, err := openListeners([]app.Listener{
		{Addr: "127.0.0.1:0", Entry: app.EntryLAN},
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
	for i, want := range []string{string(app.EntryLAN), string(app.EntryPublic)} {
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
		{Addr: addr, Entry: app.EntryLAN},
		{Addr: "not-an-address", Entry: app.EntryPublic},
	}, http.NewServeMux(), nil)
	if err == nil {
		t.Fatal("bad address started")
	}
	conn, dialErr := net.DialTimeout("tcp", addr, 200*time.Millisecond)
	if dialErr == nil {
		conn.Close()
		t.Fatal("first listener stayed open after the second bind failed")
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
