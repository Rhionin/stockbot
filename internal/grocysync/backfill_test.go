package grocysync

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/app"
	"github.com/Rhionin/stockbot/internal/syncsnap"
	"modernc.org/sqlite"
)

func TestApplyHistoryWritesRealDatesAndIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "grocy.db")
	db := openGrocyDB(t, path)
	if _, err := db.Exec(`INSERT INTO stock (id, product_id, amount) VALUES (1, 9, 3)`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	at := time.Date(2024, 5, 6, 7, 8, 9, 0, time.UTC)
	exp := time.Date(2024, 8, 1, 0, 0, 0, 0, time.UTC)
	opts := HistoryOptions{
		DBPath:     path,
		LocationID: 4,
		Products:   map[string]int{"p1": 9},
		Now:        time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC),
		Loc:        time.UTC,
		Confirm:    true,
		Snap: syncsnap.Snapshot{
			Moves: []syncsnap.Move{
				{ID: "in1", ProductID: "p1", Direction: "in", Quantity: 2, At: at},
				{ID: "out1", ProductID: "p1", Direction: "out", Quantity: 1, At: at.Add(24 * time.Hour)},
			},
			Removals: []syncsnap.Removal{{
				InstanceID: "i1", ProductID: "p1", Reason: "expired",
				StockMoveID: "in1", RemovedByMoveID: "out1",
				StockInAt: at, ExpiresAt: &exp,
			}},
		},
	}
	refused := opts
	refused.Confirm = false
	if _, err := ApplyHistory(context.Background(), refused); err != ErrNotConfirmed {
		t.Fatalf("confirm gate: %v", err)
	}
	if backups(t, path) != 0 {
		t.Fatal("refusing to write still created a backup")
	}

	result, err := ApplyHistory(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if result.Inserted != 2 || result.BackupPath == "" {
		t.Fatalf("result = %+v", result)
	}
	assertHistory(t, path, func(rows []histRow, stockAmount float64) {
		if stockAmount != 3 {
			t.Fatalf("current stock changed to %v", stockAmount)
		}
		if len(rows) != 2 {
			t.Fatalf("rows = %+v", rows)
		}
		var purchase, consume histRow
		for _, row := range rows {
			if row.Type == "purchase" {
				purchase = row
			}
			if row.Type == "consume" {
				consume = row
			}
		}
		if purchase.Amount != 2 || purchase.Stamp != "2024-05-06 07:08:09" || purchase.Undone != 0 {
			t.Fatalf("purchase = %+v", purchase)
		}
		if consume.Amount != -1 || consume.Stamp != "2024-05-07 07:08:09" || consume.Spoiled != 1 {
			t.Fatalf("consume = %+v", consume)
		}
	})
	bak, err := app.OpenSQLite(result.BackupPath)
	if err != nil {
		t.Fatal(err)
	}
	var n int
	if err := bak.QueryRow(`SELECT COUNT(*) FROM stock_log`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	bak.Close()
	if n != 0 {
		t.Fatalf("backup already had %d journal rows", n)
	}

	again, err := ApplyHistory(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if again.Inserted != 0 || again.Updated != 0 || again.BackupPath != "" {
		t.Fatalf("second run = %+v", again)
	}

	opts.Snap.Moves[1].Quantity = 2
	updated, err := ApplyHistory(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Updated != 1 {
		t.Fatalf("update = %+v", updated)
	}
	assertHistory(t, path, func(rows []histRow, _ float64) {
		for _, row := range rows {
			if row.Type == "consume" && row.Amount != -2 {
				t.Fatalf("consume amount = %v", row.Amount)
			}
		}
		if len(rows) != 2 {
			t.Fatalf("rows = %d", len(rows))
		}
	})

	opts.Snap.Moves = opts.Snap.Moves[:1]
	undone, err := ApplyHistory(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if undone.Undone != 1 {
		t.Fatalf("undo = %+v", undone)
	}
	assertHistory(t, path, func(rows []histRow, _ float64) {
		for _, row := range rows {
			if row.Type == "consume" && row.Undone != 1 {
				t.Fatalf("consume undone = %d", row.Undone)
			}
		}
	})
}

func TestHistorySameAcceptsTheDriverDatetimeForm(t *testing.T) {
	row := historyRow{
		Type: "purchase", Amount: 1, Stamp: "2024-11-02 23:30:00",
		BestBefore: "2025-11-02", Purchased: "2024-11-02",
	}
	ex := existingHistory{
		Type: "purchase", Amount: 1, Stamp: "2024-11-02T23:30:00Z",
		Best: "2025-11-02T00:00:00Z", Purchased: "2024-11-02T00:00:00Z",
	}
	if !historySame(ex, row) {
		t.Fatal("a datetime the sqlite driver rewrote was treated as a different journal time")
	}
}

func TestApplyHistoryRefusesUnknownProductsBeforeBackup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "grocy.db")
	db := openGrocyDB(t, path)
	db.Close()
	_, err := ApplyHistory(context.Background(), HistoryOptions{
		DBPath:     path,
		LocationID: 1,
		Products:   map[string]int{},
		Confirm:    true,
		Snap: syncsnap.Snapshot{Moves: []syncsnap.Move{{
			ID: "m1", ProductID: "missing", Direction: "out", Quantity: 1, At: time.Now(),
		}}},
	})
	if err == nil {
		t.Fatal("expected an error")
	}
	if backups(t, path) != 0 {
		t.Fatal("failed plan still backed up the database")
	}
}

func TestApplyHistoryRefusesWhenDatabaseLooksInUse(t *testing.T) {
	path := filepath.Join(t.TempDir(), "grocy.db")
	db := openGrocyDB(t, path)
	db.Close()
	if err := os.WriteFile(path+"-wal", []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := ApplyHistory(context.Background(), HistoryOptions{
		DBPath:     path,
		LocationID: 1,
		Products:   map[string]int{"p1": 9},
		Confirm:    true,
		Now:        time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC),
		Snap: syncsnap.Snapshot{Moves: []syncsnap.Move{{
			ID: "m1", ProductID: "p1", Direction: "in", Quantity: 1, At: time.Now(),
		}}},
	})
	if err == nil || !strings.Contains(err.Error(), "looks in use") || !strings.Contains(err.Error(), "Nothing was changed") {
		t.Fatalf("err = %v", err)
	}
	if backups(t, path) != 0 {
		t.Fatal("in-use database was backed up")
	}

	locked := filepath.Join(t.TempDir(), "grocy.db")
	open := openGrocyDB(t, locked)
	open.Close()
	holder, err := app.OpenSQLite(locked)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	holder.SetMaxOpenConns(1)
	if _, err := holder.Exec(`BEGIN IMMEDIATE`); err != nil {
		t.Fatal(err)
	}
	defer holder.Exec(`ROLLBACK`)
	started := time.Now()
	_, err = ApplyHistory(context.Background(), HistoryOptions{
		DBPath:     locked,
		LocationID: 1,
		Products:   map[string]int{"p1": 9},
		Confirm:    true,
		Now:        time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC),
		Snap: syncsnap.Snapshot{Moves: []syncsnap.Move{{
			ID: "m1", ProductID: "p1", Direction: "in", Quantity: 1, At: time.Now(),
		}}},
	})
	elapsed := time.Since(started)
	if err == nil || !strings.Contains(err.Error(), "looks in use") || !strings.Contains(err.Error(), "Nothing was changed") {
		t.Fatalf("locked err = %v", err)
	}
	var driverErr *sqlite.Error
	if !errors.As(err, &driverErr) {
		t.Fatalf("locked cause dropped: %v", err)
	}
	// The in-use check waits on a 200ms busy timeout. An instant refusal
	// means that wait was skipped, and a multi-second wait means the
	// server default replaced it.
	if elapsed < 150*time.Millisecond || elapsed >= 2*time.Second {
		t.Fatalf("locked refusal took %s", elapsed)
	}
	if backups(t, locked) != 0 {
		t.Fatal("locked database was backed up")
	}
}

func TestSQLiteBusyUsesTheDriverCode(t *testing.T) {
	locked := lockedDriverError(t)
	lower := strings.ToLower(locked.Error())
	if strings.Contains(lower, "database is locked") || strings.Contains(lower, "sqlite_busy") {
		t.Fatalf("locked text = %q", locked.Error())
	}
	if !sqliteBusy(locked) {
		t.Fatalf("locked = %v", locked)
	}

	full := fullDriverError(t)
	wrapped := fmt.Errorf("database is locked: %w", full)
	if sqliteBusy(wrapped) {
		t.Fatalf("full disk classified from its text: %v", wrapped)
	}
	if !sqliteBusy(errors.New("database is locked")) || !sqliteBusy(errors.New("SQLITE_BUSY")) {
		t.Fatal("string fallback missed")
	}
	if sqliteBusy(errors.New("disk is full")) || sqliteBusy(nil) {
		t.Fatal("unrelated error classified as busy")
	}
}

func lockedDriverError(t *testing.T) error {
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

func fullDriverError(t *testing.T) error {
	t.Helper()
	db, err := app.OpenSQLite(filepath.Join(t.TempDir(), "full.db") + "?_pragma=busy_timeout(0)")
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

func TestApplyHistoryRefusesWhenDatabaseIsNotWritable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "grocy.db")
	db := openGrocyDB(t, path)
	db.Close()
	if err := os.Chmod(path, 0o444); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o644) })
	_, err := ApplyHistory(context.Background(), HistoryOptions{
		DBPath:     path,
		LocationID: 1,
		Products:   map[string]int{"p1": 9},
		Confirm:    true,
		Now:        time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC),
		Snap: syncsnap.Snapshot{Moves: []syncsnap.Move{{
			ID: "m1", ProductID: "p1", Direction: "in", Quantity: 1, At: time.Now(),
		}}},
	})
	if err == nil || !strings.Contains(err.Error(), "uid 1001") || !strings.Contains(err.Error(), "65532") || !strings.Contains(err.Error(), "Nothing was changed") {
		t.Fatalf("err = %v", err)
	}
	if backups(t, path) != 0 {
		t.Fatal("unwritable database was backed up")
	}
}

