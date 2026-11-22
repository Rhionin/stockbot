package attention

// Reason is a closed needs-attention reason (precedence order in Reasons).
type Reason string

const (
	ReasonNotConnected      Reason = "not_connected"
	ReasonReconnectRequired Reason = "reconnect_required"
	ReasonNoBarcode         Reason = "no_barcode"
	ReasonNoStoreMatch      Reason = "no_store_match"
	ReasonSendUnsure        Reason = "send_unsure"
	ReasonSendFailed        Reason = "send_failed"
	ReasonProbableMiss      Reason = "probable_miss"
	ReasonNotPlanned        Reason = "not_planned"
)

// Reasons is every wire reason in precedence order (parity with shared/contract.ts).
var Reasons = []Reason{
	ReasonNotConnected,
	ReasonReconnectRequired,
	ReasonNoBarcode,
	ReasonNoStoreMatch,
	ReasonSendUnsure,
	ReasonSendFailed,
	ReasonProbableMiss,
	ReasonNotPlanned,
}

// emittableReasons are the five values ticket A may put on the wire.
var emittableReasons = map[Reason]struct{}{
	ReasonNoBarcode:    {},
	ReasonNoStoreMatch: {},
	ReasonSendUnsure:   {},
	ReasonSendFailed:   {},
	ReasonNotPlanned:   {},
}

func reasonPrecedence(r Reason) int {
	for i, x := range Reasons {
		if x == r {
			return i
		}
	}
	return len(Reasons)
}

func isEmittable(r Reason) bool {
	_, ok := emittableReasons[r]
	return ok
}
