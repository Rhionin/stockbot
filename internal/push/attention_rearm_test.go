package push

import (
	"context"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/product"
	"github.com/Rhionin/stockbot/internal/scan"
)

func pendingNamed(id, name string, direction scan.ScanDirection) scan.ScanEntry {
	entry := pending(id, id, direction)
	entry.Product = &product.ProductSummary{Name: name}
	return entry
}

func withStatus(entry scan.ScanEntry, status scan.ScanStatus) scan.ScanEntry {
	entry.Status = status
	return entry
}

// scheduleBatch arms a batch the way a live scan does, then stops the
// process without firing the timer. The stored due time is what a restart sees.
func scheduleBatch(t *testing.T, store *Subscriptions, entries ...scan.ScanEntry) time.Time {
	t.Helper()
	alerts, clock, notices := harness()
	alerts.schedules = store
	alerts.scope = testScope(t, store)
	for _, entry := range entries {
		alerts.noteScan(entry)
	}
	if len(*notices) != 0 {
		t.Fatalf("batch sent before shutdown: %#v", *notices)
	}
	due := clock.Now().Add(alerts.settle)
	alerts.stop()
	return due
}

func restartAlerts(t *testing.T, store *Subscriptions, scans scanReader, elapsed time.Duration) (*attention, *fakeClock, *[]Notice) {
	t.Helper()
	alerts, clock, notices := harnessWith(scans)
	clock.now = elapsed
	alerts.schedules = store
	alerts.scope = testScope(t, store)
	alerts.rearm(context.Background())
	return alerts, clock, notices
}

func savedBatch(t *testing.T, store *Subscriptions) scheduledBatch {
	t.Helper()
	rows, err := store.ListBatchAlerts(context.Background(), testScope(t, store))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("scheduled batches = %#v", rows)
	}
	return rows[0]
}

func TestRearmPendingBatchWaitsForTheStoredDueTime(t *testing.T) {
	store := newTestSubscriptions(t)
	milk := pendingNamed("milk", "Whole Milk", scan.StockIn)
	oats := pendingNamed("oats", "Rolled Oats", scan.StockIn)
	due := scheduleBatch(t, store, milk, oats)

	saved := savedBatch(t, store)
	if !saved.DueAt.Equal(due) {
		t.Fatalf("due = %s, want %s", saved.DueAt, due)
	}
	if len(saved.ScanIDs) != 2 || saved.ScanIDs[0] != "milk" || saved.ScanIDs[1] != "oats" {
		t.Fatalf("scan ids = %#v", saved.ScanIDs)
	}
	if saved.Direction != scan.StockIn {
		t.Fatalf("direction = %s", saved.Direction)
	}

	queue := scriptedScans{byID: map[string]scan.ScanEntry{"milk": milk, "oats": oats}}
	alerts, clock, notices := restartAlerts(t, store, queue, 2*time.Minute)
	alerts.rearm(context.Background())
	clock.advance(2 * time.Minute)
	if len(*notices) != 0 {
		t.Fatalf("fired before the stored due time: %#v", *notices)
	}
	clock.advance(time.Minute)
	if len(*notices) != 1 {
		t.Fatalf("notices = %#v", *notices)
	}
	got := (*notices)[0]
	if got.Count != 2 || len(got.Names) != 2 || got.Names[0] != "Whole Milk" || got.Names[1] != "Rolled Oats" {
		t.Fatalf("names = %#v count %d", got.Names, got.Count)
	}
	if got.Title != "Stock in batch is ready" || got.URL != "/?scanAlert=stock_in" {
		t.Fatalf("notice = %#v", got)
	}
	left, err := store.ListBatchAlerts(context.Background(), testScope(t, store))
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 0 {
		t.Fatalf("sent batch still scheduled: %#v", left)
	}

	again, clock2, againNotices := restartAlerts(t, store, queue, 2*time.Minute)
	clock2.advance(BatchSettle)
	if len(*againNotices) != 0 {
		t.Fatalf("restart sent the batch again: %#v", *againNotices)
	}
	again.stop()
}

