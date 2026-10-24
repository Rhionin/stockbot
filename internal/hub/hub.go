package hub

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Rhionin/stockbot/internal/auth"
	"github.com/Rhionin/stockbot/internal/scan"
	"github.com/google/uuid"
)

const (
	// HeartbeatInterval is how often a paired hub checks in while idle.
	HeartbeatInterval = 60 * time.Second
	// BatchSize is the most scans posted in one flush.
	BatchSize = 50

	defaultName = "Hub"
)

// ErrBadPairingCheck means the code matched the pairing shape and failed
// its check character. It is not a product barcode and it is not sent.
var ErrBadPairingCheck = errors.New("that pairing code doesn't match its check character")

// ErrRevoked means the server rejected the device token. The token is wiped
// and the unsent queue is kept.
var ErrRevoked = errors.New("this hub was disconnected and needs to pair again")

// ErrRestart means a release was staged and this process should exit so the
// supervisor starts the binary at current. The running version does not change.
var ErrRestart = errors.New("the hub is restarting into the staged release")

// Options configures a hub process. Zero values use the production defaults.
type Options struct {
	ServerURL   string
	StatePath   string
	BufferPath  string
	ReleaseRoot string
	Name        string
	Version     string
	BootID      string
	PublicKey   ed25519.PublicKey
	HTTP        *http.Client
	Now         func() time.Time
	Mono        func() int64
	Synced      func(context.Context) (bool, error)
	Sleep       func(context.Context, time.Duration) error
	After       func(time.Duration) <-chan time.Time
	Rand        func() float64
	Log         *slog.Logger
	SelfCheck   func(context.Context, string) error
	// OnRestart is called with the staged binary after it passes its self-check.
	// Production execs that binary. Tests record the path. A nil hook still
	// returns ErrRestart so Run can exit for a supervisor restart.
	OnRestart func(binaryPath string) error
	// Lines is optional input. Run reads it when set; otherwise it reads Stdin.
	Stdin io.Reader
}

// Hub is one paired (or waiting-to-pair) scanner gateway.
type Hub struct {
	mu           sync.Mutex
	opts         Options
	state        State
	buf          *Buffer
	client       *Client
	scanner      *mode
	bootID       string
	prevBoot     string
	prevUptime   int64
	version      string
	started      time.Time
	lastBeat     time.Time
	flushAttempt int
	flushAfter   time.Time
	log          *slog.Logger
	pub          ed25519.PublicKey
	releaseRoot  string
}

// Open loads credentials and the scan buffer.
func Open(opts Options) (*Hub, error) {
	if opts.StatePath == "" || opts.BufferPath == "" {
		return nil, errors.New("the hub needs a credentials file and a scan buffer")
	}
	if opts.Version == "" {
		opts.Version = "s12"
	}
	if opts.Name == "" {
		opts.Name = defaultName
	}
	state, err := loadState(opts.StatePath)
	if err != nil {
		return nil, err
	}
	if state.ServerURL == "" {
		state.ServerURL = strings.TrimRight(opts.ServerURL, "/")
	}
	if opts.ServerURL != "" {
		state.ServerURL = strings.TrimRight(opts.ServerURL, "/")
	}
	if state.Name == "" {
		state.Name = opts.Name
	}
	buf, err := OpenBuffer(opts.BufferPath)
	if err != nil {
		return nil, err
	}
	bootID := opts.BootID
	if bootID == "" {
		id, err := uuid.NewV7()
		if err != nil {
			_ = buf.Close()
			return nil, err
		}
		bootID = id.String()
	}
	prevBoot, prevUptime, err := buf.RotateBoot(bootID)
	if err != nil {
		_ = buf.Close()
		return nil, err
	}
	log := opts.Log
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	h := &Hub{
		opts:        opts,
		state:       state,
		buf:         buf,
		client:      &Client{BaseURL: state.ServerURL, HTTP: opts.HTTP},
		bootID:      bootID,
		prevBoot:    prevBoot,
		prevUptime:  prevUptime,
		version:     opts.Version,
		started:     time.Time{},
		log:         log,
		pub:         opts.PublicKey,
		releaseRoot: opts.ReleaseRoot,
	}
	h.started = h.now()
	h.scanner = newMode(h.now)
	if h.state.BootID != bootID {
		h.state.BootID = bootID
		if err := h.persist(); err != nil {
			_ = buf.Close()
			return nil, err
		}
	}
	return h, nil
}

