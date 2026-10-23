//go:build !linux

package history

// statfsFree is unavailable off Linux. Report a large free amount so a
// preview still runs; the copy itself still fails if the disk fills up.
func statfsFree(string) (uint64, error) {
	return 1 << 62, nil
}
