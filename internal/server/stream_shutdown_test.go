package server

import (
	"context"
	"net/http"
	"testing"
)

func TestStreamShutdownIgnoresANilReceiver(t *testing.T) {
	var shutdown *StreamShutdown
	shutdown.Register(nil)
	shutdown.Register(&http.Server{})
	shutdown.Stop()
	parent := context.Background()
	if shutdown.Context(parent) != parent {
		t.Fatal("a nil shutdown replaced the request context")
	}
	if shutdown.Context(nil) != nil {
		t.Fatal("a nil parent was replaced")
	}
	live := NewStreamShutdown()
	if live.Context(nil) != nil {
		t.Fatal("a live shutdown invented a context for a nil parent")
	}
}
