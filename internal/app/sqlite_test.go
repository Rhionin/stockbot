package app

import (
	"context"
	"database/sql"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestMemoryDatabaseSurvivesACancelledQuery(t *testing.T) {
	db, err := OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if db.Stats().MaxOpenConnections != 1 {
		t.Fatalf("pool %d", db.Stats().MaxOpenConnections)
	}
	var seq int
	var name, file string
	if err := db.QueryRow(`PRAGMA database_list`).Scan(&seq, &name, &file); err != nil {
		t.Fatal(err)
	}
	if file != "" {
		t.Fatalf("memory database is a file %q", file)
	}
	if _, err := db.Exec(`CREATE TABLE scan_entries (id TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO scan_entries (id) VALUES ('scan-1')`); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var id string
	err = db.QueryRowContext(ctx, `SELECT id FROM scan_entries`).Scan(&id)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled query: %v", err)
	}
	if err := db.QueryRow(`SELECT id FROM scan_entries`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if id != "scan-1" {
		t.Fatalf("id %q", id)
	}

	other, err := OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { other.Close() })
	if err := other.QueryRow(`SELECT id FROM scan_entries`).Scan(&id); err == nil {
		t.Fatal("a second :memory: open saw the first database")
	}

	db.SetMaxOpenConns(2)
	left, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { left.Close() })
	right, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { right.Close() })
	if err := right.QueryRowContext(context.Background(), `SELECT id FROM scan_entries`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if id != "scan-1" {
		t.Fatalf("second connection id %q", id)
	}
}

func TestWithPrivateFilesRunsTheFunction(t *testing.T) {
	called := false
	err := WithPrivateFiles(func() error {
		called = true
		return nil
	})
	if err != nil || !called {
		t.Fatalf("err %v called %v", err, called)
	}
}

func TestWithUTCTimezone(t *testing.T) {
	if got := withUTCTimezone(":memory:"); got != ":memory:?_timezone=UTC" {
		t.Fatalf("memory: %s", got)
	}
	if got := withUTCTimezone("file:pantry.db?_pragma=busy_timeout(5000)"); got != "file:pantry.db?_pragma=busy_timeout(5000)&_timezone=UTC" {
		t.Fatalf("pragma: %s", got)
	}
	if got := withUTCTimezone("pantry.db?_timezone=America/Denver&_txlock=immediate"); got != "pantry.db?_timezone=UTC&_txlock=immediate" {
		t.Fatalf("replace: %s", got)
	}
	if got := withUTCTimezone("pantry.db?_txlock=immediate&_timezone=Local"); got != "pantry.db?_txlock=immediate&_timezone=UTC" {
		t.Fatalf("replace tail: %s", got)
	}
}

