package server

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/cart"
	"github.com/Rhionin/stockbot/internal/cart/carttest"
	"github.com/Rhionin/stockbot/internal/cart/connection"
	"github.com/Rhionin/stockbot/internal/cart/kroger"
	"github.com/Rhionin/stockbot/internal/clientmsg"
	"github.com/Rhionin/stockbot/internal/restock"
	"github.com/Rhionin/stockbot/internal/scan"
	"github.com/getsentry/sentry-go"
	"modernc.org/sqlite"
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
	// The DSN timeout is the test config. The default wait is longer than this.
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
	if res.body != busyBody() {
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

func TestBusy503BodyIsTheClientSentence(t *testing.T) {
	busy := realDriverBusy(t)
	handler := HandleJSON(func(Request[struct{}, struct{}]) (struct{}, error) {
		return struct{}{}, InternalError(busy)
	})
	res := serveJSON(handler, http.MethodPost, "/api/scans", `{}`)
	if res.code != http.StatusServiceUnavailable || res.body != busyBody() {
		t.Fatalf("body = %d %s", res.code, res.body)
	}
	if strings.Contains(res.body, "SQLITE_BUSY") || strings.Contains(res.body, "database is locked") {
		t.Fatalf("body leaked driver text: %s", res.body)
	}
}

func TestRealSQLiteLockErrors(t *testing.T) {
	busy := realDriverBusy(t)
	locked := realDriverLocked(t)
	var busyErr, lockedErr *sqlite.Error
	if !errors.As(busy, &busyErr) || busyErr.Code() != sqlitePrimaryBusy {
		t.Fatalf("busy = %v", busy)
	}
	if !errors.As(locked, &lockedErr) || lockedErr.Code() != sqlitePrimaryLocked {
		t.Fatalf("locked = %v", locked)
	}
	// A missing driver-code check still sees SQLITE_BUSY in the message, and
	// misses SQLITE_LOCKED, whose text is "database table is locked".
	if !sqliteBusy(locked) {
		t.Fatal("SQLITE_LOCKED was not classified")
	}
	lockedHandler := HandleJSON(func(Request[struct{}, struct{}]) (struct{}, error) {
		return struct{}{}, InternalError(locked)
	})
	res := serveJSON(lockedHandler, http.MethodGet, "/api/inventory", "")
	if res.code != http.StatusServiceUnavailable || res.body != busyBody() {
		t.Fatalf("locked = %d %s", res.code, res.body)
	}

	// The driver text is one unwrap down, and that error is not *sqlite.Error.
	// Dropping the unwrap loop leaves this as a generic 500.
	hidden := hiddenCause{inner: fmt.Errorf("%s", busy.Error())}
	if !sqliteBusy(hidden) {
		t.Fatal("unwrapped driver text was not classified")
	}
	hiddenHandler := HandleJSON(func(Request[struct{}, struct{}]) (struct{}, error) {
		return struct{}{}, InternalError(hidden)
	})
	res = serveJSON(hiddenHandler, http.MethodGet, "/api/inventory", "")
	if res.code != http.StatusServiceUnavailable || res.body != busyBody() {
		t.Fatalf("hidden = %d %s", res.code, res.body)
	}

	// A client error that happens to wrap a lock stays a 4xx.
	if got := httpStatusFromError(&HTTPError{Code: http.StatusBadRequest, Err: busy}); got != http.StatusBadRequest {
		t.Fatalf("bad request status = %d", got)
	}
	if got := httpStatusFromError(&HTTPError{Code: http.StatusConflict, Err: locked}); got != http.StatusConflict {
		t.Fatalf("conflict status = %d", got)
	}
	clientErr := HandleJSON(func(Request[struct{}, struct{}]) (struct{}, error) {
		return struct{}{}, &HTTPError{Code: http.StatusConflict, Message: "That group already exists.", Err: busy}
	})
	res = serveJSON(clientErr, http.MethodPost, "/api/groups", `{}`)
	if res.code != http.StatusConflict || strings.Contains(res.body, clientmsg.Busy) {
		t.Fatalf("client error = %d %s", res.code, res.body)
	}

	// SQLITE_LOCKED's text is "database table is locked", which is not the busy
	// sentence. Classification has to use the result code.
	if strings.Contains(strings.ToLower(locked.Error()), "database is locked") || strings.Contains(strings.ToLower(locked.Error()), "sqlite_busy") {
		t.Fatalf("locked text = %q", locked.Error())
	}

	// A WAL snapshot upgrade returns SQLITE_BUSY_SNAPSHOT (517). The low byte
	// is still busy, and the text does not say SQLITE_BUSY, so a check for
	// the primary code alone misses it.
	snap := realDriverBusySnapshot(t)
	var snapErr *sqlite.Error
	if !errors.As(snap, &snapErr) || snapErr.Code()&0xff != sqlitePrimaryBusy || snapErr.Code() == sqlitePrimaryBusy {
		t.Fatalf("extended = %v", snap)
	}
	if strings.Contains(strings.ToLower(snap.Error()), "sqlite_busy") {
		t.Fatalf("snapshot text = %q", snap.Error())
	}
	if !sqliteBusy(snap) {
		t.Fatal("extended SQLITE_BUSY was not classified")
	}

	other := HandleJSON(func(Request[struct{}, struct{}]) (struct{}, error) {
		return struct{}{}, InternalError(errors.New("sql: no such table: products"))
	})
	res = serveJSON(other, http.MethodGet, "/api/inventory", "")
	if res.code != http.StatusInternalServerError || res.body != genericBody() {
		t.Fatalf("other = %d %s", res.code, res.body)
	}
}

func TestNoSpace503KeepsItsSentence(t *testing.T) {
	full := realDriverFull(t)
	var fullErr *sqlite.Error
	if !errors.As(full, &fullErr) || fullErr.Code()&0xff != sqlitePrimaryFull {
		t.Fatalf("full = %v", full)
	}
	if sqliteBusy(full) {
		t.Fatal("a full disk was classified as busy")
	}
	handler := HandleJSON(func(Request[struct{}, struct{}]) (struct{}, error) {
		return struct{}{}, InternalError(full)
	})
	res := serveJSON(handler, http.MethodPost, "/api/scans", `{}`)
	if res.code != http.StatusServiceUnavailable || res.body != noSpaceBody() {
		t.Fatalf("full = %d %s", res.code, res.body)
	}
	if strings.Contains(res.body, clientmsg.Busy) || strings.Contains(res.body, clientmsg.Generic) {
		t.Fatalf("full disk used another sentence: %s", res.body)
	}

	// Naming the code without a driver error does not select the sentence.
	coded := HandleJSON(func(Request[struct{}, struct{}]) (struct{}, error) {
		return struct{}{}, &HTTPError{Code: http.StatusServiceUnavailable, Message: clientmsg.NoSpace, ErrorCode: "no_space"}
	})
	res = serveJSON(coded, http.MethodGet, "/api/restock", "")
	if res.code != http.StatusServiceUnavailable || res.body != genericBody() {
		t.Fatalf("coded = %d %s", res.code, res.body)
	}

	unclassified := HandleJSON(func(Request[struct{}, struct{}]) (struct{}, error) {
		return struct{}{}, &HTTPError{Code: http.StatusServiceUnavailable, Message: "raw database detail"}
	})
	res = serveJSON(unclassified, http.MethodGet, "/api/restock", "")
	if res.code != http.StatusServiceUnavailable || res.body != genericBody() {
		t.Fatalf("unclassified = %d %s", res.code, res.body)
	}
}

func TestRestockLockLongerThanTimeoutIsUnavailable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pantry.db")
	handler, env := setupTestOnDSN(t, path+"?_pragma=busy_timeout(100)", nil)
	release := holdSQLiteLock(t, path, "BEGIN EXCLUSIVE")
	defer release()
	for _, route := range []string{"/api/restock", "/api/restock/categories", "/api/restock/suggestions"} {
		done := make(chan lockedHTTPResult, 1)
		go func(route string) {
			done <- serveJSON(handler, http.MethodGet, route, "")
		}(route)
		res := awaitLockedResult(t, env.DB, done, 50*time.Millisecond)
		if res.code != http.StatusServiceUnavailable || res.body != busyBody() {
			t.Fatalf("%s = %d %s", route, res.code, res.body)
		}
	}
}

