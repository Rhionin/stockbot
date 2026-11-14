package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/changes"
	"github.com/Rhionin/stockbot/internal/shopping"
	"github.com/Rhionin/stockbot/internal/tenant"
)

// TestW3T25InvalidLineChangeEnumsRejected — T25: invalid effect/cause is rejected.
func TestW3T25InvalidLineChangeEnumsRejected(t *testing.T) {
	runHandlerTests(t, []handlerTestCase{{
		name: "invalid effect rejected",
		setup: func(env testEnv) {
			scope := primaryScope(env.T, env.DB)
			mustExec(env, `INSERT INTO list_line_changes (household_id, line_key, effect, cause, at, trip_seq) VALUES (?, 'item:item-571-a', 'added', 'taken_out', '2026-01-01T00:00:00Z', 0)`, scope.HouseholdID())
		},
		afterRequest: func(env testEnv) {
			scope := primaryScope(env.T, env.DB)
			var before int
			if err := env.DB.QueryRow(`SELECT COUNT(*) FROM list_line_changes WHERE household_id = ?`, scope.HouseholdID()).Scan(&before); err != nil {
				env.T.Fatal(err)
			}
			ch := shopping.LineChange{LineKey: "item:item-571-b", Effect: shopping.Effect("bad_effect"), Cause: shopping.CauseTakenOut, At: time.Now().UTC(), TripSeq: 0}
			if err := ch.Validate(); err == nil {
				env.T.Fatal("expected validation failure")
			}
			var after int
			if err := env.DB.QueryRow(`SELECT COUNT(*) FROM list_line_changes WHERE household_id = ?`, scope.HouseholdID()).Scan(&after); err != nil {
				env.T.Fatal(err)
			}
			if after != before {
				env.T.Fatalf("count changed %d -> %d", before, after)
			}
		},
		httpExchange: httpExchange{method: http.MethodGet, path: "/api/shopping-list", expectedStatus: http.StatusOK},
	}})
}

// TestW3T21StatusEnvelopeKeys — T21: status=1 since changed + unchanged with cache headers.
func TestW3T21StatusEnvelopeKeys(t *testing.T) {
	handler, env := setupTestWithDB(t)
	scope := primaryScope(t, env.DB)
	beginUsing(t, env.DB, time.Now())
	mustW3SeedItem(t, env)
	mustExecTest(t, env.DB, `UPDATE households SET trip_closed_at = '2026-01-15T10:00:00Z', trip_closed_cause = 'new_cart' WHERE id = ?`, scope.HouseholdID())
	cursor, err := changes.Current(context.Background(), env.DB, scope.HouseholdID(), changes.ScopeList)
	if err != nil {
		t.Fatal(err)
	}
	itemID := "item-w3-t21"
	exchangeJSON(t, handler, http.MethodPut, "/api/restock/items/"+itemID, `{"kind":"amount","value":2}`, http.StatusOK)
	defaultChangedPath := "/api/shopping-list?since=" + cursor
	reqDef := httptest.NewRequest(http.MethodGet, defaultChangedPath, nil)
	recDef := httptest.NewRecorder()
	handler.ServeHTTP(recDef, reqDef)
	if recDef.Code != http.StatusOK {
		t.Fatalf("default changed since status %d", recDef.Code)
	}
	var defaultDoc map[string]any
	if err := json.Unmarshal(recDef.Body.Bytes(), &defaultDoc); err != nil {
		t.Fatal(err)
	}
	if defaultDoc["changed"] != true {
		t.Fatalf("expected changed:true %#v", defaultDoc)
	}
	quietCursor, _ := defaultDoc["cursor"].(string)
	if quietCursor == "" {
		t.Fatal("missing cursor on changed response")
	}
	changedPath := "/api/shopping-list?status=1&since=" + cursor
	req := httptest.NewRequest(http.MethodGet, changedPath, nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("changed since status %d %s", rec.Code, rec.Body.String())
	}
	var changedDoc map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &changedDoc); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"cursor", "listStale", "items", "offList", "stores", "attentionGroups", "storeDisabled", "lastTripEndedAt", "lastTripEndedBy"} {
		if _, ok := changedDoc[k]; !ok {
			t.Fatalf("status=1 since missing %q", k)
		}
	}
	if _, has := changedDoc["changed"]; has {
		t.Fatalf("status=1 changed envelope must not include top-level changed %#v", changedDoc["changed"])
	}
	sameReq := httptest.NewRequest(http.MethodGet, "/api/shopping-list?since="+quietCursor, nil)
	sameRec := httptest.NewRecorder()
	handler.ServeHTTP(sameRec, sameReq)
	if sameRec.Code != http.StatusOK {
		t.Fatalf("unchanged since status %d", sameRec.Code)
	}
	var quiet map[string]any
	if err := json.Unmarshal(sameRec.Body.Bytes(), &quiet); err != nil {
		t.Fatal(err)
	}
	if quiet["changed"] != false {
		t.Fatalf("expected changed:false %#v", quiet)
	}
	if sameRec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control %q want no-store", sameRec.Header().Get("Cache-Control"))
	}
	reqFull := httptest.NewRequest(http.MethodGet, "/api/shopping-list?status=1", nil)
	recFull := httptest.NewRecorder()
	handler.ServeHTTP(recFull, reqFull)
	var fullDoc map[string]any
	if err := json.Unmarshal(recFull.Body.Bytes(), &fullDoc); err != nil {
		t.Fatal(err)
	}
	if _, bad := fullDoc["changed"]; bad {
		t.Fatal("full status=1 envelope must not include changed")
	}
	for _, k := range []string{"cursor", "listStale", "items", "offList", "stores", "attentionGroups"} {
		if _, ok := fullDoc[k]; !ok {
			t.Fatalf("missing key %q", k)
		}
	}
}

