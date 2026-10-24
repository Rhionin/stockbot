package hub

import (
	"database/sql"
	"errors"
	"os"
	"strconv"
	"strings"

	"github.com/Rhionin/stockbot/internal/app"
)

// BufferCap is the offline queue limit. The oldest scan is dropped to make
// room, and that drop is counted until a heartbeat or flush acknowledges it.
const BufferCap = 500

// queuedScan is one unsent scan. CapturedMono is milliseconds on the boot
// identified by BootID.
type queuedScan struct {
	ID           string
	Barcode      string
	Direction    string
	UnitCount    int
	ScannedAt    string
	CapturedMono int64
	BootID       string
	ClockSynced  bool
}

// Buffer is the on-hub SQLite queue. It uses WAL so a flush can read while
// a scan is appended.
type Buffer struct {
	db   *sql.DB
	path string
	cap  int
}

// OpenBuffer opens or creates the queue at path. The file is the hub's own
// queue, in WAL mode, and is not the server database that has to stay portable.
func OpenBuffer(path string) (*Buffer, error) {
	db, err := app.OpenSQLite(path + "?_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	b := &Buffer{db: db, path: path, cap: BufferCap}
	if err := b.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	b.tighten()
	return b, nil
}

func (b *Buffer) migrate() error {
	_, err := b.db.Exec(`
		CREATE TABLE IF NOT EXISTS scans (
			id TEXT PRIMARY KEY,
			seq INTEGER NOT NULL,
			barcode TEXT NOT NULL,
			direction TEXT NOT NULL,
			unit_count INTEGER NOT NULL,
			scanned_at TEXT NOT NULL DEFAULT '',
			captured_mono INTEGER NOT NULL,
			boot_id TEXT NOT NULL DEFAULT '',
			clock_synced INTEGER NOT NULL DEFAULT 0
		);
		CREATE INDEX IF NOT EXISTS scans_seq ON scans(seq);
		CREATE TABLE IF NOT EXISTS meta (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		);
	`)
	return err
}

// Close closes the database.
func (b *Buffer) Close() error {
	if b == nil || b.db == nil {
		return nil
	}
	return b.db.Close()
}

func (b *Buffer) tighten() {
	_ = os.Chmod(b.path, 0o600)
	_ = os.Chmod(b.path+"-wal", 0o600)
	_ = os.Chmod(b.path+"-shm", 0o600)
}

// Enqueue appends a scan. When the queue is already at the cap, the oldest
// scans are removed first and the dropped counter increases by that many.
func (b *Buffer) Enqueue(row queuedScan) (int, error) {
	tx, err := b.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var n int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM scans`).Scan(&n); err != nil {
		return 0, err
	}
	var nextSeq int64
	if err := tx.QueryRow(`SELECT COALESCE(MAX(seq), 0) + 1 FROM scans`).Scan(&nextSeq); err != nil {
		return 0, err
	}
	dropped := 0
	for n >= b.cap {
		res, err := tx.Exec(`DELETE FROM scans WHERE seq = (SELECT seq FROM scans ORDER BY seq ASC LIMIT 1)`)
		if err != nil {
			return 0, err
		}
		removed, _ := res.RowsAffected()
		if removed == 0 {
			return 0, errors.New("the scan queue is full and the oldest scan could not be dropped")
		}
		n--
		dropped++
	}
	synced := 0
	if row.ClockSynced {
		synced = 1
	}
	if row.UnitCount <= 0 {
		row.UnitCount = 1
	}
	if _, err := tx.Exec(`
		INSERT INTO scans (id, seq, barcode, direction, unit_count, scanned_at, captured_mono, boot_id, clock_synced)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		row.ID, nextSeq, row.Barcode, row.Direction, row.UnitCount, row.ScannedAt, row.CapturedMono, row.BootID, synced); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return 0, errors.New("that scan is already queued")
		}
		return 0, err
	}
	if dropped > 0 {
		if err := addMeta(tx, "dropped", dropped); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	b.tighten()
	return dropped, nil
}

