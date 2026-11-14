package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

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

// TestW3T21StatusEnvelopeKeys — T21 nit: status=1 since changed has no stray changed key.
func TestW3T21StatusEnvelopeKeys(t *testing.T) {
	runHandlerTests(t, []handlerTestCase{{
		name: "status=1 envelope shape",
		setup: func(env testEnv) {
			beginUsing(env.T, env.DB, time.Now())
		},
		httpExchange: httpExchange{
			method: http.MethodGet, path: "/api/shopping-list?status=1", expectedStatus: http.StatusOK,
		},
		afterRequest: func(env testEnv) {
			body, _ := ioReadAllRes(env)
			var doc map[string]any
			if err := json.Unmarshal(body, &doc); err != nil {
				env.T.Fatal(err)
			}
			for _, k := range []string{"cursor", "listStale", "items", "offList", "stores", "attentionGroups"} {
				if _, ok := doc[k]; !ok {
					env.T.Fatalf("missing key %q", k)
				}
			}
			if _, bad := doc["changed"]; bad {
				env.T.Fatal("unexpected changed key in full envelope")
			}
		},
	}})
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

// TestW3T28DefaultGETW3Keys — T28: default GET always includes sent, canSendToCart, storeId.
func TestW3T28DefaultGETW3Keys(t *testing.T) {
	handler, env := setupTestWithDB(t)
	createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", "prod-571-28", "W3 28", "item-571-28")
	beginUsing(t, env.DB, time.Now())
	exchangeJSON(t, handler, http.MethodPut, "/api/restock/items/item-571-28", `{"kind":"amount","value":3}`, http.StatusOK)
	exchangeJSON(t, handler, http.MethodPost, "/api/shopping-list/fill", ``, http.StatusOK)
	body := exchangeJSON(t, handler, http.MethodGet, "/api/shopping-list", "", http.StatusOK)
	var items []map[string]any
	if err := json.Unmarshal([]byte(body), &items); err != nil {
		t.Fatal(err)
	}
	if len(items) == 0 {
		t.Fatal("expected at least one row")
	}
	for _, row := range items {
		if _, ok := row["sent"]; !ok {
			t.Fatalf("missing sent: %#v", row)
		}
		if _, ok := row["canSendToCart"]; !ok {
			t.Fatalf("missing canSendToCart: %#v", row)
		}
		if _, ok := row["storeId"]; !ok {
			t.Fatalf("missing storeId: %#v", row)
		}
	}
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

// TestW3T20ListStaleOnDrainTimeout — T20: slow coalescer yields listStale 200.
func TestW3T20ListStaleOnDrainTimeout(t *testing.T) {
	db := setupTestDB(t)
	coalescer := shopping.NewListCoalescer(db, db, emptyW3Plan(db), shopping.NewStore(db))
	coalescer.DrainGate = func() { time.Sleep(300 * time.Millisecond) }
	coalescer.SetDebounce(time.Millisecond)
	handler, env := setupOnDB(t, db, nil, WithListCoalescer(coalescer))
	scope := primaryScope(t, env.DB)
	mustExecTest(t, env.DB, `INSERT INTO shopping_list_items (id, household_id, user_id, item_id, quantity, source, note, created_at)
		VALUES ('line-571-stale', ?, ?, 'item-571-stale', 6, 'auto', '', datetime('now'))`, scope.HouseholdID(), scope.HouseholdID())
	_ = coalescer.Enqueue(context.Background(), scope, shopping.CauseListUpdated, []string{shopping.ItemLineKey("item-571-stale")}, shopping.BurstMeta{})
	time.Sleep(5 * time.Millisecond)
	body := exchangeJSON(t, handler, http.MethodGet, "/api/shopping-list?status=1", "", http.StatusOK)
	if !strings.Contains(string(body), `"listStale":true`) {
		t.Fatalf("expected listStale true, body %s", body)
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