func TestSyncSnapshotLockIsUnavailable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pantry.db")
	_, env := setupTestOnDSN(t, path+"?_pragma=busy_timeout(100)", nil)
	handler := &syncSnapshotHandler{DB: env.DB, Token: "sync-token"}
	release := holdSQLiteLock(t, path, "BEGIN EXCLUSIVE")
	defer release()
	done := make(chan lockedHTTPResult, 1)
	go func() {
		req := httptest.NewRequest(http.MethodGet, "/api/sync/snapshot", nil)
		req.Header.Set("Authorization", "Bearer sync-token")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		done <- lockedHTTPResult{code: rec.Code, body: rec.Body.String()}
	}()
	res := awaitLockedResult(t, env.DB, done, 50*time.Millisecond)
	if res.code != http.StatusServiceUnavailable || res.body != busyBody() {
		t.Fatalf("snapshot = %d %s", res.code, res.body)
	}
}

func TestBusyReportIsAWarning(t *testing.T) {
	tr := &recordTransport{}
	useSentryTransport(t, tr)
	if err := InitSentry(SentrySettings{DSN: fakeSentryDSN, Environment: "test", IngestHost: "o1.ingest.example"}); err != nil {
		t.Fatalf("init: %v", err)
	}
	busy := realDriverBusy(t)
	handler := HandleJSON(func(Request[struct{}, struct{}]) (struct{}, error) {
		return struct{}{}, InternalError(fmt.Errorf("stock item Oat Milk: %w", busy))
	})
	res := serveJSON(handler, http.MethodPost, "/api/scans", `{}`)
	if res.code != http.StatusServiceUnavailable || res.body != busyBody() {
		t.Fatalf("status = %d %s", res.code, res.body)
	}
	events := tr.errors()
	if len(events) != 1 {
		t.Fatalf("events = %d", len(events))
	}
	event := events[0]
	if event.Level != sentry.LevelWarning {
		t.Fatalf("level = %q", event.Level)
	}
	if event.Tags["db_busy"] != "true" {
		t.Fatalf("tags = %#v", event.Tags)
	}
	if len(event.Exception) != 0 {
		t.Fatalf("busy was captured as an exception: %+v", event.Exception)
	}
	if strings.Contains(event.Message, "Oat Milk") || strings.Contains(event.Message, "database is locked") {
		t.Fatalf("message = %q", event.Message)
	}
}

