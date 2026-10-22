package server

import (
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/app"
)

// briefDatabaseLock is how long the outside connection keeps the write lock.
// The request has to still be inside SQLite for this long, which is the
// opposite of the instant SQLITE_BUSY a zero busy timeout returns.
const briefDatabaseLock = 200 * time.Millisecond

type lockedHTTPResult struct {
	code int
	body string
}

func TestScanCreateWaitsOutBriefDatabaseLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pantry.db")
	handler, env := setupTestOnDSN(t, path, nil)
	res := callWhileLocked(t, env.DB, path, "BEGIN IMMEDIATE", func() lockedHTTPResult {
		return serveJSON(handler, http.MethodPost, "/api/scans", `{"barcode":"https://example.test/pantry","userId":"user-1","direction":"stock_in"}`)
	})
	if res.code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", res.code, res.body)
	}
	listed := serveJSON(handler, http.MethodGet, "/api/scans?userId=user-1", "")
	if listed.code != http.StatusOK || !strings.Contains(listed.body, "example.test") {
		t.Fatalf("scan list = %d %s", listed.code, listed.body)
	}
}

func TestStockInWaitsOutBriefDatabaseLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pantry.db")
	handler, env := setupTestOnDSN(t, path, nil)
	createItemViaStockIn(t, env.DB, env.ProductStore, "user-1", "prod-busy", "Tamsel Crackers", "item-busy")
	res := callWhileLocked(t, env.DB, path, "BEGIN IMMEDIATE", func() lockedHTTPResult {
		return serveJSON(handler, http.MethodPost, "/api/inventory/item-busy/instances", `{}`)
	})
	if res.code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", res.code, res.body)
	}
	if !strings.Contains(res.body, "item-busy") {
		t.Fatalf("body = %s", res.body)
	}
}

func TestInventoryReadWaitsOutBriefDatabaseLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pantry.db")
	handler, env := setupTestOnDSN(t, path, nil)
	// A reserved lock still allows readers. Exclusive is the lock a writer
	// holds while it commits, which is when inventory reads returned 500.
	res := callWhileLocked(t, env.DB, path, "BEGIN EXCLUSIVE", func() lockedHTTPResult {
		return serveJSON(handler, http.MethodGet, "/api/inventory", "")
	})
	if res.code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", res.code, res.body)
	}
}

func TestDatabaseLockLongerThanTimeoutIsUnavailable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pantry.db")
	// The DSN timeout is the test config. The default wait is five seconds.
	handler, env := setupTestOnDSN(t, path+"?_pragma=busy_timeout(100)", nil)
	release := holdSQLiteLock(t, path, "BEGIN IMMEDIATE")
	defer release()

	done := make(chan lockedHTTPResult, 1)
	go func() {
		done <- serveJSON(handler, http.MethodPost, "/api/scans", `{"barcode":"https://example.test/held","userId":"user-1","direction":"stock_in"}`)
	}()
	res := awaitLockedResult(t, env.DB, done, 50*time.Millisecond)
	release()
	if res.code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, body = %s", res.code, res.body)
	}
	if !strings.Contains(res.body, busyClientMessage) {
		t.Fatalf("body = %s", res.body)
	}
	if strings.Contains(res.body, "database is locked") || strings.Contains(strings.ToLower(res.body), "sqlite_busy") {
		t.Fatalf("body leaked driver text: %s", res.body)
	}
	listed := serveJSON(handler, http.MethodGet, "/api/scans?userId=user-1", "")
	if listed.code != http.StatusOK || strings.Contains(listed.body, "example.test") {
		t.Fatalf("scan was stored or list failed: %d %s", listed.code, listed.body)
	}
}

func TestLockedDatabaseMessageIsRetryable(t *testing.T) {
	wrapped := HandleJSON(func(Request[struct{}, struct{}]) (struct{}, error) {
		return struct{}{}, InternalError(errors.New(`GetScanEntry: database is locked (5) (SQLITE_BUSY)`))
	})
	res := serveJSON(wrapped, http.MethodPost, "/api/scans", `{}`)
	if res.code != http.StatusServiceUnavailable || !strings.Contains(res.body, busyClientMessage) {
		t.Fatalf("wrapped = %d %s", res.code, res.body)
	}
	if strings.Contains(res.body, "SQLITE_BUSY") || strings.Contains(res.body, "GetScanEntry") {
		t.Fatalf("body leaked driver text: %s", res.body)
	}

	plain := HandleJSON(func(Request[struct{}, struct{}]) (struct{}, error) {
		return struct{}{}, errors.New("read inventory: database is locked")
	})
	res = serveJSON(plain, http.MethodGet, "/api/inventory", "")
	if res.code != http.StatusServiceUnavailable || !strings.Contains(res.body, busyClientMessage) {
		t.Fatalf("plain = %d %s", res.code, res.body)
	}

	other := HandleJSON(func(Request[struct{}, struct{}]) (struct{}, error) {
		return struct{}{}, InternalError(errors.New("sql: no such table: products"))
	})
	res = serveJSON(other, http.MethodGet, "/api/inventory", "")
	if res.code != http.StatusInternalServerError || !strings.Contains(res.body, internalClientMessage) {
		t.Fatalf("other = %d %s", res.code, res.body)
	}
}