func TestOpenSQLiteStoresDenverTimesAsUTC(t *testing.T) {
	denver, err := time.LoadLocation("America/Denver")
	if err != nil {
		t.Fatal(err)
	}
	db, err := OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`CREATE TABLE stamps (id TEXT, at DATETIME)`); err != nil {
		t.Fatal(err)
	}
	now := time.Now().In(denver)
	fixed := time.Date(2026, 11, 1, 0, 30, 0, 0, denver)
	// time.Now still carries a monotonic reading. CAST keeps the stored text;
	// a plain SELECT into a string is reformatted by the driver and hides " m=+".
	if _, err := db.Exec(`INSERT INTO stamps (id, at) VALUES ('now', ?), ('fixed', ?), ('mono', ?)`, now, fixed, time.Now()); err != nil {
		t.Fatal(err)
	}
	rows, err := db.Query(`SELECT id, CAST(at AS TEXT) FROM stamps ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, raw string
		if err := rows.Scan(&id, &raw); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(raw, "+0000 UTC") || strings.Contains(raw, "MDT") || strings.Contains(raw, "MST") || strings.Contains(raw, "m=") {
			t.Fatalf("%s stored %s", id, raw)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}

// A raw sqlite open outside OpenSQLite skips _timezone=UTC, so a Denver
// time.Time can reach SQL again. The needle is built at runtime so this
// file does not contain the call it forbids.
func TestSQLiteOpensGoThroughUTC(t *testing.T) {
	root := moduleRoot(t)
	needle := "sql.Open(" + `"sqlite"`
	var offenders []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "frontend", "vendor", "node_modules":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "sqlite.go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(src), needle) {
			offenders = append(offenders, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(offenders) > 0 {
		t.Fatalf("open the database with app.OpenSQLite so timestamps are UTC:\n%s", strings.Join(offenders, "\n"))
	}
	// The walk above is text. Parse OpenSQLite itself so a renamed call still
	// has to be the one sql.Open in that file.
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filepath.Join(root, "internal", "app", "sqlite.go"), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	opens := 0
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Open" {
			return true
		}
		opens++
		return true
	})
	if opens != 1 {
		t.Fatalf("sqlite.go has %d Open calls, want 1", opens)
	}
}

func TestBusyTimeoutDSNPinsTwoSeconds(t *testing.T) {
	if got, want := withSQLiteLocking("pantry.db"), "pantry.db?_pragma=busy_timeout(2000)&_txlock=immediate"; got != want {
		t.Fatalf("dsn = %s, want %s", got, want)
	}
}

func TestWithSQLiteLocking(t *testing.T) {
	busy := "busy_timeout(" + strconv.Itoa(sqliteBusyTimeoutMS) + ")"
	if got, want := withSQLiteLocking(":memory:"), ":memory:?_pragma="+busy+"&_txlock=immediate"; got != want {
		t.Fatalf("memory: got %s, want %s", got, want)
	}
	if got, want := withUTCTimezone(withSQLiteLocking(":memory:")), ":memory:?_pragma="+busy+"&_txlock=immediate&_timezone=UTC"; got != want {
		t.Fatalf("opened: got %s, want %s", got, want)
	}
	if got, want := withSQLiteLocking("pantry.db?_pragma=busy_timeout(40)"), "pantry.db?_pragma=busy_timeout(40)&_txlock=immediate"; got != want {
		t.Fatalf("pragma kept: got %s, want %s", got, want)
	}
	if got, want := withSQLiteLocking("pantry.db?_busy_timeout=40"), "pantry.db?_busy_timeout=40&_txlock=immediate"; got != want {
		t.Fatalf("shorthand kept: got %s, want %s", got, want)
	}
	if got, want := withSQLiteLocking("pantry.db?_timeout=15"), "pantry.db?_timeout=15&_txlock=immediate"; got != want {
		t.Fatalf("alias kept: got %s, want %s", got, want)
	}
	if got, want := withSQLiteLocking("pantry.db?_txlock=deferred"), "pantry.db?_txlock=deferred&_pragma="+busy; got != want {
		t.Fatalf("txlock kept: got %s, want %s", got, want)
	}
	if got, want := withSQLiteLocking("pantry.db?_pragma=BUSY_TIMEOUT(10)&_pragma=foreign_keys(1)"), "pantry.db?_pragma=BUSY_TIMEOUT(10)&_pragma=foreign_keys(1)&_txlock=immediate"; got != want {
		t.Fatalf("pragma case: got %s, want %s", got, want)
	}
}

func TestOpenSQLiteSetsBusyTimeoutAndImmediateLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pantry.db")
	db, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)

	var timeout int
	if err := db.QueryRow(`PRAGMA busy_timeout`).Scan(&timeout); err != nil {
		t.Fatal(err)
	}
	if timeout != sqliteBusyTimeoutMS {
		t.Fatalf("busy_timeout = %d, want %d", timeout, sqliteBusyTimeoutMS)
	}
	var mode string
	if err := db.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(mode, "delete") {
		t.Fatalf("journal_mode = %s, want delete", mode)
	}
	var synchronous int
	if err := db.QueryRow(`PRAGMA synchronous`).Scan(&synchronous); err != nil {
		t.Fatal(err)
	}
	if synchronous != 1 {
		t.Fatalf("synchronous = %d, want 1 (NORMAL)", synchronous)
	}
	if _, err := db.Exec(`CREATE TABLE t (id INTEGER)`); err != nil {
		t.Fatal(err)
	}

	// BEGIN takes the reserved lock before any statement. A second connection
	// with no busy wait must fail at once. A deferred begin would still be
	// unlocked here, and the second writer would succeed.
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tx.Rollback() })

	other, err := OpenSQLite(path + "?_pragma=busy_timeout(0)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { other.Close() })
	other.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	started := time.Now()
	_, err = other.ExecContext(ctx, `BEGIN IMMEDIATE`)
	if err == nil {
		t.Fatal("a second connection took the write lock during an open transaction")
	}
	if time.Since(started) > 500*time.Millisecond {
		t.Fatalf("busy handler waited %s; a zero timeout should fail at once", time.Since(started))
	}
	if !strings.Contains(strings.ToLower(err.Error()), "database is locked") && !strings.Contains(err.Error(), "SQLITE_BUSY") {
		t.Fatalf("err = %v", err)
	}
}

func TestWithSQLiteDurability(t *testing.T) {
	mem := withSQLiteDurability(":memory:?_pragma=busy_timeout(2000)&_txlock=immediate")
	if strings.Contains(mem, "journal_mode") {
		t.Fatalf("memory set a journal: %s", mem)
	}
	if !strings.Contains(mem, "synchronous(NORMAL)") {
		t.Fatalf("memory: %s", mem)
	}
	file := withSQLiteDurability("pantry.db?_pragma=busy_timeout(2000)&_txlock=immediate")
	if strings.Contains(file, "journal_mode") || !strings.Contains(file, "synchronous(NORMAL)") {
		t.Fatalf("file: %s", file)
	}
	ro := withSQLiteDurability("file:pantry.db?mode=ro&_txlock=deferred&_pragma=query_only(1)")
	if strings.Contains(ro, "journal_mode") || strings.Contains(ro, "synchronous") {
		t.Fatalf("reader: %s", ro)
	}
	kept := withSQLiteDurability("pantry.db?_pragma=journal_mode(DELETE)&_pragma=synchronous(FULL)")
	if strings.Contains(kept, "WAL") || strings.Contains(kept, "NORMAL") {
		t.Fatalf("explicit: %s", kept)
	}
}

func TestOpenPantryOneWriterTwoReaders(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pantry.db")
	writer, reader, err := OpenPantry(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		reader.Close()
		writer.Close()
	})
	if writer.Stats().MaxOpenConnections != 1 {
		t.Fatalf("writer pool %d", writer.Stats().MaxOpenConnections)
	}
	if reader.Stats().MaxOpenConnections != 2 {
		t.Fatalf("reader pool %d", reader.Stats().MaxOpenConnections)
	}
	var mode string
	if err := writer.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(mode, "wal") {
		t.Fatalf("journal_mode %s", mode)
	}
	var synchronous, busy, queryOnly int
	if err := writer.QueryRow(`PRAGMA synchronous`).Scan(&synchronous); err != nil {
		t.Fatal(err)
	}
	if err := writer.QueryRow(`PRAGMA busy_timeout`).Scan(&busy); err != nil {
		t.Fatal(err)
	}
	if err := reader.QueryRow(`PRAGMA query_only`).Scan(&queryOnly); err != nil {
		t.Fatal(err)
	}
	if synchronous != 1 || busy != sqliteBusyTimeoutMS || queryOnly != 1 {
		t.Fatalf("synchronous %d busy %d query_only %d", synchronous, busy, queryOnly)
	}
	if _, err := reader.Exec(`CREATE TABLE nope (id INTEGER)`); err == nil {
		t.Fatal("reader wrote")
	}

	if _, err := writer.Exec(`CREATE TABLE held (id INTEGER)`); err != nil {
		t.Fatal(err)
	}
	tx, err := writer.Begin()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tx.Rollback() })
	if _, err := tx.Exec(`INSERT INTO held (id) VALUES (1)`); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	var n int
	if err := reader.QueryRow(`SELECT COUNT(*) FROM held`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("reader saw an uncommitted row: %d", n)
	}
	if time.Since(started) > time.Second {
		t.Fatalf("reader waited %s behind the writer", time.Since(started))
	}

	other, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { other.Close() })
	other.SetMaxOpenConns(1)
	started = time.Now()
	_, err = other.Exec(`BEGIN IMMEDIATE`)
	elapsed := time.Since(started)
	if err == nil {
		t.Fatal("second writer took the lock")
	}
	if elapsed < 1500*time.Millisecond || elapsed > 4500*time.Millisecond {
		t.Fatalf("busy returned after %s; the wait is 2s", elapsed)
	}
}

func TestOpenPantryRejectsMemory(t *testing.T) {
	writer, reader, err := OpenPantry(":memory:")
	if err == nil {
		writer.Close()
		reader.Close()
		t.Fatal("in-memory pantry opened a read pool")
	}
}

func TestOpenPantryRejectsANonFilePath(t *testing.T) {
	for _, path := range []string{"", "pantry.db?mode=ro", "pantry.db\x00"} {
		writer, reader, err := OpenPantry(path)
		if err == nil {
			writer.Close()
			reader.Close()
			t.Fatalf("opened %q", path)
		}
		if !strings.Contains(err.Error(), "file path") {
			t.Fatalf("%q: %v", path, err)
		}
	}
}

func TestOpenSQLiteReaderRejectsANonFilePath(t *testing.T) {
	for _, path := range []string{"", ":memory:", "pantry.db?mode=ro", "pantry.db\x00"} {
		db, err := OpenSQLiteReader(path)
		if err == nil {
			db.Close()
			t.Fatalf("opened %q", path)
		}
		if !strings.Contains(err.Error(), "file path") {
			t.Fatalf("%q: %v", path, err)
		}
	}
}

func TestSQLiteLockingLeavesAnExplicitTimeout(t *testing.T) {
	got := withSQLiteLocking("file.db?_timeout=1&_txlock=deferred")
	if strings.Contains(got, "busy_timeout") || strings.Contains(got, "immediate") {
		t.Fatalf("explicit lock was rewritten: %s", got)
	}
	plain := withSQLiteLocking("file.db")
	if !strings.Contains(plain, "busy_timeout(2000)") || !strings.Contains(plain, "_txlock=immediate") {
		t.Fatalf("plain: %s", plain)
	}
	if dsnReadOnly("file:db?mode&_pragma=query_only(1)") != true {
		t.Fatal("query_only was not read-only")
	}
	if dsnReadOnly("file:db") {
		t.Fatal("plain file was read-only")
	}
	if pragmaName(" Busy_Timeout = 1 ") != "busy_timeout" {
		t.Fatalf("pragma %s", pragmaName(" Busy_Timeout = 1 "))
	}
	if dsnSetsBusyTimeout("file.db?mode&_timeout=1") != true {
		t.Fatal("a timeout alias was ignored")
	}
	if dsnSetsBusyTimeout("file.db?mode") {
		t.Fatal("a flag without a value counted as a timeout")
	}
}

func TestOpenSQLiteReadOnlyTransactionStaysDeferred(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pantry.db")
	db, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`CREATE TABLE t (id INTEGER)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO t (id) VALUES (1)`); err != nil {
		t.Fatal(err)
	}

	tx, err := db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tx.Rollback() })
	var n int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM t`).Scan(&n); err != nil {
		t.Fatal(err)
	}

	other, err := OpenSQLite(path + "?_pragma=busy_timeout(0)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { other.Close() })
	other.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := other.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		t.Fatalf("read-only transaction blocked a writer: %v", err)
	}
	if _, err := other.Exec(`ROLLBACK`); err != nil {
		t.Fatal(err)
	}
}

func TestOpenSQLitePreservesExplicitBusyTimeout(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name string
		dsn  string
		want int
	}{
		{name: "pragma", dsn: "?_pragma=busy_timeout(40)", want: 40},
		{name: "shorthand", dsn: "?_busy_timeout=40", want: 40},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db, err := OpenSQLite(filepath.Join(dir, tc.name+".db") + tc.dsn)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { db.Close() })
			var got int
			if err := db.QueryRow(`PRAGMA busy_timeout`).Scan(&got); err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("busy_timeout = %d, want %d", got, tc.want)
			}
		})
	}
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}
