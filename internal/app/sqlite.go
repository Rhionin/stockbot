package app

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"

	_ "modernc.org/sqlite"
)

// sqliteBusyTimeoutMS is how long a connection waits for another process to
// drop its write lock. It is long enough for a brief shell, backup, or
// grocy-sync lock, and short enough that a stuck lock becomes a retryable
// HTTP error instead of hanging the request.
const sqliteBusyTimeoutMS = 2000

// OpenSQLite opens a pantry or Grocy database.
//
// The modernc driver stores a time.Time with time.Time.String, which includes
// the value's zone ("2006-01-02 15:04:05.999999999 -0700 MST"). SQL then
// compares those strings alphabetically. Production runs with no TZ, so the
// rows already on disk are UTC in that same default format. _timezone=UTC
// makes every later bind use that format too, including a Denver time or a
// time.Time that still carries a monotonic clock reading.
//
// The DSN also sets PRAGMA busy_timeout on every pooled connection. An Exec
// after Open would apply only to the one connection it happened to check out.
// An explicit busy timeout already in the DSN is left alone so a caller can
// use a shorter wait.
//
// Write transactions start with BEGIN IMMEDIATE. A deferred transaction that
// has already read can still fail to upgrade its lock without running the
// busy handler, so the reserved lock is taken at BEGIN, which is when the
// handler runs. The driver leaves a ReadOnly transaction deferred. An
// explicit _txlock in the DSN wins. Journal mode is left as the file already
// uses it: Grocy and a backup snapshot must not be switched to WAL just
// because this process opened them. The pantry server sets WAL in OpenPantry.
// A file connection uses synchronous=NORMAL. An in-memory database does too,
// and it does not switch journals.
func OpenSQLite(dsn string) (*sql.DB, error) {
	return sql.Open("sqlite", withUTCTimezone(withSQLiteDurability(withSQLiteLocking(dsn))))
}

// OpenPantry opens the writer and a read-only pool on one pantry file.
// The writer is one connection in WAL with synchronous=NORMAL, so two
// requests from this process do not wait on each other for a second writer
// and a reader does not block that writer. Two readers can run while the
// writer is busy. The read pool is query-only and keeps a deferred lock.
func OpenPantry(path string) (writer, reader *sql.DB, err error) {
	if path == "" || path == ":memory:" || strings.Contains(path, "?") || strings.Contains(path, "\x00") {
		return nil, nil, fmt.Errorf("the pantry needs a file path")
	}
	writer, err = OpenSQLite(path + "?_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, nil, err
	}
	writer.SetMaxOpenConns(1)
	reader, err = OpenSQLiteReader(path)
	if err != nil {
		writer.Close()
		return nil, nil, err
	}
	return writer, reader, nil
}

// OpenSQLiteReader opens a query-only pool of two connections. path is a
// filesystem path with no query string. The file must already exist; the
// writer creates it.
func OpenSQLiteReader(path string) (*sql.DB, error) {
	if path == "" || path == ":memory:" || strings.Contains(path, "?") || strings.Contains(path, "\x00") {
		return nil, fmt.Errorf("the read-only pantry needs a file path")
	}
	dsn := "file:" + path + "?mode=ro&_txlock=deferred&_pragma=query_only(1)&_pragma=busy_timeout(" + strconv.Itoa(sqliteBusyTimeoutMS) + ")"
	db, err := OpenSQLite(dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(2)
	return db, nil
}

// withSQLiteDurability sets synchronous=NORMAL. It does not set the journal:
// journal_mode is stored in the file, and only OpenPantry chooses WAL.
// A read-only DSN is left alone. An explicit synchronous pragma wins.
func withSQLiteDurability(dsn string) string {
	if dsnReadOnly(dsn) {
		return dsn
	}
	if !dsnSetsPragma(dsn, "synchronous") {
		dsn = appendQuery(dsn, "_pragma", "synchronous(NORMAL)")
	}
	return dsn
}

// withSQLiteLocking adds the modernc busy-timeout pragma and immediate
// transaction lock when the DSN does not already set them.
func withSQLiteLocking(dsn string) string {
	if !dsnSetsBusyTimeout(dsn) {
		dsn = appendQuery(dsn, "_pragma", "busy_timeout("+strconv.Itoa(sqliteBusyTimeoutMS)+")")
	}
	if !dsnHasKey(dsn, "_txlock") {
		dsn = appendQuery(dsn, "_txlock", "immediate")
	}
	return dsn
}

// withUTCTimezone sets the driver's _timezone DSN parameter to UTC.
// An explicit _timezone already in the DSN is replaced, so a caller cannot
// open a connection that writes another zone.
func withUTCTimezone(dsn string) string {
	const key = "_timezone="
	if i := strings.Index(dsn, key); i >= 0 {
		rest := dsn[i+len(key):]
		if end := strings.IndexByte(rest, '&'); end >= 0 {
			return dsn[:i] + key + "UTC" + rest[end:]
		}
		return dsn[:i] + key + "UTC"
	}
	if strings.Contains(dsn, "?") {
		return dsn + "&_timezone=UTC"
	}
	return dsn + "?_timezone=UTC"
}

func dsnReadOnly(dsn string) bool {
	for _, part := range dsnQuery(dsn) {
		key, val, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		if key == "mode" && val == "ro" {
			return true
		}
		if key == "_pragma" && pragmaName(val) == "query_only" {
			return true
		}
	}
	return false
}

func dsnSetsPragma(dsn, name string) bool {
	name = strings.ToLower(name)
	for _, part := range dsnQuery(dsn) {
		key, val, ok := strings.Cut(part, "=")
		if ok && key == "_pragma" && pragmaName(val) == name {
			return true
		}
	}
	return false
}

func dsnSetsBusyTimeout(dsn string) bool {
	for _, part := range dsnQuery(dsn) {
		key, val, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		switch key {
		case "_busy_timeout", "_timeout":
			return true
		case "_pragma":
			if pragmaName(val) == "busy_timeout" {
				return true
			}
		}
	}
	return false
}

func pragmaName(val string) string {
	val = strings.TrimSpace(val)
	if i := strings.IndexAny(val, "(= "); i >= 0 {
		val = val[:i]
	}
	return strings.ToLower(strings.TrimSpace(val))
}

func dsnHasKey(dsn, key string) bool {
	for _, part := range dsnQuery(dsn) {
		k, _, ok := strings.Cut(part, "=")
		if ok && k == key {
			return true
		}
	}
	return false
}

func dsnQuery(dsn string) []string {
	i := strings.IndexByte(dsn, '?')
	if i < 0 || i == len(dsn)-1 {
		return nil
	}
	return strings.Split(dsn[i+1:], "&")
}

func appendQuery(dsn, key, value string) string {
	if strings.Contains(dsn, "?") {
		return dsn + "&" + key + "=" + value
	}
	return dsn + "?" + key + "=" + value
}
