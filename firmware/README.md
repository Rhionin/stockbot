# Stockbot ESP32-S3 Scanner Firmware

Rust firmware for **Setup A** (`M5StickS3` + `U173`), **day-1 bench** (`AtomS3 Lite` + `U173` + `U120`), and **Setup B** (`AtomS3 Lite` + Symcode USB HID + `U120`).

The firmware lives in `firmware/rust/`. Portable scanner logic is in `crates/stockbot-scanner-core` (host-testable, no ESP-IDF dependency); `crates/stockbot-scanner-esp` is the ESP32-S3 binary crate.

The captive portal advertises a Wi-Fi network named `Scanner-Setup-XXXX`, where `XXXX` is the last four hex digits of the device MAC. That suffix is not guaranteed unique across devices.

## Hardware assembly

### Setup A — `sticks3_u173` (M5StickS3 K150 + U173)

**Mode 1 (default — zero extra wiring)**

- Connect `U173` to the StickS3 bottom Grove port (`HY2.0-4P`): `SDA` → `GPIO2`, `SCL` → `GPIO1`, `5V`, `GND` (`I2C @ 0x21`).
- **BtnA** (`GPIO11`): scan trigger (software trigger / continuous read on `U173`).
- **BtnB** (`GPIO12`): toggle `Stock out` ↔ `Stock in`.
- Dock sense uses `M5PM1` **VBUS**; scans are suppressed for **1.5 s** after dock/undock. While docked, `BMI270` jostle guard ignores reads when motion exceeds the configured threshold.

**Mode 2 (optional split-pin handle, `sticks3_u173_uart` target)**

- Set `U173` slide switch to **UART** (`115200 8N1`).
- `U173` pin 2 (`TX`) → Grove pin 2 / `GPIO2` (UART RX).
- `U027` button on Grove pin 1 / `GPIO1` (**active LOW**, open-drain) wired **only** to `U173` trigger — **never** parallel a pull-down on pin 1 while `U173` is in I2C mode (that shorts `SCL` to GND).

### Day-1 bench — `atoms3lite_u173_bench`

- `AtomS3 Lite` (`C124`) + `A162` Grove hub + `U120` LCD (`I2C @ 0x3E`) + `U173` (`0x21`) on `GPIO2`/`GPIO1`.
- Front button `GPIO41`, RGB `GPIO35`.

### Setup B — `atoms3lite_symcode_hid`

- Wall power into `A162` 5V Grove rail; `U120` on `I2C @ 0x3E`.
- `AtomS3` USB-C OTG hosts Symcode MJ-2030-B 2.4 GHz dongle on `GPIO19`/`GPIO20`.
- Scan strings arrive as USB HID keyboard reports; firmware applies the same 1D-only filter as Setup A.

## Host tests (no hardware)

```bash
cd firmware/rust
cargo test --locked
cargo clippy --all-targets -- -D warnings
cargo fmt --all -- --check
```

The pinned host toolchain is in `firmware/rust/rust-toolchain.toml`.

## Device build

Install the ESP toolchain with `espup` at the versions pinned in `firmware/rust/esp-toolchain.env`, then build one target at a time from the ESP crate:

```bash
cd firmware/rust/crates/stockbot-scanner-esp
cargo build --release --features sticks3_u173
```

Targets (cargo features): `sticks3_u173`, `sticks3_u173_uart`, `atoms3lite_u173_bench`, `atoms3lite_symcode_hid`.

## Flashing

Flashing instructions are tracked in #740 (in progress).

## Partition table

`partitions_8mb_ota.csv` is the single committed copy, used by the Rust build: dual OTA apps (`3 MB` each) + `1 MB` LittleFS for `/scans.ring` offline queue.

## Provisioning summary

1. SoftAP **`Scanner-Setup-XXXX`** captive portal (`192.168.4.1`) saves Wi-Fi + server URL (+ optional `SBP-…` code).
2. Scan **1D** pairing barcode `SBP-XXXX-XXXX-C` → `POST /api/devices/pair/complete` with `hub.kind = "esp32_scanner"`.
3. On pair / boot when paired, `U173` receives **1D-only** symbology lock; factory reset (10 s button hold) re-enables 2D for setup.

Normal operation accepts only control barcodes, valid pairing codes (in pairing state), setup QR/JSON (unprovisioned only), and **1–14 digit** product barcodes — matching server `scan.IsProductBarcode`.
