//go:build !unix

package app

import (
	"os"
	"path/filepath"
)

// HoldPreviewDir keeps a marker file in dir. Without an advisory lock, startup
// cannot tell a live copy from a leftover, so the sweep still removes it.
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
	return func() { file.Close() }, nil
}

func previewDirBusy(string) bool { return false }
