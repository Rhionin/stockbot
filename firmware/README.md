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
- Dock sense uses `M5PM1` **VBUS**; scans are suppressed for **1.5 s** after dock/undock. While docked, the `BMI270` jostle guard pauses hands-free reads while `|‖a‖ − 1 g| ≥ 0.08 g`, and for 0.5 s after the last such reading.

**Mode 2 (optional split-pin handle, `sticks3_u173_uart` target)**

- Set `U173` slide switch to **UART** (`115200 8N1`).
- `U173` pin 2 (`TX`) → Grove pin 2 / `GPIO2` (UART RX).
- `U027` button on Grove pin 1 / `GPIO1` (**active LOW**, open-drain) wired **only** to `U173` trigger — **never** parallel a pull-down on pin 1 while `U173` is in I2C mode (that shorts `SCL` to GND).

### Day-1 bench — `atoms3lite_u173_bench`

- `AtomS3 Lite` (`C124`) + `A162` Grove hub + `U120` LCD (`I2C @ 0x3E`) + `U173` (`0x21`) on `GPIO2`/`GPIO1`. Both units share the one Grove I2C bus at 100 kHz and run from USB 5 V.
- Front button `GPIO41`: trigger and mode in one. Short press toggles `Stock out` ↔ `Stock in`; hold 5 s for Wi-Fi setup, 10 s to erase.
- RGB LED `GPIO35`: flashes green (stock in) or orange (stock out) for about 30 ms per scan. There is no speaker.

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

