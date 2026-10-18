package inventory

// NoneOnHandMessage is the sentence when a stock-out finds no unit on the shelf.
// Scan commit and manual stock-out share this wording.
const NoneOnHandMessage = "Nothing on hand to take out."

// InstanceGoneMessage is the sentence when the chosen unit is already gone
// and another unit of the same item is still on the shelf.
const InstanceGoneMessage = "That one was already taken out. Pick another."

// Codes on a stock-out 409. The frontend matches these, not the sentences.
const (
	CodeNoneOnHand   = "none_on_hand"
	CodeInstanceGone = "instance_gone"
)
