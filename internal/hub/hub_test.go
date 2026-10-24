package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPairingScannedTypedAndBadCheck(t *testing.T) {
	api := &fakeAPI{}
	h, logs := newTestHub(t, api, nil)
	if err := h.HandleLine(context.Background(), "SBP-0123-ABCD-0"); !errors.Is(err, ErrBadPairingCheck) {
		t.Fatalf("bad check = %v", err)
	}
	if api.pairCalls != 0 {
		t.Fatal("bad check reached the server")
	}
	if err := h.HandleLine(context.Background(), "not-a-code"); err == nil {
		t.Fatal("unpaired product scan was accepted")
	}
	depth, err := h.Queued()
	if err != nil {
		t.Fatal(err)
	}
	if depth != 0 {
		t.Fatalf("unpaired scan queued %d", depth)
	}
	if err := h.HandleLine(context.Background(), "  sbp-0123-abcd-g\r\n"); err != nil {
		t.Fatal(err)
	}
	if !h.Paired() || h.DeviceID() == "" {
		t.Fatal("typed code did not pair")
	}
	body := api.lastPair()
	if body["code"] != "SBP-0123-ABCD-G" || body["name"] != "Counter" || body["kind"] != "pi_hub" || body["version"] != "1.0.0" {
		t.Fatalf("pair body = %#v", body)
	}
	hubBody, _ := body["hub"].(map[string]any)
	if hubBody["kind"] != "pi_hub" || hubBody["bootId"] != "boot-1" {
		t.Fatalf("hub object = %#v", hubBody)
	}
	info, err := os.Stat(h.opts.StatePath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("state mode = %o", info.Mode().Perm())
	}
	raw, err := os.ReadFile(h.opts.StatePath)
	if err != nil || !bytes.Contains(raw, []byte(api.token)) {
		t.Fatal("token was not stored")
	}
	if strings.Contains(logs.String(), api.token) || strings.Contains(logs.String(), "SBP-0123-ABCD-G") {
		t.Fatalf("log leaked a secret: %s", logs.String())
	}
	before := api.pairCalls
	if err := h.HandleLine(context.Background(), "SBP-0123-ABCD-G"); err != nil {
		t.Fatal(err)
	}
	if api.pairCalls != before {
		t.Fatal("paired hub sent another pairing code")
	}
}

func TestCLITypedCodeAndBadCheck(t *testing.T) {
	api := &fakeAPI{}
	srv := httptest.NewServer(api)
	defer srv.Close()
	dir := t.TempDir()
	var stderr bytes.Buffer
	code := mainWith([]string{
		"hub", "--server", srv.URL, "--code", "SBP-0123-ABCD-0",
		"--state", filepath.Join(dir, "state.json"),
		"--buffer", filepath.Join(dir, "buffer.sqlite"),
	}, strings.NewReader(""), io.Discard, &stderr)
	if code != 2 || !strings.Contains(stderr.String(), ErrBadPairingCheck.Error()) {
		t.Fatalf("bad check exit %d %s", code, stderr.String())
	}
	if api.pairCalls != 0 {
		t.Fatal("cli sent a bad check character")
	}
	stderr.Reset()
	var stdout bytes.Buffer
	code = mainWith([]string{
		"--server", srv.URL, "--code", "sbp-0l23-abcd-g", "--pair-only",
		"--name", "Keyboard",
		"--state", filepath.Join(dir, "state.json"),
		"--buffer", filepath.Join(dir, "buffer.sqlite"),
		"--releases", filepath.Join(dir, "rel"),
	}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 || strings.TrimSpace(stdout.String()) != "paired" {
		t.Fatalf("typed pair exit %d %s %s", code, stdout.String(), stderr.String())
	}
	if api.lastPair()["code"] != "SBP-0123-ABCD-G" || api.lastPair()["name"] != "Keyboard" {
		t.Fatalf("cli body = %#v", api.lastPair())
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "state.json"))
	if bytes.Contains(stderr.Bytes(), []byte(api.token)) || bytes.Contains(stdout.Bytes(), []byte(api.token)) {
		t.Fatal("cli printed the token")
	}
	if !bytes.Contains(raw, []byte(api.token)) {
		t.Fatal("cli did not store the token")
	}
	var help bytes.Buffer
	if mainWith([]string{"--help"}, nil, io.Discard, &help) != 2 {
		t.Fatal("help")
	}
	if mainWith([]string{"--self-check"}, nil, &stdout, io.Discard) != 0 {
		t.Fatal("self-check")
	}
	if mainWith([]string{"--pubkey", "not-a-key", "--state", filepath.Join(dir, "s.json"), "--buffer", filepath.Join(dir, "b.sqlite")}, nil, io.Discard, io.Discard) != 2 {
		t.Fatal("bad key")
	}
}

