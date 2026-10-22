package history

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/app"
	"modernc.org/sqlite"
)

func TestPreviewCopyLivesBesideTheDatabaseAndIsRemoved(t *testing.T) {
	dir := t.TempDir()
	db := openHistoryFile(t, filepath.Join(dir, "pantry.db"))
	var seen string
	previewDuringBackup = func(path string) error {
		seen = path
		if filepath.Dir(filepath.Dir(path)) != dir {
			t.Errorf("copy %s is not beside %s", path, dir)
		}
		if strings.Contains(path, string(filepath.Separator)+"tmp"+string(filepath.Separator)) && !strings.Contains(dir, string(filepath.Separator)+"tmp"+string(filepath.Separator)) {
			t.Errorf("copy went to tmp: %s", path)
		}
		return nil
	}
	t.Cleanup(func() { previewDuringBackup = nil })

	_, err := NewJournal(db).PreviewUndo(context.Background(), householdUser, "missing")
	var missing *NotFoundError
	if !errors.As(err, &missing) {
		t.Fatalf("err = %v", err)
	}
	if seen == "" || filepath.Base(seen) != "preview.db" {
		t.Fatalf("copy = %s", seen)
	}
	if dirs := previewDirNames(t, dir); len(dirs) != 0 {
		t.Fatalf("preview dirs left: %v", dirs)
	}
}

func TestPreviewRemovesTheCopyWhenTheBackupFails(t *testing.T) {
	dir := t.TempDir()
	db := openHistoryFile(t, filepath.Join(dir, "pantry.db"))
	previous := previewBackup
	previewBackup = func(context.Context, *sql.DB, string) error {
		return errors.New("backup failed")
	}
	t.Cleanup(func() { previewBackup = previous })

	_, err := NewJournal(db).PreviewUndo(context.Background(), householdUser, "missing")
	if err == nil || !strings.Contains(err.Error(), "backup failed") {
		t.Fatalf("err = %v", err)
	}
	if dirs := previewDirNames(t, dir); len(dirs) != 0 {
		t.Fatalf("preview dirs left: %v", dirs)
	}
}

func TestPreviewRemovesTheCopyWhenOpenFails(t *testing.T) {
	dir := t.TempDir()
	db := openHistoryFile(t, filepath.Join(dir, "pantry.db"))
	previous := previewOpen
	previewOpen = func(string) (*sql.DB, error) {
		return nil, errors.New("open failed")
	}
	t.Cleanup(func() { previewOpen = previous })

	_, err := NewJournal(db).PreviewUndo(context.Background(), householdUser, "missing")
	if err == nil || !strings.Contains(err.Error(), "open failed") {
		t.Fatalf("err = %v", err)
	}
	if dirs := previewDirNames(t, dir); len(dirs) != 0 {
		t.Fatalf("preview dirs left: %v", dirs)
	}
}

func TestPreviewRemovesTheCopyWhenTheModeCheckFails(t *testing.T) {
	dir := t.TempDir()
	db := openHistoryFile(t, filepath.Join(dir, "pantry.db"))
	previewDuringBackup = func(string) error {
		return errors.New("mode check failed")
	}
	t.Cleanup(func() { previewDuringBackup = nil })

	_, err := NewJournal(db).PreviewUndo(context.Background(), householdUser, "missing")
	if err == nil || !strings.Contains(err.Error(), "mode check failed") {
		t.Fatalf("err = %v", err)
	}
	if dirs := previewDirNames(t, dir); len(dirs) != 0 {
		t.Fatalf("preview dirs left: %v", dirs)
	}
}

