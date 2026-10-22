package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/inventory"
	"github.com/Rhionin/stockbot/internal/scan"
)

// commitGate holds every concurrent commit inside the stock change until each
// one has already observed the scan as open. Releasing the gate is what
// makes the status claim race, instead of letting one request finish first.
type commitGate struct {
	*scan.Queue
	start chan struct{}
	ready sync.WaitGroup
}

func (g *commitGate) CommitStockOut(ctx context.Context, entry *scan.ScanEntry, instanceID *string) (int, error) {
	g.ready.Done()
	<-g.start
	return g.Queue.CommitStockOut(ctx, entry, instanceID)
}

func (g *commitGate) CommitStockIn(ctx context.Context, entry *scan.ScanEntry) error {
	g.ready.Done()
	<-g.start
	return g.Queue.CommitStockIn(ctx, entry)
}

func (g *commitGate) CommitStockOutLink(ctx context.Context, entry *scan.ScanEntry, instanceID *string, linkProductID string) (int, error) {
	g.ready.Done()
	<-g.start
	return g.Queue.CommitStockOutLink(ctx, entry, instanceID, linkProductID)
}

func waitForCommitGate(t *testing.T, ready *sync.WaitGroup) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		ready.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for concurrent commits to reach the scan")
	}
}

type commitResult struct {
	status int
	body   string
}

func postCommit(mux http.Handler, path, body string) commitResult {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	mux.ServeHTTP(rec, req)
	return commitResult{status: rec.Code, body: rec.Body.String()}
}

// singleOutcome is one single-commit response. A loss is the existing
// not_pending sentence and code, with no removed count of its own.
func singleOutcome(t *testing.T, res commitResult) (won bool, removed int) {
	t.Helper()
	switch res.status {
	case http.StatusOK:
		var body struct {
			RemovedCount int `json:"removedCount"`
		}
		if err := json.Unmarshal([]byte(res.body), &body); err != nil {
			t.Fatalf("success body: %v\n%s", err, res.body)
		}
		return true, body.RemovedCount
	case http.StatusBadRequest:
		var body struct {
			Error string `json:"error"`
			Code  string `json:"code"`
		}
		if err := json.Unmarshal([]byte(res.body), &body); err != nil {
			t.Fatalf("error body: %v\n%s", err, res.body)
		}
		if body.Error != inventory.NotPendingMessage || body.Code != inventory.CodeNotPending {
			t.Fatalf("loser response %s", res.body)
		}
		return false, 0
	default:
		t.Fatalf("status %d body %s", res.status, res.body)
		return false, 0
	}
}

// batchOutcome is one batch response for a single stock-out id. A loss keeps
// that id out of committedIds and reports the existing not_pending sentence.
func batchOutcome(t *testing.T, res commitResult, scanID string) (won bool, removed int) {
	t.Helper()
	if res.status != http.StatusOK {
		t.Fatalf("status %d body %s", res.status, res.body)
	}
	var body struct {
		CommittedIDs   []string `json:"committedIds"`
		FailedIDs      []string `json:"failedIds"`
		FailedReasons  []string `json:"failedReasons"`
		FailedCodes    []string `json:"failedCodes"`
		StockOutCounts map[string]struct {
			RemovedCount int `json:"removedCount"`
		} `json:"stockOutCounts"`
	}
	if err := json.Unmarshal([]byte(res.body), &body); err != nil {
		t.Fatalf("batch body: %v\n%s", err, res.body)
	}
	switch {
	case len(body.CommittedIDs) == 1 && body.CommittedIDs[0] == scanID:
		if len(body.FailedIDs) != 0 {
			t.Errorf("winner also failed %s", res.body)
		}
		return true, body.StockOutCounts[scanID].RemovedCount
	case len(body.FailedIDs) == 1 && body.FailedIDs[0] == scanID:
		if len(body.CommittedIDs) != 0 {
			t.Errorf("loser also committed %s", res.body)
		}
		if len(body.FailedCodes) != 1 || body.FailedCodes[0] != inventory.CodeNotPending ||
			len(body.FailedReasons) != 1 || body.FailedReasons[0] != inventory.NotPendingMessage {
			t.Fatalf("loser response %s", res.body)
		}
		return false, 0
	default:
		t.Fatalf("unexpected batch body %s", res.body)
		return false, 0
	}
}

// assertRemovedOnce checks the shelf dropped by removedCount and that loss
// did not write another stock-out move.
func assertRemovedOnce(t *testing.T, env testEnv, handler http.Handler, itemID string, before float64, removed int) {
	t.Helper()
	after := onHand(env, handler)
	if removed < 1 || before-after != float64(removed) {
		t.Fatalf("on hand %v to %v, removedCount %d", before, after, removed)
	}
	var outs int
	var qty float64
	for _, move := range productHistory(env, handler, itemID).Moves {
		if move.Direction != "out" {
			continue
		}
		outs++
		qty += move.Quantity
	}
	if outs != 1 || qty != float64(removed) {
		t.Fatalf("stock-out moves %d quantity %v, want 1 move of %d", outs, qty, removed)
	}
}