// Close closes the scan buffer.
func (h *Hub) Close() error {
	return h.buf.Close()
}

func (h *Hub) now() time.Time {
	if h.opts.Now != nil {
		return h.opts.Now()
	}
	return time.Now()
}

func (h *Hub) mono() int64 {
	if h.opts.Mono != nil {
		return h.opts.Mono()
	}
	return NewMono(h.started).Millis(h.now())
}

func (h *Hub) sleep(ctx context.Context, d time.Duration) error {
	if h.opts.Sleep != nil {
		return h.opts.Sleep(ctx, d)
	}
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (h *Hub) persist() error {
	return saveState(h.opts.StatePath, h.state)
}

func (h *Hub) paired() bool {
	return h.state.Token != "" && h.state.DeviceID != ""
}

// Paired reports whether a device token is stored.
func (h *Hub) Paired() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.paired()
}

// DeviceID is the paired device, or empty.
func (h *Hub) DeviceID() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.state.DeviceID
}

// Queued is the number of scans not yet accepted by the server.
func (h *Hub) Queued() (int, error) {
	return h.buf.Depth()
}

// Dropped is the unacknowledged oldest-drop count.
func (h *Hub) Dropped() (int, error) {
	return h.buf.Dropped()
}

// Pair stores a device token for code. A bad check character is rejected
// before any request. The token is flushed to disk before Pair returns.
func (h *Hub) Pair(ctx context.Context, code string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.pair(ctx, code)
}

func (h *Hub) pair(ctx context.Context, code string) error {
	norm, well, ok := auth.NormalizePairingCode(code)
	if !well {
		return errors.New("that is not a pairing code")
	}
	if !ok {
		return ErrBadPairingCheck
	}
	if h.client.BaseURL == "" {
		return errors.New("the hub has no server address")
	}
	name := h.state.Name
	if name == "" {
		name = h.opts.Name
	}
	result, err := h.client.Pair(ctx, norm, name, "pi_hub", h.version, h.bootID)
	if err != nil {
		if revoked(err) {
			return errors.New("the server did not accept that pairing code")
		}
		return err
	}
	if result.Token == "" || result.DeviceID == "" {
		return errors.New("the server did not return hub credentials")
	}
	h.state.Token = result.Token
	h.state.DeviceID = result.DeviceID
	h.state.Name = name
	h.state.ServerURL = h.client.BaseURL
	h.state.BootID = h.bootID
	if err := h.persist(); err != nil {
		h.state.Token = ""
		h.state.DeviceID = ""
		return err
	}
	h.applyConfig(result.Config)
	h.log.Info("hub paired", "device", result.DeviceID)
	return nil
}

// HandleLine accepts one scanner or keyboard line. Pairing codes pair when
// the hub is unpaired and are ignored once it is paired. Control barcodes
// change direction and are not queued.
func (h *Hub) HandleLine(ctx context.Context, line string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.handleLine(ctx, line)
}

func (h *Hub) handleLine(ctx context.Context, line string) error {
	norm, well, ok := auth.NormalizePairingCode(line)
	if well && !ok {
		return ErrBadPairingCheck
	}
	if well && ok {
		if h.paired() {
			return nil
		}
		return h.pair(ctx, norm)
	}
	barcode := scan.TrimASCII(line)
	if barcode == "" {
		return nil
	}
	if d, isControl := h.scanner.control(barcode); isControl {
		h.scanner.set(d)
		return nil
	}
	if !h.paired() {
		return errors.New("pair this hub before scanning products")
	}
	if len(barcode) > 64 || !printable(barcode) {
		return errors.New("that barcode can't be stored")
	}
	direction := h.scanner.direction()
	h.scanner.noteScan()
	synced := h.clockSynced(ctx)
	row := queuedScan{
		ID:           newScanID(),
		Barcode:      barcode,
		Direction:    string(direction),
		UnitCount:    1,
		CapturedMono: h.mono(),
		BootID:       h.bootID,
		ClockSynced:  synced,
	}
	if synced {
		row.ScannedAt = h.now().UTC().Format(time.RFC3339)
	}
	if _, err := h.buf.Enqueue(row); err != nil {
		return errors.New("that scan could not be stored")
	}
	if err := h.buf.Checkpoint(h.mono()); err != nil {
		return err
	}
	return nil
}