func TestOfflineQueueFlushAndHeartbeat(t *testing.T) {
	api := &fakeAPI{offlineScans: 2, retryAfter: "45"}
	var mono int64 = 1000
	var sleeps []time.Duration
	h, logs := newTestHub(t, api, func(o *Options) {
		o.Mono = func() int64 { return mono }
		o.Synced = func(context.Context) (bool, error) { return false, nil }
		o.Sleep = func(_ context.Context, d time.Duration) error {
			sleeps = append(sleeps, d)
			return nil
		}
		o.Rand = func() float64 { return 0 }
	})
	if err := h.Pair(context.Background(), "SBP-0123-ABCD-G"); err != nil {
		t.Fatal(err)
	}
	api.offlineScans = 100
	for i := 0; i < BufferCap+1; i++ {
		mono = 1000 + int64(i)
		if err := h.HandleLine(context.Background(), barcodeOf(i)); err != nil {
			t.Fatal(err)
		}
	}
	depth, _ := h.Queued()
	dropped, _ := h.Dropped()
	if depth != BufferCap || dropped != 1 {
		t.Fatalf("queue depth %d dropped %d", depth, dropped)
	}
	oldest, err := h.buf.Peek(1)
	if err != nil || oldest[0].Barcode != barcodeOf(1) {
		t.Fatalf("oldest = %+v", oldest)
	}
	if strings.Contains(logs.String(), barcodeOf(1)) || strings.Contains(logs.String(), api.token) {
		t.Fatal("logs included a barcode or token")
	}
	mono = 20000
	if err := h.Beat(context.Background()); err != nil {
		t.Fatal(err)
	}
	if api.beats[0].Queued != BufferCap || api.beats[0].DroppedCount != 1 || api.beats[0].BootID != "boot-1" || api.beats[0].Version != "1.0.0" {
		t.Fatalf("heartbeat = %+v", api.beats[0])
	}
	if left, _ := h.Dropped(); left != 0 {
		t.Fatalf("dropped after heartbeat = %d", left)
	}
	api.offlineScans = 0
	api.scanStatus = map[string]string{}
	// Put a dropped count back and also exercise retry on the way out.
	if _, err := h.buf.Enqueue(queuedScan{ID: "extra-drop", Barcode: "12345670", Direction: "stock_in", UnitCount: 1, CapturedMono: 1, BootID: "boot-1"}); err != nil {
		t.Fatal(err)
	}
	// Queue is over cap again; the extra enqueue drops one more.
	api.offlineScans = 1
	api.retryAfter = ""
	api.failStatus = http.StatusInternalServerError
	if err := h.FlushAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(sleeps) == 0 {
		t.Fatal("offline flush did not back off")
	}
	seen := map[string]int{}
	var sizes []int
	for _, batch := range api.batches {
		sizes = append(sizes, len(batch.Scans))
		if len(batch.Scans) > BatchSize {
			t.Fatalf("batch %d", len(batch.Scans))
		}
		for _, scan := range batch.Scans {
			seen[scan.ID]++
			if scan.AgeMs <= 0 {
				t.Fatalf("ageMs = %d for %s", scan.AgeMs, scan.ID)
			}
			if scan.ScannedAt != "" {
				t.Fatal("unsynced scan included scannedAt")
			}
		}
	}
	if len(sizes) == 0 || sizes[0] != BatchSize {
		t.Fatalf("batch sizes %v", sizes)
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("id %s sent %d times", id, n)
		}
	}
	if depth, _ = h.Queued(); depth != 0 {
		t.Fatalf("queue left %d", depth)
	}
	api.beats = nil
	if err := h.Beat(context.Background()); err != nil {
		t.Fatal(err)
	}
	if api.beats[0].DroppedCount != 0 || api.beats[0].Queued != 0 {
		t.Fatalf("second heartbeat = %+v", api.beats[0])
	}
}