func TestNoSpaceReportIsAWarning(t *testing.T) {
	tr := &recordTransport{}
	useSentryTransport(t, tr)
	if err := InitSentry(SentrySettings{DSN: fakeSentryDSN, Environment: "test", IngestHost: "o1.ingest.example"}); err != nil {
		t.Fatalf("init: %v", err)
	}
	full := realDriverFull(t)
	handler := HandleJSON(func(Request[struct{}, struct{}]) (struct{}, error) {
		return struct{}{}, InternalError(fmt.Errorf("stock item Oat Milk: %w", full))
	})
	res := serveJSON(handler, http.MethodPost, "/api/scans", `{}`)
	if res.code != http.StatusServiceUnavailable || res.body != noSpaceBody() {
		t.Fatalf("status = %d %s", res.code, res.body)
	}
	events := tr.errors()
	if len(events) != 1 {
		t.Fatalf("events = %d", len(events))
	}
	event := events[0]
	if event.Level != sentry.LevelWarning {
		t.Fatalf("level = %q", event.Level)
	}
	if event.Tags["db_no_space"] != "true" {
		t.Fatalf("tags = %#v", event.Tags)
	}
	if event.Message != clientmsg.NoSpace {
		t.Fatalf("message = %q", event.Message)
	}
	if len(event.Exception) != 0 {
		t.Fatalf("full disk was captured as an exception: %+v", event.Exception)
	}
	if strings.Contains(event.Message, "Oat Milk") || strings.Contains(event.Message, "disk is full") {
		t.Fatalf("message = %q", event.Message)
	}
}

// commitClientError chooses code internal before the handler classifies the
// cause. A real driver error still has to win. InternalError does not set
// that code, so it does not catch a check that prefers internal.
func TestCommitClientErrorDriverBusyIs503(t *testing.T) {
	busy := realDriverBusy(t)
	var driverErr *sqlite.Error
	if !errors.As(busy, &driverErr) {
		t.Fatalf("busy = %v", busy)
	}
	handler := HandleJSON(func(Request[struct{}, struct{}]) (struct{}, error) {
		return struct{}{}, commitClientError(fmt.Errorf("x: %w", busy))
	})
	res := serveJSON(handler, http.MethodPost, "/api/scans/scan-1/commit", "")
	if res.code != http.StatusServiceUnavailable || res.body != busyBody() {
		t.Fatalf("busy = %d %s", res.code, res.body)
	}
}

func TestCommitClientErrorDriverFullIs503(t *testing.T) {
	full := realDriverFull(t)
	var driverErr *sqlite.Error
	if !errors.As(full, &driverErr) || driverErr.Code()&0xff != sqlitePrimaryFull {
		t.Fatalf("full = %v", full)
	}
	handler := HandleJSON(func(Request[struct{}, struct{}]) (struct{}, error) {
		return struct{}{}, commitClientError(fmt.Errorf("x: %w", full))
	})
	res := serveJSON(handler, http.MethodPost, "/api/scans/scan-1/commit", "")
	if res.code != http.StatusServiceUnavailable || res.body != noSpaceBody() {
		t.Fatalf("full = %d %s", res.code, res.body)
	}
}

func TestSingleCommitLockIsBusyWarning(t *testing.T) {
	tr := &recordTransport{}
	useSentryTransport(t, tr)
	if err := InitSentry(SentrySettings{DSN: fakeSentryDSN, Environment: "test", IngestHost: "o1.ingest.example"}); err != nil {
		t.Fatalf("init: %v", err)
	}
	path := filepath.Join(t.TempDir(), "pantry.db")
	handler, env := setupTestOnDSN(t, path+"?_pragma=busy_timeout(100)", nil)
	mustProduct(env, "prod-lock", "Locked Beans")
	insertPendingScan(env, "scan-lock", "prod-lock", scan.StockIn)

	release := holdSQLiteLock(t, path, "BEGIN EXCLUSIVE")
	defer release()
	done := make(chan lockedHTTPResult, 1)
	go func() {
		done <- serveJSON(handler, http.MethodPost, "/api/scans/scan-lock/commit", "")
	}()
	res := awaitLockedResult(t, env.DB, done, 50*time.Millisecond)
	release()
	if res.code != http.StatusServiceUnavailable || res.body != busyBody() {
		t.Fatalf("commit = %d %s", res.code, res.body)
	}
	assertClassifiedWarning(t, tr, "db_busy")
}

