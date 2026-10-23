//go:build !unix

package backup

func withPrivateFiles(fn func() error) error {
	return fn()
}
