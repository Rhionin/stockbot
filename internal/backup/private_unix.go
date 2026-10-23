//go:build unix

package backup

import "syscall"

// withPrivateFiles runs fn while new files are created owner-readable only.
// VACUUM INTO creates the snapshot itself, so the umask is what makes a
// killed copy 0600.
func withPrivateFiles(fn func() error) error {
	old := syscall.Umask(0o077)
	defer syscall.Umask(old)
	return fn()
}
