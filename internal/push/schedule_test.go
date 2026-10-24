package push

import (
	"context"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/scan"
)

func TestBatchAlertScheduleReplacesAndDeletes(t *testing.T) {
	store := newTestSubscriptions(t)
	ctx := context.Background()
	scope := testScope(t, store)
	due := time.Date(2026, 6, 1, 12, 5, 0, 0, time.UTC)
	first := scheduledBatch{
		Direction: scan.StockIn,
		DueAt:     due,
		ScanIDs:   []string{"milk", "oats"},
	}
	if err := store.SaveBatchAlert(ctx, scope, first); err != nil {
		t.Fatal(err)
	}
	later := first
	later.DueAt = due.Add(time.Minute)
	later.ScanIDs = []string{"milk"}
	if err := store.SaveBatchAlert(ctx, scope, later); err != nil {
		t.Fatal(err)
	}
	out := scheduledBatch{
		Direction: scan.StockOut,
		DueAt:     due.Add(2 * time.Minute),
		ScanIDs:   []string{"bread"},
	}
	if err := store.SaveBatchAlert(ctx, scope, out); err != nil {
		t.Fatal(err)
	}
	rows, err := store.ListBatchAlerts(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %#v", rows)
	}
	if rows[0].Direction != scan.StockIn || !rows[0].DueAt.Equal(later.DueAt) || len(rows[0].ScanIDs) != 1 || rows[0].ScanIDs[0] != "milk" {
		t.Fatalf("stock in = %#v", rows[0])
	}
	if rows[1].Direction != scan.StockOut || rows[1].ScanIDs[0] != "bread" {
		t.Fatalf("stock out = %#v", rows[1])
	}
	if err := store.DeleteBatchAlert(ctx, scope, scan.StockIn); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteBatchAlert(ctx, scope, scan.StockIn); err != nil {
		t.Fatal(err)
	}
	rows, err = store.ListBatchAlerts(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Direction != scan.StockOut {
		t.Fatalf("after delete = %#v", rows)
	}
}

func TestBatchAlertScheduleRejectsAnIncompleteRow(t *testing.T) {
	store := newTestSubscriptions(t)
	ctx := context.Background()
	scope := testScope(t, store)
	err := store.SaveBatchAlert(ctx, scope, scheduledBatch{Direction: scan.StockIn, ScanIDs: []string{"milk"}})
	if err == nil {
		t.Fatal("accepted a batch with no due time")
	}
	err = store.SaveBatchAlert(ctx, scope, scheduledBatch{Direction: "sideways", DueAt: time.Now(), ScanIDs: []string{"milk"}})
	if err == nil {
		t.Fatal("accepted a batch with no direction")
	}
}

func TestListBatchAlertsSkipsAnUnreadableRow(t *testing.T) {
	store := newTestSubscriptions(t)
	ctx := context.Background()
	scope := testScope(t, store)
	due := time.Date(2026, 6, 1, 12, 5, 0, 0, time.UTC).Format(time.RFC3339Nano)
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO scan_alert_schedule (direction, due_at, scan_ids) VALUES (?, ?, ?)`,
		"stock_out", "not-a-time", `["milk"]`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO scan_alert_schedule (direction, due_at, scan_ids) VALUES (?, ?, ?)`,
		"stock_in", due, `not-json`); err != nil {
		t.Fatal(err)
	}
	rows, err := store.ListBatchAlerts(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("unreadable rows were returned: %#v", rows)
	}
}
