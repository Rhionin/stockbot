package server

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
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
		{"link_target_gone", scan.ErrLinkTargetGone, "That item isn't available anymore. Pick another.", "link_target_gone"},
		{"unlinked batch", &scan.NoItemError{}, "Choose which item this is first.", "product_unresolved"},
		{"instance_gone", inventory.ErrInstanceNotFound, "That one was already taken out. Pick another.", "instance_gone"},
		{"not_pending", scan.ErrNotPending, "This scan was already handled.", "not_pending"},
		{"product_unresolved", scan.ErrProductUnresolved, "Choose which item this is first.", "product_unresolved"},
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
	if commitStatus(scan.ErrLinkTargetGone) != http.StatusConflict {
		t.Fatalf("link_target_gone status = %d, want 409", commitStatus(scan.ErrLinkTargetGone))
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
	previous := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(previous) })

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

func TestUnlinkedStockOutOffersALinkAndEmptyShelfDoesNot(t *testing.T) {
	handler, env := setupTestWithContributor(t, nil)
	mustProduct(env, "prod-new", "New Soup")
	mustProduct(env, "prod-out", "Missing Soup")
	insertPendingScanUnits(env, "scan-in", "prod-new", scan.StockIn, 1)
	insertPendingScanUnits(env, "scan-out", "prod-out", scan.StockOut, 1)
	seedShelfItem(env, "prod-empty", "Empty Shelf", "item-empty")
	insertPendingScanUnits(env, "scan-empty", "prod-empty", scan.StockOut, 1)

	var logs bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(previous) })

	logs.Reset()
	outRec := httptest.NewRecorder()
	handler.ServeHTTP(outRec, httptest.NewRequest(http.MethodPost, "/api/scans/scan-out/commit", nil))
	outBody := outRec.Body.String()
	if outRec.Code != http.StatusConflict {
		t.Fatalf("stock-out status %d body %s", outRec.Code, outBody)
	}
	if !strings.Contains(outBody, `"error":"Which item is this?"`) || !strings.Contains(outBody, `"code":"no_item"`) {
		t.Fatalf("stock-out body = %s", outBody)
	}
	if strings.Contains(outBody, "suggestions") || strings.Contains(outBody, "Not in your pantry") || strings.Contains(outBody, "Nothing on hand to take out.") || strings.Contains(outBody, "No close match found.") || strings.Contains(outBody, "Choose which item this is first.") {
		t.Fatalf("stock-out body = %s", outBody)
	}
	if logged := logs.String(); strings.Contains(logged, "scan commit failed") || strings.Contains(logged, "request failed") {
		t.Fatalf("single unlinked stock-out was logged as a server error:\n%s", logged)
	}
	assertScanStillPending(t, handler, "scan-out")

	logs.Reset()
	batchReq := httptest.NewRequest(http.MethodPost, "/api/scans/batch-commit", strings.NewReader(`{"scanEntryIds":["scan-out"],"commit":true}`))
	batchRec := httptest.NewRecorder()
	handler.ServeHTTP(batchRec, batchReq)
	batchBody := batchRec.Body.String()
	if batchRec.Code != http.StatusOK {
		t.Fatalf("batch status %d body %s", batchRec.Code, batchBody)
	}
	if !strings.Contains(batchBody, `"failedReasons":["Choose which item this is first."]`) || !strings.Contains(batchBody, `"failedCodes":["product_unresolved"]`) {
		t.Fatalf("batch body = %s", batchBody)
	}
	if strings.Contains(batchBody, "no_item") || strings.Contains(batchBody, "suggestions") || strings.Contains(batchBody, "Which item is this?") || strings.Contains(batchBody, "Something went wrong") || strings.Contains(batchBody, "Not in your pantry") {
		t.Fatalf("batch body = %s", batchBody)
	}
	if logged := logs.String(); strings.Contains(logged, "scan commit failed") || strings.Contains(logged, "request failed") {
		t.Fatalf("batch unlinked stock-out was logged as a server error:\n%s", logged)
	}
	assertScanStillPending(t, handler, "scan-out")

	inRec := httptest.NewRecorder()
	handler.ServeHTTP(inRec, httptest.NewRequest(http.MethodPost, "/api/scans/scan-in/commit", nil))
	inBody := inRec.Body.String()
	if inRec.Code != http.StatusOK || !strings.Contains(inBody, `"status":"committed"`) {
		t.Fatalf("stock-in status %d body %s", inRec.Code, inBody)
	}
	if strings.Contains(inBody, "no_item") || strings.Contains(inBody, "Which item is this?") || strings.Contains(inBody, "Not in your pantry") || strings.Contains(inBody, "Nothing on hand to take out.") {
		t.Fatalf("stock-in body = %s", inBody)
	}

	if _, err := env.DB.Exec(`ALTER TABLE barcodes RENAME TO barcodes_hidden`); err != nil {
		t.Fatalf("hide barcodes: %v", err)
	}

	logs.Reset()
	emptyRec := httptest.NewRecorder()
	handler.ServeHTTP(emptyRec, httptest.NewRequest(http.MethodPost, "/api/scans/scan-empty/commit", nil))
	emptyBody := emptyRec.Body.String()
	if emptyRec.Code != http.StatusConflict {
		t.Fatalf("empty shelf status %d body %s", emptyRec.Code, emptyBody)
	}
	if !strings.Contains(emptyBody, `"error":"Nothing on hand to take out."`) || !strings.Contains(emptyBody, `"code":"none_on_hand"`) {
		t.Fatalf("empty shelf body = %s", emptyBody)
	}
	if strings.Contains(emptyBody, "suggestions") || strings.Contains(emptyBody, "no_item") || strings.Contains(emptyBody, "Which item is this?") || strings.Contains(emptyBody, "list link candidates") || strings.Contains(emptyBody, "no such table") {
		t.Fatalf("empty shelf body = %s", emptyBody)
	}
	if logged := logs.String(); strings.Contains(logged, "scan commit failed") || strings.Contains(logged, "request failed") || strings.Contains(logged, "list link candidates") {
		t.Fatalf("empty shelf stock-out ran the link query:\n%s", logged)
	}

	logs.Reset()
	emptyBatchReq := httptest.NewRequest(http.MethodPost, "/api/scans/batch-commit", strings.NewReader(`{"scanEntryIds":["scan-empty"],"commit":true}`))
	emptyBatchRec := httptest.NewRecorder()
	handler.ServeHTTP(emptyBatchRec, emptyBatchReq)
	emptyBatchBody := emptyBatchRec.Body.String()
	if emptyBatchRec.Code != http.StatusOK {
		t.Fatalf("empty batch status %d body %s", emptyBatchRec.Code, emptyBatchBody)
	}
	if !strings.Contains(emptyBatchBody, `"failedReasons":["Nothing on hand to take out."]`) || !strings.Contains(emptyBatchBody, `"failedCodes":["none_on_hand"]`) {
		t.Fatalf("empty batch body = %s", emptyBatchBody)
	}
	if strings.Contains(emptyBatchBody, "suggestions") || strings.Contains(emptyBatchBody, "no_item") || strings.Contains(emptyBatchBody, "Which item is this?") || strings.Contains(emptyBatchBody, "Something went wrong") {
		t.Fatalf("empty batch body = %s", emptyBatchBody)
	}
	if logged := logs.String(); strings.Contains(logged, "scan commit failed") || strings.Contains(logged, "request failed") || strings.Contains(logged, "list link candidates") {
		t.Fatalf("batch empty shelf ran the link query:\n%s", logged)
	}
	assertScanStillPending(t, handler, "scan-empty")
}

