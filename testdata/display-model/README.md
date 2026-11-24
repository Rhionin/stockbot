# Display model golden vectors

Golden LCD band text and RGB565 colors from the C++ `display_model` implementation
(`firmware/src/core/display_model.cpp`). Rust parity tests in
`firmware/rust/crates/stockbot-scanner-core/tests/display_model_vectors.rs` read these files.

## Files

| File | Contents |
|------|-----------|
| `screens.json` | `buildDisplayModel` + `applyDisplayBandColors` cases (`input`, `bands`). |
| `helpers.json` | Pure helpers (`formatBand2BarcodeKeepEnd`, wrapping, revert countdown, `rgb565FromHex`, band-3 color finalize). |

## Regeneration

From the repository root:

```bash
bash firmware/run_host_tests.sh
```

To refresh vectors after changing C++ or generator inputs:

```bash
cd firmware
mapfile -t CORE_SRC < <(find src/core -name '*.cpp' | sort)
g++ -std=c++17 -Wall -Wextra -Werror -Isrc/core -Isrc/net -Isrc/hal \
  "${CORE_SRC[@]}" test/gen_display_model_vectors.cpp -o /tmp/gen_display_model_vectors
/tmp/gen_display_model_vectors ../testdata/display-model
```

Commit updated JSON when diffs are intentional.