func TestFlushStatusesBackoffAndRevoke(t *testing.T) {
	api := &fakeAPI{failStatus: http.StatusTooManyRequests, retryAfter: "45", offlineScans: 1}
	var slept time.Duration
	var mono int64 = 1000
	h, _ := newTestHub(t, api, func(o *Options) {
		o.Rand = func() float64 { return 1 }
		o.Mono = func() int64 {
			mono += 4000
			return mono
		}
		o.Sleep = func(_ context.Context, d time.Duration) error {
			slept = d
			return nil
		}
		o.Synced = func(context.Context) (bool, error) { return true, nil }
	})
	if err := h.Pair(context.Background(), "SBP-0123-ABCD-G"); err != nil {
		t.Fatal(err)
	}
	if h.Direction() != "stock_in" {
		t.Fatalf("direction %s", h.Direction())
	}
	if err := h.HandleLine(context.Background(), "STOCK_OUT"); err != nil {
		t.Fatal(err)
	}
	if err := h.HandleLine(context.Background(), "012345678905"); err != nil {
		t.Fatal(err)
	}
	if err := h.HandleLine(context.Background(), "STOCK_IN"); err != nil {
		t.Fatal(err)
	}
	if err := h.HandleLine(context.Background(), "012345678906"); err != nil {
		t.Fatal(err)
	}
	rows, _ := h.buf.Peek(10)
	if len(rows) != 2 || rows[0].Direction != "stock_out" || rows[1].Direction != "stock_in" || rows[1].ScannedAt == "" {
		t.Fatalf("rows = %+v", rows)
	}
	api.scanStatus = map[string]string{rows[0].ID: "duplicate", rows[1].ID: "conflict"}
	if err := h.FlushAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if slept != 45*time.Second {
		t.Fatalf("retry wait = %s", slept)
	}
	if depth, _ := h.Queued(); depth != 0 {
		t.Fatalf("terminal statuses left %d", depth)
	}
	if api.batches[0].Scans[1].ScannedAt == "" || api.batches[0].Scans[1].AgeMs <= 0 {
		t.Fatalf("synced payload = %+v", api.batches[0].Scans[1])
	}

	api.scanStatus = map[string]string{}
	if err := h.HandleLine(context.Background(), "012345678907"); err != nil {
		t.Fatal(err)
	}
	kept, _ := h.buf.Peek(1)
	api.scanStatus[kept[0].ID] = "rejected"
	api.offlineScans = 0
	if err := h.FlushAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if depth, _ := h.Queued(); depth != 0 {
		t.Fatal("rejected scan stayed queued")
	}

	if err := h.HandleLine(context.Background(), "012345678908"); err != nil {
		t.Fatal(err)
	}
	api.revoked = true
	err := h.FlushAll(context.Background())
	if !errors.Is(err, ErrRevoked) {
		t.Fatalf("revoke = %v", err)
	}
	if h.Paired() {
		t.Fatal("token kept after revoke")
	}
	if depth, _ := h.Queued(); depth != 1 {
		t.Fatalf("queue after revoke = %d", depth)
	}
	raw, _ := os.ReadFile(h.opts.StatePath)
	if bytes.Contains(raw, []byte(api.token)) {
		t.Fatal("revoked token still on disk")
	}
	api.revoked = false
	if err := h.HandleLine(context.Background(), "SBP-ZZZZ-ZZZZ-F"); err != nil {
		t.Fatal(err)
	}
	if !h.Paired() {
		t.Fatal("did not pair again")
	}
	if depth, _ := h.Queued(); depth != 1 {
		t.Fatal("queue was wiped on re-pair")
	}
	api.offlineScans = 1
	api.failStatus = http.StatusBadRequest
	api.retryAfter = ""
	if err := h.FlushAll(context.Background()); err == nil {
		t.Fatal("bad request was retried or ignored")
	}
	if depth, _ := h.Queued(); depth != 1 {
		t.Fatal("a rejected request dropped the queue")
	}
}

func TestUnknownStatusStaysQueued(t *testing.T) {
	if !terminalStatus("rejected") || !terminalStatus("created") || !terminalStatus("accepted") || !terminalStatus("duplicate") || !terminalStatus("conflict") {
		t.Fatal("known statuses should leave the queue")
	}
	if terminalStatus("retry") || terminalStatus("") || terminalStatus("RETRY") {
		t.Fatal("unknown status was treated as terminal")
	}
	api := &fakeAPI{}
	h, _ := newTestHub(t, api, nil)
	if err := h.Pair(context.Background(), "SBP-0123-ABCD-G"); err != nil {
		t.Fatal(err)
	}
	if err := h.HandleLine(context.Background(), "012345678905"); err != nil {
		t.Fatal(err)
	}
	rows, err := h.buf.Peek(1)
	if err != nil || len(rows) != 1 {
		t.Fatal(err)
	}
	api.scanStatus = map[string]string{rows[0].ID: "retry"}
	if err := h.FlushAll(context.Background()); err == nil {
		t.Fatal("unknown status was accepted")
	}
	if depth, _ := h.Queued(); depth != 1 {
		t.Fatalf("retry left the queue: %d", depth)
	}
	api.scanStatus[rows[0].ID] = "rejected"
	if err := h.FlushAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if depth, _ := h.Queued(); depth != 0 {
		t.Fatal("rejected scan stayed queued")
	}
}

