package push

import (
	"context"
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/Rhionin/stockbot/internal/events"
	"github.com/Rhionin/stockbot/internal/scan"
	"github.com/Rhionin/stockbot/internal/tenant"
)

// scanReader is the queue a settled batch reads to see which scans are still
// waiting. *scan.Queue satisfies it. A nil reader trusts the events already
// seen, which is only for tests that do not stand up a queue.
type scanReader interface {
	GetScanEntry(ctx context.Context, scope tenant.Scope, id string) (*scan.ScanEntry, error)
}

// announcedCap bounds ids remembered for the life of the process. Clearing
// the set can repeat an alert; holding every scan id cannot.
const announcedCap = 4096

type clock interface {
	After(delay time.Duration, fn func()) (cancel func())
	Now() time.Time
}

type realClock struct{}

func (realClock) After(delay time.Duration, fn func()) func() {
	timer := time.AfterFunc(delay, fn)
	return func() { timer.Stop() }
}

func (realClock) Now() time.Time { return time.Now() }

type openBatch struct {
	ids    map[string]struct{}
	order  []string
	names  map[string]string
	due    time.Time
	gen    int
	cancel func()
}

func newBatch() *openBatch {
	return &openBatch{
		ids:   map[string]struct{}{},
		names: map[string]string{},
	}
}

// attention turns scan events into at most one notice per failure, one per
// unrecognized product, and one per in or out session after it goes quiet.
type attention struct {
	mu        sync.Mutex
	emit      func(Notice)
	clock     clock
	settle    time.Duration
	scans     scanReader
	schedules scheduleStore
	scope     tenant.Scope
	announced map[string]struct{}
	batches   map[scan.ScanDirection]*openBatch
}

func newAttention(emit func(Notice), settle time.Duration, clk clock, scans scanReader) *attention {
	if clk == nil {
		clk = realClock{}
	}
	if settle <= 0 {
		settle = BatchSettle
	}
	return &attention{
		emit:      emit,
		clock:     clk,
		settle:    settle,
		scans:     scans,
		announced: map[string]struct{}{},
		batches: map[scan.ScanDirection]*openBatch{
			scan.StockIn:  newBatch(),
			scan.StockOut: newBatch(),
		},
	}
}

func (a *attention) handle(msg events.Message) {
	switch msg.EventType {
	case "scan_processing_failed":
		var failure scan.ProcessingFailure
		if err := json.Unmarshal(msg.Data, &failure); err != nil {
			return
		}
		a.noteFailure(failure)
	case "scan":
		var entry scan.ScanEntry
		if err := json.Unmarshal(msg.Data, &entry); err != nil {
			return
		}
		a.noteScan(entry)
	}
}

func (a *attention) noteFailure(failure scan.ProcessingFailure) {
	a.mu.Lock()
	defer a.mu.Unlock()
	key := "failure:" + failure.ID
	if failure.ID == "" {
		key = "failure:" + failure.Barcode + ":" + failure.Message
	}
	if !a.remember(key) {
		return
	}
	a.emit(failureNotice(failure))
}

func (a *attention) noteScan(entry scan.ScanEntry) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if entry.ID == "" {
		return
	}
	if entry.Status == scan.QR {
		a.drop(entry.ID)
		if !a.remember("qr:" + entry.ID) {
			return
		}
		a.emit(qrNotice(entry))
		return
	}
	if unrecognized(entry) {
		a.drop(entry.ID)
		if !a.remember("unrecognized:" + entry.ID) {
			return
		}
		a.emit(unrecognizedNotice(entry))
		return
	}
	if entry.Status == scan.Committed || entry.Status == scan.Cancelled {
		a.drop(entry.ID)
		return
	}
	direction, ok := batchDirection(entry)
	if !ok {
		return
	}
	a.place(entry.ID, productName(entry), direction)
}

func (a *attention) stop() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.disarm(scan.StockIn)
	a.disarm(scan.StockOut)
}

func (a *attention) remember(key string) bool {
	if _, ok := a.announced[key]; ok {
		return false
	}
	if len(a.announced) >= announcedCap {
		a.announced = map[string]struct{}{}
	}
	a.announced[key] = struct{}{}
	return true
}

func unrecognized(entry scan.ScanEntry) bool {
	if entry.Status == scan.Flagged {
		return true
	}
	return entry.Status == scan.Pending && entry.ProductID == nil
}

func batchDirection(entry scan.ScanEntry) (scan.ScanDirection, bool) {
	if entry.Status != scan.Pending || entry.ProductID == nil || entry.Direction == nil {
		return "", false
	}
	if *entry.Direction != scan.StockIn && *entry.Direction != scan.StockOut {
		return "", false
	}
	return *entry.Direction, true
}

func (a *attention) place(id, name string, direction scan.ScanDirection) {
	other := scan.StockOut
	if direction == scan.StockOut {
		other = scan.StockIn
	}
	if _, ok := a.batches[other].ids[id]; ok {
		a.batches[other].remove(id)
		if len(a.batches[other].ids) == 0 {
			a.disarm(other)
		}
		a.writeSchedule(other)
	}
	a.batches[direction].put(id, name)
	a.arm(direction)
}

func (a *attention) drop(id string) {
	for _, direction := range []scan.ScanDirection{scan.StockIn, scan.StockOut} {
		batch := a.batches[direction]
		if _, ok := batch.ids[id]; !ok {
			continue
		}
		batch.remove(id)
		if len(batch.ids) == 0 {
			a.disarm(direction)
		}
		a.writeSchedule(direction)
	}
}

