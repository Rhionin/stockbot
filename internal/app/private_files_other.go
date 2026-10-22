//go:build !unix

package app

func withPrivateFiles(fn func() error) error {
	return fn()
}
