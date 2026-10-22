//go:build linux

package history

import (
	"syscall"
)

func statfsFree(path string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	if st.Bsize <= 0 {
		return 0, errFreeSpace
	}
	return uint64(st.Bsize) * st.Bavail, nil
}
