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

## First bench flash (StickS3)

The image runs the shared `ScannerController` on the real clock, NVS, Wi-Fi and HTTPS. The barcode reader, screen, buzzer and power gauge are stand-ins behind the `bench-serial` feature, which every target feature turns on until the driver tickets (#741 to #745) land: each line typed on the USB serial console is one scan, and the three display bands are printed to the log. The offline queue is in memory only (flash storage is #469), so a reboot drops unsent scans but keeps the pairing.

**Build.** Install the toolchain from `firmware/rust/esp-toolchain.env`: `espup install --targets esp32s3 --toolchain-version <ESP_RUST_VERSION>`, then source the export file it writes. Install `ldproxy` and `espflash` at the pinned versions, then:

```bash
cd firmware/rust/crates/stockbot-scanner-esp
cargo build --release --features sticks3_u173
```

**Flash.** The script flashes with the bootloader `esp-idf-sys` built from our `sdkconfig.defaults` (app rollback is on), not espflash's bundled one. It stops with a message unless it finds exactly one such `bootloader.bin`, and checks the image was built for the target you name:

```bash
./flash.sh sticks3_u173
```

It runs `espflash flash --bootloader <bootloader.bin> --partition-table partitions_8mb_ota.csv --monitor target/xtensa-esp32s3-espidf/release/stockbot-scanner-esp`. That command is unverified until the first bench run.

**Bench steps.** Type into the serial monitor, one line per scan, then Enter.

1. After flashing, the log shows boot, the target `sticks3_u173` and the version.
2. Type the setup QR text. The board joins Wi-Fi.
   ```json
   {"v":1,"ssid":"MyHomeWifi","pass":"my-wifi-password","url":"https://stockbot.rhionin.com"}
   ```
   Add `"code":"SBP-XXXX-XXXX-C"` to pair in the same step.
3. Type a pairing code from the web app (`SBP-XXXX-XXXX-C`). The web app shows the scanner as paired and online.
4. Type a known product barcode. In queue mode it appears in the Scan Queue.
5. Type `STOCK-IN`, then a barcode. The direction changes.
6. Unplug and replug. The scanner is still paired and needs no setup.
7. Heartbeats arrive every 60 s, and the web app shows the version from the crate (`0.1.0`).
8. Stop the server or Wi-Fi briefly. The log shows request backoff waits, and scans queued in memory go out on recovery.

An `https://` server URL is always verified against the ESP-IDF certificate bundle. An `http://` URL (a LAN dev server such as `http://192.168.1.10:8080`) is plain HTTP. There is no way to skip certificate checks. Logs never contain the Wi-Fi password or device token; check before attaching a log anywhere.

## Partition table

`partitions_8mb_ota.csv` is the canonical partition table: dual OTA apps (`3 MB` each) + `1 MB` LittleFS for `/scans.ring` offline queue. The ESP crate keeps a byte-identical copy at `rust/crates/stockbot-scanner-esp/partitions_8mb_ota.csv` (the IDF build reads it from the crate; a test checks the two match).

## Provisioning summary

1. SoftAP **`Scanner-Setup-XXXX`** captive portal (`192.168.4.1`) saves Wi-Fi + server URL (+ optional `SBP-…` code).
2. Scan **1D** pairing barcode `SBP-XXXX-XXXX-C` → `POST /api/devices/pair/complete` with `hub.kind = "esp32_scanner"`.
3. On pair / boot when paired, `U173` receives **1D-only** symbology lock; factory reset (10 s button hold) re-enables 2D for setup.

Normal operation accepts only control barcodes, valid pairing codes (in pairing state), setup QR/JSON (unprovisioned only), and **1–14 digit** product barcodes — matching server `scan.IsProductBarcode`.
