package app

import (
	"database/sql"
	"strings"

	_ "modernc.org/sqlite"
)

// OpenSQLite opens a pantry or Grocy database.
//
// The modernc driver stores a time.Time with time.Time.String, which includes
// the value's zone ("2006-01-02 15:04:05.999999999 -0700 MST"). SQL then
// compares those strings alphabetically. Production runs with no TZ, so the
// rows already on disk are UTC in that same default format. _timezone=UTC
// makes every later bind use that format too, including a Denver time or a
// time.Time that still carries a monotonic clock reading.
func OpenSQLite(dsn string) (*sql.DB, error) {
	return sql.Open("sqlite", withUTCTimezone(dsn))
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
