//go:build unix

package backup

import (
	"os"
	"syscall"
	"testing"
	"time"
)

func TestSnapshotIsOwnerReadOnly(t *testing.T) {
	old := syscall.Umask(0)
	t.Cleanup(func() { syscall.Umask(old) })
	dest := t.TempDir() + "/snap.db"
	if err := Snapshot(fixtureDB(t), dest, SnapshotOptions{BusyTimeout: time.Second}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(dest)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %#o", info.Mode().Perm())
	}
}
