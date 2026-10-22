//go:build unix

package history

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestPreviewFilesArePrivateWhileTheyAreCreated(t *testing.T) {
	old := syscall.Umask(0)
	t.Cleanup(func() { syscall.Umask(old) })

	dir := t.TempDir()
	db := openHistoryFile(t, filepath.Join(dir, "pantry.db"))
	var checked bool
	previewDuringBackup = func(path string) error {
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		if info.Mode().Perm() != 0o600 {
			t.Errorf("preview.db mode %o, want 0600", info.Mode().Perm())
		}
		parent, err := os.Stat(filepath.Dir(path))
		if err != nil {
			return err
		}
		if parent.Mode().Perm() != 0o700 {
			t.Errorf("dir mode %o, want 0700", parent.Mode().Perm())
		}
		journal := path + "-journal"
		jinfo, err := os.Stat(journal)
		if err != nil {
			entries, _ := os.ReadDir(filepath.Dir(path))
			t.Errorf("journal: %v; dir = %v", err, entries)
			return nil
		}
		if jinfo.Mode().Perm() != 0o600 {
			t.Errorf("journal mode %o, want 0600", jinfo.Mode().Perm())
		}
		if filepath.Dir(filepath.Dir(path)) != dir {
			t.Errorf("copy parent = %s, want %s", filepath.Dir(filepath.Dir(path)), dir)
		}
		checked = true
		return nil
	}
	t.Cleanup(func() { previewDuringBackup = nil })

	if _, err := NewJournal(db).PreviewUndo(context.Background(), householdUser, "missing"); err == nil {
		t.Fatal("expected a missing move")
	}
	if !checked {
		t.Fatal("backup hook did not run")
	}
	if dirs := previewDirNames(t, dir); len(dirs) != 0 {
		t.Fatalf("preview dirs left: %v", dirs)
	}
}

func TestSnapshotHoldsThePreviewLock(t *testing.T) {
	dir := t.TempDir()
	db := openHistoryFile(t, filepath.Join(dir, "pantry.db"))
	var held bool
	previewDuringBackup = func(path string) error {
		previewDir := filepath.Dir(path)
		if !previewLockHeld(t, previewDir) {
			t.Errorf("snapshot did not hold the preview lock in %s", previewDir)
		}
		held = true
		return nil
	}
	t.Cleanup(func() { previewDuringBackup = nil })

	if _, err := NewJournal(db).PreviewUndo(context.Background(), householdUser, "missing"); err == nil {
		t.Fatal("expected a missing move")
	}
	if !held {
		t.Fatal("backup hook did not run")
	}
	if dirs := previewDirNames(t, dir); len(dirs) != 0 {
		t.Fatalf("preview dirs left: %v", dirs)
	}
}

func previewLockHeld(t *testing.T, dir string) bool {
	t.Helper()
	file, err := os.Open(filepath.Join(dir, "preview.lock"))
	if err != nil {
		t.Fatalf("open preview lock: %v", err)
	}
	defer file.Close()
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return true
	}
	_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	return false
}

func TestPreviewLeavesTheLiveJournalModeAlone(t *testing.T) {
	old := syscall.Umask(0)
	t.Cleanup(func() { syscall.Umask(old) })

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "pantry.db")
	db := openHistoryFile(t, dbPath)
	journal := dbPath + "-journal"
	sentinel := filepath.Join(dir, "created-during-preview")
	var journalFile *os.File
	var checked bool
	previewDuringBackup = func(string) error {
		// The live journal is created while the copy is in progress. Its mode
		// is the process umask, which preview must not change. The descriptor
		// stays open so the mode can be read after SQLite drops a stale name.
		var err error
		journalFile, err = os.OpenFile(journal, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o640)
		if err != nil {
			return err
		}
		if _, err := journalFile.Write([]byte("journal")); err != nil {
			return err
		}
		info, err := journalFile.Stat()
		if err != nil {
			return err
		}
		if info.Mode().Perm() != 0o640 {
			t.Errorf("live journal mode %o during preview, want 0640", info.Mode().Perm())
		}
		file, err := os.OpenFile(sentinel, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o666)
		if err != nil {
			return err
		}
		file.Close()
		created, err := os.Stat(sentinel)
		if err != nil {
			return err
		}
		if created.Mode().Perm() != 0o666 {
			t.Errorf("sentinel mode %o, want 0666", created.Mode().Perm())
		}
		checked = true
		return nil
	}
	t.Cleanup(func() {
		previewDuringBackup = nil
		if journalFile != nil {
			journalFile.Close()
		}
	})

	if _, err := NewJournal(db).PreviewUndo(context.Background(), householdUser, "missing"); err == nil {
		t.Fatal("expected a missing move")
	}
	if !checked {
		t.Fatal("backup hook did not run")
	}
	info, err := journalFile.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("live journal mode %o after preview, want 0640", info.Mode().Perm())
	}
	if pathInfo, err := os.Stat(journal); err == nil && pathInfo.Mode().Perm() != 0o640 {
		t.Fatalf("live journal path mode %o after preview, want 0640", pathInfo.Mode().Perm())
	}
	created, err := os.Stat(sentinel)
	if err != nil {
		t.Fatal(err)
	}
	if created.Mode().Perm() != 0o666 {
		t.Fatalf("sentinel mode %o after preview, want 0666", created.Mode().Perm())
	}
}