func TestPreviewDiskFullIsRetryable(t *testing.T) {
	dir := t.TempDir()
	db := openHistoryFile(t, filepath.Join(dir, "pantry.db"))
	previous := previewBackup
	previewBackup = func(context.Context, *sql.DB, string) error {
		return errors.New("database or disk is full (13)")
	}
	t.Cleanup(func() { previewBackup = previous })

	_, err := NewJournal(db).PreviewUndo(context.Background(), householdUser, "missing")
	var full *PreviewFullError
	if !errors.As(err, &full) {
		t.Fatalf("err = %v", err)
	}
	if dirs := previewDirNames(t, dir); len(dirs) != 0 {
		t.Fatalf("preview dirs left: %v", dirs)
	}
}

func TestPreviewWaitsForTheSingleSlotUntilCanceled(t *testing.T) {
	dir := t.TempDir()
	db := openHistoryFile(t, filepath.Join(dir, "pantry.db"))
	started := make(chan struct{})
	release := make(chan struct{})
	previous := previewBackup
	previewBackup = func(ctx context.Context, src *sql.DB, dest string) error {
		select {
		case <-started:
		default:
			close(started)
		}
		select {
		case <-release:
		case <-ctx.Done():
			return ctx.Err()
		}
		return backupDatabase(ctx, src, dest)
	}
	t.Cleanup(func() {
		previewBackup = previous
		select {
		case <-release:
		default:
			close(release)
		}
	})

	journal := NewJournal(db)
	firstDone := make(chan error, 1)
	go func() {
		_, err := journal.PreviewUndo(context.Background(), householdUser, "missing")
		firstDone <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("first preview did not start")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, err := journal.PreviewUndo(ctx, householdUser, "missing")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waiter err = %v", err)
	}
	if dirs := previewDirNames(t, dir); len(dirs) != 1 {
		t.Fatalf("preview dirs = %v", dirs)
	}
	close(release)
	select {
	case err := <-firstDone:
		var missing *NotFoundError
		if !errors.As(err, &missing) {
			t.Fatalf("first err = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("first preview did not finish")
	}
	if dirs := previewDirNames(t, dir); len(dirs) != 0 {
		t.Fatalf("preview dirs left: %v", dirs)
	}
}

func TestPreviewRefusesWhenFreeSpaceCannotHoldTheCopy(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pantry.db")
	db := openHistoryFile(t, path)
	need, err := previewSpaceNeed(path)
	if err != nil {
		t.Fatal(err)
	}
	if need < previewReserveFloor {
		t.Fatalf("need = %d", need)
	}
	previousFree := previewFree
	previewFree = func(string) (uint64, error) { return need - 1, nil }
	previous := previewBackup
	var copied atomic.Bool
	previewBackup = func(context.Context, *sql.DB, string) error {
		copied.Store(true)
		return nil
	}
	t.Cleanup(func() {
		previewFree = previousFree
		previewBackup = previous
	})

	_, err = NewJournal(db).PreviewUndo(context.Background(), householdUser, "missing")
	var full *PreviewFullError
	if !errors.As(err, &full) {
		t.Fatalf("err = %v", err)
	}
	if copied.Load() {
		t.Fatal("copy started without room for the reserve")
	}
	if dirs := previewDirNames(t, dir); len(dirs) != 0 {
		t.Fatalf("preview dirs left: %v", dirs)
	}
}

func TestPreviewProceedsWhenFreeSpaceCoversTheReserve(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pantry.db")
	db := openHistoryFile(t, path)
	need, err := previewSpaceNeed(path)
	if err != nil {
		t.Fatal(err)
	}
	previousFree := previewFree
	previewFree = func(string) (uint64, error) { return need, nil }
	t.Cleanup(func() { previewFree = previousFree })

	_, err = NewJournal(db).PreviewUndo(context.Background(), householdUser, "missing")
	var missing *NotFoundError
	if !errors.As(err, &missing) {
		t.Fatalf("err = %v", err)
	}
	if dirs := previewDirNames(t, dir); len(dirs) != 0 {
		t.Fatalf("preview dirs left: %v", dirs)
	}
}

func TestPreviewSpaceNeedIncludesTheJournalAndTheReserve(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pantry.db")
	if err := os.WriteFile(path, make([]byte, 100), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+"-journal", make([]byte, 40), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+"-wal", make([]byte, 7), 0o600); err != nil {
		t.Fatal(err)
	}
	need, err := previewSpaceNeed(path)
	if err != nil {
		t.Fatal(err)
	}
	if need != 100+40+7+previewReserveFloor {
		t.Fatalf("need = %d", need)
	}
	mem, err := previewSpaceNeed(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if mem != previewReserveFloor {
		t.Fatalf("memory need = %d", mem)
	}
	if previewReserve(1) != previewReserveFloor {
		t.Fatalf("small reserve = %d", previewReserve(1))
	}
	if previewReserve(previewReserveFloor+5) != previewReserveFloor+5 {
		t.Fatalf("large reserve = %d", previewReserve(previewReserveFloor+5))
	}
	if _, err := os.Stat(path + "-shm"); err == nil {
		t.Fatal("shm should be absent")
	}
}

func TestPreviewStatfsErrorIsNotAFullDisk(t *testing.T) {
	dir := t.TempDir()
	db := openHistoryFile(t, filepath.Join(dir, "pantry.db"))
	previous := previewFree
	previewFree = func(string) (uint64, error) { return 0, errors.New("statfs failed") }
	t.Cleanup(func() { previewFree = previous })

	_, err := NewJournal(db).PreviewUndo(context.Background(), householdUser, "missing")
	var full *PreviewFullError
	if errors.As(err, &full) || err == nil || !strings.Contains(err.Error(), "statfs failed") {
		t.Fatalf("err = %v", err)
	}
	if dirs := previewDirNames(t, dir); len(dirs) != 0 {
		t.Fatalf("preview dirs left: %v", dirs)
	}
}

func TestPreviewUndoHoldsTheOnlySlot(t *testing.T) {
	dir := t.TempDir()
	db := openHistoryFile(t, filepath.Join(dir, "pantry.db"))
	started := make(chan struct{})
	release := make(chan struct{})
	secondStarted := make(chan struct{})
	var calls atomic.Int32
	previous := previewBackup
	previewBackup = func(ctx context.Context, src *sql.DB, dest string) error {
		n := calls.Add(1)
		if n == 1 {
			close(started)
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
			return backupDatabase(ctx, src, dest)
		}
		close(secondStarted)
		return backupDatabase(ctx, src, dest)
	}
	t.Cleanup(func() {
		previewBackup = previous
		select {
		case <-release:
		default:
			close(release)
		}
	})

	journal := NewJournal(db)
	firstDone := make(chan error, 1)
	go func() {
		_, err := journal.PreviewUndo(context.Background(), householdUser, "missing")
		firstDone <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("first preview did not start")
	}
	secondDone := make(chan error, 1)
	go func() {
		_, err := journal.PreviewUndo(context.Background(), householdUser, "missing")
		secondDone <- err
	}()
	select {
	case <-secondStarted:
		t.Fatal("second preview copied while the first held the slot")
	case <-time.After(200 * time.Millisecond):
	}
	if calls.Load() != 1 {
		t.Fatalf("backup calls = %d", calls.Load())
	}
	close(release)
	select {
	case <-secondStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("second preview did not start after the slot was released")
	}
	for range 2 {
		select {
		case err := <-firstDone:
			firstDone = secondDone
			var missing *NotFoundError
			if !errors.As(err, &missing) {
				t.Fatalf("err = %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("preview did not finish")
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("backup calls = %d", calls.Load())
	}
}

func TestAbortedPreviewStopsBetweenPagesAndReleasesTheSlot(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pantry.db")
	db := openHistoryFile(t, path)
	var pages int
	if err := db.QueryRow(`PRAGMA page_count`).Scan(&pages); err != nil {
		t.Fatal(err)
	}
	if pages < 3 {
		t.Fatalf("database has %d pages", pages)
	}
	previousPages := previewBackupPages
	previewBackupPages = 1
	var dest string
	var steps int
	stop := true
	previewDuringBackup = func(got string) error {
		dest = got
		return nil
	}
	previewEachStep = func() error {
		steps++
		if !stop || steps != 2 {
			return nil
		}
		copyInfo, err := os.Stat(dest)
		if err != nil {
			return err
		}
		srcInfo, err := os.Stat(path)
		if err != nil {
			return err
		}
		if copyInfo.Size() >= srcInfo.Size() {
			t.Errorf("second step copied the whole database (%d >= %d)", copyInfo.Size(), srcInfo.Size())
		}
		return context.Canceled
	}
	t.Cleanup(func() {
		previewBackupPages = previousPages
		previewDuringBackup = nil
		previewEachStep = nil
	})

	journal := NewJournal(db)
	_, err := journal.PreviewUndo(context.Background(), householdUser, "missing")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	if steps != 2 {
		t.Fatalf("steps = %d", steps)
	}
	if dirs := previewDirNames(t, dir); len(dirs) != 0 {
		t.Fatalf("preview dirs left: %v", dirs)
	}
	stop = false
	_, err = journal.PreviewUndo(context.Background(), householdUser, "missing")
	var missing *NotFoundError
	if !errors.As(err, &missing) {
		t.Fatalf("next preview err = %v", err)
	}
}

func TestDiskFullRecognizesDriverCodes(t *testing.T) {
	if !diskFull(syscall.ENOSPC) {
		t.Fatal("ENOSPC was not full")
	}
	if !diskFull(fmt.Errorf("copy: %w", syscall.ENOSPC)) {
		t.Fatal("wrapped ENOSPC was not full")
	}
	full := sqliteFull(t)
	var driverErr *sqlite.Error
	if !errors.As(full, &driverErr) || driverErr.Code()&0xff != 13 {
		t.Fatalf("sqlite full = %T %v", full, full)
	}
	if !diskFull(full) {
		t.Fatalf("code 13 was not full: %v", full)
	}
	other := sqliteOther(t)
	if diskFull(other) {
		t.Fatalf("unrelated sqlite error was full: %v", other)
	}
	if diskFull(nil) || diskFull(errors.New("busy")) {
		t.Fatal("unrelated error was full")
	}
}

func sqliteFull(t *testing.T) error {
	t.Helper()
	db, err := app.OpenSQLite(filepath.Join(t.TempDir(), "full.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`CREATE TABLE blobs (b BLOB)`); err != nil {
		t.Fatal(err)
	}
	var pages int
	if err := db.QueryRow(`PRAGMA page_count`).Scan(&pages); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(fmt.Sprintf(`PRAGMA max_page_count = %d`, pages)); err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO blobs VALUES (zeroblob(1000000))`)
	if err == nil {
		t.Fatal("insert into a full database succeeded")
	}
	return err
}

func sqliteOther(t *testing.T) error {
	t.Helper()
	db, err := app.OpenSQLite(filepath.Join(t.TempDir(), "other.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	_, err = db.Exec(`NOT SQL`)
	if err == nil {
		t.Fatal("bad SQL succeeded")
	}
	return err
}

func TestApplyUndoReturnsTheRepairError(t *testing.T) {
	db := openHistoryDB(t)
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DROP TABLE item_instances`); err != nil {
		t.Fatal(err)
	}
	_, err = applyUndo(context.Background(), tx, householdUser, "missing", false)
	if err == nil {
		t.Fatal("repair error was dropped")
	}
	if errors.As(err, new(*NotFoundError)) {
		t.Fatal("missing move hid the repair error")
	}
}

func openHistoryFile(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := app.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	if err := app.RunMigrations(db); err != nil {
		t.Fatal(err)
	}
	return db
}

func previewDirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), app.PreviewDirPrefix) {
			names = append(names, entry.Name())
		}
	}
	return names
}