func TestShelvedStockOutSkipsLinkSuggestions(t *testing.T) {
	handler, env := setupTestWithContributor(t, nil)
	seedShelfItem(env, "prod-wiped", "Wiped Soup", "item-wiped")
	if _, err := env.DB.Exec(`UPDATE items SET shelved = 1 WHERE id = 'item-wiped'`); err != nil {
		t.Fatalf("shelve item: %v", err)
	}
	insertPendingScanUnits(env, "scan-wiped", "prod-wiped", scan.StockOut, 1)
	if _, err := env.DB.Exec(`ALTER TABLE barcodes RENAME TO barcodes_hidden`); err != nil {
		t.Fatalf("hide barcodes: %v", err)
	}

	var logs bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(previous) })

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/scans/scan-wiped/commit", nil))
	body := rec.Body.String()
	if rec.Code != http.StatusConflict {
		t.Fatalf("status %d body %s", rec.Code, body)
	}
	if !strings.Contains(body, `"error":"Nothing on hand to take out."`) || !strings.Contains(body, `"code":"none_on_hand"`) {
		t.Fatalf("body = %s", body)
	}
	if strings.Contains(body, "suggestions") || strings.Contains(body, "no_item") || strings.Contains(body, "Which item is this?") || strings.Contains(body, "list link candidates") || strings.Contains(body, "no such table") {
		t.Fatalf("body = %s", body)
	}
	if logged := logs.String(); strings.Contains(logged, "scan commit failed") || strings.Contains(logged, "request failed") || strings.Contains(logged, "list link candidates") {
		t.Fatalf("shelved stock-out ran the link query:\n%s", logged)
	}
	assertScanStillPending(t, handler, "scan-wiped")

	logs.Reset()
	batchRec := httptest.NewRecorder()
	handler.ServeHTTP(batchRec, httptest.NewRequest(http.MethodPost, "/api/scans/batch-commit", strings.NewReader(`{"scanEntryIds":["scan-wiped"],"commit":true}`)))
	batchBody := batchRec.Body.String()
	if batchRec.Code != http.StatusOK {
		t.Fatalf("batch status %d body %s", batchRec.Code, batchBody)
	}
	if !strings.Contains(batchBody, `"failedReasons":["Nothing on hand to take out."]`) || !strings.Contains(batchBody, `"failedCodes":["none_on_hand"]`) {
		t.Fatalf("batch body = %s", batchBody)
	}
	if strings.Contains(batchBody, "suggestions") || strings.Contains(batchBody, "no_item") || strings.Contains(batchBody, "Which item is this?") || strings.Contains(batchBody, "Something went wrong") {
		t.Fatalf("batch body = %s", batchBody)
	}
	if logged := logs.String(); strings.Contains(logged, "scan commit failed") || strings.Contains(logged, "request failed") || strings.Contains(logged, "list link candidates") {
		t.Fatalf("batch shelved stock-out ran the link query:\n%s", logged)
	}
	assertScanStillPending(t, handler, "scan-wiped")
}

