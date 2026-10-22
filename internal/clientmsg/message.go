// Package clientmsg holds the sentences a browser sees for classified failures.
// A generic server error must not replace one of these.
package clientmsg

// Busy is the sentence for HTTP 503 when the pantry database stays locked
// past the busy timeout. It is the only busy sentence.
const Busy = "Stockbot is busy right now. Try again in a moment."

// NoSpace is the sentence for HTTP 503 when SQLite cannot write because
// the disk is full.
const NoSpace = "Stockbot is out of space to save changes."
