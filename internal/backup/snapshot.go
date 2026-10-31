package backup

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/Rhionin/stockbot/internal/app"
)

// SnapshotOptions controls the connection that runs VACUUM INTO.
type SnapshotOptions struct {
	BusyTimeout time.Duration
	Retries     int
	RetryWait   time.Duration
	Sleep       func(time.Duration)
}

// Snapshot copies src to dest with VACUUM INTO. The copy is consistent for
// both the rollback journal and WAL. The connection sets a busy timeout and,
// when the database is busy, retries. query_only is not set: SQLite rejects
// VACUUM INTO on a query-only connection.
func Snapshot(src, dest string, opts SnapshotOptions) error {
	if opts.BusyTimeout <= 0 {
		opts.BusyTimeout = 10 * time.Second
	}
	if opts.Sleep == nil {
		opts.Sleep = time.Sleep
	}
	if opts.RetryWait <= 0 {
		opts.RetryWait = 30 * time.Second
	}
	if opts.Retries < 0 {
		return errClass(ExitPreflight, "preflight", "snapshot retry count cannot be negative")
	}
	var err error
	attempts := opts.Retries + 1
	for attempt := 0; attempt < attempts; attempt++ {
		err = vacuumInto(src, dest, opts.BusyTimeout)
		if err == nil || !isBusy(err) || attempt == attempts-1 {
			break
		}
		_ = os.Remove(dest)
		opts.Sleep(opts.RetryWait)
	}
	if err != nil {
		_ = os.Remove(dest)
		if isBusy(err) {
			return errWrap(ExitPreflight, "preflight", "the pantry database stayed busy during the snapshot", err)
		}
		return errWrap(ExitPreflight, "preflight", "could not snapshot the pantry database", err)
	}
	return nil
}

func vacuumInto(src, dest string, busy time.Duration) error {
	if err := os.Remove(dest); err != nil && !os.IsNotExist(err) {
		return err
	}
	db, err := openSource(src, busy)
	if err != nil {
		return err
	}
	defer db.Close()
	err = withPrivateFiles(func() error {
		_, err := db.Exec(`VACUUM INTO ?`, dest)
		return err
	})
	if err != nil {
		_ = os.Remove(dest)
		return err
	}
	if err := os.Chmod(dest, 0o600); err != nil {
		_ = os.Remove(dest)
		return err
	}
	return nil
}

