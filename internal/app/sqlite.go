package app

import (
	"database/sql"
	"strconv"
	"strings"

	_ "modernc.org/sqlite"
)

// sqliteBusyTimeoutMS is how long a connection waits for another process to
// drop its write lock. It is long enough for a brief shell, backup, or
// grocy-sync lock, and short enough that a stuck lock becomes a retryable
// HTTP error instead of hanging the request.
const sqliteBusyTimeoutMS = 5000

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
// Write transactions start with BEGIN IMMEDIATE. The server keeps the
// rollback journal (not WAL). A deferred transaction that has already read
// holds a shared lock, and upgrading that to a write lock returns SQLITE_BUSY
// without running the busy handler, so the timeout would not help. The
// reserved lock is taken at BEGIN, which is when the handler runs. The driver
// leaves a ReadOnly transaction deferred, so a read does not block other
// writers from reserving. An explicit _txlock in the DSN wins.
func OpenSQLite(dsn string) (*sql.DB, error) {
	return sql.Open("sqlite", withUTCTimezone(withSQLiteLocking(dsn)))
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
