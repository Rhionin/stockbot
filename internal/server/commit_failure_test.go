package server

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/inventory"
	"github.com/Rhionin/stockbot/internal/scan"
)

func TestKnownCommitFailureMessages(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		message string
		code    string
	}{
		{"none_on_hand", scan.ErrNoneOnHand, "Nothing on hand to take out.", "none_on_hand"},
		{"instance_gone", inventory.ErrInstanceNotFound, "That one was already taken out. Pick another.", "instance_gone"},
		{"not_pending", scan.ErrNotPending, "This scan was already handled.", "not_pending"},
		{"product_unresolved", scan.ErrProductUnresolved, "Pick a product for this scan first.", "product_unresolved"},
		{"scan_not_found", scan.ErrScanNotFound, "This scan is no longer in the queue.", "scan_not_found"},
		{"direction_unset", scan.ErrDirectionUnset, "Choose stock in or stock out first.", "direction_unset"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			message, code, ok := knownCommitFailure(fmt.Errorf("wrapped: %w", tt.err))
			if !ok {
				t.Fatal("expected a known commit failure")
			}
			if message != tt.message || code != tt.code {
				t.Fatalf("got %q %q, want %q %q", message, code, tt.message, tt.code)
			}
		})
	}
}

func TestCommitFailuresHideDatabaseErrors(t *testing.T) {
	const (
		secret = "sqlite: disk I/O error on scan_entries"
		leakID = "6f1e8c2a-9b34-4d77-8a10-0c5b7e2d91aa"
	)
	dbErr := fmt.Errorf("%s while reading %s", secret, leakID)
	queue := dbErrorQueue{err: dbErr}

	checks := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{
			name:   "batch commit",
			method: http.MethodPost,
			path:   "/api/scans/batch-commit",
			body:   `{"scanEntryIds":["scan-db"],"commit":true}`,
		},
		{
			name:   "single commit",
			method: http.MethodPost,
			path:   "/api/scans/scan-db/commit",
		},
		{
			name:   "manual stock-out",
			method: http.MethodPost,
			path:   "/api/inventory/item-db/stock-out",
		},
	}
	for _, tt := range checks {
		t.Run(tt.name, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("POST /api/scans/batch-commit", HandleJSON((&ScanBatchCommitHandler{Queue: queue}).Handle))
			mux.HandleFunc("POST /api/scans/{id}/commit", HandleJSON((&ScanCommitHandler{Queue: queue}).Handle))
			mux.HandleFunc("POST /api/inventory/{itemId}/stock-out", HandleJSON((&InventoryStockOutHandler{Queue: queue}).Handle))
			req := httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body))
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			body, err := io.ReadAll(rec.Body)
			if err != nil {
				t.Fatal(err)
			}
			text := string(body)
			if strings.Contains(text, secret) || strings.Contains(text, leakID) || strings.Contains(text, "sqlite") {
				t.Fatalf("body leaked the database error: %s", text)
			}
			if tt.name == "batch commit" {
				if rec.Code != http.StatusOK {
					t.Fatalf("status %d body %s", rec.Code, text)
				}
				if !strings.Contains(text, `"failedReasons":["Something went wrong. Try again."]`) || !strings.Contains(text, `"failedCodes":["internal"]`) {
					t.Fatalf("body = %s", text)
				}
				return
			}
			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("status %d body %s", rec.Code, text)
			}
			if !strings.Contains(text, `"error":"Something went wrong. Try again."`) || !strings.Contains(text, `"code":"internal"`) {
				t.Fatalf("body = %s", text)
			}
		})
	}
}

type dbErrorQueue struct {
	err error
}

func (q dbErrorQueue) GetScanEntry(context.Context, string) (*scan.ScanEntry, error) {
	return nil, q.err
}
func (q dbErrorQueue) BatchUpdateScanEntries(context.Context, []string, *scan.ScanDirection, *int, *time.Time) error {
	return q.err
}
func (q dbErrorQueue) CommitStockIn(context.Context, *scan.ScanEntry) error { return q.err }
func (q dbErrorQueue) CommitStockOut(context.Context, *scan.ScanEntry, *string) error {
	return q.err
}
func (q dbErrorQueue) CommitStockOutLink(context.Context, *scan.ScanEntry, *string, string) error {
	return q.err
}
func (q dbErrorQueue) StockOut(context.Context, string, time.Time) error { return q.err }
