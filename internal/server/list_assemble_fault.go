package server

import "sync"

var (
	listFaultMu                 sync.Mutex
	faultAfterHouseholdTripMeta func() error
	faultAfterBatchActiveSkips  func() error
	faultAfterSupplyOpeningRead func() error
	faultAfterSupplyPlanView     func() error
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

// SetFaultAfterSupplyOpeningReadForTest injects a fault after supply Settings is read in status mode.
func SetFaultAfterSupplyOpeningReadForTest(fn func() error) (restore func()) {
	listFaultMu.Lock()
	prev := faultAfterSupplyOpeningRead
	faultAfterSupplyOpeningRead = fn
	listFaultMu.Unlock()
	return func() {
		listFaultMu.Lock()
		faultAfterSupplyOpeningRead = prev
		listFaultMu.Unlock()
	}
}

// SetFaultAfterSupplyPlanViewForTest injects a fault before supply PlanView.
func SetFaultAfterSupplyPlanViewForTest(fn func() error) (restore func()) {
	listFaultMu.Lock()
	prev := faultAfterSupplyPlanView
	faultAfterSupplyPlanView = fn
	listFaultMu.Unlock()
	return func() {
		listFaultMu.Lock()
		faultAfterSupplyPlanView = prev
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