func TestRestoreStepsNamesTheFiles(t *testing.T) {
	text := RestoreSteps("/data/grocy.db.bak-1", "/data/grocy.db")
	for _, part := range []string{"docker stop grocy", "/data/grocy.db.bak-1", "/data/grocy.db-wal", "/data/grocy.db-shm"} {
		if !strings.Contains(text, part) {
			t.Fatalf("restore steps missing %s: %s", part, text)
		}
	}
}

type histRow struct {
	Type    string
	Amount  float64
	Stamp   string
	Spoiled int
	Undone  int
}

func openGrocyDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := app.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
		CREATE TABLE stock_log (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			product_id INTEGER NOT NULL,
			amount REAL NOT NULL,
			best_before_date TEXT,
			purchased_date TEXT,
			used_date TEXT,
			spoiled INTEGER NOT NULL DEFAULT 0,
			stock_id TEXT NOT NULL,
			transaction_id TEXT NOT NULL,
			location_id INTEGER,
			undone INTEGER NOT NULL DEFAULT 0,
			undone_timestamp TEXT,
			transaction_type TEXT NOT NULL,
			note TEXT,
			user_id INTEGER NOT NULL,
			row_created_timestamp TEXT,
			correlation_id TEXT
		);
		CREATE TABLE stock (
			id INTEGER PRIMARY KEY,
			product_id INTEGER,
			amount REAL
		);`)
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func assertHistory(t *testing.T, path string, check func([]histRow, float64)) {
	t.Helper()
	db, err := app.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query(`SELECT transaction_type, amount, row_created_timestamp, spoiled, undone FROM stock_log ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []histRow
	for rows.Next() {
		var row histRow
		if err := rows.Scan(&row.Type, &row.Amount, &row.Stamp, &row.Spoiled, &row.Undone); err != nil {
			t.Fatal(err)
		}
		got = append(got, row)
	}
	var amount float64
	if err := db.QueryRow(`SELECT amount FROM stock WHERE id = 1`).Scan(&amount); err != nil {
		t.Fatal(err)
	}
	check(got, amount)
}

