package server

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/product"
	"github.com/Rhionin/stockbot/internal/scan"
	sqlite "modernc.org/sqlite"
)

func TestScanListReportsOnHand(t *testing.T) {
	handler, env := setupTestWithDB(t)
	finishOpening(env)
	at := time.Date(2026, 5, 11, 8, 0, 0, 0, time.UTC)

	seedShelfItem(env, "prod-zero", "Empty Cream", "item-zero")
	seedScanAt(env, "scan-zero", "prod-zero", scan.StockOut, 1, at)

	seedShelfItem(env, "prod-shelved", "Hidden Cream", "item-shelved")
	mustInsertInstance(env, "shelved-1", "item-shelved", at, nil, "")
	mustInsertInstance(env, "shelved-2", "item-shelved", at, nil, "")
	if _, err := env.DB.Exec(`UPDATE items SET shelved = 1 WHERE id = 'item-shelved'`); err != nil {
		t.Fatal(err)
	}
	seedScanAt(env, "scan-shelved", "prod-shelved", scan.StockOut, 1, at.Add(time.Second))

	seedShelfItem(env, "prod-some", "Full Cream", "item-some")
	mustInsertInstance(env, "some-1", "item-some", at, nil, "")
	mustInsertInstance(env, "some-2", "item-some", at, nil, "")
	removed := at
	mustInsertInstance(env, "some-gone", "item-some", at, &removed, "consumed")
	if _, err := env.DB.Exec(`INSERT INTO items (id, user_id, product_id) VALUES ('item-some-2', 'user-2', 'prod-some')`); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"u2-1", "u2-2", "u2-3", "u2-4"} {
		mustInsertInstance(env, id, "item-some-2", at, nil, "")
	}
	seedScanAt(env, "scan-some", "prod-some", scan.StockOut, 1, at.Add(2*time.Second))

	if err := env.ProductStore.CreateProduct(context.Background(), product.Product{
		ID: "prod-open", Name: "Unstocked Cream", Category: "Pantry", UnitOfMeasure: "unit",
	}); err != nil {
		t.Fatal(err)
	}
	openID := "prod-open"
	seedLooseScan(env, "scan-open", "055550000001", scan.StockOut, 1, &openID, scan.Pending, at.Add(3*time.Second))
	seedLooseScan(env, "scan-unresolved", "066660000001", scan.StockOut, 1, nil, scan.Flagged, at.Add(4*time.Second))

	seedShelfItem(env, "prod-in", "Inbound Cream", "item-in")
	for _, id := range []string{"in-1", "in-2", "in-3"} {
		mustInsertInstance(env, id, "item-in", at, nil, "")
	}
	seedScanAt(env, "scan-in", "prod-in", scan.StockIn, 1, at.Add(5*time.Second))

	seedShelfItem(env, "prod-net", "Net Cream", "item-net")
	seedScanAt(env, "scan-net", "prod-net", scan.StockOut, 2, at.Add(6*time.Second))
	seedScanAt(env, "scan-still", "prod-net", scan.StockOut, 1, at.Add(7*time.Second))
	exchangeJSON(t, handler, http.MethodPost, "/api/scans/scan-net/commit", `{"backfill":true}`, http.StatusOK)
	if n := mustCount(t, env.DB, `SELECT COUNT(*) FROM item_instances WHERE item_id = 'item-net' AND removed_at IS NOT NULL`); n != 2 {
		t.Fatalf("backfill removed instances = %d", n)
	}

	body := exchangeJSON(t, handler, http.MethodGet, "/api/scans?userId=user-1", "", http.StatusOK)
	got := listedOnHand(t, body)
	requireOnHand(t, got, "scan-zero", 0)
	requireOnHand(t, got, "scan-shelved", 0)
	requireOnHand(t, got, "scan-some", 2)
	requireOnHand(t, got, "scan-net", 0)
	requireOnHand(t, got, "scan-still", 0)
	requireNoOnHand(t, got, "scan-open")
	requireNoOnHand(t, got, "scan-unresolved")
	requireNoOnHand(t, got, "scan-in")
	if got["scan-net"].status != "committed" || got["scan-still"].status != "pending" {
		t.Fatalf("backfill pair statuses = %s %s", got["scan-net"].status, got["scan-still"].status)
	}

	assertCode(t, exchangeJSON(t, handler, http.MethodPost, "/api/scans/scan-zero/commit", "", http.StatusConflict),
		"none_on_hand", "Nothing on hand to take out.")
	assertCode(t, exchangeJSON(t, handler, http.MethodPost, "/api/scans/scan-shelved/commit", "", http.StatusConflict),
		"none_on_hand", "Nothing on hand to take out.")
	exchangeJSON(t, handler, http.MethodPost, "/api/scans/scan-some/commit", "", http.StatusOK)
}

type listedScanOnHand struct {
	status string
	onHand *int
}

