// Package clientmsg holds sentences the household sees when a request
// cannot finish. The server log keeps the underlying error.
package clientmsg

// PreviewNoSpace is the sentence when a preview copy cannot be written
// because the data volume is full. The client can retry.
const PreviewNoSpace = "There isn't enough space to preview that right now. Please try again."
