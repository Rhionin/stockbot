# Display model golden vectors

Golden LCD band text and RGB565 colors for the scanner display model. The files are maintained
by hand and are the contract that
`firmware/rust/crates/stockbot-scanner-core/tests/display_model_vectors.rs` checks against.

## Files

| File | Contents |
|------|-----------|
| `screens.json` | `buildDisplayModel` + `applyDisplayBandColors` cases (`input`, `bands`). |
| `helpers.json` | Pure helpers (`formatBand2BarcodeKeepEnd`, wrapping, revert countdown, `rgb565FromHex`, band-3 color finalize). |

## Changing the vectors

Edit the JSON directly when the display model intentionally changes, then run
`cd firmware/rust && cargo test --locked`.
