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
