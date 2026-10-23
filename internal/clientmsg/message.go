// Package clientmsg holds the sentences a household sees when a request
// cannot be completed. A generic server error must not replace a classified
// failure. The real error stays in the server log.
package clientmsg

// Busy is the sentence for HTTP 503 when the pantry database stays locked
// past the busy timeout. It is the only busy sentence.
const Busy = "Stockbot is busy right now. Try again in a moment."

// NoSpace is the sentence for HTTP 503 when SQLite cannot write because
// the disk is full.
const NoSpace = "Stockbot is out of storage space, so this wasn't saved."

// Generic is the sentence for every other 5xx. It is one constant so the
// wording can change without hunting through handlers.
const Generic = "Something went wrong. Try again."