func openSource(path string, busy time.Duration) (*sql.DB, error) {
	ms := busy.Milliseconds()
	if ms < 1 {
		ms = 1
	}
	// Deferred txlock: VACUUM INTO takes the write lock itself. Immediate
	// would reserve the database while PRAGMAs run and fight the pantry
	// writer, especially in rollback-journal mode.
	q := fmt.Sprintf("_txlock=deferred&_pragma=busy_timeout(%d)&_pragma=temp_store(MEMORY)", ms)
	db, err := app.OpenSQLite(sqliteFileDSN(path, q))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

func openReadOnly(path string) (*sql.DB, error) {
	db, err := app.OpenSQLite(sqliteFileDSN(path, "mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(10000)"))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

func sqliteFileDSN(path, query string) string {
	u := url.URL{Scheme: "file", Path: path, RawQuery: query}
	return u.String()
}

func isBusy(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "sqlite_busy") || strings.Contains(msg, "database is locked")
}

// SnapshotInfo is the part of a snapshot that is safe to record: counts,
// ids, sizes, and hashes. It does not include row contents.
type SnapshotInfo struct {
	Integrity       string
	ForeignKeyCheck string
	Tables          map[string]int64
	RowTotal        int64
	LastMigration   string
	PageCount       int64
	PageSize        int64
	PlaintextSHA256 string
	PlaintextSize   int64
}

// Inspect checks a snapshot file. integrity_check must be exactly "ok".
// foreign_key_check is recorded and does not fail the run.
func Inspect(path string) (SnapshotInfo, error) {
	sum, size, err := fileSHA256(path)
	if err != nil {
		return SnapshotInfo{}, errWrap(ExitIntegrity, "integrity", "could not read the snapshot", err)
	}
	db, err := openReadOnly(path)
	if err != nil {
		return SnapshotInfo{}, errWrap(ExitIntegrity, "integrity", "could not open the snapshot", err)
	}
	defer db.Close()

	info := SnapshotInfo{
		Tables:          map[string]int64{},
		PlaintextSHA256: sum,
		PlaintextSize:   size,
	}
	integrity, err := integrityResult(db)
	if err != nil {
		return SnapshotInfo{}, err
	}
	info.Integrity = integrity

	fk, err := foreignKeyResult(db)
	if err != nil {
		return SnapshotInfo{}, errWrap(ExitIntegrity, "integrity", "could not check foreign keys", err)
	}
	info.ForeignKeyCheck = fk

	tables, total, err := rowCounts(db)
	if err != nil {
		return SnapshotInfo{}, err
	}
	info.Tables = tables
	info.RowTotal = total

	last, err := lastMigration(db)
	if err != nil {
		return SnapshotInfo{}, err
	}
	info.LastMigration = last

	if err := db.QueryRow(`PRAGMA page_count`).Scan(&info.PageCount); err != nil {
		return SnapshotInfo{}, errWrap(ExitIntegrity, "integrity", "could not read the snapshot page count", err)
	}
	if err := db.QueryRow(`PRAGMA page_size`).Scan(&info.PageSize); err != nil {
		return SnapshotInfo{}, errWrap(ExitIntegrity, "integrity", "could not read the snapshot page size", err)
	}
	return info, nil
}

func integrityResult(db *sql.DB) (string, error) {
	rows, err := db.Query(`PRAGMA integrity_check`)
	if err != nil {
		return "", errWrap(ExitIntegrity, "integrity", "could not check the snapshot", err)
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			return "", errWrap(ExitIntegrity, "integrity", "could not check the snapshot", err)
		}
		lines = append(lines, line)
	}
	if err := rows.Err(); err != nil {
		return "", errWrap(ExitIntegrity, "integrity", "could not check the snapshot", err)
	}
	if len(lines) != 1 || lines[0] != "ok" {
		return "", errClass(ExitIntegrity, "integrity", "the snapshot failed the integrity check")
	}
	return "ok", nil
}

func foreignKeyResult(db *sql.DB) (string, error) {
	rows, err := db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		n++
		cols, err := rows.Columns()
		if err != nil {
			return "", err
		}
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return "", err
		}
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	if n > 0 {
		return "warning", nil
	}
	return "ok", nil
}

func rowCounts(db *sql.DB) (map[string]int64, int64, error) {
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		return nil, 0, errWrap(ExitIntegrity, "integrity", "could not list snapshot tables", err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, 0, errWrap(ExitIntegrity, "integrity", "could not list snapshot tables", err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, errWrap(ExitIntegrity, "integrity", "could not list snapshot tables", err)
	}
	counts := make(map[string]int64, len(names))
	var total int64
	for _, name := range names {
		var n int64
		query := `SELECT COUNT(*) FROM ` + quoteIdent(name)
		if err := db.QueryRow(query).Scan(&n); err != nil {
			return nil, 0, errWrap(ExitIntegrity, "integrity", "could not count a snapshot table", err)
		}
		counts[name] = n
		total += n
	}
	return counts, total, nil
}

func lastMigration(db *sql.DB) (string, error) {
	var present int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'schema_migrations'`).Scan(&present); err != nil {
		return "", errWrap(ExitIntegrity, "integrity", "could not read the snapshot migrations", err)
	}
	if present == 0 {
		return "", nil
	}
	var name string
	err := db.QueryRow(`SELECT filename FROM schema_migrations ORDER BY filename DESC LIMIT 1`).Scan(&name)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", errWrap(ExitIntegrity, "integrity", "could not read the snapshot migrations", err)
	}
	return name, nil
}

func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func fileSHA256(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}
