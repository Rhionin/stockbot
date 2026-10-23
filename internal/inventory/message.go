package inventory

import "github.com/Rhionin/stockbot/internal/clientmsg"

// NoneOnHandMessage is the sentence when a stock-out finds no unit on the shelf.
// Scan commit and manual stock-out share this wording.
const NoneOnHandMessage = "Nothing on hand to take out."

// InstanceGoneMessage is the sentence when the chosen unit is already gone
// and another unit of the same item is still on the shelf.
const InstanceGoneMessage = "That one was already taken out. Pick another."

// NotPendingMessage is the sentence when a scan was already committed or cancelled.
const NotPendingMessage = "This scan was already handled."

// ProductUnresolvedMessage is the sentence when a scan has no product yet,
// and when a batch stock-out barcode is not linked to a household product.
const ProductUnresolvedMessage = "Choose which item this is first."

// WhichItemMessage is the sentence on a single stock-out whose barcode is
// not linked to a household product. The client matches the code, not this sentence.
const WhichItemMessage = "Which item is this?"

// LinkTargetGoneMessage is the sentence when linkProductId names a missing,
// unknown, or wiped product. A live item with nothing on hand is none_on_hand.
const LinkTargetGoneMessage = "That item isn't available anymore. Pick another."

// ScanNotFoundMessage is the sentence when the scan is no longer in the queue.
const ScanNotFoundMessage = "This scan is no longer in the queue."

// DirectionUnsetMessage is the sentence when a scan has no stock direction.
const DirectionUnsetMessage = "Choose stock in or stock out first."

// InternalMessage is the sentence for an unexpected failure. The server logs
// the real error and does not send it to the household. It is the same
// sentence every generic 5xx uses.
const InternalMessage = clientmsg.Generic

// Codes on a commit failure. The frontend matches these, not the sentences.
const (
	CodeNoneOnHand        = "none_on_hand"
	CodeInstanceGone      = "instance_gone"
	CodeNotPending        = "not_pending"
	CodeProductUnresolved = "product_unresolved"
	CodeNoItem            = "no_item"
	CodeLinkTargetGone    = "link_target_gone"
	CodeScanNotFound      = "scan_not_found"
	CodeDirectionUnset    = "direction_unset"
	CodeInternal          = "internal"
)
