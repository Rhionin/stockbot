package server

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
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
		{"unstocked", &scan.NoItemError{}, "Nothing on hand to take out.", "none_on_hand"},
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
	if commitStatus(scan.ErrScanNotFound) != http.StatusNotFound {
		t.Fatalf("scan_not_found status = %d, want 404", commitStatus(scan.ErrScanNotFound))
	}
}

func TestCommitFailuresHideDatabaseErrors(t *testing.T) {
	const (
		secret = "sqlite: disk I/O error on scan_entries"
		leakID = "6f1e8c2a-9b34-4d77-8a10-0c5b7e2d91aa"
	)
	dbErr := fmt.Errorf("%s while reading %s", secret, leakID)
	queue := dbErrorQueue{err: dbErr}
	var logs bytes.Buffer
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

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
			logs.Reset()
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
			logged := logs.String()
			if !strings.Contains(logged, secret) || !strings.Contains(logged, leakID) {
				t.Fatalf("server log did not record the database error: %q", logged)
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

func TestUnstockedStockOutIsNothingOnHand(t *testing.T) {
	handler, env := setupTestWithContributor(t, nil)
	mustProduct(env, "prod-new", "New Soup")
	mustProduct(env, "prod-out", "Missing Soup")
	insertPendingScanUnits(env, "scan-in", "prod-new", scan.StockIn, 1)
	insertPendingScanUnits(env, "scan-out", "prod-out", scan.StockOut, 1)

	var logs bytes.Buffer
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	inRec := httptest.NewRecorder()
	handler.ServeHTTP(inRec, httptest.NewRequest(http.MethodPost, "/api/scans/scan-in/commit", nil))
	inBody := inRec.Body.String()
	if inRec.Code != http.StatusOK || !strings.Contains(inBody, `"status":"committed"`) {
		t.Fatalf("stock-in status %d body %s", inRec.Code, inBody)
	}
	if strings.Contains(inBody, "no_item") || strings.Contains(inBody, "Not in your pantry") || strings.Contains(inBody, "Nothing on hand to take out.") {
		t.Fatalf("stock-in body = %s", inBody)
	}

	logs.Reset()
	outRec := httptest.NewRecorder()
	handler.ServeHTTP(outRec, httptest.NewRequest(http.MethodPost, "/api/scans/scan-out/commit", nil))
	outBody := outRec.Body.String()
	if outRec.Code != http.StatusConflict {
		t.Fatalf("stock-out status %d body %s", outRec.Code, outBody)
	}
	if !strings.Contains(outBody, `"error":"Nothing on hand to take out."`) || !strings.Contains(outBody, `"code":"none_on_hand"`) {
		t.Fatalf("stock-out body = %s", outBody)
	}
	if strings.Contains(outBody, "no_item") || strings.Contains(outBody, "Not in your pantry") {
		t.Fatalf("stock-out body = %s", outBody)
	}
	if logged := logs.String(); strings.Contains(logged, "scan commit failed") || strings.Contains(logged, "request failed") {
		t.Fatalf("single unstocked stock-out was logged as a server error:\n%s", logged)
	}

	logs.Reset()
	batchReq := httptest.NewRequest(http.MethodPost, "/api/scans/batch-commit", strings.NewReader(`{"scanEntryIds":["scan-out"],"commit":true}`))
	batchRec := httptest.NewRecorder()
	handler.ServeHTTP(batchRec, batchReq)
	batchBody := batchRec.Body.String()
	if batchRec.Code != http.StatusOK {
		t.Fatalf("batch status %d body %s", batchRec.Code, batchBody)
	}
	if !strings.Contains(batchBody, `"failedReasons":["Nothing on hand to take out."]`) || !strings.Contains(batchBody, `"failedCodes":["none_on_hand"]`) {
		t.Fatalf("batch body = %s", batchBody)
	}
	if strings.Contains(batchBody, "no_item") || strings.Contains(batchBody, "Something went wrong") || strings.Contains(batchBody, "Not in your pantry") {
		t.Fatalf("batch body = %s", batchBody)
	}
	if logged := logs.String(); strings.Contains(logged, "scan commit failed") || strings.Contains(logged, "request failed") {
		t.Fatalf("batch unstocked stock-out was logged as a server error:\n%s", logged)
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
