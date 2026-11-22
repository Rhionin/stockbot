package attention

var faultAfterLoadConnFact func() error

// SetFaultAfterLoadConnFactForTest installs a fault after the connection read (tests).
func SetFaultAfterLoadConnFactForTest(fn func() error) func() {
	prev := faultAfterLoadConnFact
	faultAfterLoadConnFact = fn
	return func() { faultAfterLoadConnFact = prev }
}

func beginConnFactFault() error {
	if faultAfterLoadConnFact != nil {
		return faultAfterLoadConnFact()
	}
	return nil
}

func endConnFactFault() error {
	return invokeFault(faultAfterLoadConnFact)
}

// BeginConnFactLoad exposes the H7 fault seam (server calls before reading a row).
func BeginConnFactLoad() error {
	return beginConnFactFault()
}

// EndConnFactLoad exposes the H7 fault seam after a successful read.
func EndConnFactLoad() error {
	return endConnFactFault()
}

// ConnectionFactForKey reports whether line key k should get the household ConnFact.
func ConnectionFactForKey(in EvaluateInput, key string) bool {
	if in.ConnFact == "" {
		return false
	}
	if in.Sendable != nil {
		sendable, ok := in.Sendable[key]
		if !ok || !sendable {
			return false
		}
	}
	return true
}