func TestRunBuffersWhileServerIsDown(t *testing.T) {
	api := &fakeAPI{offlineScans: 100, failStatus: http.StatusServiceUnavailable}
	start := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	var clockMu sync.Mutex
	now := start
	pr, pw := io.Pipe()
	t.Cleanup(func() {
		_ = pw.Close()
		_ = pr.Close()
	})
	wakes := make(chan chan time.Time, 8)
	var restarted string
	h, _ := newTestHub(t, api, func(o *Options) {
		o.Now = func() time.Time {
			clockMu.Lock()
			defer clockMu.Unlock()
			return now
		}
		o.Stdin = pr
		o.Rand = func() float64 { return 0 }
		o.Sleep = func(context.Context, time.Duration) error {
			t.Error("Run slept inside a retry")
			return errors.New("run must not sleep")
		}
		o.After = func(time.Duration) <-chan time.Time {
			ch := make(chan time.Time, 1)
			select {
			case wakes <- ch:
			default:
			}
			return ch
		}
		o.OnRestart = func(path string) error {
			restarted = path
			return nil
		}
	})
	if err := h.Pair(context.Background(), "SBP-0123-ABCD-G"); err != nil {
		t.Fatal(err)
	}
	prev := filepath.Join(h.releaseRoot, "releases", "1.0.0")
	if err := os.MkdirAll(prev, 0o755); err != nil {
		t.Fatal(err)
	}
	next := filepath.Join(h.releaseRoot, "releases", "9.9.9")
	if err := os.MkdirAll(next, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := swapLink(filepath.Join(h.releaseRoot, "current"), next); err != nil {
		t.Fatal(err)
	}
	h.state.Pending = &PendingUpdate{
		Version: "9.9.9", PreviousVersion: "1.0.0", PreviousPath: prev,
		Deadline: start.Add(30 * time.Second),
	}
	if err := h.persist(); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- h.Run(ctx) }()

	for _, code := range []string{"111111111117", "222222222224", "333333333331"} {
		if _, err := io.WriteString(pw, code+"\n"); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		depth, _ := h.Queued()
		if depth == 3 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("queued %d while the server was down", depth)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(api.beats) == 0 {
		t.Fatal("startup heartbeat did not run")
	}

	wake := latestWake(t, wakes)
	clockMu.Lock()
	now = start.Add(HeartbeatInterval)
	clockMu.Unlock()
	wake <- time.Now()

	deadline = time.Now().Add(5 * time.Second)
	for {
		api.mu.Lock()
		n := len(api.beats)
		var queued int
		if n > 0 {
			queued = api.beats[n-1].Queued
		}
		api.mu.Unlock()
		if n >= 2 && queued == 3 && h.RollbackPending() && currentTarget(t, h.releaseRoot) == prev {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("beats %d queued %d rollback %v current %s", n, queued, h.RollbackPending(), currentTarget(t, h.releaseRoot))
		}
		time.Sleep(10 * time.Millisecond)
	}

	api.mu.Lock()
	api.offlineScans = 0
	api.mu.Unlock()
	clockMu.Lock()
	now = now.Add(time.Minute)
	clockMu.Unlock()
	wake = latestWake(t, wakes)
	wake <- time.Now()

	deadline = time.Now().Add(5 * time.Second)
	for {
		depth, _ := h.Queued()
		if depth == 0 && len(api.batches) > 0 {
			break
		}
		select {
		case extra := <-wakes:
			extra <- time.Now()
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("queue %d batches %d after recovery", depth, len(api.batches))
		}
		time.Sleep(10 * time.Millisecond)
	}
	seen := map[string]int{}
	api.mu.Lock()
	batches := append([]batchRequest(nil), api.batches...)
	api.mu.Unlock()
	for _, batch := range batches {
		for _, scan := range batch.Scans {
			seen[scan.Barcode]++
		}
	}
	for _, code := range []string{"111111111117", "222222222224", "333333333331"} {
		if seen[code] != 1 {
			t.Fatalf("barcode %s sent %d times", code, seen[code])
		}
	}
	if restarted != "" {
		t.Fatal("offline run restarted")
	}
	_ = pw.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("run did not return after stdin closed")
	}
}

