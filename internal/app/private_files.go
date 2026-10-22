package app

// WithPrivateFiles runs fn while new files are created owner-readable only.
func WithPrivateFiles(fn func() error) error {
	return withPrivateFiles(fn)
}