func printable(value string) bool {
	for i := 0; i < len(value); i++ {
		if value[i] < 0x21 || value[i] > 0x7e {
			return false
		}
	}
	return true
}

func newScanID() string {
	id, err := uuid.NewV7()
	if err != nil {
		return uuid.NewString()
	}
	return id.String()
}

func (h *Hub) clockSynced(ctx context.Context) bool {
	if h.opts.Synced == nil {
		return false
	}
	ok, err := h.opts.Synced(ctx)
	if err != nil {
		return false
	}
	return ok
}

func (h *Hub) applyConfig(cfg HubConfig) {
	idle := time.Duration(cfg.IdleSeconds) * time.Second
	h.scanner.configure(cfg.ControlBarcodes.StockIn, cfg.ControlBarcodes.StockOut, idle)
}

// FlushAll sends queued scans oldest-first in batches of 50. Network errors,
// 429, and 5xx wait with exponential backoff and jitter. A 401 wipes the token.
// The lock is not held across the request or the wait, so a caller that shares
// the hub can keep buffering. Run does not use this loop; it flushes one batch
// and schedules the next attempt so scan intake stays responsive.
func (h *Hub) FlushAll(ctx context.Context) error {
	attempt := 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := h.flushPass(ctx)
		if err != nil {
			if errors.Is(err, ErrRevoked) {
				return err
			}
			if !retryable(err) {
				return err
			}
			wait := Backoff(attempt, h.opts.Rand, retryAfterOf(err))
			attempt++
			h.log.Info("will retry sending scans", "wait", wait.String())
			if err := h.sleep(ctx, wait); err != nil {
				return err
			}
			continue
		}
		attempt = 0
		if n == 0 {
			return nil
		}
	}
}

// flushPass posts one batch. The device token and the queue snapshot are
// taken under the lock; the HTTP call and any retry wait happen without it.
func (h *Hub) flushPass(ctx context.Context) (int, error) {
	h.mu.Lock()
	if !h.paired() {
		h.mu.Unlock()
		return 0, errors.New("pair this hub before it can send scans")
	}
	rows, err := h.buf.Peek(BatchSize)
	if err != nil {
		h.mu.Unlock()
		return 0, err
	}
	dropped, err := h.buf.Dropped()
	if err != nil {
		h.mu.Unlock()
		return 0, err
	}
	if len(rows) == 0 {
		h.mu.Unlock()
		return 0, nil
	}
	payload := batchRequest{DroppedCount: dropped, Scans: make([]scanPayload, 0, len(rows))}
	for _, row := range rows {
		item := scanPayload{
			ID:        row.ID,
			Barcode:   row.Barcode,
			Direction: row.Direction,
			UnitCount: row.UnitCount,
			AgeMs:     h.age(row),
			BootID:    row.BootID,
		}
		if row.ClockSynced && row.ScannedAt != "" {
			item.ScannedAt = row.ScannedAt
		}
		payload.Scans = append(payload.Scans, item)
	}
	token := h.state.Token
	client := h.client
	h.mu.Unlock()

	resp, err := client.Scans(ctx, token, payload)

	h.mu.Lock()
	defer h.mu.Unlock()
	if err != nil {
		if revoked(err) {
			if wipeErr := h.unpair(); wipeErr != nil {
				return 0, wipeErr
			}
			return 0, ErrRevoked
		}
		return 0, err
	}
	remove := make([]string, 0, len(resp.Results))
	for _, result := range resp.Results {
		if terminalStatus(result.Status) {
			remove = append(remove, result.ID)
		}
	}
	if len(remove) == 0 {
		return 0, errors.New("the server did not accept the scans")
	}
	if err := h.buf.Remove(remove); err != nil {
		return 0, err
	}
	if err := h.buf.AckDropped(dropped); err != nil {
		return 0, err
	}
	h.applyConfig(resp.Config)
	h.touchContact()
	return len(remove), nil
}

