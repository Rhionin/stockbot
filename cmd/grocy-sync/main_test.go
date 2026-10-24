package main

import (
	"bytes"
	"log"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// lockedBuffer lets the test read the log while main writes it.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestProcessMainWaitsUntilTheProcessIsSignaled(t *testing.T) {
	for _, key := range []string{"GROCY_URL", "GROCY_API_KEY", "PANTRY_SYNC_TOKEN", "GROCY_SYNC_INTERVAL", "PANTRY_URL", "GROCY_SYNC_MAP"} {
		t.Setenv(key, "")
	}
	var logs lockedBuffer
	log.SetOutput(&logs)
	t.Cleanup(func() {
		log.SetOutput(os.Stderr)
		signal.Reset(os.Interrupt, syscall.SIGTERM)
	})

	done := make(chan struct{})
	go func() {
		main()
		close(done)
	}()
	deadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(logs.String(), "disabled until") {
		if time.Now().After(deadline) {
			t.Fatalf("main did not wait: %s", logs.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("main did not return after SIGTERM")
	}
}