func TestSingleCommitFullIsNoSpaceWarning(t *testing.T) {
	tr := &recordTransport{}
	useSentryTransport(t, tr)
	if err := InitSentry(SentrySettings{DSN: fakeSentryDSN, Environment: "test", IngestHost: "o1.ingest.example"}); err != nil {
		t.Fatalf("init: %v", err)
	}
	path := filepath.Join(t.TempDir(), "pantry.db")
	handler, env := setupTestOnDSN(t, path, nil)
	mustProduct(env, "prod-full", "Full Beans")
	insertPendingScan(env, "scan-full", "prod-full", scan.StockIn)
	// The stock-in rows can fit in pages that already exist. The trigger
	// allocates one inside the commit transaction, which is what hits the cap.
	if _, err := env.DB.Exec(`CREATE TABLE nospace_fill (x BLOB)`); err != nil {
		t.Fatal(err)
	}
	if _, err := env.DB.Exec(`
		CREATE TRIGGER nospace_on_commit
		BEFORE UPDATE ON scan_entries
		BEGIN
			INSERT INTO nospace_fill (x) VALUES (randomblob(8192));
		END`); err != nil {
		t.Fatal(err)
	}
	var pages int
	if err := env.DB.QueryRow(`PRAGMA page_count`).Scan(&pages); err != nil {
		t.Fatal(err)
	}
	var limit int
	if err := env.DB.QueryRow(`PRAGMA max_page_count = ` + fmt.Sprint(pages)).Scan(&limit); err != nil {
		t.Fatal(err)
	}
	if limit != pages {
		t.Fatalf("max_page_count = %d, page_count = %d", limit, pages)
	}

	res := serveJSON(handler, http.MethodPost, "/api/scans/scan-full/commit", "")
	if res.code != http.StatusServiceUnavailable || res.body != noSpaceBody() {
		t.Fatalf("commit = %d %s", res.code, res.body)
	}
	assertClassifiedWarning(t, tr, "db_no_space")
	events := tr.errors()
	if events[0].Message != clientmsg.NoSpace {
		t.Fatalf("message = %q", events[0].Message)
	}
}

func assertClassifiedWarning(t *testing.T, tr *recordTransport, tag string) {
	t.Helper()
	events := tr.errors()
	if len(events) != 1 {
		t.Fatalf("events = %d", len(events))
	}
	event := events[0]
	if event.Level != sentry.LevelWarning {
		t.Fatalf("level = %q", event.Level)
	}
	if event.Tags[tag] != "true" {
		t.Fatalf("tags = %#v", event.Tags)
	}
	if len(event.Exception) != 0 {
		t.Fatalf("captured as an exception: %+v", event.Exception)
	}
}

