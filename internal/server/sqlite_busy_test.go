package server

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/cart"
	"github.com/Rhionin/stockbot/internal/cart/carttest"
	"github.com/Rhionin/stockbot/internal/cart/connection"
	"github.com/Rhionin/stockbot/internal/cart/kroger"
	"github.com/Rhionin/stockbot/internal/clientmsg"
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
	if res.code != http.StatusInternalServerError || !strings.Contains(res.body, clientmsg.Generic) || strings.Contains(res.body, clientmsg.Busy) {
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
	if res.code != http.StatusServiceUnavailable || strings.Contains(res.body, `"code":"no_space"`) || !strings.Contains(res.body, clientmsg.Generic) {
		t.Fatalf("coded = %d %s", res.code, res.body)
	}

	unclassified := HandleJSON(func(Request[struct{}, struct{}]) (struct{}, error) {
		return struct{}{}, &HTTPError{Code: http.StatusServiceUnavailable, Message: "raw database detail"}
	})
	res = serveJSON(unclassified, http.MethodGet, "/api/restock", "")
	if res.code != http.StatusServiceUnavailable || !strings.Contains(res.body, clientmsg.Generic) || strings.Contains(res.body, "raw database") {
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
	if len(event.Exception) != 0 {
		t.Fatalf("full disk was captured as an exception: %+v", event.Exception)
	}
	if strings.Contains(event.Message, "Oat Milk") || strings.Contains(event.Message, "disk is full") {
		t.Fatalf("message = %q", event.Message)
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
	requireDriverCause(t, err)

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
	requireDriverCause(t, err)

	release = holdSQLiteLock(t, path, "BEGIN EXCLUSIVE")
	defer release()
	_, err = (&ProviderLedgerGetHandler{ProviderLedgerHandler: ProviderLedgerHandler{ProvidersHandler: providers, Ledger: ledger}}).Handle(Request[struct{}, ProviderLedgerGetRequest]{
		Context:    context.Background(),
		PathParams: ProviderLedgerGetRequest{ProviderID: string(open.ID())},
	})
	requireDriverCause(t, err)
}

func busyBody() string {
	return `{"error":"` + clientmsg.Busy + `","code":"busy"}`
}

func noSpaceBody() string {
	return `{"error":"` + clientmsg.NoSpace + `","code":"no_space"}`
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

func requireDriverCause(t *testing.T, err error) {
	t.Helper()
	var driverErr *sqlite.Error
	if !errors.As(err, &driverErr) {
		t.Fatalf("driver cause dropped: %v", err)
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
