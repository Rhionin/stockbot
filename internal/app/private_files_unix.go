//go:build unix

package app

import "syscall"

// withPrivateFiles runs fn while new files are created owner-readable only.
func withPrivateFiles(fn func() error) error {
	old := syscall.Umask(0o077)
	defer syscall.Umask(old)
	return fn()
}
