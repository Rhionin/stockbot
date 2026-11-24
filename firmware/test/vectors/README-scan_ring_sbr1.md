# `scan_ring_sbr1.json`

Golden SBR1 (`scan_ring.bin`) blobs for the offline scan ring file format. The file is maintained
by hand and is the contract that the Rust integration test
`firmware/rust/crates/stockbot-scanner-core/tests/scan_ring_vectors.rs` checks against.

Rust integration tests decode each `bytesHex` case and re-encode byte-identically; they also
assert the case count (10). Changing the SBR1 layout means editing this file and that test
together.