func TestNoSpaceLogsTheOperatorLine(t *testing.T) {
	var slogBuf, logBuf bytes.Buffer
	oldSlog := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&slogBuf, nil)))
	t.Cleanup(func() { slog.SetDefault(oldSlog) })
	oldLog := log.Writer()
	log.SetOutput(&logBuf)
	t.Cleanup(func() { log.SetOutput(oldLog) })

	full := realDriverFull(t)
	handler := HandleJSON(func(Request[struct{}, struct{}]) (struct{}, error) {
		return struct{}{}, InternalError(fmt.Errorf("stock item Oat Milk: %w", full))
	})
	req := httptest.NewRequest(http.MethodPost, "/api/scans?token=secret", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Pattern = "POST /api/scans"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable || rec.Body.String() != noSpaceBody() {
		t.Fatalf("status = %d %s", rec.Code, rec.Body.String())
	}
	line := noSpaceLogLine(t, slogBuf.String())
	for _, part := range []string{
		"level=WARN",
		"msg=db_no_space",
		"event=db_no_space",
		"method=POST",
		`route="POST /api/scans"`,
		"household_id=" + restock.HouseholdID,
		"sqlite_code=13",
	} {
		if !strings.Contains(line, part) {
			t.Fatalf("log line missing %s: %s", part, line)
		}
	}
	if strings.Count(slogBuf.String(), "msg=db_no_space") != 1 {
		t.Fatalf("lines = %q", slogBuf.String())
	}
	assertNoSpaceLogIsSafe(t, line+"\n"+logBuf.String())

	slogBuf.Reset()
	logBuf.Reset()
	plain := httptest.NewRequest(http.MethodPost, "/api/inventory/item-1?name=Oat+Milk", strings.NewReader(`{}`))
	plain.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, plain)
	line = noSpaceLogLine(t, slogBuf.String())
	if !strings.Contains(line, "route=/api/inventory/item-1") || strings.Contains(line, "Oat") || strings.Contains(line, "name=") {
		t.Fatalf("route = %s", line)
	}
	assertNoSpaceLogIsSafe(t, line+"\n"+logBuf.String())

	slogBuf.Reset()
	logBuf.Reset()
	rec = httptest.NewRecorder()
	writeHTTPError(rec, http.StatusServiceUnavailable, InternalError(errors.New("disk is full")))
	line = noSpaceLogLine(t, slogBuf.String())
	if strings.Contains(line, "household_id=") || strings.Contains(line, "sqlite_code=") || strings.Contains(strings.ToLower(line), "disk is full") {
		t.Fatalf("line = %s", line)
	}
	if !strings.Contains(line, "msg=db_no_space") || !strings.Contains(line, "event=db_no_space") {
		t.Fatalf("line = %s", line)
	}

	slogBuf.Reset()
	busyHandler := HandleJSON(func(Request[struct{}, struct{}]) (struct{}, error) {
		return struct{}{}, InternalError(realDriverBusy(t))
	})
	res := serveJSON(busyHandler, http.MethodPost, "/api/scans", `{}`)
	if res.code != http.StatusServiceUnavailable || strings.Contains(slogBuf.String(), "db_no_space") {
		t.Fatalf("busy = %d log %q", res.code, slogBuf.String())
	}
}

func noSpaceLogLine(t *testing.T, logged string) string {
	t.Helper()
	var line string
	for _, candidate := range strings.Split(logged, "\n") {
		if strings.Contains(candidate, "level=WARN") && strings.Contains(candidate, "msg=db_no_space") {
			if line != "" {
				t.Fatalf("more than one no-space line: %s", logged)
			}
			line = candidate
		}
	}
	if line == "" {
		t.Fatalf("no no-space line: %s", logged)
	}
	return line
}

func assertNoSpaceLogIsSafe(t *testing.T, logged string) {
	t.Helper()
	lower := strings.ToLower(logged)
	for _, leak := range []string{"oat milk", "database or disk is full", "insert ", "token=secret", "sqlite_full"} {
		if strings.Contains(lower, leak) {
			t.Fatalf("log leaked %s: %s", leak, logged)
		}
	}
}

func TestRestockSuggestionsLockFailsAfterOneWait(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pantry.db")
	handler, env := setupTestOnDSN(t, path+"?_pragma=busy_timeout(300)", nil)
	open := serveJSON(handler, http.MethodGet, "/api/restock/suggestions", "")
	if open.code != http.StatusOK {
		t.Fatalf("unlocked = %d %s", open.code, open.body)
	}
	release := holdSQLiteLock(t, path, "BEGIN EXCLUSIVE")
	defer release()
	done := make(chan lockedHTTPResult, 1)
	started := time.Now()
	go func() {
		done <- serveJSON(handler, http.MethodGet, "/api/restock/suggestions", "")
	}()
	res := awaitLockedResult(t, env.DB, done, 200*time.Millisecond)
	if res.code != http.StatusServiceUnavailable || res.body != busyBody() {
		t.Fatalf("suggestions = %d %s", res.code, res.body)
	}
	// One 300ms wait. A swallowed settings read would wait again before the
	// query that returns the error, and that second wait does not fit here.
	if elapsed := time.Since(started); elapsed >= 500*time.Millisecond {
		t.Fatalf("suggestions waited %s", elapsed)
	}
}

func TestDuplicateGroupNameDoesNotWaitOnWriteLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pantry.db")
	handler, _ := setupTestOnDSN(t, path+"?_pragma=busy_timeout(2000)", nil)
	created := serveJSON(handler, http.MethodPost, "/api/groups", `{"name":"Beans"}`)
	if created.code != http.StatusCreated {
		t.Fatalf("create = %d %s", created.code, created.body)
	}
	release := holdSQLiteLock(t, path, "BEGIN IMMEDIATE")
	defer release()
	started := time.Now()
	res := serveJSON(handler, http.MethodPost, "/api/groups", `{"name":"Beans"}`)
	elapsed := time.Since(started)
	if res.code != http.StatusConflict || !strings.Contains(res.body, "already exists") {
		t.Fatalf("duplicate = %d %s", res.code, res.body)
	}
	if strings.Contains(res.body, clientmsg.Busy) {
		t.Fatalf("duplicate = %s", res.body)
	}
	if elapsed >= 400*time.Millisecond {
		t.Fatalf("duplicate waited %s", elapsed)
	}
}