func latestWake(t *testing.T, wakes <-chan chan time.Time) chan time.Time {
	t.Helper()
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	var wake chan time.Time
	for {
		select {
		case ch := <-wakes:
			wake = ch
		case <-timer.C:
			t.Fatal("run did not schedule a wake")
		}
		for {
			select {
			case ch := <-wakes:
				wake = ch
			default:
				return wake
			}
		}
	}
}

func TestHeartbeatControlBarcodeAndIdle(t *testing.T) {
	api := &fakeAPI{}
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	h, _ := newTestHub(t, api, func(o *Options) {
		o.Now = func() time.Time { return now }
		o.Synced = func(context.Context) (bool, error) { return true, nil }
	})
	if !h.ShouldBeat(now) {
		t.Fatal("first heartbeat should be due")
	}
	if err := h.Pair(context.Background(), "SBP-0123-ABCD-G"); err != nil {
		t.Fatal(err)
	}
	api.config.ControlBarcodes.StockIn = "IN-NOW"
	api.config.ControlBarcodes.StockOut = "OUT-NOW"
	api.config.IdleSeconds = 1
	api.config.ConfigVersion = 2
	if err := h.Beat(context.Background()); err != nil {
		t.Fatal(err)
	}
	if h.ShouldBeat(now.Add(59 * time.Second)) {
		t.Fatal("heartbeat early")
	}
	if !h.ShouldBeat(now.Add(HeartbeatInterval)) {
		t.Fatal("heartbeat late")
	}
	if HeartbeatInterval != 60*time.Second || WatchdogLimit != 120*time.Second || BatchSize != 50 || BufferCap != 500 {
		t.Fatal("protocol constants drifted")
	}
	if err := h.HandleLine(context.Background(), "IN-NOW"); err != nil {
		t.Fatal(err)
	}
	if err := h.HandleLine(context.Background(), "11111111"); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Second)
	if err := h.HandleLine(context.Background(), "22222222"); err != nil {
		t.Fatal(err)
	}
	rows, _ := h.buf.Peek(5)
	if len(rows) != 2 || rows[0].Direction != "stock_in" || rows[1].Direction != "stock_out" {
		t.Fatalf("idle rows = %+v", rows)
	}
	if err := h.HandleLine(context.Background(), strings.Repeat("9", 65)); err == nil {
		t.Fatal("long barcode stored")
	}
	if err := h.HandleLine(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
}

func TestRunFlushesAndHeartbeats(t *testing.T) {
	api := &fakeAPI{}
	h, _ := newTestHub(t, api, func(o *Options) {
		o.Stdin = strings.NewReader("SBP-0123-ABCD-G\n1234567890128\n")
		o.Sleep = func(context.Context, time.Duration) error { return nil }
	})
	if err := h.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !h.Paired() {
		t.Fatal("run did not pair")
	}
	if depth, _ := h.Queued(); depth != 0 {
		t.Fatalf("run left %d scans", depth)
	}
	if len(api.batches) != 1 || api.batches[0].Scans[0].Barcode != "1234567890128" {
		t.Fatalf("batches = %+v", api.batches)
	}

	ctx, cancel := context.WithCancel(context.Background())
	h.opts.Stdin = nil
	calls := 0
	h.opts.After = func(time.Duration) <-chan time.Time {
		calls++
		if calls > 1 {
			cancel()
			return make(chan time.Time)
		}
		fired := make(chan time.Time)
		close(fired)
		return fired
	}
	err := h.Run(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel = %v", err)
	}
	if calls < 2 {
		t.Fatal("heartbeat timer did not fire")
	}
}