func serveJSON(handler http.Handler, method, target, body string) lockedHTTPResult {
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, target, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, target, nil)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return lockedHTTPResult{code: rec.Code, body: rec.Body.String()}
}

// holdSQLiteLock takes the write lock on path from a second pool and returns
// the function that drops it. The holder uses a zero busy timeout so acquiring
// the lock does not itself wait.
func holdSQLiteLock(t *testing.T, path, statement string) func() {
	t.Helper()
	holder, err := app.OpenSQLite(path + "?_pragma=busy_timeout(0)")
	if err != nil {
		t.Fatalf("open lock holder: %v", err)
	}
	holder.SetMaxOpenConns(1)
	if _, err := holder.Exec(statement); err != nil {
		holder.Close()
		t.Fatalf("acquire %s: %v", statement, err)
	}
	released := false
	release := func() {
		if released {
			return
		}
		released = true
		_, _ = holder.Exec(`ROLLBACK`)
		holder.Close()
	}
	t.Cleanup(release)
	return release
}

// callWhileLocked runs fn while path is locked, and releases only after fn
// has stayed inside a statement for briefDatabaseLock. CreateScanEntry reads
// before it inserts. A reserved lock does not block that read, so a single
// sample of a connection in use can be the read. The read returns at once;
// the insert does not, and that is the stretch this waits for.
func callWhileLocked(t *testing.T, db *sql.DB, path, statement string, run func() lockedHTTPResult) lockedHTTPResult {
	t.Helper()
	if got := db.Stats().InUse; got != 0 {
		t.Fatalf("connections in use before the request: %d", got)
	}
	release := holdSQLiteLock(t, path, statement)
	done := make(chan lockedHTTPResult, 1)
	started := time.Now()
	go func() {
		done <- run()
	}()
	waitUntilBlocked(t, db, done)
	release()
	select {
	case res := <-done:
		if time.Since(started) < briefDatabaseLock {
			t.Fatalf("request took %s, want at least %s", time.Since(started), briefDatabaseLock)
		}
		return res
	case <-time.After(3 * time.Second):
		t.Fatal("request did not finish after the lock was released")
	}
	return lockedHTTPResult{}
}

func waitUntilBlocked(t *testing.T, db *sql.DB, done <-chan lockedHTTPResult) {
	t.Helper()
	var blockedSince time.Time
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case res := <-done:
			t.Fatalf("request finished while the lock was held: %d %s", res.code, res.body)
		case <-deadline.C:
			t.Fatal("request did not stay blocked on the database lock")
		default:
		}
		if db.Stats().InUse > 0 {
			if blockedSince.IsZero() {
				blockedSince = time.Now()
			}
			if time.Since(blockedSince) >= briefDatabaseLock {
				return
			}
		} else {
			blockedSince = time.Time{}
		}
		runtime.Gosched()
	}
}

// awaitLockedResult waits until the request finishes while the lock is still
// held. Time is accumulated only while a connection is checked out, so the
// read that returns immediately is not the timeout wait. The checkout ends
// before the handler sends the result, so a finished stretch is kept.
func awaitLockedResult(t *testing.T, db *sql.DB, done <-chan lockedHTTPResult, minWait time.Duration) lockedHTTPResult {
	t.Helper()
	var blockedSince time.Time
	var blockedFor time.Duration
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case res := <-done:
			if !blockedSince.IsZero() {
				blockedFor += time.Since(blockedSince)
			}
			if blockedFor < minWait {
				t.Fatalf("lock error returned after %s in the driver, want at least %s: %d %s", blockedFor, minWait, res.code, res.body)
			}
			return res
		case <-deadline.C:
			t.Fatal("busy timeout exceeded the test config")
		default:
		}
		if db.Stats().InUse > 0 {
			if blockedSince.IsZero() {
				blockedSince = time.Now()
			}
		} else if !blockedSince.IsZero() {
			blockedFor += time.Since(blockedSince)
			blockedSince = time.Time{}
		}
		runtime.Gosched()
	}
}