The image runs the shared `ScannerController` on the real clock, NVS, Wi-Fi and HTTPS. The barcode reader and buzzer are stand-ins (the StickS3 targets read the real M5PM1 power chip since #741 draw on the built-in screen since #742, and play cues on the speaker since #744) behind the `bench-serial` feature, which every target feature turns on until the driver tickets (#741 to #745) land: each line typed on the USB serial console is one scan. The `atoms3lite_u173_bench` target draws on the U120 since #747; the `atoms3lite_symcode_hid` target has no screen driver yet, so it prints the three display bands to the log. The offline queue is in memory only (flash storage is #469), so a reboot drops unsent scans but keeps the pairing.

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
2. Wi-Fi setup page (captive portal, #745). Start from an erased board (`espflash erase-flash` before `./flash.sh`; a plain reflash keeps the saved Wi-Fi and pairing).
   1. The screen shows `READY TO SET UP` and `Join Wi-Fi: Scanner-Setup-XXXX`, and the log shows `portal: join Scanner-Setup-XXXX`.
   2. Join `Scanner-Setup-XXXX` from a phone (it has no password). The phone opens the "Set up your scanner" page by itself; if it does not, open `http://192.168.4.1` in the phone's browser. Check this on both an iPhone and an Android phone if you can.
   3. Submit with an empty Wi-Fi name, then with a pairing code that has one character changed. Each time the page comes back with a plain message in red and the Wi-Fi name still filled in.
   4. Enter the Wi-Fi name and password, keep the server address (`https://stockbot.rhionin.com`), and optionally a pairing code from the web app. The page says "Saved", the `Scanner-Setup-XXXX` network disappears about a second and a half later, and the phone goes back to its usual Wi-Fi.
   5. The log shows `portal: settings saved for Wi-Fi <name>`, then `wifi: connecting to <name>`. With a code the web app shows the scanner as paired; without one the screen asks for a pairing code (step 3). If a code was entered but the screen still asks for one, note it on #745: the controller sends the code straight after starting the Wi-Fi join, which may be before the scanner is online. Scanning the code then finishes pairing.
   6. Unplug and replug: the scanner rejoins Wi-Fi without the setup network.

   Typing the setup QR text on the serial console still works instead of the page:
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
10. Buttons (#744 part 1, StickS3 only; BtnA is GPIO11, BtnB is GPIO12). Run this undocked (USB unplugged) with the board paired, because on USB power the reader already decodes all the time. With `sticks3_u173`, hold BtnA over a barcode: the U173 decodes while the button is held, and stops when you let go. Press and release BtnB quickly (under 5 s): it toggles stock in and out, and the screen changes to the new direction. If the screen was dimmed, the first press only wakes it. Hold BtnB for 5 s up to 10 s: the screen shows the Wi-Fi setup hold, and releasing starts Wi-Fi setup. Hold BtnB for 10 s or more: the screen shows the erase warning, and releasing erases the pairing (factory reset), so do this one last. A very short tap that does not register means the 20 ms debounce is too long or the pin level is wrong; the pins are active low, with the internal pull-up on.
11. Speaker (#744 part 2, StickS3 only). Each cue logs a `speaker: <Cue>` line as it starts, and the first one logs `speaker: ES8311 and amplifier on`. Check that each cue sounds: dock and undock (`DockTick`, then `OffDockChirp`, a double chirp), press BtnB (`RisingTwoTone` into stock in, `FallingTwoTone` back out), scan a known barcode (`HighBeep` in stock in, `MediumBeep` in stock out), scan an unknown one (`QuestionTone`), scan with Wi-Fi off (`ScanBeepLowTick`), and pair again (`AscendingPairSuccess`). The pairs and the triple buzz should sound as separate tones with clean gaps, not one smeared tone, and a cue must not stutter while the scanner talks to the server. No sound with that log line means the codec or amplifier did not start: check the log for a `speaker:` error, and the facts in `HARDWARE-sticks3.md`.
12. Docked hands-free and the shake guard (#744 part 2, StickS3 only). The log shows `imu: BMI270 ready at 0x69` (or `0x68`) about a second after boot. With the board paired, put it on the USB dock: after 1.5 s the U173 reads hands-free with no button. Hold a barcode in front of it: it scans once (the same code again waits for the rescan cooldown). Shake or tap the dock: the reader's light goes off and nothing scans until the dock has been still for half a second. Holding BtnA still scans while the dock moves. Lift the board off the dock: scans pause for 1.5 s, then the reader decodes only while BtnA is held. If the reader keeps pausing on a still dock, or a firm tap does not pause it, note what happened; the 0.08 g threshold is `MOTION_THRESHOLD_MG` in `stockbot-scanner-core/src/motion.rs`. `imu: no BMI270` means the guard is off and docked scanning works without it.
13. Stop the server or Wi-Fi briefly. The log shows request backoff waits, and scans queued in memory go out on recovery.

## First bench flash (AtomS3 Lite, `atoms3lite_u173_bench`)

Wire the `A162` hub to the AtomS3 Grove port, then plug the `U173` and the `U120` into the hub. Build and flash the same way as the StickS3, naming this target:

```bash
cd firmware/rust/crates/stockbot-scanner-esp
cargo build --release --features atoms3lite_u173_bench
./flash.sh atoms3lite_u173_bench
```

**Bench steps.** Run StickS3 steps 1 to 7 and 11 first, with `atoms3lite_u173_bench` as the target in step 1. The serial console still works as a scanner alongside the U173. Then:

1. Boot log. It shows `display: U120 ready (firmware N)` and `scanner: U173 ready (firmware N)`. A `display:` error line means the U120 did not answer at `0x3E`; the scanner keeps working without a screen and tries the U120 again every 5 s, on the next screen change.
2. Screen states (#747). The scanner screens appear on the U120 in landscape: a coloured title strip, white text in the middle and a coloured line at the bottom, the same layout as the StickS3. Step through them and check each one: the setup screen before Wi-Fi, the connected banner, the idle screen in stock in and in stock out, a scan result, an unknown barcode, and the offline-queued screen (stop the server for that one). If the picture is upside down, change `ROTATION_LANDSCAPE` from 1 to 3 in `stockbot-scanner-core/src/u120.rs`. If the colours are wrong (for example red shows as green or blue), the pixel byte order is the other way round: change `byteswap_cmd(false)` to `byteswap_cmd(true)` in `U120::begin` in the same file. A full screen takes about a second over the shared 100 kHz bus; later changes redraw only the rows that changed.
3. Real scans through the U173. Once paired, the AtomS3 is on USB power, so the reader decodes hands free: hold a product barcode under it. The scan reaches the hub (Scan Queue or stock change), the screen shows the result, and the RGB LED flashes green in stock in or orange in stock out. If the LED shows the wrong colour (for example red instead of green), the LED takes a different colour order than GRB; see `grb` in `stockbot-scanner-esp/src/feedback_atoms3.rs`.
4. Button (GPIO41, both trigger and mode). Press and release quickly (under 5 s): it toggles stock in and out, and the screen changes to the new direction. If the screen was dimmed, the first press only wakes it. Hold 5 s up to 10 s: the screen shows the Wi-Fi setup hold, and releasing starts Wi-Fi setup. Hold 10 s or more: the screen shows the erase warning, and releasing erases the pairing (factory reset), so do this one last. Before pairing, holding the button also turns the reader on, so you can scan a pairing barcode with it.
5. Unplug the U120 while the scanner runs, scan something, then plug it back in. Scans keep working, the log shows a `display:` warning, and the screen comes back at the next screen change.

An `https://` server URL is always verified against the ESP-IDF certificate bundle. An `http://` URL (a LAN dev server such as `http://192.168.1.10:8080`) is plain HTTP. There is no way to skip certificate checks. Logs never contain the Wi-Fi password or device token; check before attaching a log anywhere.

## Partition table

`partitions_8mb_ota.csv` is the canonical partition table: dual OTA apps (`3 MB` each) + `1 MB` LittleFS for `/scans.ring` offline queue. The ESP crate keeps a byte-identical copy at `rust/crates/stockbot-scanner-esp/partitions_8mb_ota.csv` (the IDF build reads it from the crate; a test checks the two match).

## Provisioning summary

1. SoftAP **`Scanner-Setup-XXXX`** captive portal (`192.168.4.1`) saves Wi-Fi + server URL (+ optional `SBP-…` code).
2. Scan **1D** pairing barcode `SBP-XXXX-XXXX-C` → `POST /api/devices/pair/complete` with `hub.kind = "esp32_scanner"`.
3. The U173 has no symbology setting, so QR codes are filtered out in software: once paired, the core accepts only 1D product barcodes, control codes and pairing codes. Factory reset (BtnB held 10 s) returns the scanner to setup, where the setup QR text is accepted again.

Normal operation accepts only control barcodes, valid pairing codes (in pairing state), setup QR/JSON (unprovisioned only), and **1–14 digit** product barcodes — matching server `scan.IsProductBarcode`.