func TestBatchLinkTargetGoneIsNotLogged(t *testing.T) {
	productID := "prod-stale"
	direction := scan.StockOut
	queue := linkGoneQueue{entry: &scan.ScanEntry{
		ID: "scan-stale", UserID: "user-1", Status: scan.Pending,
		Direction: &direction, ProductID: &productID, UnitCount: 1,
	}}
	var logs bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(previous) })

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/scans/batch-commit", HandleJSON((&ScanBatchCommitHandler{Queue: queue}).Handle))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/scans/batch-commit", strings.NewReader(`{"scanEntryIds":["scan-stale"],"commit":true}`)))
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, body)
	}
	if !strings.Contains(body, `"failedReasons":["That item isn't available anymore. Pick another."]`) || !strings.Contains(body, `"failedCodes":["link_target_gone"]`) {
		t.Fatalf("body = %s", body)
	}
	if strings.Contains(body, "That product is not in your pantry") || strings.Contains(body, "Something went wrong") || strings.Contains(body, "suggestions") {
		t.Fatalf("body = %s", body)
	}
	if logged := logs.String(); strings.Contains(logged, "scan commit failed") || strings.Contains(logged, "request failed") {
		t.Fatalf("batch link target was logged as a server error:\n%s", logged)
	}
}

type linkGoneQueue struct {
	entry *scan.ScanEntry
}

func (q linkGoneQueue) GetScanEntry(context.Context, string) (*scan.ScanEntry, error) {
	return q.entry, nil
}
func (q linkGoneQueue) BatchUpdateScanEntries(context.Context, []string, *scan.ScanDirection, *int, *time.Time) error {
	return nil
}
func (q linkGoneQueue) CommitStockIn(context.Context, *scan.ScanEntry) error { return nil }
func (q linkGoneQueue) CommitStockOut(context.Context, *scan.ScanEntry, *string) error {
	return scan.ErrLinkTargetGone
}

func assertScanStillPending(t *testing.T, handler http.Handler, id string) {
	t.Helper()
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/scans?userId=user-1&status=pending", nil))
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, `"`+id+`"`) || !strings.Contains(body, `"status":"pending"`) {
		t.Fatalf("pending queue status %d body %s", rec.Code, body)
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
func (q dbErrorQueue) CommitBackfill(context.Context, *scan.ScanEntry) (string, int, error) {
	return "", 0, q.err
}
func (q dbErrorQueue) CountOnHand(context.Context, string, string) (int, error) {
	return 0, q.err
}
func (q dbErrorQueue) StockOut(context.Context, string, time.Time) error { return q.err }