func TestConcurrentStockOutCommitRemovesOnce(t *testing.T) {
	handler, env := setupTestWithDB(t)
	mustProduct(env, "prod-race", "Race Oats")
	soon := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	stockShelf(env, "item-race", "user-1", "prod-race", []shelfUnit{
		{"inst-race-1", &soon},
		{"inst-race-2", &soon},
		{"inst-race-3", &soon},
		{"inst-race-4", &soon},
	})
	insertPendingScanUnits(env, "scan-race", "prod-race", scan.StockOut, 2)
	before := onHand(env, handler)

	const n = 20
	gate := &commitGate{
		Queue: scan.NewQueue(env.DB),
		start: make(chan struct{}),
	}
	gate.ready.Add(n)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/scans/{id}/commit", HandleJSON((&ScanCommitHandler{Queue: gate}).Handle))

	results := make([]commitResult, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = postCommit(mux, "/api/scans/scan-race/commit", "")
		}()
	}

	waitForCommitGate(t, &gate.ready)
	close(gate.start)
	wg.Wait()

	var wins, losses, removed int
	for _, res := range results {
		won, nRemoved := singleOutcome(t, res)
		if won {
			wins++
			removed = nRemoved
			continue
		}
		losses++
	}
	if wins != 1 || losses != n-1 {
		t.Fatalf("wins %d losses %d, want 1 and %d", wins, losses, n-1)
	}
	assertRemovedOnce(t, env, handler, "item-race", before, removed)
}

func TestConcurrentBatchCommitRemovesSharedScanOnce(t *testing.T) {
	handler, env := setupTestWithDB(t)
	mustProduct(env, "prod-batch-race", "Batch Race Oats")
	soon := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	stockShelf(env, "item-batch-race", "user-1", "prod-batch-race", []shelfUnit{
		{"inst-batch-race-1", &soon},
		{"inst-batch-race-2", &soon},
		{"inst-batch-race-3", &soon},
		{"inst-batch-race-4", &soon},
	})
	insertPendingScanUnits(env, "scan-batch-race", "prod-batch-race", scan.StockOut, 2)
	before := onHand(env, handler)

	const n = 2
	gate := &commitGate{
		Queue: scan.NewQueue(env.DB),
		start: make(chan struct{}),
	}
	gate.ready.Add(n)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/scans/batch-commit", HandleJSON((&ScanBatchCommitHandler{Queue: gate}).Handle))

	results := make([]commitResult, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = postCommit(mux, "/api/scans/batch-commit", `{"scanEntryIds":["scan-batch-race"],"commit":true}`)
		}()
	}

	waitForCommitGate(t, &gate.ready)
	close(gate.start)
	wg.Wait()

	var wins, losses, removed int
	for _, res := range results {
		won, nRemoved := batchOutcome(t, res, "scan-batch-race")
		if won {
			wins++
			removed = nRemoved
			continue
		}
		losses++
	}
	if wins != 1 || losses != 1 {
		t.Fatalf("wins %d losses %d, want 1 and 1", wins, losses)
	}
	assertRemovedOnce(t, env, handler, "item-batch-race", before, removed)
}

func TestConcurrentSingleAndBatchCommitRemovesOnce(t *testing.T) {
	handler, env := setupTestWithDB(t)
	mustProduct(env, "prod-mixed-race", "Mixed Race Oats")
	soon := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	stockShelf(env, "item-mixed-race", "user-1", "prod-mixed-race", []shelfUnit{
		{"inst-mixed-race-1", &soon},
		{"inst-mixed-race-2", &soon},
		{"inst-mixed-race-3", &soon},
		{"inst-mixed-race-4", &soon},
	})
	insertPendingScanUnits(env, "scan-mixed-race", "prod-mixed-race", scan.StockOut, 2)
	before := onHand(env, handler)

	gate := &commitGate{
		Queue: scan.NewQueue(env.DB),
		start: make(chan struct{}),
	}
	gate.ready.Add(2)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/scans/{id}/commit", HandleJSON((&ScanCommitHandler{Queue: gate}).Handle))
	mux.HandleFunc("POST /api/scans/batch-commit", HandleJSON((&ScanBatchCommitHandler{Queue: gate}).Handle))

	var single, batch commitResult
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		single = postCommit(mux, "/api/scans/scan-mixed-race/commit", "")
	}()
	go func() {
		defer wg.Done()
		batch = postCommit(mux, "/api/scans/batch-commit", `{"scanEntryIds":["scan-mixed-race"],"commit":true}`)
	}()

	waitForCommitGate(t, &gate.ready)
	close(gate.start)
	wg.Wait()

	singleWon, singleRemoved := singleOutcome(t, single)
	batchWon, batchRemoved := batchOutcome(t, batch, "scan-mixed-race")
	if singleWon == batchWon {
		t.Fatalf("single won %v batch won %v\nsingle %s\nbatch %s", singleWon, batchWon, single.body, batch.body)
	}
	removed := singleRemoved
	if batchWon {
		removed = batchRemoved
	}
	assertRemovedOnce(t, env, handler, "item-mixed-race", before, removed)
}