func listedOnHand(t *testing.T, body []byte) map[string]listedScanOnHand {
	t.Helper()
	var rows []struct {
		ID     string `json:"id"`
		Status string `json:"status"`
		OnHand *int   `json:"onHand"`
	}
	if err := json.Unmarshal(body, &rows); err != nil {
		t.Fatal(err)
	}
	out := make(map[string]listedScanOnHand, len(rows))
	for _, row := range rows {
		out[row.ID] = listedScanOnHand{status: row.Status, onHand: row.OnHand}
	}
	return out
}

func requireOnHand(t *testing.T, got map[string]listedScanOnHand, id string, want int) {
	t.Helper()
	row, ok := got[id]
	if !ok || row.onHand == nil || *row.onHand != want {
		t.Fatalf("%s = %+v, want onHand %d", id, row, want)
	}
}

func requireNoOnHand(t *testing.T, got map[string]listedScanOnHand, id string) {
	t.Helper()
	row, ok := got[id]
	if !ok || row.onHand != nil {
		t.Fatalf("%s = %+v, want onHand omitted", id, row)
	}
}

// On-hand is one statement in the list, however many scans are queued.
// The group hints are the other two, and they do not grow with the queue.
func TestScanListOnHandIsOneQuery(t *testing.T) {
	db := openCountingDB(t)
	handler, env := setupOnDB(t, db, nil)
	finishOpening(env)
	at := time.Date(2026, 5, 12, 9, 0, 0, 0, time.UTC)
	seedShelfItem(env, "prod-q1", "Query Cream", "item-q1")
	seedScanAt(env, "scan-q1", "prod-q1", scan.StockOut, 1, at)

	one := countListQueries(t, handler)
	if n := onHandQueries(one); len(one) != 3 || n != 1 {
		t.Fatalf("queries for one scan = %d (%d read on hand): %v", len(one), n, one)
	}

	for i, name := range []string{"prod-q2", "prod-q3", "prod-q4", "prod-q5"} {
		item := "item-q" + name[len(name)-1:]
		seedShelfItem(env, name, name, item)
		seedScanAt(env, "scan-"+name, name, scan.StockOut, 1, at.Add(time.Duration(i+1)*time.Second))
	}
	many := countListQueries(t, handler)
	if n := onHandQueries(many); len(many) != len(one) || n != 1 {
		t.Fatalf("queries for five scans = %d (%d read on hand): %v", len(many), n, many)
	}
	body := exchangeJSON(t, handler, http.MethodGet, "/api/scans?userId=user-1", "", http.StatusOK)
	var rows []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 5 {
		t.Fatalf("listed %d scans", len(rows))
	}
}

func onHandQueries(stmts []string) int {
	n := 0
	for _, stmt := range stmts {
		if strings.Contains(stmt, "on_hand_counts") {
			n++
		}
	}
	return n
}

func countListQueries(t *testing.T, handler http.Handler) []string {
	t.Helper()
	scanListQueries.reset()
	exchangeJSON(t, handler, http.MethodGet, "/api/scans?userId=user-1", "", http.StatusOK)
	return scanListQueries.snapshot()
}

func openCountingDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite-count", ":memory:?_pragma=busy_timeout(3000)&_txlock=immediate&_timezone=UTC")
	if err != nil {
		t.Fatalf("open SQLite: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	if err := app.RunMigrations(db); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}
	return db
}

type queryLog struct {
	mu    sync.Mutex
	stmts []string
}

func (q *queryLog) add(stmt string) {
	q.mu.Lock()
	q.stmts = append(q.stmts, stmt)
	q.mu.Unlock()
}

func (q *queryLog) reset() {
	q.mu.Lock()
	q.stmts = nil
	q.mu.Unlock()
}

func (q *queryLog) snapshot() []string {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]string, len(q.stmts))
	copy(out, q.stmts)
	return out
}

var scanListQueries queryLog

type countingDriver struct {
	base driver.Driver
}

func init() {
	sql.Register("sqlite-count", &countingDriver{base: &sqlite.Driver{}})
}

func (d *countingDriver) Open(name string) (driver.Conn, error) {
	conn, err := d.base.Open(name)
	if err != nil {
		return nil, err
	}
	return &countingConn{Conn: conn, log: &scanListQueries}, nil
}

type countingConn struct {
	driver.Conn
	log *queryLog
}

func (c *countingConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	c.log.add(query)
	return c.Conn.(driver.QueryerContext).QueryContext(ctx, query, args)
}

func (c *countingConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	c.log.add(query)
	return c.Conn.(driver.ExecerContext).ExecContext(ctx, query, args)
}

func (c *countingConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	return c.Conn.(driver.ConnPrepareContext).PrepareContext(ctx, query)
}

func (c *countingConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	return c.Conn.(driver.ConnBeginTx).BeginTx(ctx, opts)
}

func (c *countingConn) ResetSession(ctx context.Context) error {
	return c.Conn.(driver.SessionResetter).ResetSession(ctx)
}

func (c *countingConn) IsValid() bool {
	return c.Conn.(driver.Validator).IsValid()
}