func TestRearmKeepsTheExtendedDueTime(t *testing.T) {
	store := newTestSubscriptions(t)
	milk := pendingNamed("milk", "Whole Milk", scan.StockIn)
	bread := pendingNamed("bread", "Rye Bread", scan.StockIn)
	alerts, clock, _ := harness()
	alerts.schedules = store
	alerts.scope = testScope(t, store)
	alerts.noteScan(milk)
	clock.advance(4 * time.Minute)
	alerts.noteScan(bread)
	due := clock.Now().Add(BatchSettle)
	alerts.stop()

	saved := savedBatch(t, store)
	if !saved.DueAt.Equal(due) {
		t.Fatalf("due = %s, want %s", saved.DueAt, due)
	}
	if len(saved.ScanIDs) != 2 || saved.ScanIDs[1] != "bread" {
		t.Fatalf("scan ids = %#v", saved.ScanIDs)
	}

	queue := scriptedScans{byID: map[string]scan.ScanEntry{"milk": milk, "bread": bread}}
	// 5 minutes is when the first scan's quiet period would have ended.
	restarted, restartedClock, notices := restartAlerts(t, store, queue, 5*time.Minute)
	restartedClock.advance(0)
	if len(*notices) != 0 {
		t.Fatalf("fired at the first scan's due time: %#v", *notices)
	}
	restartedClock.advance(3 * time.Minute)
	if len(*notices) != 0 {
		t.Fatalf("fired early: %#v", *notices)
	}
	restartedClock.advance(time.Minute)
	if len(*notices) != 1 || (*notices)[0].Count != 2 {
		t.Fatalf("notices = %#v", *notices)
	}
	restarted.stop()
}

func TestRearmFullyClearedBatchDoesNotPush(t *testing.T) {
	store := newTestSubscriptions(t)
	milk := pendingNamed("milk", "Whole Milk", scan.StockIn)
	oats := pendingNamed("oats", "Rolled Oats", scan.StockIn)
	scheduleBatch(t, store, milk, oats)

	queue := scriptedScans{byID: map[string]scan.ScanEntry{
		"milk": withStatus(milk, scan.Committed),
		"oats": withStatus(oats, scan.Cancelled),
	}}
	alerts, clock, notices := restartAlerts(t, store, queue, 10*time.Minute)
	clock.advance(0)
	if len(*notices) != 0 {
		t.Fatalf("cleared batch notified: %#v", *notices)
	}
	left, err := store.ListBatchAlerts(context.Background(), testScope(t, store))
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 0 {
		t.Fatalf("cleared batch still scheduled: %#v", left)
	}
	alerts.stop()

	again, againClock, againNotices := restartAlerts(t, store, queue, 0)
	againClock.advance(BatchSettle)
	if len(*againNotices) != 0 {
		t.Fatalf("cleared batch notified after another restart: %#v", *againNotices)
	}
	again.stop()
}

func TestConfirmedBatchIsNotRearmed(t *testing.T) {
	store := newTestSubscriptions(t)
	milk := pendingNamed("milk", "Whole Milk", scan.StockOut)
	alerts, clock, notices := harness()
	alerts.schedules = store
	alerts.scope = testScope(t, store)
	alerts.noteScan(milk)
	done := milk
	done.Status = scan.Committed
	alerts.noteScan(done)
	alerts.stop()
	clock.advance(BatchSettle)
	if len(*notices) != 0 {
		t.Fatalf("confirmed batch notified: %#v", *notices)
	}
	left, err := store.ListBatchAlerts(context.Background(), testScope(t, store))
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 0 {
		t.Fatalf("confirmed batch still scheduled: %#v", left)
	}

	queue := scriptedScans{byID: map[string]scan.ScanEntry{"milk": milk}}
	restarted, restartedClock, restartedNotices := restartAlerts(t, store, queue, 0)
	restartedClock.advance(BatchSettle)
	if len(*restartedNotices) != 0 {
		t.Fatalf("confirmed batch notified after restart: %#v", *restartedNotices)
	}
	restarted.stop()
}

