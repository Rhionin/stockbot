//go:build unix

package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStartupKeepsAPreviewDirWhoseLockIsHeld(t *testing.T) {
	dir := t.TempDir()
	conn := openFileDB(t, filepath.Join(dir, "pantry.db"))
	if err := RunMigrations(conn); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}
	live := filepath.Join(dir, PreviewDirPrefix+"live")
	if err := os.Mkdir(live, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(live, "preview.db"), []byte("copy"), 0o600); err != nil {
		t.Fatal(err)
	}
	unlock, err := HoldPreviewDir(live)
	if err != nil {
		t.Fatal(err)
	}
	leftover := filepath.Join(dir, PreviewDirPrefix+"crash")
	if err := os.Mkdir(leftover, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := RunMigrations(conn); err != nil {
		t.Fatalf("startup: %v", err)
	}
	if _, err := os.Stat(filepath.Join(live, "preview.db")); err != nil {
		t.Fatalf("startup removed the locked preview: %v", err)
	}
	if _, err := os.Stat(leftover); !os.IsNotExist(err) {
		t.Fatalf("startup left the unlocked preview: %v", err)
	}
	unlock()
	if err := RunMigrations(conn); err != nil {
		t.Fatalf("startup after unlock: %v", err)
	}
	if _, err := os.Stat(live); !os.IsNotExist(err) {
		t.Fatalf("unlocked preview was kept: %v", err)
	}
}
