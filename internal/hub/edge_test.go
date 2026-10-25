package hub

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/scan"
)

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("keyboard closed") }

func TestOpenAndProtocolEdges(t *testing.T) {
	if _, err := Open(Options{}); err == nil {
		t.Fatal("open without files")
	}
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(Options{StatePath: bad, BufferPath: filepath.Join(dir, "b.sqlite")}); err == nil {
		t.Fatal("unreadable credentials were accepted")
	}
	var empty *Buffer
	if err := empty.Close(); err != nil {
		t.Fatal(err)
	}
	if atoi("12") != 12 || atoi("-3") != 0 || atoi("nope") != 0 {
		t.Fatal("atoi")
	}
	if FormatVersion("") != "s12" || FormatVersion("1") != "1" {
		t.Fatal("version")
	}

	api := &fakeAPI{}
	h, _ := newTestHub(t, api, nil)
	h.opts.Now = nil
	h.opts.Mono = nil
	if h.now().IsZero() || h.mono() < 0 {
		t.Fatal("default clock")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	h.opts.Sleep = nil
	if err := h.sleep(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("sleep = %v", err)
	}
	if err := h.sleep(context.Background(), 0); err != nil {
		t.Fatal(err)
	}
	h.opts.Synced = func(context.Context) (bool, error) { return true, errors.New("chronyc") }
	if h.clockSynced(context.Background()) {
		t.Fatal("probe error looked synced")
	}
	h.client.BaseURL = ""
	if err := h.Pair(context.Background(), "SBP-0123-ABCD-G"); err == nil {
		t.Fatal("paired without a server")
	}
	if err := h.Pair(context.Background(), "not-a-code"); err == nil {
		t.Fatal("bad pairing code")
	}
	h.client.BaseURL = h.opts.ServerURL
	api.failPair = http.StatusUnauthorized
	if err := h.Pair(context.Background(), "SBP-0123-ABCD-G"); err == nil || strings.Contains(err.Error(), "sbd_") {
		t.Fatalf("revoked pair = %v", err)
	}
	api.failPair = 0
	api.emptyToken = true
	if err := h.Pair(context.Background(), "SBP-0123-ABCD-G"); err == nil {
		t.Fatal("empty token")
	}
	api.emptyToken = false
	if err := h.Beat(context.Background()); err == nil {
		t.Fatal("heartbeat before pairing")
	}
	if err := h.FlushAll(context.Background()); err == nil {
		t.Fatal("flush before pairing")
	}

	now := time.Date(2026, 10, 24, 2, 0, 0, 0, time.UTC)
	m := newMode(func() time.Time { return now })
	m.idle = 0
	m.current = scan.StockIn
	m.idleAnchor = now
	if m.direction() != scan.StockIn {
		t.Fatal("default idle should still be in the window")
	}
	m.idle = time.Second
	m.idleAnchor = now.Add(-2 * time.Second)
	m.current = scan.StockIn
	m.noteScan()
	if m.current != scan.StockOut {
		t.Fatal("late scan should not keep stock-in")
	}
	m.stockIn = "IN"
	m.stockOut = ""
	if _, ok := m.control(""); ok {
		t.Fatal("blank stock-out matched")
	}
}

func TestFlushRetryAndBlankResult(t *testing.T) {
	api := &fakeAPI{}
	h, logs := newTestHub(t, api, func(o *Options) {
		o.Sleep = func(context.Context, time.Duration) error { return errors.New("stop retrying") }
	})
	if err := h.Pair(context.Background(), "SBP-0123-ABCD-G"); err != nil {
		t.Fatal(err)
	}
	if err := h.HandleLine(context.Background(), "123456789012"); err != nil {
		t.Fatal(err)
	}
	api.blankResults = true
	if err := h.FlushAll(context.Background()); err == nil {
		t.Fatal("blank result was treated as accepted")
	}
	api.blankResults = false
	h.client.BaseURL = "http://127.0.0.1:1"
	if err := h.FlushAll(context.Background()); err == nil || !strings.Contains(logs.String(), "will retry") {
		t.Fatalf("network retry = %v %s", err, logs.String())
	}
	if depth, _ := h.Queued(); depth != 1 {
		t.Fatalf("network error dropped the queue: %d", depth)
	}

	h.opts.Stdin = strings.NewReader("SBP-0123-ABCD-0\n")
	h.opts.After = func(time.Duration) <-chan time.Time { return make(chan time.Time) }
	if err := h.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logs.String(), "scan not stored") {
		t.Fatalf("logs = %s", logs.String())
	}
	api.failBeats = 1
	h.opts.Stdin = nil
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ticks := 0
	h.opts.After = func(time.Duration) <-chan time.Time {
		ticks++
		if ticks > 1 {
			cancel()
			return make(chan time.Time)
		}
		fired := make(chan time.Time)
		close(fired)
		return fired
	}
	err := h.Run(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("run = %v", err)
	}
	if !strings.Contains(logs.String(), "heartbeat did not reach") {
		t.Fatalf("logs = %s", logs.String())
	}
	h.opts.Stdin = errReader{}
	h.opts.After = func(time.Duration) <-chan time.Time { return make(chan time.Time) }
	if err := h.Run(context.Background()); err == nil {
		t.Fatal("reader error was ignored")
	}
}