func TestRearmPartiallyClearedBatchAlertsOnlyWhatIsLeft(t *testing.T) {
	store := newTestSubscriptions(t)
	milk := pendingNamed("milk", "Whole Milk", scan.StockOut)
	oats := pendingNamed("oats", "Rolled Oats", scan.StockOut)
	bread := pendingNamed("bread", "Rye Bread", scan.StockOut)
	scheduleBatch(t, store, milk, oats, bread)

	queue := scriptedScans{byID: map[string]scan.ScanEntry{
		"milk": milk,
		"oats": withStatus(oats, scan.Committed),
	}}
	alerts, clock, notices := restartAlerts(t, store, queue, time.Minute)
	clock.advance(3 * time.Minute)
	if len(*notices) != 0 {
		t.Fatalf("fired early: %#v", *notices)
	}
	clock.advance(time.Minute)
	if len(*notices) != 1 {
		t.Fatalf("notices = %#v", *notices)
	}
	got := (*notices)[0]
	if got.Count != 1 || len(got.Names) != 1 || got.Names[0] != "Whole Milk" {
		t.Fatalf("names = %#v count %d", got.Names, got.Count)
	}
	if got.Title != "Stock out batch is ready" || got.Body != "1 scan is waiting. Open the scan queue to confirm it." {
		t.Fatalf("notice = %#v", got)
	}
	alerts.stop()
}

func TestRearmThenANewScanDoesNotSendTwice(t *testing.T) {
	store := newTestSubscriptions(t)
	milk := pendingNamed("milk", "Whole Milk", scan.StockIn)
	bread := pendingNamed("bread", "Rye Bread", scan.StockIn)
	scheduleBatch(t, store, milk)

	queue := scriptedScans{byID: map[string]scan.ScanEntry{"milk": milk, "bread": bread}}
	alerts, clock, notices := restartAlerts(t, store, queue, 4*time.Minute)
	alerts.noteScan(bread)
	clock.advance(time.Minute)
	if len(*notices) != 0 {
		t.Fatalf("the original due time still fired: %#v", *notices)
	}
	clock.advance(4 * time.Minute)
	if len(*notices) != 1 || (*notices)[0].Count != 2 {
		t.Fatalf("notices = %#v", *notices)
	}
	if (*notices)[0].Names[0] != "Whole Milk" || (*notices)[0].Names[1] != "Rye Bread" {
		t.Fatalf("names = %#v", (*notices)[0].Names)
	}
	alerts.stop()
}

func TestRearmOverdueBatchSendsPromptly(t *testing.T) {
	store := newTestSubscriptions(t)
	milk := pendingNamed("milk", "Whole Milk", scan.StockIn)
	oats := pendingNamed("oats", "Rolled Oats", scan.StockIn)
	scheduleBatch(t, store, milk, oats)

	queue := scriptedScans{byID: map[string]scan.ScanEntry{"milk": milk, "oats": oats}}
	alerts, clock, notices := restartAlerts(t, store, queue, 10*time.Minute)
	if len(*notices) != 0 {
		t.Fatalf("sent inside rearm: %#v", *notices)
	}
	clock.advance(0)
	if len(*notices) != 1 {
		t.Fatalf("notices = %#v", *notices)
	}
	if (*notices)[0].Count != 2 || (*notices)[0].Title != "Stock in batch is ready" {
		t.Fatalf("notice = %#v", (*notices)[0])
	}
	clock.advance(BatchSettle)
	if len(*notices) != 1 {
		t.Fatalf("overdue batch sent twice: %#v", *notices)
	}
	alerts.stop()
}