func TestConcurrentFlaggedStockOutCommitRemovesOnce(t *testing.T) {
	handler, env := setupTestWithDB(t)
	mustProduct(env, "prod-flag-race", "Flagged Race Oats")
	soon := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	stockShelf(env, "item-flag-race", "user-1", "prod-flag-race", []shelfUnit{
		{"inst-flag-race-1", &soon},
		{"inst-flag-race-2", &soon},
	})
	insertPendingScanUnits(env, "scan-flag-race", "prod-flag-race", scan.StockOut, 1)
	if _, err := env.DB.Exec(`UPDATE scan_entries SET status = 'flagged' WHERE id = ?`, "scan-flag-race"); err != nil {
		t.Fatalf("flag scan: %v", err)
	}
	before := onHand(env, handler)

	gate := &commitGate{Queue: scan.NewQueue(env.DB), start: make(chan struct{})}
	gate.ready.Add(2)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/scans/{id}/commit", HandleJSON((&ScanCommitHandler{Queue: gate}).Handle))

	results := make([]commitResult, 2)
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = postCommit(mux, "/api/scans/scan-flag-race/commit", "")
		}()
	}
	waitForCommitGate(t, &gate.ready)
	close(gate.start)
	wg.Wait()

	var wins, losses, removed int
	for _, res := range results {
		won, nRemoved := singleOutcome(t, res)
		if won {
			wins++
			removed = nRemoved
			continue
		}
		losses++
	}
	if wins != 1 || losses != 1 {
		t.Fatalf("wins %d losses %d", wins, losses)
	}
	assertRemovedOnce(t, env, handler, "item-flag-race", before, removed)
}

func TestConcurrentFlaggedBatchCommitRemovesOnce(t *testing.T) {
	handler, env := setupTestWithDB(t)
	mustProduct(env, "prod-flag-batch", "Flagged Batch Oats")
	soon := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	stockShelf(env, "item-flag-batch", "user-1", "prod-flag-batch", []shelfUnit{
		{"inst-flag-batch-1", &soon},
		{"inst-flag-batch-2", &soon},
	})
	insertPendingScanUnits(env, "scan-flag-batch", "prod-flag-batch", scan.StockOut, 1)
	if _, err := env.DB.Exec(`UPDATE scan_entries SET status = 'flagged' WHERE id = ?`, "scan-flag-batch"); err != nil {
		t.Fatalf("flag scan: %v", err)
	}
	before := onHand(env, handler)

	gate := &commitGate{Queue: scan.NewQueue(env.DB), start: make(chan struct{})}
	gate.ready.Add(2)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/scans/batch-commit", HandleJSON((&ScanBatchCommitHandler{Queue: gate}).Handle))

	results := make([]commitResult, 2)
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = postCommit(mux, "/api/scans/batch-commit", `{"scanEntryIds":["scan-flag-batch"],"commit":true}`)
		}()
	}
	waitForCommitGate(t, &gate.ready)
	close(gate.start)
	wg.Wait()

	var wins, losses, removed int
	for _, res := range results {
		won, nRemoved := batchOutcome(t, res, "scan-flag-batch")
		if won {
			wins++
			removed = nRemoved
			continue
		}
		losses++
	}
	if wins != 1 || losses != 1 {
		t.Fatalf("wins %d losses %d", wins, losses)
	}
	assertRemovedOnce(t, env, handler, "item-flag-batch", before, removed)
}