type fakeAPI struct {
	mu           sync.Mutex
	token        string
	deviceID     string
	pairCalls    int
	pairBody     map[string]any
	batches      []batchRequest
	beats        []beatRequest
	auths        []string
	failBeats    int
	failRotate   bool
	badJSON      bool
	artifact     []byte
	offlineScans int
	failStatus   int
	retryAfter   string
	scanStatus   map[string]string
	revoked      bool
	failPair     int
	blankResults bool
	emptyToken   bool
	config       HubConfig
	rotate       bool
	nextToken    string
	update       *Release
	downloads    int
	statePath    string
	onDownload   func()
}

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.revoked && r.URL.Path != pairPath {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"code":"device_revoked"}`))
		return
	}
	if strings.HasPrefix(r.URL.Path, "/artifact") {
		f.downloads++
		if f.onDownload != nil {
			f.onDownload()
		}
		if len(f.artifact) == 0 {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write(f.artifact)
		return
	}
	switch r.URL.Path {
	case pairPath:
		f.pairCalls++
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.pairBody = body
		if f.failPair != 0 {
			w.WriteHeader(f.failPair)
			_, _ = w.Write([]byte(`{"code":"not_found"}`))
			return
		}
		if f.emptyToken {
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"deviceId": "device-1"})
			return
		}
		f.token = "sbd_SECRETTOKENVALUE"
		f.deviceID = "device-1"
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"deviceId": f.deviceID, "token": f.token,
			"heartbeatSeconds": 60, "config": f.liveConfig(),
		})
	case scansPath:
		if f.offlineScans > 0 {
			f.offlineScans--
			status := f.failStatus
			if status == 0 {
				status = http.StatusServiceUnavailable
			}
			if f.retryAfter != "" {
				w.Header().Set("Retry-After", f.retryAfter)
			}
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"code":"busy"}`))
			return
		}
		var batch batchRequest
		if err := json.NewDecoder(r.Body).Decode(&batch); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		f.batches = append(f.batches, batch)
		results := make([]batchResult, 0, len(batch.Scans))
		for _, scan := range batch.Scans {
			status := "created"
			if f.blankResults {
				status = ""
			} else if f.scanStatus != nil && f.scanStatus[scan.ID] != "" {
				status = f.scanStatus[scan.ID]
			}
			results = append(results, batchResult{ID: scan.ID, Status: status})
		}
		_ = json.NewEncoder(w).Encode(batchResponse{Results: results, Config: f.liveConfig()})
	case heartbeatPath:
		f.auths = append(f.auths, r.Header.Get("Authorization"))
		if f.failBeats > 0 {
			f.failBeats--
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`{"code":"busy"}`))
			return
		}
		if f.badJSON {
			_, _ = w.Write([]byte(`{`))
			return
		}
		var beat beatRequest
		_ = json.NewDecoder(r.Body).Decode(&beat)
		f.beats = append(f.beats, beat)
		_ = json.NewEncoder(w).Encode(beatResponse{
			ServerTime: "2026-10-24T12:00:00Z", Config: f.liveConfig(),
			HeartbeatSeconds: 60, RotateToken: f.rotate, Update: f.update,
		})
	case rotatePath:
		if f.failRotate {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		f.nextToken = "sbd_ROTATEDTOKENVALUE"
		_ = json.NewEncoder(w).Encode(map[string]string{"token": f.nextToken})
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (f *fakeAPI) liveConfig() HubConfig {
	cfg := f.config
	if cfg.ControlBarcodes.StockIn == "" {
		cfg.ControlBarcodes.StockIn = "STOCK_IN"
	}
	if cfg.ControlBarcodes.StockOut == "" {
		cfg.ControlBarcodes.StockOut = "STOCK_OUT"
	}
	if cfg.IdleSeconds == 0 {
		cfg.IdleSeconds = 300
	}
	return cfg
}

func (f *fakeAPI) lastPair() map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pairBody
}

func newTestHub(t *testing.T, api *fakeAPI, tweak func(*Options)) (*Hub, *bytes.Buffer) {
	t.Helper()
	srv := httptest.NewServer(api)
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	logs := &bytes.Buffer{}
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	opts := Options{
		ServerURL:   srv.URL,
		StatePath:   filepath.Join(dir, "state.json"),
		BufferPath:  filepath.Join(dir, "buffer.sqlite"),
		ReleaseRoot: filepath.Join(dir, "opt"),
		Name:        "Counter",
		Version:     "1.0.0",
		BootID:      "boot-1",
		Now:         func() time.Time { return now },
		Mono:        func() int64 { return 1000 },
		Sleep:       func(context.Context, time.Duration) error { return nil },
		Rand:        func() float64 { return 1 },
		Log:         slog.New(slog.NewTextHandler(logs, nil)),
		HTTP:        srv.Client(),
	}
	if tweak != nil {
		tweak(&opts)
	}
	h, err := Open(opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	return h, logs
}
