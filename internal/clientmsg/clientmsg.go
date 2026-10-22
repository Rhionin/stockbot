// Package clientmsg holds the fixed sentences and codes a client sees.
// The server log keeps the underlying detail.
package clientmsg

import "log"

const (
	// ProductUnresolvedText is the sentence for a product id that names
	// nothing in the household catalog.
	ProductUnresolvedText = "Choose which item this is first."
	// ProductUnresolvedCode is the machine-readable code for ProductUnresolvedText.
	ProductUnresolvedCode = "product_unresolved"

	// InvalidResolveText is the sentence when a flagged resolve includes
	// anything other than the product and, optionally, status pending.
	InvalidResolveText = "Choose the item first, then change other details."
	// InvalidResolveCode is the machine-readable code for InvalidResolveText.
	InvalidResolveCode = "invalid_resolve"
)

// Reject logs detail and returns the fixed sentence and code for the HTTP body.
func Reject(text, code string, detail error) (string, string) {
	if detail != nil {
		log.Printf("client response %s: %s", code, detail.Error())
	}
	return text, code
}