func TestConcurrentStockInCommitAddsOnce(t *testing.T) {
	handler, env := setupTestWithDB(t)
	mustProduct(env, "prod-in-race", "Stock In Race")
	insertPendingScanUnits(env, "scan-in-race", "prod-in-race", scan.StockIn, 2)

	gate := &commitGate{Queue: scan.NewQueue(env.DB), start: make(chan struct{})}
	gate.ready.Add(2)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/scans/{id}/commit", HandleJSON((&ScanCommitHandler{Queue: gate}).Handle))

	results := make([]commitResult, 2)
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = postCommit(mux, "/api/scans/scan-in-race/commit", "")
		}()
	}
	waitForCommitGate(t, &gate.ready)
	close(gate.start)
	wg.Wait()

	var wins, losses int
	for _, res := range results {
		won, _ := singleOutcome(t, res)
		if won {
			wins++
			continue
		}
		losses++
	}
	if wins != 1 || losses != 1 {
		t.Fatalf("wins %d losses %d", wins, losses)
	}
	got := onHand(env, handler)
	if got != 2 {
		t.Fatalf("on hand %v, want 2", got)
	}
}

func TestConcurrentLinkCommitRemovesOnce(t *testing.T) {
	handler, env := setupTestWithDB(t)
	mustProduct(env, "prod-link-race", "Link Race Pears")
	mustProduct(env, "prod-link-scan", "Unknown Pear")
	soon := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	stockShelf(env, "item-link-race", "user-1", "prod-link-race", []shelfUnit{
		{"inst-link-race-1", &soon},
		{"inst-link-race-2", &soon},
	})
	insertPendingScanUnits(env, "scan-link-race", "prod-link-scan", scan.StockOut, 1)
	before := onHand(env, handler)

	gate := &commitGate{Queue: scan.NewQueue(env.DB), start: make(chan struct{})}
	gate.ready.Add(2)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/scans/{id}/commit", HandleJSON((&ScanCommitHandler{Queue: gate}).Handle))

	body := `{"linkProductId":"prod-link-race"}`
	results := make([]commitResult, 2)
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = postCommit(mux, "/api/scans/scan-link-race/commit", body)
		}()
	}
	waitForCommitGate(t, &gate.ready)
	close(gate.start)
	wg.Wait()

	var wins, losses, removed int
	for _, res := range results {
		won, nRemoved := singleOutcome(t, res)
		if won {
			wins++
			removed = nRemoved
			continue
		}
		losses++
	}
	if wins != 1 || losses != 1 || removed != 1 {
		t.Fatalf("wins %d losses %d removed %d", wins, losses, removed)
	}
	assertRemovedOnce(t, env, handler, "item-link-race", before, removed)
}

// TestLosingConcurrentCommitOutcomes pins the two loser responses. Both
// requests inside the claim: the single loser is HTTP 400 not_pending. A
// batch that reads the scan after that commit reports the stored counts.
func TestLosingConcurrentCommitOutcomes(t *testing.T) {
	handler, env := setupTestWithDB(t)
	mustProduct(env, "prod-lose", "Loser Oats")
	soon := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	stockShelf(env, "item-lose", "user-1", "prod-lose", []shelfUnit{
		{"inst-lose-1", &soon},
		{"inst-lose-2", &soon},
	})
	insertPendingScanUnits(env, "scan-lose", "prod-lose", scan.StockOut, 1)
	before := onHand(env, handler)

	gate := &commitGate{Queue: scan.NewQueue(env.DB), start: make(chan struct{})}
	gate.ready.Add(2)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/scans/{id}/commit", HandleJSON((&ScanCommitHandler{Queue: gate}).Handle))
	mux.HandleFunc("POST /api/scans/batch-commit", HandleJSON((&ScanBatchCommitHandler{Queue: scan.NewQueue(env.DB)}).Handle))

	results := make([]commitResult, 2)
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = postCommit(mux, "/api/scans/scan-lose/commit", "")
		}()
	}
	waitForCommitGate(t, &gate.ready)
	close(gate.start)
	wg.Wait()

	var wins, losses, removed int
	for _, res := range results {
		won, nRemoved := singleOutcome(t, res)
		if won {
			wins++
			removed = nRemoved
			if res.status != http.StatusOK {
				t.Fatalf("winner status %d", res.status)
			}
			continue
		}
		losses++
		if res.status != http.StatusBadRequest {
			t.Fatalf("single loser status %d body %s", res.status, res.body)
		}
	}
	if wins != 1 || losses != 1 {
		t.Fatalf("wins %d losses %d", wins, losses)
	}
	assertRemovedOnce(t, env, handler, "item-lose", before, removed)

	again := postCommit(mux, "/api/scans/batch-commit", `{"scanEntryIds":["scan-lose"],"commit":true}`)
	won, got := batchOutcome(t, again, "scan-lose")
	if !won || got != removed {
		t.Fatalf("batch after commit won %v removed %d body %s", won, got, again.body)
	}
	singleAgain := postCommit(mux, "/api/scans/scan-lose/commit", "")
	if won, _ := singleOutcome(t, singleAgain); won || singleAgain.status != http.StatusBadRequest {
		t.Fatalf("single after commit %d %s", singleAgain.status, singleAgain.body)
	}
}