func TestWatchdogRestoreFailure(t *testing.T) {
	api := &fakeAPI{}
	h, logs := newTestHub(t, api, nil)
	file := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	past := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	h.releaseRoot = file
	h.state.Pending = &PendingUpdate{Version: "9", PreviousPath: "/tmp/previous-release", Deadline: past}
	if _, err := h.EvaluateWatchdog(time.Now()); err == nil {
		t.Fatal("restore should fail when the release root is a file")
	}
	h.opts.Stdin = nil
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ticks := 0
	h.opts.After = func(time.Duration) <-chan time.Time {
		ticks++
		if ticks > 1 {
			cancel()
			return make(chan time.Time)
		}
		fired := make(chan time.Time)
		close(fired)
		return fired
	}
	if err := h.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("run = %v", err)
	}
	if !strings.Contains(logs.String(), "watchdog") {
		t.Fatalf("logs = %s", logs.String())
	}
	if err := restoreLink(t.TempDir(), ""); err == nil {
		t.Fatal("empty restore")
	}
	ctx, cancel = context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, _ = ChronySynced(ctx, nil)
	if got := (&Client{}).http().Timeout; got != defaultHTTPTimeout {
		t.Fatalf("default timeout = %s", got)
	}
	c := &Client{BaseURL: "http://127.0.0.1", HTTP: nil}
	if err := c.do(context.Background(), http.MethodPost, "/x", "", map[string]any{"n": make(chan int)}, nil); err == nil {
		t.Fatal("bad body was encoded")
	}
	if err := c.do(context.Background(), "BAD", "/x", "", nil, nil); err == nil {
		t.Fatal("bad method was sent")
	}
}

func TestCLIWatchdogAndKeyFile(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.json")
	bufferPath := filepath.Join(dir, "buffer.sqlite")
	root := filepath.Join(dir, "opt")
	prev := filepath.Join(root, "releases", "1.0.0")
	if err := os.MkdirAll(prev, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(prev, "stockbot-hub"), []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "releases", "2.0.0"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := swapLink(filepath.Join(root, "current"), filepath.Join(root, "releases", "2.0.0")); err != nil {
		t.Fatal(err)
	}
	state := State{
		ServerURL: "http://127.0.0.1:9",
		BootID:    "boot",
		Pending: &PendingUpdate{
			Version: "2.0.0", PreviousVersion: "1.0.0", PreviousPath: prev,
			Deadline: time.Now().Add(-time.Second),
		},
	}
	if err := saveState(statePath, state); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr strings.Builder
	code := mainWith([]string{"--watchdog", "--state", statePath, "--buffer", bufferPath, "--releases", root}, nil, &stdout, &stderr)
	if code != 0 || !strings.Contains(stdout.String(), "rolled back") {
		t.Fatalf("watchdog %d %s %s", code, stdout.String(), stderr.String())
	}
	if target, _ := os.Readlink(filepath.Join(root, "current")); target != prev {
		t.Fatalf("current -> %s", target)
	}
	stdout.Reset()
	code = mainWith([]string{"--watchdog", "--state", statePath, "--buffer", bufferPath, "--releases", root}, nil, &stdout, &stderr)
	if code != 0 || strings.Contains(stdout.String(), "rolled back") {
		t.Fatalf("second watchdog %d %s", code, stdout.String())
	}

	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(dir, "key")
	if err := os.WriteFile(keyPath, []byte(base64.StdEncoding.EncodeToString(pub)), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HUB_NAME", "From Env")
	stdout.Reset()
	stderr.Reset()
	code = mainWith([]string{
		"--pubkey", keyPath, "--state", statePath, "--buffer", bufferPath,
		"--releases", root, "--server", "http://127.0.0.1:9", "--version", "",
	}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run exit %d %s", code, stderr.String())
	}
	if mainWith([]string{"--nope"}, nil, io.Discard, io.Discard) != 2 {
		t.Fatal("unknown flag")
	}
	if Main([]string{"--self-check"}) != 0 {
		t.Fatal("main self-check")
	}
	stderr.Reset()
	if mainWith([]string{"--state", dir, "--buffer", bufferPath}, nil, io.Discard, &stderr) != 1 {
		t.Fatalf("open error = %s", stderr.String())
	}
	api := &fakeAPI{failPair: http.StatusNotFound}
	srvCode := mainWith([]string{
		"--server", httptestServer(t, api), "--code", "SBP-0123-ABCD-G", "--pair-only",
		"--state", filepath.Join(dir, "pair-state.json"), "--buffer", filepath.Join(dir, "pair-buffer.sqlite"),
	}, nil, io.Discard, &stderr)
	if srvCode != 1 {
		t.Fatal("pair failure should exit 1")
	}
	if mainWith([]string{
		"--state", statePath, "--buffer", bufferPath, "--releases", root,
	}, errReader{}, io.Discard, &stderr) != 1 {
		t.Fatal("stdin error should exit 1")
	}
}

func TestRestartFlagRewrite(t *testing.T) {
	got := replaceFlag([]string{"stockbot-hub", "--version", "1.0.0", "--version=old", "--state", "s"}, "version", "1.2.0")
	if strings.Join(got, " ") != "stockbot-hub --version 1.2.0 --version=1.2.0 --state s" {
		t.Fatalf("args = %q", got)
	}
	if err := restartInto(filepath.Join(t.TempDir(), "missing-hub")); err == nil {
		t.Fatal("missing binary replaced this process")
	}
}

func httptestServer(t *testing.T, api *fakeAPI) string {
	t.Helper()
	h, _ := newTestHub(t, api, nil)
	t.Cleanup(func() { _ = h.Close() })
	return h.ServerURL()
}
