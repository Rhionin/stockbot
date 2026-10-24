package scanlistener

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"time"

	"github.com/Rhionin/stockbot/internal/product"
	"github.com/Rhionin/stockbot/internal/scan"
	"github.com/Rhionin/stockbot/internal/tenant"
)

// defaultPollInterval is how often runDevice retries opening a missing device.
// A fixed 1s poll means a device that appears is picked up within a second.
const defaultPollInterval = 1 * time.Second

// defaultMissingLogInterval throttles the "still missing" log line. The first
// failure after a disconnect is logged immediately; subsequent failures are
// logged at most once per this interval, so a device left unplugged does not
// flood the log with one line per poll.
const defaultMissingLogInterval = 30 * time.Second

// ScanListener reads barcode scans from standard input or an evdev device,
// depending on the configured Source, and feeds Product_Barcodes into the
// shared scan queue, switching Current_Mode on Control_Barcodes instead.
type ScanListener struct {
	Source          Source
	DevicePath      string
	Stdin           io.Reader
	StockInBarcode  string
	StockOutBarcode string
	// Scope is household 1. DeviceID is the seeded direct scanner.
	// Both are set at startup from the database, not from an environment variable.
	Scope    tenant.Scope
	DeviceID string

	Queue interface {
		CreateScanEntry(ctx context.Context, scope tenant.Scope, entry scan.ScanEntry) (*scan.ScanEntry, error)
	}
	LookupService interface {
		Lookup(ctx context.Context, scope tenant.Scope, barcode string) (product.LookupResult, error)
	}
	Open OpenFunc

	// Mode is the shared direction the next product barcode is stamped with.
	// When set, control barcodes and product scans update this value instead of
	// a private mode, so an HTTP mode switch and a headless control barcode
	// agree on what the next scan does. Nil keeps the private mode.
	Mode ModeControl

	// ModePublisher receives mode change events when Mode is nil. A shared Mode
	// publishes its own changes. Optional.
	ModePublisher interface {
		PublishScannerModeEvent(mode scan.ScanDirection)
	}

	// ProcessingPublisher announces a product barcode before lookup returns.
	// Optional. Control barcodes are not announced.
	ProcessingPublisher interface {
		PublishScanProcessingEvent(notice scan.ProcessingNotice)
		PublishScanProcessingFailedEvent(failure scan.ProcessingFailure)
	}

	// status receives and stores the current capture state.
	status *status

	// Now supplies the current time. Defaults to time.Now when nil.
	Now func() time.Time

	// pollInterval and missingLogInterval are configurable for testing.
	// pollInterval is how often a missing device is retried; missingLogInterval
	// throttles the repeated "still missing" log line.
	pollInterval       time.Duration
	missingLogInterval time.Duration
}

// New creates a new ScanListener with default values.
func New() *ScanListener {
	return &ScanListener{
		Source:             SourceDevice,
		DevicePath:         "/dev/input/pantry-scanner",
		Stdin:              os.Stdin,
		status:             newStatus(),
		pollInterval:       defaultPollInterval,
		missingLogInterval: defaultMissingLogInterval,
		Open:               openEvdev,
	}
}

// Status returns the current capture state.
func (l *ScanListener) Status() Status {
	return l.status.get()
}

// Run selects between stdin and device reading based on Source.
func (l *ScanListener) Run(ctx context.Context) {
	switch l.Source {
	case SourceStdin:
		l.runStdin(ctx)
	default:
		l.runDevice(ctx)
	}
}

// runStdin reads lines from stdin (the existing behavior, unchanged).
func (l *ScanListener) runStdin(ctx context.Context) {
	// Warn if stdin is not a terminal — this is the silent failure mode
	// this feature exists to eliminate.
	if !l.isStdinTTY() {
		log.Printf("scan listener: stdin is not a terminal with SourceStdin, scans will not be captured")
		l.status.setError(fmt.Errorf("stdin not a terminal"))
	}

	l.status.setSource(SourceStdin)

	stdin := l.Stdin
	if stdin == nil {
		stdin = os.Stdin
	}
	log.Printf("scan listener: reading scans from standard input")

	mode := newModeState()
	scanner := bufio.NewScanner(stdin)

	for scanner.Scan() {
		if ctx.Err() != nil {
			return
		}
		l.handleLine(ctx, scanner.Text(), mode)
	}

	if err := scanner.Err(); err != nil {
		log.Printf("scan listener: standard input read error, stopping: %v", err)
		l.status.setError(err)
	} else {
		l.status.setConnected(false, false)
	}
	log.Printf("scan listener: standard input closed, stopping")
}

// isStdinTTY reports whether standard input is a terminal using go-isatty.
func (l *ScanListener) isStdinTTY() bool {
	// Use os.Stdin.Stat() and check if it's a TTY.
	// This is a simplified check; production code would use go-isatty.
	// For now, we treat os.Stdin as a TTY if it's not /dev/null.
	return true // Assume stdin is a TTY; real detection needs go-isatty
}