func (b *openBatch) put(id, name string) {
	if _, ok := b.ids[id]; !ok {
		b.order = append(b.order, id)
	}
	b.ids[id] = struct{}{}
	if name != "" {
		b.names[id] = name
	}
}

func (b *openBatch) remove(id string) {
	delete(b.ids, id)
	delete(b.names, id)
	kept := b.order[:0]
	for _, existing := range b.order {
		if existing != id {
			kept = append(kept, existing)
		}
	}
	b.order = kept
}

// take reports the scans still in this batch, in scan order, then forgets
// them so a later session starts empty.
func (b *openBatch) take() (ids []string, names map[string]string) {
	ids = append([]string(nil), b.order...)
	names = b.names
	b.ids = map[string]struct{}{}
	b.names = map[string]string{}
	b.order = nil
	return ids, names
}

// stillPending keeps the scans from this batch that the queue still has
// waiting. Approved and rejected rows are gone, and so is a row the queue
// no longer has. A nil queue trusts the events already applied to the batch.
func (a *attention) stillPending(direction scan.ScanDirection, ids []string, remembered map[string]string) (int, []string) {
	if a.scans == nil {
		return len(ids), namesInOrder(ids, remembered)
	}
	var names []string
	count := 0
	for _, id := range ids {
		entry, err := a.scans.GetScanEntry(context.Background(), a.scope, id)
		if err != nil {
			log.Printf("scan alerts: could not check whether a scan is still waiting: %v", err)
			count++
			appendName(&names, remembered[id])
			continue
		}
		if entry == nil || !waitingInBatch(*entry, direction) {
			continue
		}
		count++
		name := productName(*entry)
		if name == "" {
			name = remembered[id]
		}
		appendName(&names, name)
	}
	return count, names
}

func waitingInBatch(entry scan.ScanEntry, direction scan.ScanDirection) bool {
	got, ok := batchDirection(entry)
	return ok && got == direction
}

func namesInOrder(ids []string, remembered map[string]string) []string {
	if len(remembered) == 0 {
		return nil
	}
	names := make([]string, 0, len(ids))
	for _, id := range ids {
		appendName(&names, remembered[id])
	}
	return names
}

func appendName(names *[]string, name string) {
	if name == "" {
		return
	}
	*names = append(*names, name)
}

func (a *attention) arm(direction scan.ScanDirection) {
	a.armAfter(direction, a.settle, true)
}

// armAfter starts the quiet-period timer. persist records due as now+delay
// so a restart can wait out the same moment. A re-armed timer passes
// persist false and keeps the due time already stored.
func (a *attention) armAfter(direction scan.ScanDirection, delay time.Duration, persist bool) {
	batch := a.batches[direction]
	a.disarm(direction)
	if persist {
		batch.due = a.clock.Now().Add(delay)
		a.writeSchedule(direction)
	}
	gen := batch.gen
	batch.cancel = a.clock.After(delay, func() {
		a.mu.Lock()
		if batch.gen != gen {
			a.mu.Unlock()
			return
		}
		batch.cancel = nil
		// Claim the row before sending. A crash after the send but before
		// the delete would notify again on the next start.
		if !a.claim(direction) {
			a.mu.Unlock()
			return
		}
		ids, remembered := batch.take()
		a.mu.Unlock()
		count, names := a.stillPending(direction, ids, remembered)
		if count > 0 {
			a.emit(batchNotice(direction, count, names))
		}
	})
}

// rearm waits out batch alerts that were already scheduled when this process
// started. A due time that has passed fires on the next tick. Claiming the
// row when the timer fires keeps a second start from sending it again.
func (a *attention) rearm(ctx context.Context) {
	if a.schedules == nil {
		return
	}
	rows, err := a.schedules.ListBatchAlerts(ctx, a.scope)
	if err != nil {
		log.Printf("scan alerts: could not read pending batch alerts: %v", err)
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, row := range rows {
		batch := a.batches[row.Direction]
		if batch == nil || len(row.ScanIDs) == 0 {
			continue
		}
		for _, id := range row.ScanIDs {
			batch.put(id, "")
		}
		batch.due = row.DueAt
		delay := row.DueAt.Sub(a.clock.Now())
		if delay < 0 {
			delay = 0
		}
		a.armAfter(row.Direction, delay, false)
	}
}

func (a *attention) writeSchedule(direction scan.ScanDirection) {
	if a.schedules == nil {
		return
	}
	batch := a.batches[direction]
	if len(batch.ids) == 0 {
		if err := a.schedules.DeleteBatchAlert(context.Background(), a.scope, direction); err != nil {
			log.Printf("scan alerts: could not clear a batch alert: %v", err)
		}
		return
	}
	if batch.due.IsZero() {
		return
	}
	err := a.schedules.SaveBatchAlert(context.Background(), a.scope, scheduledBatch{
		Direction: direction,
		DueAt:     batch.due,
		ScanIDs:   append([]string(nil), batch.order...),
	})
	if err != nil {
		log.Printf("scan alerts: could not save a batch alert: %v", err)
	}
}

// claim removes the stored alert. False means the row is still there, so
// this process must not send; the next start will.
func (a *attention) claim(direction scan.ScanDirection) bool {
	if a.schedules == nil {
		return true
	}
	if err := a.schedules.DeleteBatchAlert(context.Background(), a.scope, direction); err != nil {
		log.Printf("scan alerts: could not settle a batch alert: %v", err)
		return false
	}
	return true
}

func (a *attention) disarm(direction scan.ScanDirection) {
	batch := a.batches[direction]
	batch.gen++
	if batch.cancel == nil {
		return
	}
	batch.cancel()
	batch.cancel = nil
}
