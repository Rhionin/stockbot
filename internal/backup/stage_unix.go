//go:build unix

package backup

import "golang.org/x/sys/unix"

func filesystemSize(path string) (int64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, err
	}
	return int64(st.Bsize) * int64(st.Blocks), nil
}