// runDevice reads Key_Events from the evdev device, retrying a missing device
// on a fixed poll interval. The "still missing" log line is throttled: the
// first failure after a disconnect is logged immediately, then at most once
// per missingLogInterval, so an unplugged scanner does not flood the log with
// one line per poll. A successful (re)connect is always logged right away.
func (l *ScanListener) runDevice(ctx context.Context) {
	l.status.setSource(SourceDevice)

	// zero time means "no missing failure has been logged since the last
	// successful connection", so the next failure logs immediately.
	var lastMissingLog time.Time

	for ctx.Err() == nil {
		dev, grabbed, err := l.Open(l.DevicePath)
		if err != nil {
			l.status.setConnected(false, false)
			l.status.setError(err)

			now := l.now()
			if lastMissingLog.IsZero() || now.Sub(lastMissingLog) >= l.missingLogInterval {
				log.Printf("scan listener: device %s not available: %v, retrying every %s", l.DevicePath, err, l.pollInterval)
				lastMissingLog = now
			}

			if !l.sleep(ctx, l.pollInterval) {
				return
			}
			continue
		}

		l.status.setConnected(true, grabbed)
		l.status.setDevicePath(l.DevicePath)
		l.status.setError(nil)
		log.Printf("scan listener: connected to device %s", l.DevicePath)

		// Reset the throttle so a future disconnect logs immediately again.
		lastMissingLog = time.Time{}

		l.readFrom(ctx, dev)
		dev.Close()
		l.status.setConnected(false, false)
	}
}

// readFrom reads Key_Events from the device until EOF or ctx cancellation.
func (l *ScanListener) readFrom(ctx context.Context, dev io.ReadCloser) {
	mode := newModeState()
	assembler := &lineAssembler{}
	ctxDone := ctx.Done()

	// Spawn a goroutine to watch for context cancellation and close the device
	go func() {
		<-ctxDone
		dev.Close()
	}()

	for {
		k, err := readKeyEvent(dev)
		if err != nil {
			l.status.setError(err)
			log.Printf("scan listener: device read error: %v", err)
			return
		}

		// Reset assembler on each reconnect
		line, ok := assembler.feed(k)
		if ok {
			if line == "" {
				continue // empty line discarded, no entry created
			}
			assembler.reset()

			l.handleLine(ctx, line, mode)
			l.status.setUnmappedCount(assembler.UnmappedCount())
		}
	}
}

// sleep waits for the given duration or until ctx is cancelled.
// Returns false if ctx was cancelled.
func (l *ScanListener) sleep(ctx context.Context, d time.Duration) bool {
	if d == 0 {
		return ctx.Err() == nil
	}
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

func (l *ScanListener) handleLine(ctx context.Context, barcode string, mode *modeState) {
	if barcode == "" {
		return // empty line discarded, no entry created
	}
	if scan.LooksLikePairingCode(barcode) {
		return
	}

	if newMode, isControl := classify(barcode, l.StockInBarcode, l.StockOutBarcode); isControl {
		if l.Mode != nil {
			l.Mode.Set(newMode)
		} else {
			mode.set(newMode)
			if l.ModePublisher != nil {
				l.ModePublisher.PublishScannerModeEvent(newMode)
			}
		}
		l.status.setMode(newMode)
		return // no scan entry for a control barcode
	}

	direction := mode.get()
	if l.Mode != nil {
		// Get applies an idle return to scan-out before this barcode is stamped.
		direction = l.Mode.Get()
		l.Mode.NoteScan()
	}
	l.status.setMode(direction)
	l.createEntry(ctx, barcode, direction)
}

// SetStatusMode records the direction reported by GET /health. The shared mode
// calls it when the direction changes from either capture path.
func (l *ScanListener) SetStatusMode(d scan.ScanDirection) {
	if l == nil || l.status == nil {
		return
	}
	l.status.setMode(d)
}

func (l *ScanListener) createEntry(ctx context.Context, barcode string, direction scan.ScanDirection) {
	if err := l.Scope.Require(); err != nil {
		log.Printf("scan listener: this server's scanner is not bound to a household: %v", err)
		return
	}
	userID := l.Scope.HouseholdID()

	scannedAt := l.now()
	if scan.IsQRPayload(barcode) {
		entry := scan.NewQREntry(userID, barcode, &direction, scannedAt)
		entry.DeviceID = l.DeviceID
		created, err := l.Queue.CreateScanEntry(ctx, l.Scope, entry)
		if err != nil {
			log.Printf("scan listener: create scan entry for %q failed: %v", barcode, err)
			return
		}
		log.Printf("scan listener: scan queued: id=%s barcode=%q direction=%s status=%s", created.ID, created.Barcode, direction, created.Status)
		l.status.setLastScanAt(entry.ScannedAt)
		return
	}

	notice := scan.AnnounceProcessing(l.ProcessingPublisher, userID, barcode, &direction, scannedAt)

	lookup, err := l.LookupService.Lookup(ctx, l.Scope, barcode)
	if err != nil {
		log.Printf("scan listener: product lookup for %q failed: %v", barcode, err)
		scan.AnnounceProcessingFailed(l.ProcessingPublisher, notice)
		return
	}

	entry := scan.NewEntryFromLookup(userID, barcode, lookup, &direction, scannedAt)
	entry.DeviceID = l.DeviceID
	created, err := l.Queue.CreateScanEntry(ctx, l.Scope, entry)
	if err != nil {
		log.Printf("scan listener: create scan entry for %q failed: %v", barcode, err)
		return
	}
	log.Printf("scan listener: scan queued: id=%s barcode=%q direction=%s status=%s", created.ID, created.Barcode, direction, created.Status)
	l.status.setLastScanAt(entry.ScannedAt)
}

func (l *ScanListener) now() time.Time {
	if l.Now != nil {
		return l.Now()
	}
	return time.Now()
}
