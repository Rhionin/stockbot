package attention

import "sync"

var (
	faultMu                         sync.Mutex
	faultAfterLoadMarks             func() error
	faultAfterLoadActiveZero        func() error
	faultAfterLoadSendResults       func() error
	faultAfterLoadItemProductGroups func() error
)

func invokeFault(fn func() error) error {
	if fn == nil {
		return nil
	}
	return fn()
}

// SetFaultAfterLoadMarksForTest injects a fault after marks load succeeds.
func SetFaultAfterLoadMarksForTest(fn func() error) (restore func()) {
	faultMu.Lock()
	prev := faultAfterLoadMarks
	faultAfterLoadMarks = fn
	faultMu.Unlock()
	return func() {
		faultMu.Lock()
		faultAfterLoadMarks = prev
		faultMu.Unlock()
	}
}

// SetFaultAfterLoadActiveZeroForTest injects a fault after active-zero load succeeds.
func SetFaultAfterLoadActiveZeroForTest(fn func() error) (restore func()) {
	faultMu.Lock()
	prev := faultAfterLoadActiveZero
	faultAfterLoadActiveZero = fn
	faultMu.Unlock()
	return func() {
		faultMu.Lock()
		faultAfterLoadActiveZero = prev
		faultMu.Unlock()
	}
}

// SetFaultAfterLoadSendResultsForTest injects a fault after send-result load succeeds.
func SetFaultAfterLoadSendResultsForTest(fn func() error) (restore func()) {
	faultMu.Lock()
	prev := faultAfterLoadSendResults
	faultAfterLoadSendResults = fn
	faultMu.Unlock()
	return func() {
		faultMu.Lock()
		faultAfterLoadSendResults = prev
		faultMu.Unlock()
	}
}

// SetFaultAfterLoadItemProductGroupsForTest injects a fault after item-group load succeeds.
func SetFaultAfterLoadItemProductGroupsForTest(fn func() error) (restore func()) {
	faultMu.Lock()
	prev := faultAfterLoadItemProductGroups
	faultAfterLoadItemProductGroups = fn
	faultMu.Unlock()
	return func() {
		faultMu.Lock()
		faultAfterLoadItemProductGroups = prev
		faultMu.Unlock()
	}
}
