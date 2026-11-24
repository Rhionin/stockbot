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

The image runs the shared `ScannerController` on the real clock, NVS, Wi-Fi and HTTPS. The barcode reader and buzzer are stand-ins (the StickS3 targets read the real M5PM1 power chip since #741 and draw on the built-in screen since #742) behind the `bench-serial` feature, which every target feature turns on until the driver tickets (#741 to #745) land: each line typed on the USB serial console is one scan. The AtomS3 targets have no screen driver yet, so they print the three display bands to the log. The offline queue is in memory only (flash storage is #469), so a reboot drops unsent scans but keeps the pairing.

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
8. Power chip (M5PM1, #741). Unplug USB: the board keeps running on battery. The heartbeat `batteryPct` is plausible for the battery's charge (0 or no value means the gauge read failed or no battery is fitted). The U173 reader's LED lights, which shows the Grove 5 V output is on. The charge-pin polarity is unverified: with USB plugged in and the battery below full, check that the heartbeat reports the scanner as charging, and flip `CHARGE_PIN_ACTIVE_LOW` in `stockbot-scanner-core/src/m5pm1.rs` if it is the wrong way round. Facts and sources are in `rust/crates/stockbot-scanner-esp/HARDWARE-sticks3.md`.
9. Screen (#742, StickS3 only). The scanner screens appear on the built-in display in landscape, with a coloured title strip, white text in the middle and a coloured line at the bottom, and the backlight is on. Check that the text is not mirrored or upside down, and that the colours are not inverted (black background, not white). If the picture is upside down, change `LANDSCAPE_ROTATION` from `Rotation::Deg90` to `Rotation::Deg270` in `stockbot-scanner-esp/src/display_sticks3.rs`. If the colours are inverted, flip `invert_colors` there. A blank screen with a lit backlight means the panel did not start: the log has a `display:` error line, and the scanner keeps working without a screen. Check the backlight is not dark because of its polarity, and that text does not start a pixel or two off the edge, which would mean the panel offsets (52, 40) need a tweak.
11. Buttons (#744 part 1, StickS3 only; BtnA is GPIO11, BtnB is GPIO12). Run this undocked (USB unplugged) with the board paired, because on USB power the reader already decodes all the time. With `sticks3_u173`, hold BtnA over a barcode: the U173 decodes while the button is held, and stops when you let go. Press and release BtnB quickly (under 5 s): it toggles stock in and out, and the screen changes to the new direction. If the screen was dimmed, the first press only wakes it. Hold BtnB for 5 s up to 10 s: the screen shows the Wi-Fi setup hold, and releasing starts Wi-Fi setup. Hold BtnB for 10 s or more: the screen shows the erase warning, and releasing erases the pairing (factory reset), so do this one last. A very short tap that does not register means the 20 ms debounce is too long or the pin level is wrong; the pins are active low, with the internal pull-up on.
10. Stop the server or Wi-Fi briefly. The log shows request backoff waits, and scans queued in memory go out on recovery.

An `https://` server URL is always verified against the ESP-IDF certificate bundle. An `http://` URL (a LAN dev server such as `http://192.168.1.10:8080`) is plain HTTP. There is no way to skip certificate checks. Logs never contain the Wi-Fi password or device token; check before attaching a log anywhere.

## Partition table

`partitions_8mb_ota.csv` is the canonical partition table: dual OTA apps (`3 MB` each) + `1 MB` LittleFS for `/scans.ring` offline queue. The ESP crate keeps a byte-identical copy at `rust/crates/stockbot-scanner-esp/partitions_8mb_ota.csv` (the IDF build reads it from the crate; a test checks the two match).

## Provisioning summary

1. SoftAP **`Scanner-Setup-XXXX`** captive portal (`192.168.4.1`) saves Wi-Fi + server URL (+ optional `SBP-…` code).
2. Scan **1D** pairing barcode `SBP-XXXX-XXXX-C` → `POST /api/devices/pair/complete` with `hub.kind = "esp32_scanner"`.
3. On pair / boot when paired, `U173` receives **1D-only** symbology lock; factory reset (10 s button hold) re-enables 2D for setup.

Normal operation accepts only control barcodes, valid pairing codes (in pairing state), setup QR/JSON (unprovisioned only), and **1–14 digit** product barcodes — matching server `scan.IsProductBarcode`.