func mustW3SeedItem(t *testing.T, env testEnv) string {
	t.Helper()
	const itemID = "item-w3-t21"
	createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", "prod-w3-t21", "T21", itemID)
	return itemID
}

// TestW3T8GETDoesNotRewriteLineChanges — T8: two GETs do not append list_line_changes.
func TestW3T8GETDoesNotRewriteLineChanges(t *testing.T) {
	handler, env := setupTestWithDB(t)
	scope := primaryScope(t, env.DB)
	mustExecTest(t, env.DB, `INSERT INTO list_line_changes (household_id, line_key, effect, cause, at, trip_seq) VALUES (?, 'item:item-571-marker', 'amount_up', 'taken_out', '2026-01-15T10:00:00Z', 0)`, scope.HouseholdID())
	executeExchange(t, handler, httpExchange{method: http.MethodGet, path: "/api/shopping-list", expectedStatus: http.StatusOK})
	executeExchange(t, handler, httpExchange{method: http.MethodGet, path: "/api/shopping-list", expectedStatus: http.StatusOK})
	var count int
	var at string
	if err := env.DB.QueryRow(`SELECT COUNT(*), MAX(at) FROM list_line_changes WHERE household_id = ?`, scope.HouseholdID()).Scan(&count, &at); err != nil {
		t.Fatal(err)
	}
	if count != 1 || at != "2026-01-15T10:00:00Z" {
		t.Fatalf("GETs mutated changes: count=%d at=%q", count, at)
	}
}

// TestW3T24NeedsAttentionKeyPresent — T24: status=1 rows always include needsAttention.
func TestW3T24NeedsAttentionKeyPresent(t *testing.T) {
	runHandlerTests(t, []handlerTestCase{{
		name: "needsAttention key on status=1 items",
		setup: func(env testEnv) {
			beginUsing(env.T, env.DB, time.Now())
		},
		httpExchange: httpExchange{method: http.MethodGet, path: "/api/shopping-list?status=1", expectedStatus: http.StatusOK},
		afterRequest: func(env testEnv) {
			body, _ := ioReadAllRes(env)
			var doc struct {
				Items   []map[string]any `json:"items"`
				OffList []map[string]any `json:"offList"`
			}
			if err := json.Unmarshal(body, &doc); err != nil {
				env.T.Fatal(err)
			}
			for i, row := range doc.Items {
				if _, ok := row["needsAttention"]; !ok {
					env.T.Fatalf("items[%d] missing needsAttention: %#v", i, row)
				}
			}
			for i, row := range doc.OffList {
				if _, ok := row["needsAttention"]; !ok {
					env.T.Fatalf("offList[%d] missing needsAttention: %#v", i, row)
				}
			}
		},
	}})
}



// TestW3T31ScanCommitEnqueuesCoalescer — T31: commit does not run Plan before HTTP returns.
func TestW3T31ScanCommitEnqueuesCoalescer(t *testing.T) {
	planDuringCommit := false
	db := setupTestDB(t)
	coalescer := shopping.NewListCoalescer(db, db, func(context.Context, tenant.Scope, time.Time) ([]shopping.PlannedLine, []shopping.ShelfMember, error) {
		planDuringCommit = true
		return nil, nil, nil
	}, shopping.NewStore(db))
	coalescer.SetDebounce(time.Hour)
	handler, env := setupOnDB(t, db, nil, WithListCoalescer(coalescer))
	createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", "prod-571-31", "W3 31", "item-571-31")
	if err := env.ProductStore.UpsertBarcodeMapping(context.Background(), "571310000031", "prod-571-31", "global", ""); err != nil {
		t.Fatal(err)
	}
	body := exchangeJSON(t, handler, http.MethodPost, "/api/scans",
		`{"barcode":"571310000031","direction":"stock_out","userId":"user-1"}`, http.StatusCreated)
	var created struct{ ID string `json:"id"` }
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatal(err)
	}
	env.Refresher.Wait()
	exchangeJSON(t, handler, http.MethodPost, "/api/scans/"+created.ID+"/commit", ``, http.StatusOK)
	if planDuringCommit {
		t.Fatal("plan ran during commit HTTP")
	}
	if got := coalescer.EnqueueCallCount.Load(); got < 1 {
		t.Fatalf("enqueue calls = %d, want >=1", got)
	}
}