func TestRenameUniqueConstraintIsConflict(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pantry.db")
	handler, env := setupTestOnDSN(t, path, nil)
	created := serveJSON(handler, http.MethodPost, "/api/groups", `{"name":"Beans"}`)
	if created.code != http.StatusCreated {
		t.Fatalf("create = %d %s", created.code, created.body)
	}
	var group struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(created.body), &group); err != nil || group.ID == "" {
		t.Fatalf("id from %s: %v", created.body, err)
	}
	// The name check inside the write sees a free name. The update then
	// hits SQLITE_CONSTRAINT_UNIQUE because this trigger takes the name
	// after that check. Removing the 2067 mapping turns the response into 500.
	if _, err := env.DB.Exec(`
		CREATE TRIGGER force_group_name_taken
		BEFORE UPDATE OF name_key ON product_groups
		WHEN NEW.name_key != OLD.name_key
		BEGIN
			INSERT INTO product_groups (id, user_id, name, name_key, rule)
			VALUES ('forced-other', NEW.user_id, NEW.name, NEW.name_key, 'same_as_ran_out');
		END`); err != nil {
		t.Fatal(err)
	}
	renamed := serveJSON(handler, http.MethodPatch, "/api/groups/"+group.ID, `{"name":"Lentils"}`)
	if renamed.code != http.StatusConflict || !strings.Contains(renamed.body, "already exists") {
		t.Fatalf("rename = %d %s", renamed.code, renamed.body)
	}

	if _, err := env.DB.Exec(`DROP TRIGGER force_group_name_taken`); err != nil {
		t.Fatal(err)
	}
	// A NOT NULL failure on the same update is a different constraint code.
	if _, err := env.DB.Exec(`
		CREATE TRIGGER force_group_not_null
		BEFORE UPDATE OF name_key ON product_groups
		WHEN NEW.name_key != OLD.name_key
		BEGIN
			INSERT INTO product_groups (id, user_id, name, name_key, rule)
			VALUES ('forced-null', 'user-1', NULL, 'unused', 'same_as_ran_out');
		END`); err != nil {
		t.Fatal(err)
	}
	notNull := serveJSON(handler, http.MethodPatch, "/api/groups/"+group.ID, `{"name":"Peas"}`)
	if notNull.code == http.StatusConflict || notNull.body != genericBody() {
		t.Fatalf("not null = %d %s", notNull.code, notNull.body)
	}
}

func TestNoSpaceDebugKeepsTheDriverText(t *testing.T) {
	var slogBuf bytes.Buffer
	oldSlog := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&slogBuf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(oldSlog) })

	full := realDriverFull(t)
	handler := HandleJSON(func(Request[struct{}, struct{}]) (struct{}, error) {
		return struct{}{}, InternalError(fmt.Errorf("stock item Oat Milk: %w", full))
	})
	res := serveJSON(handler, http.MethodPost, "/api/scans", `{}`)
	if res.code != http.StatusServiceUnavailable || res.body != noSpaceBody() {
		t.Fatalf("status = %d %s", res.code, res.body)
	}
	warn := noSpaceLogLine(t, slogBuf.String())
	if strings.Contains(strings.ToLower(warn), "database or disk is full") || strings.Contains(warn, "Oat Milk") || !strings.Contains(warn, "level=WARN") {
		t.Fatalf("warn = %s", warn)
	}
	debug := debugNoSpaceLine(t, slogBuf.String())
	if !strings.Contains(debug, "level=DEBUG") || !strings.Contains(debug, "driver_message=") || !strings.Contains(debug, "database or disk is full") {
		t.Fatalf("debug = %s", debug)
	}

	// A commit failure sets HTTPError.Message to the client sentence.
	// Error() then hides the driver text, which is what the debug line must keep.
	slogBuf.Reset()
	handler = HandleJSON(func(Request[struct{}, struct{}]) (struct{}, error) {
		return struct{}{}, commitClientError(fmt.Errorf("commit stock-in: %w", full))
	})
	res = serveJSON(handler, http.MethodPost, "/api/scans/scan-1/commit", "")
	if res.code != http.StatusServiceUnavailable || res.body != noSpaceBody() {
		t.Fatalf("wrapped = %d %s", res.code, res.body)
	}
	warn = noSpaceLogLine(t, slogBuf.String())
	if strings.Contains(strings.ToLower(warn), "database or disk is full") || !strings.Contains(warn, "level=WARN") {
		t.Fatalf("wrapped warn = %s", warn)
	}
	debug = debugNoSpaceLine(t, slogBuf.String())
	if !strings.Contains(debug, "database or disk is full") || strings.Contains(debug, clientmsg.Generic) {
		t.Fatalf("wrapped debug = %s", debug)
	}
}