func addMeta(tx *sql.Tx, key string, delta int) error {
	var raw sql.NullString
	err := tx.QueryRow(`SELECT value FROM meta WHERE key = ?`, key).Scan(&raw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	current := 0
	if raw.Valid {
		current = atoi(raw.String)
	}
	next := current + delta
	if next < 0 {
		next = 0
	}
	_, err = tx.Exec(`
		INSERT INTO meta (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, strconv.Itoa(next))
	return err
}

// Depth is the number of scans waiting to be sent.
func (b *Buffer) Depth() (int, error) {
	var n int
	err := b.db.QueryRow(`SELECT COUNT(*) FROM scans`).Scan(&n)
	return n, err
}

// Dropped is the number of scans discarded since the server last accepted
// the count.
func (b *Buffer) Dropped() (int, error) {
	var raw sql.NullString
	err := b.db.QueryRow(`SELECT value FROM meta WHERE key = ?`, "dropped").Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return atoi(raw.String), nil
}

// AckDropped reduces the dropped counter by n after the server accepted it.
// New drops that landed after the snapshot stay in the counter.
func (b *Buffer) AckDropped(n int) error {
	if n <= 0 {
		return nil
	}
	tx, err := b.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := addMeta(tx, "dropped", -n); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	b.tighten()
	return nil
}

// Peek returns the oldest scans, at most limit.
func (b *Buffer) Peek(limit int) ([]queuedScan, error) {
	if limit <= 0 {
		return nil, nil
	}
	rows, err := b.db.Query(`
		SELECT id, barcode, direction, unit_count, scanned_at, captured_mono, boot_id, clock_synced
		FROM scans ORDER BY seq ASC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []queuedScan
	for rows.Next() {
		var row queuedScan
		var synced int
		if err := rows.Scan(&row.ID, &row.Barcode, &row.Direction, &row.UnitCount, &row.ScannedAt, &row.CapturedMono, &row.BootID, &synced); err != nil {
			return nil, err
		}
		row.ClockSynced = synced != 0
		out = append(out, row)
	}
	return out, rows.Err()
}

// Remove deletes scans by id. Unknown ids are ignored.
func (b *Buffer) Remove(ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	tx, err := b.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, id := range ids {
		if _, err := tx.Exec(`DELETE FROM scans WHERE id = ?`, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// RotateBoot records a new process boot. The previous boot id and the last
// monotonic uptime saved for it are returned so ageMs can keep growing.
func (b *Buffer) RotateBoot(bootID string) (prevBoot string, prevUptime int64, err error) {
	tx, err := b.db.Begin()
	if err != nil {
		return "", 0, err
	}
	defer tx.Rollback()
	current, err := metaString(tx, "boot_id")
	if err != nil {
		return "", 0, err
	}
	uptime, _ := strconv.ParseInt(mustMeta(tx, "uptime_ms"), 10, 64)
	if current == "" {
		if err := setMeta(tx, "boot_id", bootID); err != nil {
			return "", 0, err
		}
		if err := tx.Commit(); err != nil {
			return "", 0, err
		}
		return "", 0, nil
	}
	if current == bootID {
		prevBoot, _ = metaString(tx, "prev_boot_id")
		prevUptime, _ = strconv.ParseInt(mustMeta(tx, "prev_uptime_ms"), 10, 64)
		if err := tx.Commit(); err != nil {
			return "", 0, err
		}
		return prevBoot, prevUptime, nil
	}
	if err := setMeta(tx, "prev_boot_id", current); err != nil {
		return "", 0, err
	}
	if err := setMeta(tx, "prev_uptime_ms", strconv.FormatInt(uptime, 10)); err != nil {
		return "", 0, err
	}
	if err := setMeta(tx, "boot_id", bootID); err != nil {
		return "", 0, err
	}
	if err := setMeta(tx, "uptime_ms", "0"); err != nil {
		return "", 0, err
	}
	if err := tx.Commit(); err != nil {
		return "", 0, err
	}
	return current, uptime, nil
}

// Checkpoint remembers the monotonic uptime of the current boot so a later
// process can still age scans captured before a restart.
func (b *Buffer) Checkpoint(uptime int64) error {
	if uptime < 0 {
		uptime = 0
	}
	tx, err := b.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := setMeta(tx, "uptime_ms", strconv.FormatInt(uptime, 10)); err != nil {
		return err
	}
	return tx.Commit()
}

func metaString(tx *sql.Tx, key string) (string, error) {
	var raw sql.NullString
	err := tx.QueryRow(`SELECT value FROM meta WHERE key = ?`, key).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if !raw.Valid {
		return "", nil
	}
	return raw.String, nil
}

func mustMeta(tx *sql.Tx, key string) string {
	v, _ := metaString(tx, key)
	return v
}

func setMeta(tx *sql.Tx, key, value string) error {
	_, err := tx.Exec(`
		INSERT INTO meta (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

func atoi(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return 0
	}
	return n
}
