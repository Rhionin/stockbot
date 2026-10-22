package history

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Rhionin/stockbot/internal/app"
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
