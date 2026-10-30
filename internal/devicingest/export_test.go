package devicingest

// SetAfterDeviceStockCommitHook configures fault injection for atomic device ingest tests.
func SetAfterDeviceStockCommitHook(hook func() error) {
	afterDeviceStockCommitHook = hook
}
