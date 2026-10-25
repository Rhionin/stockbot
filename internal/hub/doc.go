// Package hub is the scan hub that runs on another household's Pi.
//
// It pairs with a Stockbot server, keeps an offline scan buffer, flushes
// that buffer in batches, and heartbeats. A signed update is staged only
// after its digest and Ed25519 signature check out, and a build that does
// not stay healthy rolls back.
package hub