func debugNoSpaceLine(t *testing.T, logged string) string {
	t.Helper()
	var line string
	for _, candidate := range strings.Split(logged, "\n") {
		if strings.Contains(candidate, "level=DEBUG") && strings.Contains(candidate, "msg=db_no_space") {
			if line != "" {
				t.Fatalf("more than one debug line: %s", logged)
			}
			line = candidate
		}
	}
	if line == "" {
		t.Fatalf("no debug line: %s", logged)
	}
	return line
}

func TestConcurrentGroupCreateOneWins(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pantry.db")
	// Long enough that the requests stay blocked until this test releases
	// the lock, then finish the insert.
	handler, env := setupTestOnDSN(t, path+"?_pragma=busy_timeout(5000)", nil)
	// The name pre-check runs before the write transaction. One connection
	// would run the requests one after another, and that pre-check alone
	// would answer 409 after the winner commits. Eight connections, held
	// until each has passed the pre-check and is waiting on the write, make
	// the losers meet the in-transaction check or the unique constraint.
	env.DB.SetMaxOpenConns(8)

	release := holdSQLiteLock(t, path, "BEGIN IMMEDIATE")
	const n = 8
	results := make(chan lockedHTTPResult, n)
	var ready sync.WaitGroup
	ready.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			ready.Done()
			results <- serveJSON(handler, http.MethodPost, "/api/groups", `{"name":"Beans"}`)
		}()
	}
	ready.Wait()
	deadline := time.Now().Add(2 * time.Second)
	var heldSince time.Time
	for {
		if env.DB.Stats().InUse >= n {
			if heldSince.IsZero() {
				heldSince = time.Now()
			}
			if time.Since(heldSince) >= 50*time.Millisecond {
				break
			}
		} else {
			heldSince = time.Time{}
		}
		if time.Now().After(deadline) {
			release()
			t.Fatalf("connections in use = %d, want %d", env.DB.Stats().InUse, n)
		}
		time.Sleep(5 * time.Millisecond)
	}
	release()

	created, conflict := 0, 0
	for i := 0; i < n; i++ {
		res := <-results
		switch res.code {
		case http.StatusCreated:
			created++
		case http.StatusConflict:
			if !strings.Contains(res.body, "already exists") || strings.Contains(res.body, `"code":"busy"`) {
				t.Fatalf("conflict = %s", res.body)
			}
			conflict++
		default:
			t.Fatalf("status = %d %s", res.code, res.body)
		}
	}
	if created != 1 || conflict != n-1 {
		t.Fatalf("created = %d conflict = %d", created, conflict)
	}
	var rows int
	if err := env.DB.QueryRow(`SELECT COUNT(*) FROM product_groups`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("rows = %d", rows)
	}
}

func TestProviderWrapKeepsDriverCause(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pantry.db")
	_, env := setupTestOnDSN(t, path+"?_pragma=busy_timeout(100)", nil)
	if err := env.Registry.Register(kroger.NewUnconfigured()); err != nil {
		t.Fatal(err)
	}
	open := carttest.NewFake(cart.Capabilities{
		Auth:         cart.AuthNone,
		Delivery:     cart.DeliveryServerPush,
		Confirmation: cart.ConfirmPerRequest,
		Mutation:     cart.MutateAddOnly,
		Identity:     cart.IdentityDerived,
	}, nil)
	if err := env.Registry.Register(open); err != nil {
		t.Fatal(err)
	}
	dir := connection.NewDirectory(env.DB)
	ledger := cart.NewLedger(env.DB)
	providers := ProvidersHandler{Registry: env.Registry, ConnectionDir: dir}

	release := holdSQLiteLock(t, path, "BEGIN IMMEDIATE")
	_, err := (&ProviderDisconnectHandler{ProvidersHandler: providers}).Handle(Request[struct{}, ProviderDisconnectRequest]{
		Context:    context.Background(),
		PathParams: ProviderDisconnectRequest{ProviderID: "kroger"},
	})
	release()
	requireDriverCause(t, err, "failed to disconnect")

	if err := dir.Write(context.Background(), &connection.Connection{
		Provider:    "kroger",
		State:       connection.StateConnected,
		AccessToken: "tok",
	}); err != nil {
		t.Fatal(err)
	}
	release = holdSQLiteLock(t, path, "BEGIN IMMEDIATE")
	_, err = (&ProviderLedgerResetHandler{ProviderLedgerHandler: ProviderLedgerHandler{ProvidersHandler: providers, Ledger: ledger}}).Handle(Request[struct{}, ProviderLedgerResetRequest]{
		Context:    context.Background(),
		PathParams: ProviderLedgerResetRequest{ProviderID: "kroger"},
	})
	release()
	requireDriverCause(t, err, "failed to reset ledger")

	// A reserved lock still allows the connection read. An exclusive lock
	// is what makes that read fail.
	release = holdSQLiteLock(t, path, "BEGIN EXCLUSIVE")
	defer release()
	_, err = (&ProviderLedgerGetHandler{ProviderLedgerHandler: ProviderLedgerHandler{ProvidersHandler: providers, Ledger: ledger}}).Handle(Request[struct{}, ProviderLedgerGetRequest]{
		Context:    context.Background(),
		PathParams: ProviderLedgerGetRequest{ProviderID: "kroger"},
	})
	requireDriverCause(t, err, "failed to read connection")
	_, err = (&ProviderLedgerResetHandler{ProviderLedgerHandler: ProviderLedgerHandler{ProvidersHandler: providers, Ledger: ledger}}).Handle(Request[struct{}, ProviderLedgerResetRequest]{
		Context:    context.Background(),
		PathParams: ProviderLedgerResetRequest{ProviderID: "kroger"},
	})
	requireDriverCause(t, err, "failed to read connection")
	_, err = (&ProviderLedgerGetHandler{ProviderLedgerHandler: ProviderLedgerHandler{ProvidersHandler: providers, Ledger: ledger}}).Handle(Request[struct{}, ProviderLedgerGetRequest]{
		Context:    context.Background(),
		PathParams: ProviderLedgerGetRequest{ProviderID: string(open.ID())},
	})
	requireDriverCause(t, err, "failed to read ledger")
}