func terminalStatus(status string) bool {
	switch status {
	case "created", "accepted", "duplicate", "conflict", "rejected":
		return true
	default:
		return false
	}
}

func (h *Hub) age(row queuedScan) int64 {
	if row.BootID == "" || row.BootID == h.bootID {
		return ageMs(h.mono(), row.CapturedMono)
	}
	if row.BootID == h.prevBoot {
		return restartedAge(h.prevUptime, row.CapturedMono, h.mono())
	}
	return ageMs(h.mono(), row.CapturedMono)
}

func (h *Hub) unpair() error {
	h.state.Token = ""
	h.state.DeviceID = ""
	h.log.Info("hub unpaired")
	return h.persist()
}

func (h *Hub) touchContact() {
	h.lastBeat = h.now()
}

// Beat sends one heartbeat. A successful beat clears the dropped count it
// reported and any rollback flag it reported. An offered update is verified
// before it is staged.
func (h *Hub) Beat(ctx context.Context) error {
	h.mu.Lock()
	if !h.paired() {
		h.mu.Unlock()
		return errors.New("pair this hub before it can check in")
	}
	queued, err := h.buf.Depth()
	if err != nil {
		h.mu.Unlock()
		return err
	}
	dropped, err := h.buf.Dropped()
	if err != nil {
		h.mu.Unlock()
		return err
	}
	uptime := int(h.now().Sub(h.started).Seconds())
	if uptime < 0 {
		uptime = 0
	}
	token := h.state.Token
	req := beatRequest{
		Version:      h.version,
		BootID:       h.bootID,
		UptimeS:      uptime,
		Queued:       queued,
		DroppedCount: dropped,
		ClockSynced:  h.clockSynced(ctx),
		Rollback:     h.state.Rollback,
	}
	client := h.client
	h.mu.Unlock()

	resp, err := client.Heartbeat(ctx, token, req)

	h.mu.Lock()
	if err != nil {
		if revoked(err) {
			if wipeErr := h.unpair(); wipeErr != nil {
				h.mu.Unlock()
				return wipeErr
			}
			h.mu.Unlock()
			return ErrRevoked
		}
		h.mu.Unlock()
		return err
	}
	if err := h.buf.AckDropped(dropped); err != nil {
		h.mu.Unlock()
		return err
	}
	if h.state.Rollback {
		h.state.Rollback = false
		if err := h.persist(); err != nil {
			h.mu.Unlock()
			return err
		}
	}
	h.applyConfig(resp.Config)
	h.touchContact()
	if h.state.Pending != nil && h.version == h.state.Pending.Version && h.now().Before(h.state.Pending.Deadline) {
		h.state.Pending = nil
		if err := h.persist(); err != nil {
			h.mu.Unlock()
			return err
		}
		if h.releaseRoot != "" {
			pruneReleases(h.releaseRoot)
		}
	}
	rotate := resp.RotateToken
	var update *Release
	if resp.Update != nil && resp.Update.Version != "" && resp.Update.Version != h.version {
		offered := *resp.Update
		update = &offered
	}
	h.mu.Unlock()
	if rotate {
		if err := h.rotate(ctx); err != nil {
			return err
		}
	}
	if update != nil {
		if err := h.applyUpdate(ctx, *update); err != nil {
			if errors.Is(err, ErrRestart) {
				return err
			}
			h.log.Info("update not installed", "version", update.Version)
		}
	}
	return nil
}

