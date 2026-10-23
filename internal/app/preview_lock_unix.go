//go:build unix

package app

import (
	"os"
	"path/filepath"
	"syscall"
)

// HoldPreviewDir keeps dir from being swept while the caller still needs it.
// release unlocks the marker. The directory must already exist.
func HoldPreviewDir(dir string) (func(), error) {
	path := filepath.Join(dir, previewLockName)
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		file.Close()
		_ = os.Remove(path)
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		file.Close()
		_ = os.Remove(path)
		return nil, err
	}
	return func() {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		file.Close()
	}, nil
}

func previewDirBusy(dir string) bool {
	file, err := os.Open(filepath.Join(dir, previewLockName))
	if err != nil {
		return false
	}
	defer file.Close()
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return true
	}
	_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	return false
}
