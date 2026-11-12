# Device protocol JSON vectors

Golden request bodies and parser outcomes from the C++ implementations in
`firmware/src/core/protocol_json.cpp`. Rust parity tests in
`firmware/rust/crates/stockbot-scanner-core/tests/device_protocol_vectors.rs` read these files.

## Files

| File | Contents |
|------|-----------|
| `requests.json` | HTTP request bodies from C++ builders (`method`, `endpoint`, `body`). Each row also has `deviceReachable` (whether C++ `isPairingCode` / `isAsciiDigitProductBarcode` would allow the firmware to send that input) and `expectedStatus` (`201` for reachable pair-complete, `200` for reachable scan/heartbeat, `400` when `deviceReachable` is false). |
| `responses.json` | Parser cases: `httpStatus`, response `body`, `fnOk`, and `parsed` (or `items` for batch). |
| `setup_qr.json` | `parseSetupQrPayload` cases (`raw`, `fnOk`, `parsed`). |
| `api_error.json` | `parseApiErrorCode` cases (`body`, `fnOk`, `code`). |
| `http_retry.json` | `httpFailureIsRetryable` table (`httpStatus`, `transportOk`, `retryable`). |

## Regeneration

From the repository root:

```bash
bash firmware/run_host_tests.sh
```

The host test script rebuilds `firmware/test/gen_device_protocol_vectors.cpp`, writes to a temp
directory, and `diff`s against the committed files under `testdata/device-protocol/`. To refresh
vectors after changing C++ or generator inputs:

```bash
cd firmware
mapfile -t CORE_SRC < <(find src/core -name '*.cpp' | sort)
g++ -std=c++17 -Wall -Wextra -Werror -Isrc/core -Isrc/net -Isrc/hal \
  "${CORE_SRC[@]}" test/gen_device_protocol_vectors.cpp -o /tmp/gen_device_protocol_vectors
/tmp/gen_device_protocol_vectors ../testdata/device-protocol
```

Commit the updated JSON when diffs are intentional.