func (h *Hub) rotate(ctx context.Context) error {
	h.mu.Lock()
	token := h.state.Token
	client := h.client
	h.mu.Unlock()
	next, err := client.Rotate(ctx, token)
	if err != nil {
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	prev := h.state.Token
	h.state.Token = next
	if err := h.persist(); err != nil {
		h.state.Token = prev
		return err
	}
	h.log.Info("device token rotated", "device", h.state.DeviceID)
	return nil
}

// ApplyUpdate downloads, verifies, and stages a release. The watchdog deadline
// is 120 seconds from now. A failed self-check restores the previous binary.
func (h *Hub) ApplyUpdate(ctx context.Context, rel Release) error {
	return h.applyUpdate(ctx, rel)
}

func (h *Hub) applyUpdate(ctx context.Context, rel Release) error {
	h.mu.Lock()
	if !newerRelease(h.version, rel.Version) {
		h.mu.Unlock()
		return errors.New("that update is not newer than this hub")
	}
	if len(h.pub) != ed25519.PublicKeySize {
		h.mu.Unlock()
		return errors.New("updates are not installed because no release key is configured")
	}
	if h.releaseRoot == "" {
		h.mu.Unlock()
		return errors.New("updates are not installed because no release directory is configured")
	}
	pub := h.pub
	root := h.releaseRoot
	client := h.client
	running := h.version
	h.mu.Unlock()

	artifact, err := client.Download(ctx, rel.URL)
	if err != nil {
		return err
	}
	if err := VerifyRelease(pub, rel.Version, artifact, rel.SHA256, rel.Sig); err != nil {
		return err
	}

	h.mu.Lock()
	if !newerRelease(h.version, rel.Version) {
		h.mu.Unlock()
		return errors.New("that update is not newer than this hub")
	}
	binary, previous, err := stageRelease(root, rel.Version, artifact)
	if err != nil {
		h.mu.Unlock()
		return err
	}
	h.state.Pending = &PendingUpdate{
		Version:         rel.Version,
		PreviousVersion: running,
		PreviousPath:    previous,
		Binary:          binary,
		Deadline:        h.now().Add(WatchdogLimit),
	}
	if err := h.persist(); err != nil {
		h.mu.Unlock()
		return err
	}
	check := h.opts.SelfCheck
	if check == nil {
		check = defaultSelfCheck
	}
	hook := h.opts.OnRestart
	h.mu.Unlock()

	if err := check(ctx, binary); err != nil {
		h.mu.Lock()
		rbErr := h.rollback()
		h.mu.Unlock()
		if rbErr != nil {
			return rbErr
		}
		return err
	}
	h.log.Info("staged hub update", "version", rel.Version)
	if hook != nil {
		if err := hook(binary); err != nil {
			return err
		}
	}
	return ErrRestart
}

// EvaluateWatchdog rolls back when the pending release has not heartbeated
// by its deadline. A check before the deadline leaves the new release in place.
func (h *Hub) EvaluateWatchdog(now time.Time) (bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	pending := h.state.Pending
	if pending == nil || now.Before(pending.Deadline) {
		return false, nil
	}
	if err := h.rollback(); err != nil {
		return false, err
	}
	return true, nil
}

func (h *Hub) rollback() error {
	pending := h.state.Pending
	if pending == nil {
		return errors.New("there is no update to roll back")
	}
	if h.releaseRoot != "" {
		if pending.PreviousPath != "" {
			if err := restoreLink(h.releaseRoot, pending.PreviousPath); err != nil {
				return err
			}
		} else if err := os.Remove(filepath.Join(h.releaseRoot, "current")); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	// h.version stays the version this process started with. The restored
	// binary reports its own version after the supervisor starts it.
	h.state.Pending = nil
	h.state.Rollback = true
	h.log.Info("rolled back hub update")
	return h.persist()
}

// PendingVersion is the staged release still inside its watchdog window.
func (h *Hub) PendingVersion() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.state.Pending == nil {
		return ""
	}
	return h.state.Pending.Version
}

// RollbackPending reports whether the next heartbeat should say rollback.
func (h *Hub) RollbackPending() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.state.Rollback
}

// Version is the release the process is running.
func (h *Hub) Version() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.version
}

