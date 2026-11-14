package server

import "sync"

var (
	listFaultMu                 sync.Mutex
	faultAfterHouseholdTripMeta func() error
	faultAfterBatchActiveSkips  func() error
	faultAfterSupplyOpeningRead func() error
	faultAfterBatchOffListNames func() error
)

// SetFaultAfterHouseholdTripMetaForTest injects a fault after trip_seq is read.
func SetFaultAfterHouseholdTripMetaForTest(fn func() error) (restore func()) {
	listFaultMu.Lock()
	prev := faultAfterHouseholdTripMeta
	faultAfterHouseholdTripMeta = fn
	listFaultMu.Unlock()
	return func() {
		listFaultMu.Lock()
		faultAfterHouseholdTripMeta = prev
		listFaultMu.Unlock()
	}
}

// SetFaultAfterBatchActiveSkipsForTest injects a fault after skip batch load succeeds.
func SetFaultAfterBatchActiveSkipsForTest(fn func() error) (restore func()) {
	listFaultMu.Lock()
	prev := faultAfterBatchActiveSkips
	faultAfterBatchActiveSkips = fn
	listFaultMu.Unlock()
	return func() {
		listFaultMu.Lock()
		faultAfterBatchActiveSkips = prev
		listFaultMu.Unlock()
	}
}
