package devicingest

import "github.com/Rhionin/stockbot/internal/auth"

// SetAfterDeviceStockCommitHook configures fault injection for atomic device ingest tests.
func SetAfterDeviceStockCommitHook(hook func() error) {
	afterDeviceStockCommitHook = hook
}

// IngestRequiresTx exposes ingestTx for tests.
func IngestRequiresTx(q auth.Queryer) error {
	_, err := ingestTx(q)
	return err
}