// TestW3T32GETDoesNotEnqueueCoalescer — T32: read routes do not enqueue.
func TestW3T32GETDoesNotEnqueueCoalescer(t *testing.T) {
	db := setupTestDB(t)
	coalescer := shopping.NewListCoalescer(db, db, emptyW3Plan(db), shopping.NewStore(db))
	handler, _ := setupOnDB(t, db, nil, WithListCoalescer(coalescer))
	before := coalescer.EnqueueCallCount.Load()
	executeExchange(t, handler, httpExchange{method: http.MethodGet, path: "/api/shopping-list", expectedStatus: http.StatusOK})
	executeExchange(t, handler, httpExchange{method: http.MethodGet, path: "/api/shopping-list/summary", expectedStatus: http.StatusOK})
	if got := coalescer.EnqueueCallCount.Load(); got != before {
		t.Fatalf("enqueue count %d -> %d on read-only GETs", before, got)
	}
}

// TestW3T20ListStaleOnDrainTimeout — T20: drain timeout → 200 listStale; summary matches list qty.
func TestW3T20ListStaleOnDrainTimeout(t *testing.T) {
	db := setupTestDB(t)
	coalescer := shopping.NewListCoalescer(db, db, emptyW3Plan(db), shopping.NewStore(db))
	coalescer.DrainGate = func() { time.Sleep(500 * time.Millisecond) }
	coalescer.SetDebounce(time.Millisecond)
	handler, env := setupOnDB(t, db, nil, WithListCoalescer(coalescer))
	scope := primaryScope(t, env.DB)
	createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", "prod-571-stale", "Stale", "item-571-stale")
	mustExecTest(t, env.DB, `INSERT INTO shopping_list_items (id, household_id, user_id, item_id, quantity, source, note, created_at)
		VALUES ('line-571-stale', ?, ?, 'item-571-stale', 6, 'auto', '', datetime('now'))`, scope.HouseholdID(), scope.HouseholdID())
	_ = coalescer.Enqueue(context.Background(), scope, shopping.CauseListUpdated, []string{shopping.ItemLineKey("item-571-stale")}, shopping.BurstMeta{})
	time.Sleep(5 * time.Millisecond)
	var listBody, sumBody []byte
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		listBody = exchangeJSON(t, handler, http.MethodGet, "/api/shopping-list?status=1", "", http.StatusOK)
	}()
	go func() {
		defer wg.Done()
		sumBody = exchangeJSON(t, handler, http.MethodGet, "/api/shopping-list/summary", "", http.StatusOK)
	}()
	wg.Wait()
	if !strings.Contains(string(listBody), `"listStale":true`) {
		t.Fatalf("expected listStale true, body %s", listBody)
	}
	var listDoc struct {
		Items   []map[string]any `json:"items"`
		OffList []map[string]any `json:"offList"`
	}
	if err := json.Unmarshal(listBody, &listDoc); err != nil {
		t.Fatal(err)
	}
	shortFromList := 0
	for _, item := range listDoc.Items {
		q, _ := item["quantity"].(float64)
		if q <= 0 {
			continue
		}
		if na, ok := item["needsAttention"]; ok && na != nil {
			continue
		}
		shortFromList++
	}
	if shortFromList < 1 {
		t.Fatalf("list short count %d", shortFromList)
	}
	offFromList := w3NotOnListCount(listDoc.OffList)
	var sum struct {
		ShortNow   int  `json:"shortNow"`
		NotOnList  int  `json:"notOnList"`
		ListStale  bool `json:"listStale"`
	}
	if err := json.Unmarshal(sumBody, &sum); err != nil {
		t.Fatal(err)
	}
	if !sum.ListStale {
		t.Fatalf("summary listStale false: %s", sumBody)
	}
	if sum.ShortNow != shortFromList {
		t.Fatalf("summary shortNow %d list short count %d", sum.ShortNow, shortFromList)
	}
	if sum.NotOnList != offFromList {
		t.Fatalf("summary notOnList %d list offList %d", sum.NotOnList, offFromList)
	}
}


// TestW3T9ParallelGETNoDeadlock — T9: parallel GETs complete.
func TestW3T9ParallelGETNoDeadlock(t *testing.T) {
	handler, env := setupTestWithDB(t)
	beginUsing(t, env.DB, time.Now())
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			executeExchange(t, handler, httpExchange{method: http.MethodGet, path: "/api/shopping-list", expectedStatus: http.StatusOK})
		}()
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("deadlock on parallel GET")
	}
}

func emptyW3Plan(db *sql.DB) shopping.PlanSnapshot {
	_ = db
	return func(context.Context, tenant.Scope, time.Time) ([]shopping.PlannedLine, []shopping.ShelfMember, error) {
		return nil, nil, nil
	}
}

func mustExecTest(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatal(err)
	}
}

func w3NotOnListCount(off []map[string]any) int {
	n := 0
	for _, row := range off {
		switch row["offListReason"] {
		case "removed", "opening", "taken_off":
			n++
		}
	}
	return n
}