func busyBody() string {
	return `{"error":"` + clientmsg.Busy + `","code":"busy"}`
}

func noSpaceBody() string {
	return `{"error":"` + clientmsg.NoSpace + `","code":"no_space"}`
}

func genericBody() string {
	return `{"error":"` + clientmsg.Generic + `","code":"internal"}`
}

type hiddenCause struct{ inner error }

func (h hiddenCause) Error() string { return "request failed" }

func (h hiddenCause) Unwrap() error { return h.inner }

func realDriverBusy(t *testing.T) error {
	t.Helper()
	path := filepath.Join(t.TempDir(), "busy.db")
	db, err := app.OpenSQLite(path + "?_pragma=busy_timeout(0)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`CREATE TABLE t (x INTEGER)`); err != nil {
		t.Fatal(err)
	}
	release := holdSQLiteLock(t, path, "BEGIN IMMEDIATE")
	defer release()
	_, err = db.Exec(`UPDATE t SET x = 2`)
	if err == nil {
		t.Fatal("expected SQLITE_BUSY")
	}
	return err
}

func realDriverLocked(t *testing.T) error {
	t.Helper()
	db, err := app.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`CREATE TABLE t (x INTEGER)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO t VALUES (1)`); err != nil {
		t.Fatal(err)
	}
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var locked error
	err = conn.Raw(func(dc any) error {
		c := dc.(interface {
			Query(string, []driver.Value) (driver.Rows, error)
			Exec(string, []driver.Value) (driver.Result, error)
		})
		rows, err := c.Query(`SELECT x FROM t`, nil)
		if err != nil {
			return err
		}
		dest := make([]driver.Value, 1)
		if err := rows.Next(dest); err != nil {
			rows.Close()
			return err
		}
		_, locked = c.Exec(`DROP TABLE t`, nil)
		rows.Close()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if locked == nil {
		t.Fatal("expected SQLITE_LOCKED")
	}
	return locked
}

func requireDriverCause(t *testing.T, err error, wrap string) {
	t.Helper()
	var driverErr *sqlite.Error
	if !errors.As(err, &driverErr) {
		t.Fatalf("driver cause dropped: %v", err)
	}
	if driverErr.Code()&0xff != sqlitePrimaryBusy {
		t.Fatalf("code = %d (%v)", driverErr.Code(), err)
	}
	if !strings.Contains(err.Error(), wrap) {
		t.Fatalf("wrap = %v", err)
	}
}

func realDriverFull(t *testing.T) error {
	t.Helper()
	path := filepath.Join(t.TempDir(), "full.db")
	db, err := app.OpenSQLite(path + "?_pragma=busy_timeout(0)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`CREATE TABLE t (x TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA max_page_count = 2`); err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO t VALUES (?)`, strings.Repeat("x", 8000))
	if err == nil {
		t.Fatal("expected SQLITE_FULL")
	}
	return err
}

func realDriverBusySnapshot(t *testing.T) error {
	t.Helper()
	path := filepath.Join(t.TempDir(), "snap.db")
	db, err := app.OpenSQLite(path + "?_pragma=busy_timeout(0)&_txlock=deferred")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA journal_mode=WAL`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE t (x INTEGER)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO t VALUES (1)`); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tx.Rollback() })
	var n int
	if err := tx.QueryRow(`SELECT x FROM t`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	other, err := app.OpenSQLite(path + "?_pragma=busy_timeout(0)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { other.Close() })
	other.SetMaxOpenConns(1)
	if _, err := other.Exec(`UPDATE t SET x = 2`); err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(`UPDATE t SET x = 3`)
	if err == nil {
		t.Fatal("expected SQLITE_BUSY_SNAPSHOT")
	}
	return err
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
