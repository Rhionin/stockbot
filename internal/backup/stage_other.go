//go:build !unix

package backup

import "errors"

func filesystemSize(string) (int64, error) {
	return 0, errors.New("filesystem size is not available")
}