// Run reads scanner lines and heartbeats every 60 seconds until ctx ends or
// the input closes. A nil input heartbeats until ctx ends. Scans are stored
// before any network attempt. A retryable flush failure schedules the next
// attempt and returns to the loop so later scans, heartbeats, and the update
// watchdog are not stuck behind backoff.
func (h *Hub) Run(ctx context.Context) error {
	lines := make(chan string)
	errc := make(chan error, 1)
	if h.opts.Stdin != nil {
		go readLines(ctx, h.opts.Stdin, lines, errc)
	}
	if _, err := h.EvaluateWatchdog(h.now()); err != nil {
		h.log.Info("update watchdog could not roll back")
	}
	if err := h.beatIfPaired(ctx); err != nil {
		return err
	}
	nextBeat := h.now().Add(HeartbeatInterval)
	for {
		wait := h.wakeIn(nextBeat)
		timer := h.after(wait)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-errc:
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		case line := <-lines:
			if err := h.HandleLine(ctx, line); err != nil {
				h.log.Info("scan not stored", "err", err.Error())
			}
			if h.flushBlocked() {
				continue
			}
			if err := h.pump(ctx); err != nil {
				return err
			}
		case <-timer:
			if h.flushDue(h.now()) {
				if err := h.pump(ctx); err != nil {
					return err
				}
			}
			if !h.now().Before(nextBeat) {
				if err := h.beatIfPaired(ctx); err != nil {
					return err
				}
				nextBeat = h.now().Add(HeartbeatInterval)
			}
			if _, err := h.EvaluateWatchdog(h.now()); err != nil {
				h.log.Info("update watchdog could not roll back")
			}
		}
	}
}

func (h *Hub) beatIfPaired(ctx context.Context) error {
	if !h.Paired() {
		return nil
	}
	err := h.Beat(ctx)
	if errors.Is(err, ErrRestart) {
		return err
	}
	if err != nil && !errors.Is(err, ErrRevoked) {
		h.log.Info("heartbeat did not reach the server")
	}
	return nil
}

func (h *Hub) wakeIn(nextBeat time.Time) time.Duration {
	now := h.now()
	wait := nextBeat.Sub(now)
	h.mu.Lock()
	if !h.flushAfter.IsZero() {
		if flushWait := h.flushAfter.Sub(now); flushWait < wait {
			wait = flushWait
		}
	}
	h.mu.Unlock()
	if wait < 0 {
		return 0
	}
	return wait
}

func (h *Hub) flushBlocked() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return !h.flushAfter.IsZero() && h.now().Before(h.flushAfter)
}

func (h *Hub) flushDue(now time.Time) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return !h.flushAfter.IsZero() && !now.Before(h.flushAfter)
}

// pump sends one batch when a flush is allowed. A retryable failure records
// the next attempt time and does not sleep.
func (h *Hub) pump(ctx context.Context) error {
	if !h.Paired() {
		return nil
	}
	n, err := h.flushPass(ctx)
	h.mu.Lock()
	defer h.mu.Unlock()
	if err == nil {
		h.flushAttempt = 0
		if n > 0 {
			if depth, derr := h.buf.Depth(); derr == nil && depth > 0 {
				h.flushAfter = h.now()
				return nil
			}
		}
		h.flushAfter = time.Time{}
		return nil
	}
	if errors.Is(err, ErrRevoked) {
		return nil
	}
	if !retryable(err) {
		h.log.Info("scans stay queued")
		return nil
	}
	wait := Backoff(h.flushAttempt, h.opts.Rand, retryAfterOf(err))
	h.flushAttempt++
	h.flushAfter = h.now().Add(wait)
	h.log.Info("will retry sending scans", "wait", wait.String())
	return nil
}

func (h *Hub) after(d time.Duration) <-chan time.Time {
	if h.opts.After != nil {
		return h.opts.After(d)
	}
	return time.After(d)
}

func readLines(ctx context.Context, in io.Reader, lines chan<- string, errc chan<- error) {
	scanner := bufio.NewScanner(in)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 64*1024)
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			errc <- ctx.Err()
			return
		case lines <- scanner.Text():
		}
	}
	if err := scanner.Err(); err != nil {
		errc <- err
		return
	}
	errc <- io.EOF
}

// ShouldBeat reports whether a heartbeat is due. The first check is immediate,
// then one is due every HeartbeatInterval.
func (h *Hub) ShouldBeat(now time.Time) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.lastBeat.IsZero() {
		return true
	}
	return !now.Before(h.lastBeat.Add(HeartbeatInterval))
}

// FormatVersion is the version string used when one was not configured.
func FormatVersion(version string) string {
	if version == "" {
		return "s12"
	}
	return version
}

// ServerURL is the address scans are sent to.
func (h *Hub) ServerURL() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.state.ServerURL
}

// Direction is the direction the next product scan would be stamped with.
func (h *Hub) Direction() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return string(h.scanner.direction())
}
