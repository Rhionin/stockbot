# Device protocol JSON vectors

Golden request bodies and parser outcomes for the device HTTP protocol. The files are
maintained by hand (or updated alongside the Rust tests that read them) and are the contract
that `firmware/rust/crates/stockbot-scanner-core/tests/device_protocol_vectors.rs` and the Go
server test check against.

## Files

| File | Contents |
|------|-----------|
| `requests.json` | HTTP request bodies from the firmware builders (`method`, `endpoint`, `body`). Each row also has `deviceReachable` (whether the firmware's `is_pairing_code` / `is_ascii_digit_product_barcode` checks would allow it to send that input) and `expectedStatus` (`201` for reachable pair-complete, `200` for reachable scan/heartbeat, `400` when `deviceReachable` is false). |
| `responses.json` | Parser cases: `httpStatus`, response `body`, `fnOk`, and `parsed` (or `items` for batch). |
| `setup_qr.json` | `parseSetupQrPayload` cases (`raw`, `fnOk`, `parsed`). |
| `api_error.json` | `parseApiErrorCode` cases (`body`, `fnOk`, `code`). |
| `http_retry.json` | `httpFailureIsRetryable` table (`httpStatus`, `transportOk`, `retryable`). |

## Changing the vectors

Edit the JSON directly when the protocol intentionally changes, then run
`cd firmware/rust && cargo test --locked` and the Go server test that reads these files.