func backups(t *testing.T, path string) int {
	t.Helper()
	matches, err := filepath.Glob(path + ".bak-*")
	if err != nil {
		t.Fatal(err)
	}
	return len(matches)
}

func TestConfigFromEnv(t *testing.T) {
	cfg := ConfigFromEnv(func(k string) string {
		switch k {
		case "GROCY_URL":
			return "http://grocy:80"
		case "GROCY_API_KEY":
			return "secret"
		case "PANTRY_SYNC_TOKEN":
			return "0123456789abcdef"
		case "GROCY_SYNC_INTERVAL":
			return "1m"
		default:
			return ""
		}
	})
	if !cfg.Ready() || cfg.Interval != time.Minute || cfg.PantryURL != "http://pantry:8080" || cfg.MapPath != "/data/map.db" {
		t.Fatalf("cfg = %+v", cfg)
	}
	off := ConfigFromEnv(func(string) string { return "" })
	if off.Ready() {
		t.Fatal("empty env should stay disabled")
	}
	flags, err := ParseBackfillArgs([]string{"--db", "/tmp/grocy.db", "--confirm"})
	if err != nil || !flags.Confirm || flags.DBPath != "/tmp/grocy.db" {
		t.Fatalf("flags = %+v %v", flags, err)
	}
}
