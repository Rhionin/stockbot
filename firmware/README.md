# Stockbot ESP32-S3 Scanner Firmware

Unified PlatformIO project for **Setup A** (`M5StickS3` + `U173`), **day-1 bench** (`AtomS3 Lite` + `U173` + `U120`), and **Setup B** (`AtomS3 Lite` + Symcode USB HID + `U120`).

Portable scanner logic lives in `src/core/` (no Arduino dependencies) and is covered by host unit tests.

## Hardware assembly

### Setup A — `sticks3_u173` (M5StickS3 K150 + U173)

**Mode 1 (default — zero extra wiring)**

- Connect `U173` to the StickS3 bottom Grove port (`HY2.0-4P`): `SDA` → `GPIO2`, `SCL` → `GPIO1`, `5V`, `GND` (`I2C @ 0x21`).
- **BtnA** (`GPIO11`): scan trigger (software trigger / continuous read on `U173`).
- **BtnB** (`GPIO12`): toggle `Stock out` ↔ `Stock in`.
- Dock sense uses `M5PM1` **VBUS**; scans are suppressed for **1.5 s** after dock/undock. While docked, `BMI270` jostle guard ignores reads when motion exceeds the configured threshold.

**Mode 2 (optional split-pin handle, `-DU173_UART_SPLIT_PIN_MODE=1`)**

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

## Build & flash

```bash
cd firmware
pio run -e sticks3_u173
pio run -e sticks3_u173 -t upload
pio device monitor
```

Other environments:

```bash
pio run -e atoms3lite_u173_bench -t upload
pio run -e atoms3lite_symcode_hid -t upload
```

## Host unit tests (Linux/macOS, no hardware)

Strict `-Wall -Wextra -Werror`:

```bash
bash firmware/run_host_tests.sh
```

PlatformIO native target (optional):

```bash
cd firmware
pio test -e native
```

## Partition table

`partitions_8mb_ota.csv`: dual OTA apps (`3 MB` each) + `1 MB` LittleFS for `/scans.ring` offline queue.

## Provisioning summary

1. SoftAP **`Scanner-Setup-XXXX`** captive portal (`192.168.4.1`) saves Wi-Fi + server URL (+ optional `SBP-…` code).
2. Scan **1D** pairing barcode `SBP-XXXX-XXXX-C` → `POST /api/devices/pair/complete` with `hub.kind = "esp32_scanner"`.
3. On pair / boot when paired, `U173` receives **1D-only** symbology lock; factory reset (10 s button hold) re-enables 2D for setup.

Normal operation accepts only control barcodes, valid pairing codes (in pairing state), setup QR/JSON (unprovisioned only), and **1–14 digit** product barcodes — matching server `scan.IsProductBarcode`.
