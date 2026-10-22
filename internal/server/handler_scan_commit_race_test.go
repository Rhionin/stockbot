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

	"github.com/Rhionin/stockbot/internal/scan"
)

// commitGate holds every concurrent commit inside CommitStockOut until each
// one has already observed the scan as pending. Releasing the gate is what
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

	type result struct {
		status int
		body   string
	}
	results := make([]result, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/scans/scan-race/commit", nil)
			mux.ServeHTTP(rec, req)
			results[i] = result{status: rec.Code, body: rec.Body.String()}
		}()
	}

	waitForCommitGate(t, &gate.ready)
	close(gate.start)
	wg.Wait()

	var wins, losses, removed int
	for _, res := range results {
		switch res.status {
		case http.StatusOK:
			wins++
			var body struct {
				RemovedCount int `json:"removedCount"`
			}
			if err := json.Unmarshal([]byte(res.body), &body); err != nil {
				t.Fatalf("success body: %v\n%s", err, res.body)
			}
			removed = body.RemovedCount
		case http.StatusBadRequest:
			losses++
			var body struct {
				Code string `json:"code"`
			}
			if err := json.Unmarshal([]byte(res.body), &body); err != nil {
				t.Fatalf("error body: %v\n%s", err, res.body)
			}
			if body.Code != "not_pending" {
				t.Errorf("loser code %q, body %s", body.Code, res.body)
			}
		default:
			t.Errorf("status %d body %s", res.status, res.body)
		}
	}
	if wins != 1 || losses != n-1 {
		t.Fatalf("wins %d losses %d, want 1 and %d", wins, losses, n-1)
	}
	after := onHand(env, handler)
	if removed < 1 || before-after != float64(removed) {
		t.Fatalf("on hand %v to %v, removedCount %d", before, after, removed)
	}
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

	type result struct {
		status int
		body   string
	}
	results := make([]result, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/scans/batch-commit", strings.NewReader(`{"scanEntryIds":["scan-batch-race"],"commit":true}`))
			mux.ServeHTTP(rec, req)
			results[i] = result{status: rec.Code, body: rec.Body.String()}
		}()
	}

	waitForCommitGate(t, &gate.ready)
	close(gate.start)
	wg.Wait()

	var wins, losses, removed int
	for _, res := range results {
		if res.status != http.StatusOK {
			t.Fatalf("status %d body %s", res.status, res.body)
		}
		var body struct {
			CommittedIDs   []string `json:"committedIds"`
			FailedIDs      []string `json:"failedIds"`
			FailedCodes    []string `json:"failedCodes"`
			StockOutCounts map[string]struct {
				RemovedCount int `json:"removedCount"`
			} `json:"stockOutCounts"`
		}
		if err := json.Unmarshal([]byte(res.body), &body); err != nil {
			t.Fatalf("batch body: %v\n%s", err, res.body)
		}
		switch {
		case len(body.CommittedIDs) == 1 && body.CommittedIDs[0] == "scan-batch-race":
			wins++
			removed = body.StockOutCounts["scan-batch-race"].RemovedCount
			if len(body.FailedIDs) != 0 {
				t.Errorf("winner also failed %s", res.body)
			}
		case len(body.FailedIDs) == 1 && body.FailedIDs[0] == "scan-batch-race":
			losses++
			if len(body.FailedCodes) != 1 || body.FailedCodes[0] != "not_pending" {
				t.Errorf("loser codes %v, body %s", body.FailedCodes, res.body)
			}
			if len(body.CommittedIDs) != 0 {
				t.Errorf("loser also committed %s", res.body)
			}
		default:
			t.Errorf("unexpected batch body %s", res.body)
		}
	}
	if wins != 1 || losses != 1 {
		t.Fatalf("wins %d losses %d, want 1 and 1", wins, losses)
	}
	after := onHand(env, handler)
	if removed < 1 || before-after != float64(removed) {
		t.Fatalf("on hand %v to %v, removedCount %d", before, after, removed)
	}
}
